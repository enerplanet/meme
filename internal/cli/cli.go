// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

// Package cli defines the server's command-line interface and resolves its
// configuration. It layers three sources, highest priority first:
//
//  1. CLI flags        (-addr, -work, -exec, -api-key, -cors-origins)
//  2. .env file        (ADDR, WORK, EXEC, API_KEY, CORS_ORIGINS) — optional,
//     via internal/env
//  3. built-in default (:8080, OS temp, dry-run, no auth, no CORS)
//
// Go's flag package only overwrites a default when a flag is actually passed,
// so seeding each default from the .env value gives exactly that precedence.
package cli

import (
	"flag"
	"os"
	"strings"

	"github.com/enerplanet/meme/internal/env"
)

// Config is the fully-resolved server configuration.
type Config struct {
	Addr        string   // listen address
	Exec        bool     // run real solvers vs. dry-run
	WorkRoot    string   // root dir for emitted files ("" -> OS temp)
	APIKey      string   // required payload api_key ("" -> auth disabled)
	CORSOrigins []string // browser origins allowed via CORS (nil -> CORS disabled)
	EnvFile     string   // .env path that was consulted
	EnvLoaded   bool     // whether that file existed and was read
}

// Parse resolves configuration from os.Args, exiting the process on a flag
// error (the conventional flag.ExitOnError behavior).
func Parse() Config {
	cfg, err := ParseArgs(os.Args[1:])
	if err != nil {
		// ParseArgs already wrote the error/usage to stderr.
		os.Exit(2)
	}
	return cfg
}

// ParseArgs resolves configuration from an explicit argument slice. It uses a
// private FlagSet (ContinueOnError) so it's safe to call from tests without
// touching global flag state or exiting the process.
func ParseArgs(args []string) (Config, error) {
	envPath := env.ResolvePath(args)
	vars, loaded := env.Load(envPath)
	get := func(key, fallback string) string {
		if v, ok := vars[key]; ok {
			return v
		}
		return fallback
	}

	// Listen address: an explicit ADDR wins (supports host:port); otherwise a
	// bare PORT is promoted to ":PORT"; otherwise the built-in default. PORT is
	// the unified key shared with docker-compose / the .env files.
	addrDefault := get("ADDR", "")
	if addrDefault == "" {
		if p := get("PORT", ""); p != "" {
			addrDefault = ":" + p
		} else {
			addrDefault = ":8080"
		}
	}

	fs := flag.NewFlagSet("meme", flag.ContinueOnError)
	envFile := fs.String("env-file", envPath, "optional KEY=VALUE env file (PORT/ADDR, WORK, EXEC); CLI flags override it")
	addr := fs.String("addr", addrDefault, "listen address")
	exec := fs.Bool("exec", env.Bool(get("EXEC", ""), false), "actually run simulators (needs pypsa/calliope/adopt on PATH); default is dry-run")
	workRoot := fs.String("work", get("WORK", ""), "root dir for emitted files (default: OS temp)")
	apiKey := fs.String("api-key", get("API_KEY", ""), "require this api_key in every request payload (default: no authentication)")
	corsOrigins := fs.String("cors-origins", get("CORS_ORIGINS", ""), `comma-separated browser origins allowed via CORS: exact origins, "*", or subdomain wildcards like https://*.example.com (default: CORS disabled)`)

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	return Config{
		Addr:        *addr,
		Exec:        *exec,
		WorkRoot:    *workRoot,
		APIKey:      *apiKey,
		CORSOrigins: splitList(*corsOrigins),
		EnvFile:     *envFile,
		EnvLoaded:   loaded,
	}, nil
}

// splitList turns a comma-separated value into its trimmed, non-empty
// elements; "" yields nil.
func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
