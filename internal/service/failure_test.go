// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package service_test

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/service"
	"github.com/enerplanet/meme/internal/target"
	_ "github.com/enerplanet/meme/internal/target/all"
)

// failRunner fails every plan whose target is in failFor and succeeds the rest.
type failRunner struct {
	failFor model.Target
}

func (f failRunner) Run(_ context.Context, p target.RunPlan) service.RunResult {
	if p.Target == f.failFor {
		return service.RunResult{Plan: p, ExitCode: 2, Stdout: "boom", Error: "solver exploded"}
	}
	return service.RunResult{Plan: p, ExitCode: 0, Stdout: "ok"}
}

// TestExecutorRunFailureMarksJobFailed: a non-zero solver exit marks the job
// failed (never silently succeeded), and the bundle still carries everything
// that was emitted plus the failing run's output.
func TestExecutorRunFailureMarksJobFailed(t *testing.T) {
	ex := service.NewExecutor(failRunner{failFor: model.TargetPyPSA}, 1)
	store := service.NewJobStore()
	rec := store.Create(model.TargetPyPSA, t.TempDir(), nil)

	ex.Execute(context.Background(), rec, loadSample(t), []model.Target{model.TargetPyPSA})

	view := rec.View(false)
	if view.State != service.StateFailed {
		t.Fatalf("state = %s, want failed", view.State)
	}
	if !strings.Contains(view.Error, "exited non-zero") {
		t.Errorf("error = %q, want non-zero-exit message", view.Error)
	}
	if len(view.Runs) != 1 || view.Runs[0].ExitCode != 2 || view.Runs[0].Stdout != "boom" {
		t.Errorf("failing run output must be preserved: %+v", view.Runs)
	}
	names := bundleNames(t, rec)
	for _, want := range []string{"metadata.json", "results.json", "files/run_0/input/generators.csv"} {
		if !names[want] {
			t.Errorf("bundle of a failed job missing %s", want)
		}
	}
}

// TestExecutorPartialTargetFailure: in a multi-target job a failing target does
// not stop the others — every target's emitted tree lands in the bundle, all
// run results are recorded, and the job as a whole is failed. (This is the
// contract promised in examples/README §5.)
func TestExecutorPartialTargetFailure(t *testing.T) {
	ex := service.NewExecutor(failRunner{failFor: model.TargetCalliope}, 1)
	store := service.NewJobStore()
	rec := store.Create("pypsa,calliope", t.TempDir(), nil)

	job := loadSample(t)
	cc := job.Model.Technologies["ccgt"] // calliope needs the UC extras removed
	cc.Operation.StartUpCost = nil
	cc.Operation.MinUptime = nil
	job.Model.Technologies["ccgt"] = cc

	ex.Execute(context.Background(), rec, job, []model.Target{model.TargetPyPSA, model.TargetCalliope})

	view := rec.View(false)
	if view.State != service.StateFailed {
		t.Fatalf("state = %s, want failed (one target failed)", view.State)
	}
	exit := map[string]int{}
	for _, r := range view.Runs {
		exit[r.Target] = r.ExitCode
	}
	if exit["pypsa"] != 0 {
		t.Errorf("pypsa run must still succeed, exit=%d", exit["pypsa"])
	}
	if exit["calliope"] != 2 {
		t.Errorf("calliope run must carry its failure, exit=%d", exit["calliope"])
	}
	names := bundleNames(t, rec)
	for _, want := range []string{
		"files/pypsa/run_0/input/generators.csv",
		"files/calliope/run_0/input/model.yaml",
	} {
		if !names[want] {
			t.Errorf("bundle missing %s — a failing target must not drop the others", want)
		}
	}
}

// holdRunner blocks its run until released (or the context ends), signalling
// when it has started.
type holdRunner struct {
	started chan struct{}
	release chan struct{}
}

func (h holdRunner) Run(ctx context.Context, p target.RunPlan) service.RunResult {
	h.started <- struct{}{}
	select {
	case <-h.release:
	case <-ctx.Done():
	}
	return service.RunResult{Plan: p, ExitCode: 0, Stdout: "released"}
}

// TestExecutorQueuedJobCancelledPromptly: graceful shutdown must fail jobs
// still waiting on the concurrency semaphore instead of leaving them queued
// forever.
func TestExecutorQueuedJobCancelledPromptly(t *testing.T) {
	h := holdRunner{started: make(chan struct{}, 1), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(h.release) }) }
	t.Cleanup(release)

	ex := service.NewExecutor(h, 1)
	store := service.NewJobStore()
	job := loadSample(t)

	// Job A occupies the only slot...
	recA := store.Create(model.TargetPyPSA, t.TempDir(), nil)
	doneA := make(chan struct{})
	go func() {
		defer close(doneA)
		ex.Execute(context.Background(), recA, job, []model.Target{model.TargetPyPSA})
	}()
	<-h.started

	// ...so job B queues on the semaphore; cancelling its context must fail it
	// promptly (A keeps the slot, so only the ctx branch can fire).
	ctx, cancel := context.WithCancel(context.Background())
	recB := store.Create(model.TargetPyPSA, t.TempDir(), nil)
	doneB := make(chan struct{})
	go func() {
		defer close(doneB)
		ex.Execute(ctx, recB, job, []model.Target{model.TargetPyPSA})
	}()
	time.Sleep(20 * time.Millisecond) // let B reach the semaphore wait
	cancel()
	select {
	case <-doneB:
	case <-time.After(2 * time.Second):
		t.Fatal("queued job did not return promptly after cancel")
	}
	view := recB.View(false)
	if view.State != service.StateFailed || !strings.Contains(view.Error, "aborted before start") {
		t.Errorf("queued job state = %s err = %q, want failed with aborted-before-start", view.State, view.Error)
	}

	release()
	select {
	case <-doneA:
	case <-time.After(2 * time.Second):
		t.Fatal("held job did not finish after release")
	}
	if got := recA.View(false).State; got != service.StateSucceeded {
		t.Errorf("held job state = %s, want succeeded", got)
	}
}

// TestExecutorPreRunTargetFailure: a target failing before any run — here
// calliope rejecting the sample's UC extras (start_up_cost/min_uptime) at
// validation — must not stop the other targets: pypsa's results and files
// land, the job is failed, and the error names the broken target.
func TestExecutorPreRunTargetFailure(t *testing.T) {
	ex := service.NewExecutor(service.DryRunner{}, 1)
	store := service.NewJobStore()
	rec := store.Create("pypsa,calliope", t.TempDir(), nil)

	ex.Execute(context.Background(), rec, loadSample(t), []model.Target{model.TargetPyPSA, model.TargetCalliope})

	view := rec.View(false)
	if view.State != service.StateFailed {
		t.Fatalf("state = %s, want failed", view.State)
	}
	if !strings.Contains(view.Error, "calliope") {
		t.Errorf("error = %q, must name the broken target", view.Error)
	}
	perTarget := map[string]int{}
	for _, r := range view.Runs {
		perTarget[r.Target]++
	}
	if perTarget["pypsa"] != 1 || perTarget["calliope"] != 0 {
		t.Errorf("runs per target = %v, want pypsa's run only", perTarget)
	}
	if !bundleNames(t, rec)["files/pypsa/run_0/input/generators.csv"] {
		t.Error("surviving target's emitted files missing from the bundle")
	}
}

// TestJobStoreGCUnderChurn: jobs being created and finished while the sweeper
// runs — the interlocking JobRecord/JobStore mutexes must hold up (this test
// is what `go test -race ./internal/service` is for).
func TestJobStoreGCUnderChurn(t *testing.T) {
	store := service.NewJobStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.StartGC(ctx, time.Nanosecond, time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				rec := store.Create(model.TargetPyPSA, t.TempDir(), nil)
				rec.Start()
				rec.AppendLog("line")
				_ = rec.View(true)
				rec.Finish(nil, nil, nil)
				_, _ = store.Get(rec.ID())
			}
		}()
	}
	wg.Wait()
}

// TestJobStoreGCRacesBundleReads: the sweeper dropping a record and removing
// its work dir must coexist with in-flight WriteBundle/LogText readers —
// graceful degradation (empty log, sparse bundle) is fine, races and panics
// are not (this is what `go test -race` pins).
func TestJobStoreGCRacesBundleReads(t *testing.T) {
	store := service.NewJobStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.StartGC(ctx, time.Nanosecond, time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 30; k++ {
				rec := store.Create(model.TargetPyPSA, t.TempDir(), nil)
				rec.Start()
				rec.AppendLog("line for bundle")
				rec.Finish(nil, nil, nil)
				// The record is sweep-eligible now: read while it may be
				// dropped and its dir removed under us.
				var buf bytes.Buffer
				_ = service.WriteBundle(&buf, rec)
				_ = rec.LogText()
				_ = rec.View(true)
			}
		}()
	}
	wg.Wait()
}

func bundleNames(t *testing.T, rec *service.JobRecord) map[string]bool {
	t.Helper()
	var buf bytes.Buffer
	if err := service.WriteBundle(&buf, rec); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip open: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	return names
}
