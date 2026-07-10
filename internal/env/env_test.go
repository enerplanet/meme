// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package env

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := `# server config
export ADDR=":9090"
WORK = /var/meme/work
EXEC=true
QUOTED='single'

# trailing comment line
MALFORMED_NO_EQ
=novalue
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	vars, loaded := Load(path)
	if !loaded {
		t.Fatal("expected loaded=true for existing file")
	}
	want := map[string]string{
		"ADDR":   ":9090",
		"WORK":   "/var/meme/work",
		"EXEC":   "true",
		"QUOTED": "single",
	}
	for k, v := range want {
		if vars[k] != v {
			t.Errorf("vars[%q] = %q, want %q", k, vars[k], v)
		}
	}
	if _, ok := vars["MALFORMED_NO_EQ"]; ok {
		t.Error("line without '=' should be skipped")
	}
	if _, ok := vars[""]; ok {
		t.Error("empty key should be skipped")
	}
}

func TestLoadMissing(t *testing.T) {
	vars, loaded := Load(filepath.Join(t.TempDir(), "does-not-exist"))
	if loaded {
		t.Error("expected loaded=false for missing file")
	}
	if len(vars) != 0 {
		t.Errorf("expected empty map, got %v", vars)
	}
}

func TestBool(t *testing.T) {
	cases := []struct {
		in       string
		fallback bool
		want     bool
	}{
		{"1", false, true}, {"true", false, true}, {"YES", false, true}, {"on", false, true},
		{"0", true, false}, {"false", true, false}, {"no", true, false}, {"off", true, false},
		{"", true, true}, {"", false, false},
		{"garbage", true, true}, {"garbage", false, false},
	}
	for _, c := range cases {
		if got := Bool(c.in, c.fallback); got != c.want {
			t.Errorf("Bool(%q, %v) = %v, want %v", c.in, c.fallback, got, c.want)
		}
	}
}

func TestResolvePath(t *testing.T) {
	t.Setenv("MEME_ENV_FILE", "")
	if got := ResolvePath(nil); got != ".env" {
		t.Errorf("default = %q, want .env", got)
	}
	if got := ResolvePath([]string{"-env-file", "custom.env"}); got != "custom.env" {
		t.Errorf("-env-file space form = %q", got)
	}
	if got := ResolvePath([]string{"--env-file=other.env"}); got != "other.env" {
		t.Errorf("--env-file= form = %q", got)
	}
	t.Setenv("MEME_ENV_FILE", "from-env.env")
	if got := ResolvePath(nil); got != "from-env.env" {
		t.Errorf("MEME_ENV_FILE = %q", got)
	}
	// A -env-file arg still beats the environment variable.
	if got := ResolvePath([]string{"-env-file=win.env"}); got != "win.env" {
		t.Errorf("arg over env = %q", got)
	}
}
