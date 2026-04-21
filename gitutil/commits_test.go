package gitutil

import (
	"strings"
	"testing"
)

func TestParsePushRefs(t *testing.T) {
	input := "refs/heads/main abc123 refs/heads/main def456\n" +
		"refs/heads/feat aaa111 refs/heads/feat bbb222\n"

	refs, err := ParsePushRefs(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParsePushRefs: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("len = %d, want 2", len(refs))
	}
	if refs[0].LocalRef != "refs/heads/main" || refs[0].LocalSHA != "abc123" {
		t.Errorf("ref 0: %+v", refs[0])
	}
	if refs[0].RemoteRef != "refs/heads/main" || refs[0].RemoteSHA != "def456" {
		t.Errorf("ref 0: %+v", refs[0])
	}
	if refs[1].LocalRef != "refs/heads/feat" {
		t.Errorf("ref 1: %+v", refs[1])
	}
}

func TestParsePushRefs_Empty(t *testing.T) {
	refs, err := ParsePushRefs(strings.NewReader(""))
	if err != nil {
		t.Fatalf("ParsePushRefs: %v", err)
	}
	if refs != nil {
		t.Errorf("expected nil, got %v", refs)
	}
}

func TestParsePushRefs_BlankLines(t *testing.T) {
	input := "\n  \nrefs/heads/main a1 refs/heads/main b1\n\n"
	refs, err := ParsePushRefs(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Errorf("len = %d, want 1", len(refs))
	}
}

func TestParsePushRefs_MalformedSkipped(t *testing.T) {
	input := "only three fields\nrefs/heads/main a1 refs/heads/main b1\n"
	refs, err := ParsePushRefs(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Errorf("len = %d, want 1", len(refs))
	}
}

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

func TestIsNewBranch(t *testing.T) {
	ref := PushRef{RemoteSHA: zeroSHA}
	if !IsNewBranch(ref) {
		t.Error("zero SHA should be new branch")
	}
	ref.RemoteSHA = "abc123"
	if IsNewBranch(ref) {
		t.Error("non-zero SHA should not be new branch")
	}
}

func TestIsDeleteBranch(t *testing.T) {
	ref := PushRef{LocalSHA: zeroSHA}
	if !IsDeleteBranch(ref) {
		t.Error("zero local SHA should be delete branch")
	}
	ref.LocalSHA = "abc123"
	if IsDeleteBranch(ref) {
		t.Error("non-zero local SHA should not be delete branch")
	}
}

func TestListCommits(t *testing.T) {
	dir := initTestRepo(t)
	base := headSHA(t, dir)

	writeFile(t, dir, "docs/page.md", "# Page\n")
	c1 := commitAll(t, dir, "add page")

	writeFile(t, dir, "docs/page.md", "# Page v2\n")
	c2 := commitAll(t, dir, "update page")

	commits, err := ListCommits(dir, base, c2)
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("len = %d, want 2", len(commits))
	}
	if commits[0] != c1 {
		t.Errorf("commits[0] = %s, want %s", commits[0], c1)
	}
	if commits[1] != c2 {
		t.Errorf("commits[1] = %s, want %s", commits[1], c2)
	}
}

func TestListCommits_Empty(t *testing.T) {
	dir := initTestRepo(t)
	head := headSHA(t, dir)

	commits, err := ListCommits(dir, head, head)
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	if len(commits) != 0 {
		t.Errorf("expected empty, got %v", commits)
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
