// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

// Package service owns job execution: the Executor bounds solver concurrency
// and runs each requested target through the Orchestrator; the JobStore tracks
// state, logs, and results; WriteBundle streams the result zip.
package service

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/enerplanet/meme/internal/model"
)

// Executor runs submitted jobs. Concurrency across jobs is bounded by a
// semaphore so N parallel submits cannot spawn N solver processes; the context
// passed to Execute (typically the server's lifetime context) is threaded down
// to the solver process so shutdown kills running solvers instead of leaking
// them.
type Executor struct {
	Runner  Runner        // nil -> DryRunner
	MaxJobs int           // concurrent jobs; <=0 -> 2
	sem     chan struct{} // lazily sized from MaxJobs
}

// NewExecutor returns an Executor with its semaphore initialized.
func NewExecutor(r Runner, maxJobs int) *Executor {
	if maxJobs <= 0 {
		maxJobs = 2
	}
	return &Executor{Runner: r, MaxJobs: maxJobs, sem: make(chan struct{}, maxJobs)}
}

// Execute emits + runs the job for each requested target and finishes the
// record. A failing target does not stop the remaining ones (the bundle stays
// as complete as possible); the job is marked failed if any target failed.
// Blocks while the concurrency semaphore is full (the job shows as queued).
func (e *Executor) Execute(ctx context.Context, rec *JobRecord, job model.Job, targets []model.Target) {
	if e.sem == nil {
		e.sem = make(chan struct{}, 2)
	}
	select {
	case e.sem <- struct{}{}:
		defer func() { <-e.sem }()
	case <-ctx.Done():
		rec.Finish(nil, nil, fmt.Errorf("job aborted before start: %w", ctx.Err()))
		return
	}

	rec.Start()
	orch := Orchestrator{Runner: e.Runner, Log: rec.AppendLog}
	var all []RunResult
	var warns []string
	var firstErr error
	for _, target := range targets {
		base := rec.Dir()
		if len(targets) > 1 {
			base = filepath.Join(rec.Dir(), string(target))
			rec.AppendLog("=== target " + string(target) + " ===")
		}
		results, warnings, err := orch.Simulate(ctx, &job, target, base)
		all = append(all, results...)
		warns = append(warns, prefixWarnings(target, warnings, len(targets) > 1)...)
		if err != nil {
			rec.AppendLog(fmt.Sprintf("target %s failed: %v", target, err))
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", target, err)
			}
		}
	}
	_ = WriteRunResults(rec.Dir(), all)
	rec.Finish(all, warns, firstErr)
}

// prefixWarnings labels warnings with their target when a job spans several.
func prefixWarnings(target model.Target, warns []string, multi bool) []string {
	if !multi {
		return warns
	}
	out := make([]string, len(warns))
	for i, w := range warns {
		out[i] = string(target) + ": " + w
	}
	return out
}
