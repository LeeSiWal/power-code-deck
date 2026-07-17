package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// `pcd mcp-approve` — the permission bridge.
//
// Claude Code's --permission-prompt-tool takes the name of an MCP tool, and the
// CLI launches MCP servers itself. So `pcd` doubles as its own MCP server: the
// driver spawns `claude` with an --mcp-config pointing back at this very binary,
// and the CLI runs it as a child. That keeps PowerCodeDeck a single binary — no
// node runtime, no sidecar, nothing extra to install or supervise.
//
//	pcd (server) ──spawns──> claude ──spawns──> pcd mcp-approve
//	     ▲                                            │
//	     └──────── HTTP /internal/native/approve ◄────┘  (blocks until the human answers)
//
// This process is deliberately dumb: it speaks MCP on stdio, forwards the request
// to the pcd server over loopback, and waits. All the UI, notification and
// decision logic lives in the server, where the WebSocket and the devices are.
//
// Wiring comes from env (the CLI passes `env` through from --mcp-config):
//
//	PCD_APPROVE_URL     http://127.0.0.1:33033/internal/native/approve
//	PCD_APPROVE_TOKEN   per-session secret; the server rejects anything else
//	PCD_APPROVE_SESSION the agent/session id this claude belongs to

const approveToolName = "approve"

// mcpRequest is a JSON-RPC 2.0 frame from the CLI. Notifications have no id.
type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// toolsCallParams is what the CLI sends for tools/call. The permission arguments
// were captured from a live run:
//
//	{"tool_name":"Write","input":{...},"tool_use_id":"toolu_..."}
type toolsCallParams struct {
	Name      string `json:"name"`
	Arguments struct {
		ToolName  string          `json:"tool_name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
	} `json:"arguments"`
}

// approveRequest/Response are the bridge↔server contract (ours, not Anthropic's).
type approveRequest struct {
	SessionID string          `json:"sessionId"`
	ToolName  string          `json:"toolName"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"toolUseId"`
}

type approveResponse struct {
	Behavior     string          `json:"behavior"` // allow | deny
	UpdatedInput json.RawMessage `json:"updatedInput,omitempty"`
	Message      string          `json:"message,omitempty"`
}

func cmdMCPApprove() {
	in := bufio.NewScanner(os.Stdin)
	// Tool inputs can be large (a Write's whole file content), so don't let the
	// default 64KB line cap truncate a frame into invalid JSON.
	in.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	out := bufio.NewWriter(os.Stdout)

	send := func(resp mcpResponse) {
		resp.JSONRPC = "2.0"
		b, err := json.Marshal(resp)
		if err != nil {
			return
		}
		out.Write(b)
		out.WriteByte('\n')
		out.Flush()
	}

	for in.Scan() {
		line := bytes.TrimSpace(in.Bytes())
		if len(line) == 0 {
			continue
		}
		var req mcpRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue // not our frame; a strict error here would kill the session
		}

		switch req.Method {
		case "initialize":
			// Echo the CLI's protocol version back rather than pinning one: it
			// picks the version, and disagreeing just fails the handshake.
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.ProtocolVersion == "" {
				p.ProtocolVersion = "2025-06-18"
			}
			send(mcpResponse{ID: req.ID, Result: map[string]any{
				"protocolVersion": p.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "powercodedeck", "version": "0.1.0"},
			}})

		case "notifications/initialized", "notifications/cancelled":
			// Notifications carry no id and must not be answered.

		case "tools/list":
			send(mcpResponse{ID: req.ID, Result: map[string]any{"tools": []any{map[string]any{
				"name":        approveToolName,
				"description": "Ask the PowerCodeDeck user to approve a tool call. Blocks until they answer.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tool_name":   map[string]any{"type": "string"},
						"input":       map[string]any{"type": "object"},
						"tool_use_id": map[string]any{"type": "string"},
					},
					"required": []string{"tool_name", "input"},
				},
			}}}})

		case "tools/call":
			var p toolsCallParams
			if err := json.Unmarshal(req.Params, &p); err != nil {
				send(mcpResponse{ID: req.ID, Error: &mcpError{Code: -32602, Message: "bad params"}})
				continue
			}
			if p.Name != approveToolName {
				send(mcpResponse{ID: req.ID, Error: &mcpError{Code: -32601, Message: "unknown tool " + p.Name}})
				continue
			}
			decision := askServer(approveRequest{
				SessionID: os.Getenv("PCD_APPROVE_SESSION"),
				ToolName:  p.Arguments.ToolName,
				Input:     p.Arguments.Input,
				ToolUseID: p.Arguments.ToolUseID,
			})
			// The CLI reads content[0].text as the JSON decision.
			payload, _ := json.Marshal(decision)
			send(mcpResponse{ID: req.ID, Result: map[string]any{
				"content": []any{map[string]any{"type": "text", "text": string(payload)}},
			}})

		default:
			if len(req.ID) > 0 {
				send(mcpResponse{ID: req.ID, Error: &mcpError{Code: -32601, Message: "no method " + req.Method}})
			}
		}
	}
}

// askServer forwards one permission request to pcd and waits for the human.
//
// There is NO client timeout: the user may be asleep, and the honest outcome of
// "nobody answered yet" is to keep waiting, not to invent an answer. The request
// ends when the human answers or the session dies (the server closes the
// response, and Claude's own exit tears this process down).
//
// If we can't reach the server at all, deny — the one safe default. Never allow
// on error: that would run an unapproved tool because a pipe broke.
func askServer(req approveRequest) approveResponse {
	url := os.Getenv("PCD_APPROVE_URL")
	if url == "" {
		return approveResponse{Behavior: "deny", Message: "PowerCodeDeck: approval bridge is not configured (no PCD_APPROVE_URL)."}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return approveResponse{Behavior: "deny", Message: "PowerCodeDeck: could not encode the request."}
	}
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return approveResponse{Behavior: "deny", Message: "PowerCodeDeck: could not build the request."}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-PCD-Approve-Token", os.Getenv("PCD_APPROVE_TOKEN"))

	client := &http.Client{Timeout: 0} // wait as long as the human takes
	resp, err := client.Do(httpReq)
	if err != nil {
		return approveResponse{Behavior: "deny", Message: fmt.Sprintf("PowerCodeDeck: could not reach the deck to ask (%v).", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return approveResponse{Behavior: "deny", Message: fmt.Sprintf("PowerCodeDeck: the deck refused the ask (%d %s).", resp.StatusCode, bytes.TrimSpace(msg))}
	}
	var out approveResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return approveResponse{Behavior: "deny", Message: "PowerCodeDeck: the deck's answer was unreadable."}
	}
	if out.Behavior != "allow" && out.Behavior != "deny" {
		return approveResponse{Behavior: "deny", Message: "PowerCodeDeck: the deck sent an unknown decision."}
	}
	return out
}

