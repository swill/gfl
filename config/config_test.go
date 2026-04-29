package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gfl.json")
	os.WriteFile(path, []byte(`{
		"confluence_root_page_id": "123456789",
		"confluence_space_key": "DOCS",
		"local_root": "docs/",
		"attachments_dir": "docs/_attachments"
	}`), 0o644)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.RootPageID != "123456789" {
		t.Errorf("RootPageID: got %q", cfg.RootPageID)
	}
	if cfg.SpaceKey != "DOCS" {
		t.Errorf("SpaceKey: got %q", cfg.SpaceKey)
	}
	if cfg.LocalRoot != "docs/" {
		t.Errorf("LocalRoot: got %q", cfg.LocalRoot)
	}
	if cfg.AttachmentsDir != "docs/_attachments" {
		t.Errorf("AttachmentsDir: got %q", cfg.AttachmentsDir)
	}
}

func TestLoadConfig_MissingRootPageID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gfl.json")
	os.WriteFile(path, []byte(`{"local_root":"docs/"}`), 0o644)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for missing root page ID")
	}
	if !strings.Contains(err.Error(), "confluence_root_page_id") {
		t.Errorf("error should mention missing field: %v", err)
	}
}

func TestLoadConfig_MissingLocalRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gfl.json")
	os.WriteFile(path, []byte(`{"confluence_root_page_id":"1"}`), 0o644)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for missing local_root")
	}
	if !strings.Contains(err.Error(), "local_root") {
		t.Errorf("error should mention missing field: %v", err)
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/.gfl.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gfl.json")
	os.WriteFile(path, []byte("{invalid"), 0o644)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestConfig_SaveAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gfl.json")

	cfg := &Config{
		RootPageID:     "999",
		SpaceKey:       "ENG",
		LocalRoot:      "documentation/",
		AttachmentsDir: "documentation/_attachments",
	}
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Trailing newline.
	data, _ := os.ReadFile(path)
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Error("saved file should end with newline")
	}

	// Reload.
	cfg2, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	if cfg2.RootPageID != "999" || cfg2.SpaceKey != "ENG" {
		t.Errorf("round-trip mismatch: %+v", cfg2)
	}
	if cfg2.LocalRoot != "documentation/" || cfg2.AttachmentsDir != "documentation/_attachments" {
		t.Errorf("round-trip mismatch: %+v", cfg2)
	}
}

func TestConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{"valid", Config{RootPageID: "1", LocalRoot: "docs/"}, ""},
		{"no root id", Config{LocalRoot: "docs/"}, "confluence_root_page_id"},
		{"no local root", Config{RootPageID: "1"}, "local_root"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.cfg.Validate()
			if c.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("error %q should contain %q", err, c.wantErr)
				}
			}
		})
	}
}
