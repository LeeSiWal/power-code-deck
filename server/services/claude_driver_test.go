package services

import (
	"encoding/json"
	"strings"
	"testing"
)

// The invocation is the contract with the CLI. These flags were each verified
// against claude 2.1.212 by hand; this pins them so a refactor can't quietly drop
// one (dropping --permission-prompt-tool, in particular, does not fail loudly —
// it silently denies every gated tool while still reporting success).
func TestBuildArgsCarriesTheProtocolFlags(t *testing.T) {
	d := NewClaudeDriver(ClaudeConfig{
		SessionID: "s1", Cwd: "/tmp", ApproveURL: "http://127.0.0.1:1/x",
		ApproveToken: "tok", SelfPath: "/usr/local/bin/pcd",
	})
	args := strings.Join(d.buildArgs(), " ")

	for _, want := range []string{
		"-p",
		"--input-format stream-json",
		"--output-format stream-json",
		"--verbose", // the CLI rejects --print + stream-json without it
		"--permission-prompt-tool mcp__pcd__approve",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("args missing %q:\n%s", want, args)
		}
	}
}

func TestBuildArgsResume(t *testing.T) {
	d := NewClaudeDriver(ClaudeConfig{ResumeID: "abc-123", ApproveURL: "u", ApproveToken: "t", SelfPath: "p"})
	args := strings.Join(d.buildArgs(), " ")
	if !strings.Contains(args, "--resume abc-123") {
		t.Fatalf("args = %s", args)
	}
}

// The MCP config points the CLI back at THIS binary and carries the session's
// secret in env — never on disk, never in argv of the bridge itself.
func TestMCPConfigPointsAtSelfWithSessionEnv(t *testing.T) {
	d := NewClaudeDriver(ClaudeConfig{
		SessionID: "agent-42", ApproveURL: "http://127.0.0.1:33033/internal/native/approve",
		ApproveToken: "secret", SelfPath: "/opt/pcd",
	})
	var cfg struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(d.mcpConfigJSON()), &cfg); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}
	pcd, ok := cfg.MCPServers["pcd"]
	if !ok {
		t.Fatalf("no pcd server in %s", d.mcpConfigJSON())
	}
	if pcd.Command != "/opt/pcd" || len(pcd.Args) != 1 || pcd.Args[0] != "mcp-approve" {
		t.Fatalf("server = %+v, want this binary running `mcp-approve`", pcd)
	}
	if pcd.Env["PCD_APPROVE_SESSION"] != "agent-42" || pcd.Env["PCD_APPROVE_TOKEN"] != "secret" {
		t.Fatalf("env = %+v", pcd.Env)
	}
}

// Starting without a bridge would put us in the mode where every permission-gated
// tool is denied while the turn still reports success — a UI would show green
// checks for work that never happened. Refuse instead.
func TestStartRefusesWithoutApprovalBridge(t *testing.T) {
	d := NewClaudeDriver(ClaudeConfig{SessionID: "s1", Cwd: "/tmp", SelfPath: "/opt/pcd"})
	if err := d.Start(); err == nil {
		t.Fatal("Start succeeded with no approval bridge configured")
	}
	d2 := NewClaudeDriver(ClaudeConfig{SessionID: "s1", Cwd: "/tmp", ApproveURL: "u", ApproveToken: "t"})
	if err := d2.Start(); err == nil {
		t.Fatal("Start succeeded with no SelfPath for the MCP bridge")
	}
}

func TestSendBeforeStartFails(t *testing.T) {
	d := NewClaudeDriver(ClaudeConfig{SessionID: "s1"})
	if err := d.Send("hello"); err == nil {
		t.Fatal("Send succeeded on a session that never started")
	}
}
