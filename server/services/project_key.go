package services

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"regexp"
	"strings"
)

// winDrive matches a Windows drive prefix on a forward-slash-normalized path,
// e.g. "c:/users/me". Used to decide when to lower-case (Windows/WSL paths are
// case-insensitive, so C:\ and c:\ must group into one project).
var winDrive = regexp.MustCompile(`^[a-zA-Z]:/`)

// NormalizeWorkingDir turns a session's working directory into a stable grouping
// key-form. It is a PURE STRING transform (never touches the filesystem) so it is
// unit-testable and safe on paths that no longer exist or belong to another OS:
//
//   - back-slashes → forward-slashes (a Windows path handed to a Linux server must
//     still group with its WSL twin);
//   - path.Clean semantics: collapse "//", drop "/.", resolve "/..", strip a
//     trailing slash;
//   - Windows drive paths lower-cased (case-insensitive filesystem).
//
// This lives on the SERVER on purpose: the client owns no path rules, so
// Windows/WSL/macOS/Linux semantics never get duplicated and drift out of sync.
func NormalizeWorkingDir(p string) string {
	s := strings.TrimSpace(p)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\\", "/")
	win := winDrive.MatchString(s)
	s = path.Clean(s)
	if s == "." {
		return ""
	}
	if win {
		s = strings.ToLower(s)
	}
	return s
}

// ProjectKey is the stable grouping id derived from the normalized path. It is a
// hash, not a DB foreign key, so grouping needs no schema/migration — the client
// groups tiles purely by this value.
func ProjectKey(workingDir string) string {
	sum := sha256.Sum256([]byte(NormalizeWorkingDir(workingDir)))
	return hex.EncodeToString(sum[:8]) // 16 hex chars — ample against collision
}

// ProjectLabel is the human-facing group header: the normalized path with the
// user's home directory collapsed to "~". Provided by the server so the client
// never re-derives path/home rules of its own.
func ProjectLabel(workingDir string) string {
	n := NormalizeWorkingDir(workingDir)
	if n == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		h := NormalizeWorkingDir(home)
		if h != "" {
			if n == h {
				return "~"
			}
			if strings.HasPrefix(n, h+"/") {
				return "~" + n[len(h):]
			}
		}
	}
	return n
}
