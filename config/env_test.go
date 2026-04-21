package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// clearCredentialEnv unsets all three credential env vars for the test.
func clearCredentialEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CONFLUENCE_BASE_URL", "")
	t.Setenv("CONFLUENCE_USER", "")
	t.Setenv("CONFLUENCE_API_TOKEN", "")
}

func TestLoadCredentials_FromEnvVars(t *testing.T) {
	t.Setenv("CONFLUENCE_BASE_URL", "https://test.atlassian.net/wiki")
	t.Setenv("CONFLUENCE_USER", "user@test.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "tok123")

	creds, err := LoadCredentials(t.TempDir())
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if creds.BaseURL != "https://test.atlassian.net/wiki" {
		t.Errorf("BaseURL: %q", creds.BaseURL)
	}
	if creds.User != "user@test.com" {
		t.Errorf("User: %q", creds.User)
	}
	if creds.APIToken != "tok123" {
		t.Errorf("APIToken: %q", creds.APIToken)
	}
}

func TestLoadCredentials_FromEnvFile(t *testing.T) {
	clearCredentialEnv(t)
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	os.WriteFile(envFile, []byte(
		"CONFLUENCE_BASE_URL=https://file.atlassian.net/wiki\n"+
			"CONFLUENCE_USER=file@test.com\n"+
			"CONFLUENCE_API_TOKEN=filetok\n",
	), 0o644)

	creds, err := LoadCredentials(dir)
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if creds.BaseURL != "https://file.atlassian.net/wiki" {
		t.Errorf("BaseURL: %q", creds.BaseURL)
	}
	if creds.User != "file@test.com" {
		t.Errorf("User: %q", creds.User)
	}
	if creds.APIToken != "filetok" {
		t.Errorf("APIToken: %q", creds.APIToken)
	}
}

func TestLoadCredentials_EnvVarsPrecedence(t *testing.T) {
	// Env var overrides .env file value.
	t.Setenv("CONFLUENCE_BASE_URL", "https://env.atlassian.net/wiki")
	t.Setenv("CONFLUENCE_USER", "env@test.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "envtok")

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env"), []byte(
		"CONFLUENCE_BASE_URL=https://file.atlassian.net/wiki\n"+
			"CONFLUENCE_USER=file@test.com\n"+
			"CONFLUENCE_API_TOKEN=filetok\n",
	), 0o644)

	creds, err := LoadCredentials(dir)
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if creds.BaseURL != "https://env.atlassian.net/wiki" {
		t.Errorf("env var should win: BaseURL=%q", creds.BaseURL)
	}
	if creds.User != "env@test.com" {
		t.Errorf("env var should win: User=%q", creds.User)
	}
}

func TestLoadCredentials_MissingAll(t *testing.T) {
	clearCredentialEnv(t)
	_, err := LoadCredentials(t.TempDir())
	if err == nil {
		t.Fatal("expected error when all credentials missing")
	}
	msg := err.Error()
	for _, key := range []string{"CONFLUENCE_BASE_URL", "CONFLUENCE_USER", "CONFLUENCE_API_TOKEN"} {
		if !strings.Contains(msg, key) {
			t.Errorf("error should mention %s: %s", key, msg)
		}
	}
	if !strings.Contains(msg, ".env") {
		t.Error("error should reference .env setup")
	}
}

func TestLoadCredentials_MissingOne(t *testing.T) {
	t.Setenv("CONFLUENCE_BASE_URL", "https://x.atlassian.net/wiki")
	t.Setenv("CONFLUENCE_USER", "u@x.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "")

	_, err := LoadCredentials(t.TempDir())
	if err == nil {
		t.Fatal("expected error when token missing")
	}
	if !strings.Contains(err.Error(), "CONFLUENCE_API_TOKEN") {
		t.Errorf("error should mention missing var: %v", err)
	}
	// The other two should NOT be mentioned.
	if strings.Contains(err.Error(), "CONFLUENCE_BASE_URL") {
		t.Error("should not mention vars that are present")
	}
}

func TestLoadCredentials_NoEnvFile(t *testing.T) {
	// No .env file, all vars from environment.
	t.Setenv("CONFLUENCE_BASE_URL", "https://x.atlassian.net/wiki")
	t.Setenv("CONFLUENCE_USER", "u@x.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "tok")

	creds, err := LoadCredentials(t.TempDir())
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if creds.BaseURL != "https://x.atlassian.net/wiki" {
		t.Errorf("BaseURL: %q", creds.BaseURL)
	}
}

// --- parseEnvFile edge cases ------------------------------------------------

func TestParseEnvFile_CommentsAndBlanks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	os.WriteFile(path, []byte(
		"# This is a comment\n"+
			"\n"+
			"KEY=value\n"+
			"  # indented comment\n"+
			"\n"+
			"OTHER=val2\n",
	), 0o644)

	vars := parseEnvFile(path)
	if vars["KEY"] != "value" || vars["OTHER"] != "val2" {
		t.Errorf("got %v", vars)
	}
	if len(vars) != 2 {
		t.Errorf("expected 2 entries, got %d", len(vars))
	}
}

func TestParseEnvFile_Quotes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	os.WriteFile(path, []byte(
		`DOUBLE="hello world"`+"\n"+
			`SINGLE='hello world'`+"\n"+
			`NONE=hello world`+"\n",
	), 0o644)

	vars := parseEnvFile(path)
	if vars["DOUBLE"] != "hello world" {
		t.Errorf("DOUBLE: %q", vars["DOUBLE"])
	}
	if vars["SINGLE"] != "hello world" {
		t.Errorf("SINGLE: %q", vars["SINGLE"])
	}
	if vars["NONE"] != "hello world" {
		t.Errorf("NONE: %q", vars["NONE"])
	}
}

func TestParseEnvFile_ExportPrefix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	os.WriteFile(path, []byte("export MY_VAR=hello\n"), 0o644)

	vars := parseEnvFile(path)
	if vars["MY_VAR"] != "hello" {
		t.Errorf("export prefix not stripped: %v", vars)
	}
}

func TestParseEnvFile_EmptyValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	os.WriteFile(path, []byte("KEY=\n"), 0o644)

	vars := parseEnvFile(path)
	v, ok := vars["KEY"]
	if !ok {
		t.Fatal("KEY should be present")
	}
	if v != "" {
		t.Errorf("expected empty value, got %q", v)
	}
}

func TestParseEnvFile_NoEqualsSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	os.WriteFile(path, []byte("no_equals_here\nKEY=val\n"), 0o644)

	vars := parseEnvFile(path)
	if len(vars) != 1 || vars["KEY"] != "val" {
		t.Errorf("got %v", vars)
	}
}

func TestParseEnvFile_FileNotFound(t *testing.T) {
	vars := parseEnvFile("/nonexistent/.env")
	if vars != nil {
		t.Errorf("expected nil for missing file, got %v", vars)
	}
}

func TestParseEnvFile_ValueWithEquals(t *testing.T) {
	// Value containing '=' should not be split.
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	os.WriteFile(path, []byte("URL=https://x.com?a=1&b=2\n"), 0o644)

	vars := parseEnvFile(path)
	if vars["URL"] != "https://x.com?a=1&b=2" {
		t.Errorf("URL: %q", vars["URL"])
	}
}
