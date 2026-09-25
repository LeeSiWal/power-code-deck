package ossbridge

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// errorPatch makes Codex reject a bad edit call with a readable reason: the
// reason stands in the file path, and Codex's error names that path.
func errorPatch(err error) string {
	msg := strings.NewReplacer("\n", " ", "\r", " ").Replace(err.Error())
	return "*** Begin Patch\n*** Update File: [edit call rejected: " + msg + "]\n@@\n-\n+\n*** End Patch"
}

// patchFor turns an edit_file/create_file call into Codex apply_patch input.
// A hunk of only "-" and "+" lines needs no context: apply_patch locates the
// removed lines themselves, which is exactly old_string.
func patchFor(name, args string) (string, error) {
	var a struct {
		Path      string  `json:"path"`
		OldString *string `json:"old_string"`
		NewString *string `json:"new_string"`
		Content   *string `json:"content"`
	}
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("%s: arguments are not JSON: %w", name, err)
	}
	path := strings.TrimSpace(a.Path)
	if path == "" || strings.ContainsAny(path, "\n\r") {
		return "", fmt.Errorf("%s: path is required", name)
	}
	var b strings.Builder
	b.WriteString("*** Begin Patch\n")
	switch name {
	case "create_file":
		if a.Content == nil {
			return "", fmt.Errorf("create_file: content is required")
		}
		b.WriteString("*** Add File: " + path + "\n")
		for _, l := range lines(*a.Content) {
			b.WriteString("+" + l + "\n")
		}
	case "edit_file":
		if a.NewString == nil {
			return "", fmt.Errorf("edit_file: new_string is required")
		}
		b.WriteString("*** Update File: " + path + "\n@@\n")
		if a.OldString == nil || strings.TrimSpace(*a.OldString) == "" {
			// Nothing to replace: append. A hunk of only "+" lines is added at the
			// end of the file (checked against codex --codex-run-as-apply-patch).
			for _, l := range lines(*a.NewString) {
				b.WriteString("+" + l + "\n")
			}
			b.WriteString("*** End of File\n*** End Patch")
			return b.String(), nil
		}
		for _, l := range lines(*a.OldString) {
			b.WriteString("-" + l + "\n")
		}
		for _, l := range lines(*a.NewString) {
			b.WriteString("+" + l + "\n")
		}
	default:
		return "", fmt.Errorf("unknown edit tool %q", name)
	}
	b.WriteString("*** End Patch")
	return b.String(), nil
}

// lines splits text into lines; a trailing newline does not add an empty line.
func lines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// The model's original edit calls, by call id, so later requests can show the
// model its own call instead of the generated patch. Bounded; losing an entry
// only means the history shows the patch text.
var (
	callsMu sync.Mutex
	calls   = map[string][2]string{}
	order   []string
)

func rememberCall(callID, name, args string) {
	callsMu.Lock()
	defer callsMu.Unlock()
	if _, ok := calls[callID]; !ok {
		order = append(order, callID)
	}
	calls[callID] = [2]string{name, args}
	for len(order) > 2000 {
		delete(calls, order[0])
		order = order[1:]
	}
}

func recallCall(callID string) (string, string, bool) {
	callsMu.Lock()
	defer callsMu.Unlock()
	c, ok := calls[callID]
	return c[0], c[1], ok
}
