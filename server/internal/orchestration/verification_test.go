package orchestration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeManifest(t *testing.T, root, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".powercodedeck"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, checkManifest), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestVerificationManifestValidation(t *testing.T) {
	valid := t.TempDir()
	writeManifest(t, valid, `{"version":1,"checks":[{"name":"unit","cwd":".","argv":["go","test","./..."],"timeoutSeconds":30}]}`)
	checks, err := loadVerificationPlan(valid)
	if err != nil || len(checks) != 1 || checks[0].Argv[2] != "./..." {
		t.Fatal(checks, err)
	}

	cases := []string{
		`{"version":2,"checks":[]}`,
		`{"version":1,"checks":[{"name":"review","argv":["true"]}]}`,
		`{"version":1,"checks":[{"name":"unit","cwd":"../outside","argv":["true"]}]}`,
		`{"version":1,"checks":[{"name":"unit","argv":["true"],"unexpected":1}]}`,
		`{"version":1,"checks":[{"name":"unit","argv":[]}]}`,
		`{"version":1,"checks":[{"name":"unit","argv":[" "]}]}`,
	}
	for _, content := range cases {
		root := t.TempDir()
		writeManifest(t, root, content)
		if _, err := loadVerificationPlan(root); err == nil {
			t.Fatalf("invalid manifest accepted: %s", content)
		}
	}
}

func TestVerificationManifestSymlinkRejected(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "checks.json")
	if err := os.WriteFile(outside, []byte(`{"version":1,"checks":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".powercodedeck"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, checkManifest)); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := loadVerificationPlan(root); err == nil {
		t.Fatal("symlink manifest accepted")
	}
}

func TestVerificationManifestParentSymlinkEscapeRejected(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "checks.json"), []byte(`{"version":1,"checks":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".powercodedeck")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := loadVerificationPlan(root); !errors.Is(err, ErrInvalid) {
		t.Fatal("manifest escaped through parent symlink", err)
	}
}

func TestDecodeReviewIsStrict(t *testing.T) {
	result, err := decodeReview(`{"verdict":"fail","summary":"missing error handling"}`)
	if err != nil || result.Verdict != "fail" {
		t.Fatal(result, err)
	}
	for _, text := range []string{
		"```json\n{\"verdict\":\"pass\",\"summary\":\"ok\"}\n```",
		`{"verdict":"pass","summary":"ok","extra":true}`,
		`{"verdict":"pass","summary":"ok","toolAction":"Finishing review","toolSummary":"Review completion"}`,
		`{"verdict":"pass","summary":"ok"} {"verdict":"pass","summary":"ok"}`,
		`{"verdict":"pass","summary":"ok"} {"verdict":"fail","summary":"unsafe"}`,
		`{"verdict":"maybe","summary":"ok"}`,
		`{"verdict":"pass","summary":""}`,
	} {
		if _, err := decodeReview(text); err == nil {
			t.Fatalf("invalid review accepted: %s", text)
		}
	}
}

func TestReviewFingerprintIncludesHead(t *testing.T) {
	repo := testRepo(t)
	before, err := reviewFingerprint(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("new commit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := git(context.Background(), repo, "add", "tracked.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(context.Background(), repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "new head"); err != nil {
		t.Fatal(err)
	}
	after, err := reviewFingerprint(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("clean commit did not change review fingerprint")
	}
}
