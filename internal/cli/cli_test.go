// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func writeEnv(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The core promise: CLI flag > .env file > built-in default.
func TestParseArgsPrecedence(t *testing.T) {
	t.Setenv("MEME_ENV_FILE", "")
	envPath := writeEnv(t, "ADDR=:17001\nEXEC=true\nWORK=/from/env\nAPI_KEY=env-secret\n")

	// .env supplies values; no matching flags -> .env wins over defaults.
	cfg, err := ParseArgs([]string{"-env-file", envPath})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":17001" || !cfg.Exec || cfg.WorkRoot != "/from/env" || cfg.APIKey != "env-secret" {
		t.Errorf("env layer: got %+v", cfg)
	}
	if !cfg.EnvLoaded {
		t.Error("expected EnvLoaded=true")
	}

	// A CLI flag overrides the .env value; unset flags keep the .env value.
	cfg, err = ParseArgs([]string{"-env-file", envPath, "-addr", ":17002", "-api-key", "flag-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":17002" {
		t.Errorf("CLI should override .env addr, got %q", cfg.Addr)
	}
	if cfg.APIKey != "flag-secret" {
		t.Errorf("CLI -api-key should override .env API_KEY, got %q", cfg.APIKey)
	}
	if !cfg.Exec || cfg.WorkRoot != "/from/env" {
		t.Errorf("unset flags should keep .env values, got %+v", cfg)
	}
}

// PORT is promoted to :PORT, and an explicit ADDR wins over PORT.
func TestParseArgsPortAndAddr(t *testing.T) {
	t.Setenv("MEME_ENV_FILE", "")

	portOnly := writeEnv(t, "PORT=9000\n")
	cfg, err := ParseArgs([]string{"-env-file", portOnly})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":9000" {
		t.Errorf("PORT should promote to :9000, got %q", cfg.Addr)
	}

	both := writeEnv(t, "PORT=9000\nADDR=127.0.0.1:7000\n")
	cfg, err = ParseArgs([]string{"-env-file", both})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:7000" {
		t.Errorf("ADDR should win over PORT, got %q", cfg.Addr)
	}

	// CLI -addr still beats both.
	cfg, err = ParseArgs([]string{"-env-file", both, "-addr", ":6000"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":6000" {
		t.Errorf("CLI -addr should win, got %q", cfg.Addr)
	}
}

// With no .env present, built-in defaults apply.
func TestParseArgsDefaults(t *testing.T) {
	t.Setenv("MEME_ENV_FILE", "")
	missing := filepath.Join(t.TempDir(), "none.env")
	cfg, err := ParseArgs([]string{"-env-file", missing})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":8080" || cfg.Exec || cfg.WorkRoot != "" || cfg.APIKey != "" {
		t.Errorf("defaults: got %+v (API_KEY default must be empty = auth disabled)", cfg)
	}
	if cfg.CORSOrigins != nil {
		t.Errorf("CORS_ORIGINS default must be nil = CORS disabled, got %v", cfg.CORSOrigins)
	}
	if cfg.EnvLoaded {
		t.Error("expected EnvLoaded=false for missing file")
	}
}

// CORS_ORIGINS is comma-split and trimmed; the -cors-origins flag overrides it.
func TestParseArgsCORSOrigins(t *testing.T) {
	t.Setenv("MEME_ENV_FILE", "")
	envPath := writeEnv(t, "CORS_ORIGINS=https://a.test, https://*.b.test,\n")

	cfg, err := ParseArgs([]string{"-env-file", envPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.CORSOrigins) != 2 || cfg.CORSOrigins[0] != "https://a.test" || cfg.CORSOrigins[1] != "https://*.b.test" {
		t.Errorf(".env origins should be split and trimmed, got %v", cfg.CORSOrigins)
	}

	cfg, err = ParseArgs([]string{"-env-file", envPath, "-cors-origins", "*"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.CORSOrigins) != 1 || cfg.CORSOrigins[0] != "*" {
		t.Errorf("CLI -cors-origins should override .env, got %v", cfg.CORSOrigins)
	}
}

// ParseArgs returns flag errors (ContinueOnError) instead of exiting the
// process — the contract that makes it safe to call from tests.
func TestParseArgsFlagError(t *testing.T) {
	t.Setenv("MEME_ENV_FILE", "")
	if _, err := ParseArgs([]string{"-no-such-flag"}); err == nil {
		t.Error("unknown flag must return an error, not exit")
	}
}
