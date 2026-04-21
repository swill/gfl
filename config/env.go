package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Credentials holds the Confluence connection details loaded from
// environment variables (and optionally supplemented by a .env file).
type Credentials struct {
	BaseURL  string
	User     string
	APIToken string
}

// LoadCredentials reads Confluence credentials from environment variables.
// If a .env file exists at repoRoot/.env, its values are used as fallbacks
// for any variables not already set in the environment. Environment
// variables always take precedence over .env values.
//
// Returns a descriptive error referencing .env setup if any of the three
// required variables (CONFLUENCE_BASE_URL, CONFLUENCE_USER,
// CONFLUENCE_API_TOKEN) are missing.
func LoadCredentials(repoRoot string) (*Credentials, error) {
	// Parse .env if present — values supplement but never override env.
	envFile := parseEnvFile(filepath.Join(repoRoot, ".env"))

	get := func(key string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return envFile[key]
	}

	creds := &Credentials{
		BaseURL:  get("CONFLUENCE_BASE_URL"),
		User:     get("CONFLUENCE_USER"),
		APIToken: get("CONFLUENCE_API_TOKEN"),
	}

	var missing []string
	if creds.BaseURL == "" {
		missing = append(missing, "CONFLUENCE_BASE_URL")
	}
	if creds.User == "" {
		missing = append(missing, "CONFLUENCE_USER")
	}
	if creds.APIToken == "" {
		missing = append(missing, "CONFLUENCE_API_TOKEN")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf(
			"missing required credentials: %s\n"+
				"Set them in .env at the repository root or export them in your shell.\n"+
				"See .env.example for the expected format.",
			strings.Join(missing, ", "),
		)
	}

	return creds, nil
}

// parseEnvFile reads a simple KEY=VALUE file (one pair per line). Lines
// starting with # and blank lines are skipped. Optional quoting (single
// or double) around values is stripped. The "export" prefix is tolerated.
// Returns nil if the file does not exist or cannot be read.
func parseEnvFile(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	vars := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		key = strings.TrimPrefix(key, "export ")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		// Strip matching surrounding quotes.
		if len(value) >= 2 {
			if (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
				value = value[1 : len(value)-1]
			}
		}
		if key != "" {
			vars[key] = value
		}
	}
	return vars
}
