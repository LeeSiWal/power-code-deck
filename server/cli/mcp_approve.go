package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
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
//	PCD_MCP_TOOLS       comma list over approve,team (unset = approve only)
//	PCD_TEAM_URL        http://127.0.0.1:33033/internal/native/team (team tools)

const (
	approveToolName = "approve"
	askMemberTool   = "ask_member"
	handoffAgyTool  = "handoff_to_agy"
)

// enabledTools reads PCD_MCP_TOOLS. Unset means approve only, so every spawn
// from before dream team mode behaves exactly as it did.
func enabledTools() (approve, team bool) {
	v := os.Getenv("PCD_MCP_TOOLS")
	if v == "" {
		return true, false
	}
	for _, t := range strings.Split(v, ",") {
		switch strings.TrimSpace(t) {
		case "approve":
			approve = true
		case "team":
			team = true
		}
	}
	return approve, team
}

// mcpTools is the tools/list payload for the enabled groups. Team descriptions
// stay short: they ride in every conductor request's context.
func mcpTools(approve, team bool) []any {
	tools := []any{}
	if approve {
		tools = append(tools, map[string]any{
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
		})
	}
	if team {
		tools = append(tools,
			map[string]any{
				"name":        askMemberTool,
				"description": "Delegate a read-only task (reading/summarizing files, an independent review) to another CLI team member and get its answer. Members cannot edit files or run commands. Pass relative paths in files.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"member": map[string]any{"type": "string", "description": "member id from the team list"},
						"task":   map[string]any{"type": "string"},
						"files":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					},
					"required": []string{"member", "task"},
				},
			},
			map[string]any{
				"name":        handoffAgyTool,
				"description": "Hand a task to the user's Antigravity (agy) app. Shows the user a card; the user runs it and pastes the result back later. Do not wait in this turn.",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"task": map[string]any{"type": "string"}},
					"required":   []string{"task"},
				},
			})
	}
	return tools
}

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
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type approveArgs struct {
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
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

func cmdMCPApprove() { serveMCP(os.Stdin, os.Stdout) }

// serveMCP speaks MCP over the given stream until it ends.
func serveMCP(r io.Reader, w io.Writer) {
	approveOn, teamOn := enabledTools()
	in := bufio.NewScanner(r)
	// Tool inputs can be large (a Write's whole file content), so don't let the
	// default 64KB line cap truncate a frame into invalid JSON.
	in.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	out := bufio.NewWriter(w)

	// Each tools/call runs in its own goroutine: a member can take minutes, and
	// the CLI must still get permission answers (and be able to cancel) meanwhile.
	// writeMu serializes frames onto stdout.
	var writeMu sync.Mutex
	send := func(resp mcpResponse) {
		resp.JSONRPC = "2.0"
		b, err := json.Marshal(resp)
		if err != nil {
			return
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		out.Write(b)
		out.WriteByte('\n')
		out.Flush()
	}

	// inflight maps a request id (compact JSON) to its call's cancel, so
	// notifications/cancelled can close that call's HTTP request — the server
	// handler's context then ends and TeamService stops the member.
	var (
		inflightMu sync.Mutex
		inflight   = map[string]context.CancelFunc{}
		wg         sync.WaitGroup
	)
	idKey := func(raw json.RawMessage) string {
		var buf bytes.Buffer
		if err := json.Compact(&buf, raw); err != nil {
			return string(raw)
		}
		return buf.String()
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

		case "notifications/initialized":
			// Notifications carry no id and must not be answered.

		case "notifications/cancelled":
			var p struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			if json.Unmarshal(req.Params, &p) == nil && len(p.RequestID) > 0 {
				inflightMu.Lock()
				cancel := inflight[idKey(p.RequestID)]
				inflightMu.Unlock()
				if cancel != nil {
					cancel()
				}
			}

		case "tools/list":
			send(mcpResponse{ID: req.ID, Result: map[string]any{"tools": mcpTools(approveOn, teamOn)}})

		case "tools/call":
			var p toolsCallParams
			if err := json.Unmarshal(req.Params, &p); err != nil {
				send(mcpResponse{ID: req.ID, Error: &mcpError{Code: -32602, Message: "bad params"}})
				continue
			}
			ctx, cancel := context.WithCancel(context.Background())
			key := idKey(req.ID)
			inflightMu.Lock()
			inflight[key] = cancel
			inflightMu.Unlock()
			wg.Add(1)
			go func(id json.RawMessage, p toolsCallParams) {
				defer wg.Done()
				resp := handleToolCall(ctx, id, p, approveOn, teamOn)
				inflightMu.Lock()
				delete(inflight, key)
				inflightMu.Unlock()
				cancelled := ctx.Err() != nil
				cancel()
				// MCP: a cancelled request should not be answered.
				if !cancelled {
					send(resp)
				}
			}(req.ID, p)

		default:
			if len(req.ID) > 0 {
				send(mcpResponse{ID: req.ID, Error: &mcpError{Code: -32601, Message: "no method " + req.Method}})
			}
		}
	}
	// stdin ended: let in-flight calls finish and answer. When the CLI really
	// exits it tears this process down anyway.
	wg.Wait()
}

// handleToolCall runs one tools/call. ctx ends when the CLI cancels the call.
func handleToolCall(ctx context.Context, id json.RawMessage, p toolsCallParams, approveOn, teamOn bool) mcpResponse {
	if teamOn && (p.Name == askMemberTool || p.Name == handoffAgyTool) {
		text, ok := askTeam(ctx, p.Name, p.Arguments)
		result := map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
		if !ok {
			result["isError"] = true
		}
		return mcpResponse{ID: id, Result: result}
	}
	if p.Name != approveToolName || !approveOn {
		return mcpResponse{ID: id, Error: &mcpError{Code: -32601, Message: "unknown tool " + p.Name}}
	}
	var a approveArgs
	_ = json.Unmarshal(p.Arguments, &a)
	decision := askServerCtx(ctx, approveRequest{
		SessionID: os.Getenv("PCD_APPROVE_SESSION"),
		ToolName:  a.ToolName,
		Input:     a.Input,
		ToolUseID: a.ToolUseID,
	})
	// The CLI reads content[0].text as the JSON decision.
	payload, _ := json.Marshal(decision)
	return mcpResponse{ID: id, Result: map[string]any{
		"content": []any{map[string]any{"type": "text", "text": string(payload)}},
	}}
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
	return askServerCtx(context.Background(), req)
}

// askServerCtx is askServer bound to ctx: only an explicit cancel from the CLI
// ends the wait — there is still no timeout.
func askServerCtx(ctx context.Context, req approveRequest) approveResponse {
	url := os.Getenv("PCD_APPROVE_URL")
	if url == "" {
		return approveResponse{Behavior: "deny", Message: "PowerCodeDeck: approval bridge is not configured (no PCD_APPROVE_URL)."}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return approveResponse{Behavior: "deny", Message: "PowerCodeDeck: could not encode the request."}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
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

// teamRequest is the bridge↔server contract for the team tools.
type teamRequest struct {
	SessionID string          `json:"sessionId"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

// askTeam forwards one team tool call to pcd and returns the server's JSON body
// as the tool result text. Like askServer there is NO client timeout — the
// server enforces the member time limit — and an unreachable deck becomes an
// error result, never a made-up answer. ok=false marks such an error.
// Cancelling ctx (the CLI interrupted the call) closes the HTTP request.
func askTeam(ctx context.Context, tool string, args json.RawMessage) (string, bool) {
	fail := func(reason string) (string, bool) {
		b, _ := json.Marshal(map[string]string{"status": "failed", "reason": "PowerCodeDeck: " + reason})
		return string(b), false
	}
	url := os.Getenv("PCD_TEAM_URL")
	if url == "" {
		return fail("team bridge is not configured (no PCD_TEAM_URL).")
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	body, err := json.Marshal(teamRequest{SessionID: os.Getenv("PCD_APPROVE_SESSION"), Tool: tool, Arguments: args})
	if err != nil {
		return fail("could not encode the request.")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fail("could not build the request.")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-PCD-Approve-Token", os.Getenv("PCD_APPROVE_TOKEN"))

	client := &http.Client{Timeout: 0} // the server enforces TimeoutSec
	resp, err := client.Do(httpReq)
	if err != nil {
		return fail(fmt.Sprintf("could not reach the deck (%v).", err))
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fail("the deck's answer was unreadable.")
	}
	if resp.StatusCode != http.StatusOK {
		msg := bytes.TrimSpace(out)
		if len(msg) > 256 {
			msg = msg[:256]
		}
		return fail(fmt.Sprintf("the deck refused the call (%d %s).", resp.StatusCode, msg))
	}
	return string(bytes.TrimSpace(out)), true
}
