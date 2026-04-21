// Package config reads and writes .confluencer.json (the project
// configuration that anchors a sync scope) and loads Confluence
// credentials from environment variables.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// Config represents the .confluencer.json project configuration.
type Config struct {
	RootPageID     string `json:"confluence_root_page_id"`
	SpaceKey       string `json:"confluence_space_key"`
	LocalRoot      string `json:"local_root"`
	AttachmentsDir string `json:"attachments_dir"`
}

// LoadConfig reads and validates a .confluencer.json file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

// Save writes the config to a JSON file with deterministic formatting.
func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// Validate checks that required fields are present.
func (c *Config) Validate() error {
	if c.RootPageID == "" {
		return errors.New("confluence_root_page_id is required")
	}
	if c.LocalRoot == "" {
		return errors.New("local_root is required")
	}
	return nil
}
