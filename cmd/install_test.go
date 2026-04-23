package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644)
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-m", "initial")
	return dir
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %s: %v", name, args, out, err)
	}
}

func TestRunInstall(t *testing.T) {
	dir := initTestRepo(t)

	// Create hook shims.
	hooksDir := filepath.Join(dir, ".confluencer", "hooks")
	os.MkdirAll(hooksDir, 0o755)
	for _, name := range hookNames {
		content := "#!/bin/sh\necho " + name + "\n"
		os.WriteFile(filepath.Join(hooksDir, name), []byte(content), 0o644)
	}

	// Run install from the repo directory.
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	var buf bytes.Buffer
	installCmd.SetOut(&buf)
	err := installCmd.RunE(installCmd, nil)
	if err != nil {
		t.Fatalf("runInstall: %v", err)
	}

	// Verify hooks were installed.
	for _, name := range hookNames {
		hookPath := filepath.Join(dir, ".git", "hooks", name)
		info, err := os.Stat(hookPath)
		if err != nil {
			t.Errorf("hook %s not installed: %v", name, err)
			continue
		}
		// Should be executable.
		if info.Mode()&0o111 == 0 {
			t.Errorf("hook %s is not executable", name)
		}
		// Content should match source.
		got, _ := os.ReadFile(hookPath)
		want, _ := os.ReadFile(filepath.Join(hooksDir, name))
		if string(got) != string(want) {
			t.Errorf("hook %s content mismatch", name)
		}
	}

	output := buf.String()
	if output == "" {
		t.Error("expected output from install")
	}
}

func TestRunInstall_MissingHooksDir(t *testing.T) {
	dir := initTestRepo(t)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	// Don't create .confluencer/hooks/ — should fail.
	err := installCmd.RunE(installCmd, nil)
	if err == nil {
		t.Fatal("expected error when hooks dir is missing")
	}
}

func TestRunInstall_Idempotent(t *testing.T) {
	dir := initTestRepo(t)
	hooksDir := filepath.Join(dir, ".confluencer", "hooks")
	os.MkdirAll(hooksDir, 0o755)
	for _, name := range hookNames {
		os.WriteFile(filepath.Join(hooksDir, name), []byte("#!/bin/sh\n"), 0o644)
	}

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	var buf bytes.Buffer
	installCmd.SetOut(&buf)

	// Run twice — second should succeed without error.
	if err := installCmd.RunE(installCmd, nil); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if err := installCmd.RunE(installCmd, nil); err != nil {
		t.Fatalf("second install: %v", err)
	}
}
