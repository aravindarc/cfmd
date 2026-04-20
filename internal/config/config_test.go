package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	content := `# comment
CFMD_BASE_URL=https://example.atlassian.net/wiki
export CFMD_USERNAME="alice@example.com"
CFMD_TOKEN='secret'
CFMD_DEFAULT_SPACE=ENG
`
	if err := os.WriteFile(env, []byte(content), 0600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	// Ensure none of these are set in the process env.
	for _, k := range []string{"CFMD_BASE_URL", "CFMD_USERNAME", "CFMD_TOKEN", "CFMD_DEFAULT_SPACE"} {
		os.Unsetenv(k)
	}
	t.Cleanup(func() {
		for _, k := range []string{"CFMD_BASE_URL", "CFMD_USERNAME", "CFMD_TOKEN", "CFMD_DEFAULT_SPACE"} {
			os.Unsetenv(k)
		}
	})

	if err := LoadDotEnvIfPresent(env); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := os.Getenv("CFMD_BASE_URL"); got != "https://example.atlassian.net/wiki" {
		t.Errorf("base_url = %q", got)
	}
	if got := os.Getenv("CFMD_USERNAME"); got != "alice@example.com" {
		t.Errorf("username = %q (quote stripping?)", got)
	}
	if got := os.Getenv("CFMD_TOKEN"); got != "secret" {
		t.Errorf("token = %q", got)
	}
	if got := os.Getenv("CFMD_DEFAULT_SPACE"); got != "ENG" {
		t.Errorf("space = %q", got)
	}
}

func TestLoadDotEnvDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	os.WriteFile(env, []byte("CFMD_TOKEN=from_file\n"), 0600)

	os.Setenv("CFMD_TOKEN", "from_env")
	t.Cleanup(func() { os.Unsetenv("CFMD_TOKEN") })

	if err := LoadDotEnvIfPresent(env); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := os.Getenv("CFMD_TOKEN"); got != "from_env" {
		t.Errorf("env var was overwritten: %q", got)
	}
}

func TestLoadDotEnvMissingIsOK(t *testing.T) {
	if err := LoadDotEnvIfPresent(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Errorf("missing .env should be ok, got %v", err)
	}
}

func TestRequireAuth(t *testing.T) {
	c := &Config{}
	if err := c.RequireAuth(); err == nil {
		t.Errorf("expected error on empty config")
	}
	c = &Config{BaseURL: "https://x", Username: "u", Token: "t"}
	if err := c.RequireAuth(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoad_TrimsTrailingSlash(t *testing.T) {
	os.Setenv("CFMD_BASE_URL", "https://x.atlassian.net/wiki/")
	t.Cleanup(func() { os.Unsetenv("CFMD_BASE_URL") })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.BaseURL != "https://x.atlassian.net/wiki" {
		t.Errorf("trailing slash not trimmed: %q", cfg.BaseURL)
	}
}
