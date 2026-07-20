package services

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestReadSessionEventsKeepsToolBlocks guards the resume bug where Bash/Read tool
// calls showed only an icon after 이어하기: the seed dropped tool_use/tool_result
// blocks. The reconstructed events must carry the command, the input, and the
// output so the chat renders them exactly like a live turn.
func TestReadSessionEventsKeepsToolBlocks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cwd := "/home/tester/code/demo"
	dir := filepath.Join(home, ".claude", "projects", encodeProjectPath(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A minimal transcript: a user text turn, an assistant tool_use (Bash), and the
	// tool_result fed back on a later user event.
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"run the build"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Running it."},{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"go build ./..."}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"build ok"}]}}`,
		`{"type":"user","isSidechain":true,"message":{"role":"user","content":"subagent noise — must be skipped"}}`,
	}
	sid := "11111111-1111-1111-1111-111111111111"
	if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte(joinLines(lines)), 0o644); err != nil {
		t.Fatal(err)
	}

	evs, err := ReadSessionEvents(cwd, sid)
	if err != nil {
		t.Fatalf("ReadSessionEvents: %v", err)
	}
	if len(evs) != 3 { // sidechain line dropped
		t.Fatalf("want 3 events, got %d", len(evs))
	}

	// The Bash call and its input must survive to the wire JSON foldEvents reads.
	var assistant map[string]any
	if err := json.Unmarshal(evs[1].Raw, &assistant); err != nil {
		t.Fatal(err)
	}
	blocks := assistant["message"].(map[string]any)["content"].([]any)
	var sawTool bool
	for _, b := range blocks {
		m := b.(map[string]any)
		if m["type"] == "tool_use" {
			sawTool = true
			if m["name"] != "Bash" {
				t.Errorf("tool name = %v, want Bash", m["name"])
			}
			if m["input"] == nil {
				t.Error("tool_use lost its input (the command)")
			}
		}
	}
	if !sawTool {
		t.Error("assistant event lost its tool_use block")
	}

	// The tool_result (output) must survive too, keyed by the same id.
	var user map[string]any
	if err := json.Unmarshal(evs[2].Raw, &user); err != nil {
		t.Fatal(err)
	}
	rb := user["message"].(map[string]any)["content"].([]any)[0].(map[string]any)
	if rb["type"] != "tool_result" || rb["tool_use_id"] != "toolu_1" {
		t.Errorf("tool_result not preserved: %+v", rb)
	}
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}
