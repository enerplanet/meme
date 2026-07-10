// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

// Package env is a dependency-free reader for optional .env files. It parses
// KEY=VALUE pairs so the CLI can use them as flag defaults; it imposes no
// precedence policy of its own (that lives in internal/cli).
package env

import (
	"bufio"
	"os"
	"strings"
)

// ResolvePath decides which .env file to read, highest priority first:
// a -env-file argument in args, then the MEME_ENV_FILE variable, then ".env"
// in the working directory. args is typically os.Args[1:]. The path must be
// resolved before flag defaults are built (they read the file), which is why
// this pre-scans the raw arguments rather than using the flag package.
func ResolvePath(args []string) string {
	path := ".env"
	if v := os.Getenv("MEME_ENV_FILE"); v != "" {
		path = v
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-env-file" || a == "--env-file":
			if i+1 < len(args) {
				path = args[i+1]
			}
		case strings.HasPrefix(a, "-env-file="):
			path = strings.TrimPrefix(a, "-env-file=")
		case strings.HasPrefix(a, "--env-file="):
			path = strings.TrimPrefix(a, "--env-file=")
		}
	}
	return path
}

// Load parses KEY=VALUE lines from path. A missing or unreadable file is not an
// error — it returns an empty map and loaded=false, so callers fall back to
// built-in defaults. Blank lines and #-comments are skipped, an optional
// leading "export " is allowed, and matching single/double quotes around a
// value are stripped. Later duplicates win.
func Load(path string) (vars map[string]string, loaded bool) {
	vars = map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return vars, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			continue
		}
		vars[key] = unquote(strings.TrimSpace(line[eq+1:]))
	}
	return vars, true
}

// Bool interprets a .env string as a boolean; unrecognized/empty -> fallback.
func Bool(s string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

// unquote strips a single pair of matching surrounding quotes, if present.
func unquote(s string) string {
	if len(s) >= 2 {
		if c := s[0]; (c == '"' || c == '\'') && s[len(s)-1] == c {
			return s[1 : len(s)-1]
		}
	}
	return s
}
