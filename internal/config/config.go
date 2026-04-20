// Package config loads cfmd configuration from environment variables, with
// optional .env file support in the current directory.
//
// Precedence (highest to lowest):
//  1. Process environment variable
//  2. Entry in ./.env (if present)
//  3. Compiled-in default
//
// Env var names use the CFMD_ prefix.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds every runtime setting cfmd supports.
type Config struct {
	// BaseURL is the Confluence Cloud wiki URL, e.g.
	// "https://yourco.atlassian.net/wiki". Required for push/pull.
	BaseURL string

	// Username is the account email address used for Basic auth.
	Username string

	// Token is the Atlassian API token. Create one at
	// https://id.atlassian.com/manage-profile/security/api-tokens.
	Token string

	// DefaultSpace is the Confluence space key used when frontmatter omits
	// 'space' on first push.
	DefaultSpace string

	// DefaultParentID, if set, is used as the parent page for newly-created
	// pages when frontmatter omits 'parent_id'.
	DefaultParentID string

	// TimeoutSeconds is the per-request HTTP timeout. Defaults to 30.
	TimeoutSeconds int

	// CacheDir is where three-way conflict-detection snapshots are stored.
	// Defaults to "$XDG_CACHE_HOME/cfmd" or "$HOME/.cache/cfmd".
	CacheDir string

	// AllowInsecureTLS disables TLS certificate verification. Only respected
	// if set via CFMD_ALLOW_INSECURE_TLS=true; never enable unless you know
	// what you're doing.
	AllowInsecureTLS bool
}

// Load reads config from process env, overlaying values from ./.env where
// the process env is not set. Applies defaults for anything still empty.
// Load does NOT validate that required fields (BaseURL, Username, Token) are
// set — command handlers call RequireAuth() when they actually need them.
func Load() (*Config, error) {
	if err := LoadDotEnvIfPresent(""); err != nil {
		return nil, fmt.Errorf("load .env: %w", err)
	}
	cfg := &Config{
		BaseURL:         os.Getenv("CFMD_BASE_URL"),
		Username:        os.Getenv("CFMD_USERNAME"),
		Token:           os.Getenv("CFMD_TOKEN"),
		DefaultSpace:    os.Getenv("CFMD_DEFAULT_SPACE"),
		DefaultParentID: os.Getenv("CFMD_DEFAULT_PARENT_ID"),
		CacheDir:        os.Getenv("CFMD_CACHE_DIR"),
	}

	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	cfg.TimeoutSeconds = 30
	if raw := os.Getenv("CFMD_TIMEOUT_SECONDS"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("CFMD_TIMEOUT_SECONDS: %w", err)
		}
		cfg.TimeoutSeconds = v
	}

	if raw := os.Getenv("CFMD_ALLOW_INSECURE_TLS"); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("CFMD_ALLOW_INSECURE_TLS: %w", err)
		}
		cfg.AllowInsecureTLS = b
	}

	if cfg.CacheDir == "" {
		cfg.CacheDir = defaultCacheDir()
	}

	return cfg, nil
}

// RequireAuth returns an error if any field required to talk to Confluence
// is missing.
func (c *Config) RequireAuth() error {
	var missing []string
	if c.BaseURL == "" {
		missing = append(missing, "CFMD_BASE_URL")
	}
	if c.Username == "" {
		missing = append(missing, "CFMD_USERNAME")
	}
	if c.Token == "" {
		missing = append(missing, "CFMD_TOKEN")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required env vars: %s\n\n"+
			"Set them in your shell or in a .env file in the current directory.\n"+
			"See `cfmd init` for a template.", strings.Join(missing, ", "))
	}
	return nil
}

// LoadDotEnvIfPresent loads key=value pairs from a .env file. If path is
// empty, it looks for ".env" in the current working directory. Missing file
// is NOT an error (returns nil). Existing process env vars are NOT
// overwritten; .env only fills in values that are currently unset.
func LoadDotEnvIfPresent(path string) error {
	if path == "" {
		path = ".env"
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip a leading `export ` for bash-style compatibility.
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimPrefix(line, "export ")
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			return fmt.Errorf(".env line %d: missing '='", lineNo)
		}
		key := strings.TrimSpace(line[:eq])
		val := line[eq+1:]
		val = unquoteEnvValue(val)
		if key == "" {
			return fmt.Errorf(".env line %d: empty key", lineNo)
		}
		// Don't overwrite existing process env.
		if _, set := os.LookupEnv(key); set {
			continue
		}
		if err := os.Setenv(key, val); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// unquoteEnvValue strips surrounding single or double quotes from a value
// and unescapes basic backslash-escapes inside double quotes.
func unquoteEnvValue(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if s[0] == '\'' && s[len(s)-1] == '\'' {
			return s[1 : len(s)-1]
		}
		if s[0] == '"' && s[len(s)-1] == '"' {
			inner := s[1 : len(s)-1]
			inner = strings.ReplaceAll(inner, `\"`, `"`)
			inner = strings.ReplaceAll(inner, `\n`, "\n")
			inner = strings.ReplaceAll(inner, `\\`, `\`)
			return inner
		}
	}
	return s
}

func defaultCacheDir() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "cfmd")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".cfmd-cache")
	}
	return filepath.Join(home, ".cache", "cfmd")
}

// DotEnvTemplate returns the content to write to a new .env file.
func DotEnvTemplate() string {
	return `# cfmd configuration. Keep this file OUT OF VERSION CONTROL.
# It contains an API token.

# Your Confluence Cloud wiki URL (no trailing slash).
CFMD_BASE_URL=https://yourco.atlassian.net/wiki

# Account email for Basic auth.
CFMD_USERNAME=you@company.com

# API token created at
# https://id.atlassian.com/manage-profile/security/api-tokens
CFMD_TOKEN=

# Optional defaults used when a file's frontmatter omits them.
#CFMD_DEFAULT_SPACE=ENG
#CFMD_DEFAULT_PARENT_ID=123456

# Optional tuning.
#CFMD_TIMEOUT_SECONDS=30
#CFMD_CACHE_DIR=~/.cache/cfmd
`
}
