package services

import "testing"

func TestNormalizeWorkingDir(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/home/user/project", "/home/user/project"},
		{"/home/user/project/", "/home/user/project"},   // trailing slash
		{"/home/user/project/.", "/home/user/project"},  // "/." noise
		{"/home/user/project//packages/app", "/home/user/project/packages/app"},
		{"/home/user/project/sub/..", "/home/user/project"}, // ".." resolves
		{"  /home/user/project  ", "/home/user/project"},    // trimmed
		{`C:\Users\me\project`, "c:/users/me/project"},      // win → slashes + lower
		{"c:/Users/me/project", "c:/users/me/project"},      // win case-fold
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeWorkingDir(c.in); got != c.want {
			t.Errorf("NormalizeWorkingDir(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestProjectKeyGroups(t *testing.T) {
	// Trailing-slash, "/.", and Windows case variants must land in one project.
	same := []string{
		"/home/user/project",
		"/home/user/project/",
		"/home/user/project/.",
	}
	first := ProjectKey(same[0])
	for _, p := range same[1:] {
		if ProjectKey(p) != first {
			t.Errorf("ProjectKey(%q) != ProjectKey(%q); grouping broken", p, same[0])
		}
	}
	if ProjectKey(`C:\Users\me\project`) != ProjectKey("c:/users/me/project") {
		t.Errorf("windows drive-case variants did not group")
	}
	// A genuinely different project must NOT collide.
	if ProjectKey("/home/user/project") == ProjectKey("/home/user/other") {
		t.Errorf("distinct projects collided")
	}
}
