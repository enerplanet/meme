// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

// Command meme serves the energymodel REST API. It wires the CLI/env config
// into the API server, bounds solver concurrency and runtime, garbage-collects
// finished jobs, and shuts down gracefully: SIGINT/SIGTERM stops accepting
// requests, drains in-flight handlers, and kills running solver processes via
// the job context.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/enerplanet/meme/internal/api"
	"github.com/enerplanet/meme/internal/cli"
	"github.com/enerplanet/meme/internal/service"
)

const (
	solverTimeout   = 10 * time.Minute // per-run solver budget
	maxParallelJobs = 2                // concurrent jobs (each may spawn a solver)
	jobTTL          = 24 * time.Hour   // finished jobs older than this are GC'd
	gcInterval      = 15 * time.Minute
	shutdownGrace   = 10 * time.Second
)

func main() {
	cfg := cli.Parse()
	if cfg.EnvLoaded {
		log.Printf("loaded env file %s", cfg.EnvFile)
	}

	// jobCtx is the lifetime handed to running solvers: cancelled on shutdown
	// so no child process outlives the server.
	jobCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var runner service.Runner
	if cfg.Exec {
		runner = service.CommandRunner{Timeout: solverTimeout}
	}
	store := service.NewJobStore()
	store.StartGC(jobCtx, jobTTL, gcInterval)

	// Fail fast on a CORS misconfiguration (e.g. "*" with credentials, or an
	// origin with a trailing slash that would silently never match).
	corsCfg := api.CORSConfig{AllowedOrigins: cfg.CORSOrigins}
	if err := corsCfg.Validate(); err != nil {
		log.Fatal(err)
	}

	handler := api.NewServer(api.Server{
		Executor:   service.NewExecutor(runner, maxParallelJobs),
		WorkRoot:   cfg.WorkRoot,
		Store:      store,
		JobContext: jobCtx,
		APIKey:     cfg.APIKey,
		CORS:       corsCfg,
	})

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	auth := "disabled (no API_KEY configured)"
	if cfg.APIKey != "" {
		auth = "enabled"
	}
	cors := "disabled"
	if len(cfg.CORSOrigins) > 0 {
		cors = "enabled"
	}
	log.Printf("energymodel API listening on %s (exec=%v, auth %s, cors %s)", cfg.Addr, cfg.Exec, auth, cors)

	select {
	case err := <-errCh:
		log.Fatal(err)
	case <-jobCtx.Done():
		log.Printf("shutdown signal received; draining for up to %s", shutdownGrace)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			log.Printf("shutdown: %v", err)
		}
	}
}
