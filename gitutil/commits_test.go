package gitutil

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestIsSyncCommit(t *testing.T) {
	if !IsSyncCommit("chore(sync): confluence") {
		t.Error("exact prefix should match")
	}
	if !IsSyncCommit("chore(sync): confluence\n\ndetails") {
		t.Error("prefix with body should match")
	}
	if IsSyncCommit("fix: something") {
		t.Error("non-sync message should not match")
	}
	if IsSyncCommit("") {
		t.Error("empty should not match")
	}
}

func TestHeadSHA(t *testing.T) {
	dir := initTestRepo(t)
	want := headSHA(t, dir)

	got, err := HeadSHA(dir)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if got != want {
		t.Errorf("HeadSHA = %s, want %s", got, want)
	}
}

func TestGitDir(t *testing.T) {
	dir := initTestRepo(t)

	gitDir, err := GitDir(dir)
	if err != nil {
		t.Fatalf("GitDir: %v", err)
	}
	if !strings.HasSuffix(gitDir, "/.git") {
		t.Errorf("gitDir = %q, expected trailing /.git", gitDir)
	}
	if !filepath.IsAbs(gitDir) {
		t.Errorf("gitDir = %q, expected absolute path", gitDir)
	}
}

func TestCommitMessage(t *testing.T) {
	dir := initTestRepo(t)
	writeFile(t, dir, "docs/a.md", "a\n")
	sha := commitAll(t, dir, "chore(sync): confluence\n\npull details")

	msg, err := CommitMessage(dir, sha)
	if err != nil {
		t.Fatalf("CommitMessage: %v", err)
	}
	if !strings.HasPrefix(msg, "chore(sync): confluence") {
		t.Errorf("msg = %q", msg)
	}
	if !strings.Contains(msg, "pull details") {
		t.Errorf("body missing: %q", msg)
	}
}
