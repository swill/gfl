package gitutil

import (
	"strings"
	"testing"
)

func TestBranchExists(t *testing.T) {
	dir := initTestRepo(t)

	// initTestRepo creates a branch named "main" or "master" depending on git config;
	// figure out what the current branch is so we can assert it exists.
	current, err := CurrentBranch(dir)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if current == "" {
		t.Fatal("expected a current branch after init")
	}

	exists, err := BranchExists(dir, current)
	if err != nil {
		t.Fatalf("BranchExists(%s): %v", current, err)
	}
	if !exists {
		t.Errorf("expected branch %s to exist", current)
	}

	exists, err = BranchExists(dir, "no-such-branch")
	if err != nil {
		t.Fatalf("BranchExists(no-such-branch): %v", err)
	}
	if exists {
		t.Error("expected no-such-branch to not exist")
	}
}

func TestEnsureBranchFromHead_CreatesIfMissing(t *testing.T) {
	dir := initTestRepo(t)

	if err := EnsureBranchFromHead(dir, "confluence"); err != nil {
		t.Fatalf("EnsureBranchFromHead: %v", err)
	}
	exists, err := BranchExists(dir, "confluence")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("expected confluence branch to exist after EnsureBranchFromHead")
	}
}

func TestEnsureBranchFromHead_IdempotentWhenExists(t *testing.T) {
	dir := initTestRepo(t)

	if err := EnsureBranchFromHead(dir, "confluence"); err != nil {
		t.Fatal(err)
	}
	// Second call must not error.
	if err := EnsureBranchFromHead(dir, "confluence"); err != nil {
		t.Errorf("second EnsureBranchFromHead returned error: %v", err)
	}
}

func TestIsClean(t *testing.T) {
	dir := initTestRepo(t)

	clean, err := IsClean(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !clean {
		t.Error("expected fresh repo to be clean")
	}

	// Modify a tracked file.
	writeFile(t, dir, "README.md", "# changed\n")
	clean, err = IsClean(dir)
	if err != nil {
		t.Fatal(err)
	}
	if clean {
		t.Error("expected dirty after modifying tracked file")
	}

	// Reset and add an untracked file — IsClean should remain true.
	run(t, dir, "git", "checkout", "--", "README.md")
	writeFile(t, dir, "untracked.txt", "x\n")
	clean, err = IsClean(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !clean {
		t.Error("expected clean despite untracked file (IsClean ignores untracked)")
	}
}

func TestCommitAllOnHead_NoChangesIsNoop(t *testing.T) {
	dir := initTestRepo(t)
	sha, err := CommitAllOnHead(dir, "test commit")
	if err != nil {
		t.Fatalf("CommitAllOnHead: %v", err)
	}
	if sha != "" {
		t.Errorf("expected empty SHA on no-op commit, got %s", sha)
	}
}

func TestCommitAllOnHead_StagesAndCommits(t *testing.T) {
	dir := initTestRepo(t)
	writeFile(t, dir, "new.md", "# new\n")

	sha, err := CommitAllOnHead(dir, "add new.md")
	if err != nil {
		t.Fatalf("CommitAllOnHead: %v", err)
	}
	if sha == "" {
		t.Fatal("expected non-empty SHA after committing real change")
	}
	// Verify the commit message is what we passed.
	msg, err := CommitMessage(dir, sha)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(msg, "add new.md") {
		t.Errorf("commit message: got %q, want prefix 'add new.md'", msg)
	}
}

func TestMergeFrom_FastForward(t *testing.T) {
	dir := initTestRepo(t)
	mainBranch, _ := CurrentBranch(dir)

	// Create a side branch with one commit.
	if err := EnsureBranchFromHead(dir, "side"); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "checkout", "side")
	writeFile(t, dir, "side.md", "side\n")
	if _, err := CommitAllOnHead(dir, "side commit"); err != nil {
		t.Fatal(err)
	}

	// Back to main; merge side. Should fast-forward.
	run(t, dir, "git", "checkout", mainBranch)
	conflict, err := MergeFrom(dir, "side")
	if err != nil {
		t.Fatalf("MergeFrom: %v", err)
	}
	if conflict {
		t.Error("expected no conflict on fast-forward")
	}
}

func TestMergeFrom_ConflictReportedNotErrored(t *testing.T) {
	dir := initTestRepo(t)
	mainBranch, _ := CurrentBranch(dir)

	// Branch off, edit a file.
	if err := EnsureBranchFromHead(dir, "side"); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "checkout", "side")
	writeFile(t, dir, "README.md", "side version\n")
	if _, err := CommitAllOnHead(dir, "side edit"); err != nil {
		t.Fatal(err)
	}

	// Back to main, edit the same file differently.
	run(t, dir, "git", "checkout", mainBranch)
	writeFile(t, dir, "README.md", "main version\n")
	if _, err := CommitAllOnHead(dir, "main edit"); err != nil {
		t.Fatal(err)
	}

	// Merge should conflict.
	conflict, err := MergeFrom(dir, "side")
	if err != nil {
		t.Fatalf("MergeFrom returned err on conflict: %v", err)
	}
	if !conflict {
		t.Fatal("expected conflict signal")
	}
	// Clean up so the test repo isn't left in a merge state for any
	// follow-up assertions in the harness.
	if err := AbortMerge(dir); err != nil {
		t.Fatalf("AbortMerge: %v", err)
	}
	clean, _ := IsClean(dir)
	if !clean {
		t.Error("expected clean working tree after AbortMerge")
	}
}

func TestDiffBranches_AddRenameDelete(t *testing.T) {
	dir := initTestRepo(t)
	mainBranch, _ := CurrentBranch(dir)

	// On main, create a baseline of three files.
	writeFile(t, dir, "a.md", "a\n")
	writeFile(t, dir, "b.md", "b\n")
	writeFile(t, dir, "c.md", "c\n")
	if _, err := CommitAllOnHead(dir, "baseline"); err != nil {
		t.Fatal(err)
	}

	// Create a "head" branch that:
	//   - adds d.md
	//   - renames a.md -> a-renamed.md
	//   - deletes b.md
	//   - leaves c.md untouched
	if err := EnsureBranchFromHead(dir, "head"); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "checkout", "head")
	run(t, dir, "git", "mv", "a.md", "a-renamed.md")
	run(t, dir, "git", "rm", "b.md")
	writeFile(t, dir, "d.md", "d\n")
	if _, err := CommitAllOnHead(dir, "changes on head"); err != nil {
		t.Fatal(err)
	}

	diffs, err := DiffBranches(dir, mainBranch, "head", "*.md")
	if err != nil {
		t.Fatalf("DiffBranches: %v", err)
	}

	got := map[string]FileDiff{}
	for _, d := range diffs {
		// Key by the operation's canonical path.
		key := d.Path
		if d.Action == ActionRenamed {
			key = d.OldPath + "->" + d.Path
		}
		got[key] = d
	}

	if d, ok := got["d.md"]; !ok || d.Action != ActionAdded {
		t.Errorf("expected A d.md, got %+v", got)
	}
	if d, ok := got["b.md"]; !ok || d.Action != ActionDeleted {
		t.Errorf("expected D b.md, got %+v", got)
	}
	if d, ok := got["a.md->a-renamed.md"]; !ok || d.Action != ActionRenamed {
		t.Errorf("expected R a.md->a-renamed.md, got %+v", got)
	}
	if _, ok := got["c.md"]; ok {
		t.Errorf("c.md was untouched, should not appear in diff: %+v", got)
	}
}

func TestReadFileAtRef(t *testing.T) {
	dir := initTestRepo(t)
	mainBranch, _ := CurrentBranch(dir)

	// initTestRepo writes README.md = "# test\n" and commits it.
	got, err := ReadFileAtRef(dir, mainBranch, "README.md")
	if err != nil {
		t.Fatalf("ReadFileAtRef: %v", err)
	}
	if string(got) != "# test\n" {
		t.Errorf("got %q, want %q", string(got), "# test\n")
	}

	// A path that doesn't exist at the ref should error.
	if _, err := ReadFileAtRef(dir, mainBranch, "nope.md"); err == nil {
		t.Error("expected error reading nonexistent path")
	}
}
