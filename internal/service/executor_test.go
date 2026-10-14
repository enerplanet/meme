// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package service_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/service"
	"github.com/enerplanet/meme/internal/target"
	_ "github.com/enerplanet/meme/internal/target/all"
)

// gaugeRunner records the peak number of concurrently running runs.
type gaugeRunner struct {
	current atomic.Int32
	peak    atomic.Int32
	block   time.Duration
}

func (g *gaugeRunner) Run(ctx context.Context, p target.RunPlan) service.RunResult {
	cur := g.current.Add(1)
	for {
		old := g.peak.Load()
		if cur <= old || g.peak.CompareAndSwap(old, cur) {
			break
		}
	}
	select {
	case <-time.After(g.block):
	case <-ctx.Done():
	}
	g.current.Add(-1)
	return service.RunResult{Plan: p, ExitCode: 0, Stdout: "fake"}
}

// TestExecutorBoundsConcurrency: with MaxJobs=2, six parallel submits never
// run more than two solver processes at once — the semaphore the old
// go-per-request design lacked.
func TestExecutorBoundsConcurrency(t *testing.T) {
	g := &gaugeRunner{block: 50 * time.Millisecond}
	ex := service.NewExecutor(g, 2)
	store := service.NewJobStore()
	job := loadSample(t)

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		rec := store.Create(model.TargetPyPSA, t.TempDir(), nil)
		wg.Add(1)
		go func() {
			defer wg.Done()
			ex.Execute(context.Background(), rec, job, []model.Target{model.TargetPyPSA})
		}()
	}
	wg.Wait()
	if peak := g.peak.Load(); peak > 2 {
		t.Errorf("peak concurrency %d, want <= 2", peak)
	}
}

// TestExecutorMultiTargetLayout: a multi-target job nests per-target run trees
// and labels every run with its target.
func TestExecutorMultiTargetLayout(t *testing.T) {
	ex := service.NewExecutor(service.DryRunner{}, 1)
	store := service.NewJobStore()
	dir := t.TempDir()
	rec := store.Create("pypsa,calliope", dir, nil)
	job := loadSample(t)
	// calliope needs the UC extras removed
	cc := job.Model.Technologies["ccgt"]
	cc.Operation.StartUpCost = nil
	cc.Operation.MinUptime = nil
	job.Model.Technologies["ccgt"] = cc

	ex.Execute(context.Background(), rec, job, []model.Target{model.TargetPyPSA, model.TargetCalliope})
	view := rec.View(false)
	if view.State != service.StateSucceeded {
		t.Fatalf("state = %s (%s)", view.State, view.Error)
	}
	perTarget := map[string]int{}
	for _, r := range view.Runs {
		perTarget[r.Target]++
	}
	if perTarget["pypsa"] != 1 || perTarget["calliope"] != 1 {
		t.Errorf("runs per target = %v", perTarget)
	}
	for _, p := range []string{
		filepath.Join(dir, "pypsa", "run_0", "input", "generators.csv"),
		filepath.Join(dir, "calliope", "run_0", "input", "model.yaml"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

// TestCommandRunnerTimeout: the run is killed when its budget expires — the
// dead Timeout field of the old design is now enforced.
func TestCommandRunnerTimeout(t *testing.T) {
	r := service.CommandRunner{Timeout: 100 * time.Millisecond}
	start := time.Now()
	res := r.Run(context.Background(), target.RunPlan{Command: []string{"sleep", "5"}})
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("runner did not enforce the timeout (took %s)", elapsed)
	}
	if res.ExitCode == 0 || !strings.Contains(res.Error, "aborted") {
		t.Errorf("timed-out run must fail with an aborted error, got exit=%d err=%q", res.ExitCode, res.Error)
	}
	if !res.TimedOut {
		t.Error("deadline-exceeded run must be marked TimedOut")
	}
}

// TestCommandRunnerContextCancel: server shutdown (context cancellation) kills
// the solver process instead of leaking it.
func TestCommandRunnerContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	res := service.CommandRunner{}.Run(ctx, target.RunPlan{Command: []string{"sleep", "5"}})
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("runner did not honor cancellation (took %s)", elapsed)
	}
	if res.ExitCode == 0 {
		t.Errorf("cancelled run must fail, got %+v", res)
	}
	if res.TimedOut {
		t.Error("shutdown cancellation must not be reported as a timeout")
	}
}

// TestCommandRunnerKillsProcessGroup: a solver grandchild inheriting the
// output pipes must die with the entrypoint — without process-group kill it
// would keep the pipe open (blocking the runner) and leak past the timeout.
func TestCommandRunnerKillsProcessGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	script := fmt.Sprintf("sleep 300 & echo $! > %s; exec sleep 300", pidFile)
	r := service.CommandRunner{Timeout: 200 * time.Millisecond}
	start := time.Now()
	res := r.Run(context.Background(), target.RunPlan{Command: []string{"sh", "-c", script}})
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("runner blocked on the grandchild's inherited pipe (took %s)", elapsed)
	}
	if !res.TimedOut || res.ExitCode == 0 {
		t.Errorf("timed-out run must be marked, got exit=%d timedOut=%v err=%q", res.ExitCode, res.TimedOut, res.Error)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("grandchild pidfile: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("pidfile content %q: %v", b, err)
	}
	// kill -0 succeeds on zombies too, so poll until init reaps the grandchild.
	deadline := time.Now().Add(2 * time.Second)
	for syscall.Kill(pid, 0) != syscall.ESRCH {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d survived the process-group kill", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestCommandRunnerCapsOutput: output beyond the head+tail cap is dropped with
// a truncation marker; the tail — where solver status/objective lines live —
// must survive.
func TestCommandRunnerCapsOutput(t *testing.T) {
	script := "yes 0123456789abcdef | head -c 3000000; echo; echo tail-sentinel"
	res := service.CommandRunner{}.Run(context.Background(), target.RunPlan{Command: []string{"sh", "-c", script}})
	if res.ExitCode != 0 {
		t.Fatalf("run failed: exit=%d err=%q", res.ExitCode, res.Error)
	}
	if len(res.Stdout) > 1100*1024 {
		t.Errorf("stdout not capped: %d bytes", len(res.Stdout))
	}
	if !strings.HasPrefix(res.Stdout, "0123456789abcdef\n") {
		t.Errorf("head of output lost: %q...", res.Stdout[:32])
	}
	if !strings.Contains(res.Stdout, "...[truncated ") {
		t.Error("truncation marker missing")
	}
	if !strings.Contains(res.Stdout[len(res.Stdout)-64:], "tail-sentinel") {
		t.Error("tail of output lost — consumers grep markers near the end")
	}
}

// TestCommandRunnerSeparatesStderr: Stdout stays the combined stream (the job
// log and its markers rely on it) while Stderr carries just the error stream.
func TestCommandRunnerSeparatesStderr(t *testing.T) {
	res := service.CommandRunner{}.Run(context.Background(), target.RunPlan{
		Command: []string{"sh", "-c", "echo to-stdout; echo to-stderr >&2"},
	})
	if res.ExitCode != 0 {
		t.Fatalf("run failed: exit=%d err=%q", res.ExitCode, res.Error)
	}
	if !strings.Contains(res.Stdout, "to-stdout") || !strings.Contains(res.Stdout, "to-stderr") {
		t.Errorf("Stdout must remain the combined stream, got %q", res.Stdout)
	}
	if strings.Contains(res.Stderr, "to-stdout") || !strings.Contains(res.Stderr, "to-stderr") {
		t.Errorf("Stderr must carry only the error stream, got %q", res.Stderr)
	}
}

// TestCommandRunnerStreamsLines: RunStreaming hands each output line (stdout
// and stderr) to the sink while the process is still running, so a long solve
// shows progress before it exits; a trailing partial line arrives at exit.
func TestCommandRunnerStreamsLines(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	first := make(chan struct{})
	sink := func(l string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, l)
		if len(lines) == 1 {
			close(first)
		}
	}
	done := make(chan service.RunResult, 1)
	go func() {
		done <- service.CommandRunner{}.RunStreaming(context.Background(), target.RunPlan{
			Command: []string{"sh", "-c", "echo one; echo two >&2; sleep 2; printf three"},
		}, sink)
	}()
	select {
	case <-first:
	case res := <-done:
		t.Fatalf("no line streamed before the process exited: %+v", res)
	case <-time.After(5 * time.Second):
		t.Fatal("no line streamed")
	}
	res := <-done
	if res.ExitCode != 0 || !res.Streamed {
		t.Fatalf("exit=%d streamed=%v err=%q", res.ExitCode, res.Streamed, res.Error)
	}
	mu.Lock()
	defer mu.Unlock()
	// stdout and stderr are separate pipes: order across them is not fixed.
	got := append([]string(nil), lines...)
	sort.Strings(got)
	if strings.Join(got, ",") != "one,three,two" {
		t.Errorf("streamed lines = %q", lines)
	}
	if !strings.Contains(res.Stdout, "three") {
		t.Errorf("Stdout capture must still hold the output, got %q", res.Stdout)
	}
}

// streamRunner is a StreamingRunner that prints two lines through the sink.
type streamRunner struct{}

func (streamRunner) Run(ctx context.Context, p target.RunPlan) service.RunResult {
	return streamRunner{}.RunStreaming(ctx, p, nil)
}

func (streamRunner) RunStreaming(_ context.Context, p target.RunPlan, line func(string)) service.RunResult {
	if line != nil {
		line("solver progress 1")
		line("solver progress 2")
	}
	return service.RunResult{Plan: p, Stdout: "solver progress 1\nsolver progress 2", Streamed: line != nil}
}

// TestExecutorStreamsRunOutput: with a StreamingRunner the job log carries the
// run output once, as streamed — not again as a block after the run.
func TestExecutorStreamsRunOutput(t *testing.T) {
	ex := service.NewExecutor(streamRunner{}, 1)
	rec := service.NewJobStore().Create(model.TargetPyPSA, t.TempDir(), nil)
	ex.Execute(context.Background(), rec, loadSample(t), []model.Target{model.TargetPyPSA})
	log := rec.LogText()
	if n := strings.Count(log, "solver progress 1"); n != 1 {
		t.Errorf("streamed line logged %d times, want once:\n%s", n, log)
	}
	if !strings.Contains(log, "run 0 output:\nsolver progress 1\nsolver progress 2\n") {
		t.Errorf("streamed output missing from the job log:\n%s", log)
	}
}

// TestJobLogOnDisk: the console log is written through to log.txt in the job
// dir (not held in memory) and read back by LogText/View.
func TestJobLogOnDisk(t *testing.T) {
	store := service.NewJobStore()
	dir := t.TempDir()
	rec := store.Create(model.TargetPyPSA, dir, nil)
	rec.AppendLog("hello")
	rec.AppendLog("world")

	b, err := os.ReadFile(filepath.Join(dir, "log.txt"))
	if err != nil {
		t.Fatalf("log.txt not written: %v", err)
	}
	if string(b) != "hello\nworld\n" {
		t.Errorf("log.txt = %q", b)
	}
	if got := rec.LogText(); got != "hello\nworld\n" {
		t.Errorf("LogText = %q", got)
	}
	if v := rec.View(true); v.Log != "hello\nworld\n" {
		t.Errorf("View log = %q", v.Log)
	}
}

// TestJobLogViewCapped: a huge on-disk log is embedded head+tail only in the
// status view, and the closing lines survive the cut.
func TestJobLogViewCapped(t *testing.T) {
	store := service.NewJobStore()
	rec := store.Create(model.TargetPyPSA, t.TempDir(), nil)
	rec.AppendLog(strings.Repeat("x", 2<<20)) // 2 MiB, beyond the head+tail cap
	rec.AppendLog("all runs complete")

	got := rec.LogText()
	if len(got) > 1100*1024 {
		t.Errorf("log view not capped: %d bytes", len(got))
	}
	if !strings.Contains(got, "...[truncated ") {
		t.Error("truncation marker missing from log view")
	}
	if !strings.Contains(got[len(got)-64:], "all runs complete") {
		t.Error("log tail lost — the e2e contract greps markers near the end")
	}
	if v := rec.View(true); v.Log != got {
		t.Error("View(true) log must match LogText")
	}
}

// TestJobLogWindow: ?since= pages through the log by byte offset, ?tail= keeps
// the last lines, and both report the offset to poll from next.
func TestJobLogWindow(t *testing.T) {
	store := service.NewJobStore()
	rec := store.Create(model.TargetPyPSA, t.TempDir(), nil)
	rec.AppendLog("one")
	rec.AppendLog("two")
	rec.AppendLog("three")

	full := rec.View(true)
	if full.LogOffset == nil || *full.LogOffset != 14 {
		t.Fatalf("View(true) log_offset = %v, want 14", full.LogOffset)
	}
	v := rec.ViewWindow(service.LogWindow{Since: 4, HasSince: true})
	if v.Log != "two\nthree\n" || *v.LogOffset != 14 {
		t.Errorf("since=4: log %q offset %d", v.Log, *v.LogOffset)
	}
	rec.AppendLog("four")
	v = rec.ViewWindow(service.LogWindow{Since: *v.LogOffset, HasSince: true})
	if v.Log != "four\n" || *v.LogOffset != 19 {
		t.Errorf("since=14 after append: log %q offset %d", v.Log, *v.LogOffset)
	}
	v = rec.ViewWindow(service.LogWindow{Since: 99, HasSince: true})
	if v.Log != "" || *v.LogOffset != 19 {
		t.Errorf("since past the end: log %q offset %d", v.Log, *v.LogOffset)
	}
	for n, want := range map[int]string{1: "four\n", 2: "three\nfour\n", 9: "one\ntwo\nthree\nfour\n"} {
		v = rec.ViewWindow(service.LogWindow{Tail: n})
		if v.Log != want || *v.LogOffset != 19 {
			t.Errorf("tail=%d: log %q offset %d", n, v.Log, *v.LogOffset)
		}
	}
	if v = rec.ViewWindow(service.LogWindow{}); v.Log != "one\ntwo\nthree\nfour\n" {
		t.Errorf("empty window must be the full view, got %q", v.Log)
	}
}

// TestJobStoreGC: finished jobs older than the TTL are dropped — record and
// work directory both; running and fresh jobs survive.
func TestJobStoreGC(t *testing.T) {
	store := service.NewJobStore()

	oldDir := t.TempDir()
	oldRec := store.Create(model.TargetPyPSA, oldDir, nil)
	oldRec.Start()
	oldRec.Finish(nil, nil, nil)

	running := store.Create(model.TargetPyPSA, t.TempDir(), nil)
	running.Start()

	// Sweep with a cutoff in the future: the finished job is "older", the
	// running one must survive.
	store.StartGC(contextWithQuickCancel(t), time.Nanosecond, time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := store.Get(oldRec.ID()); !ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, ok := store.Get(oldRec.ID()); ok {
		t.Fatal("finished job should have been GC'd")
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("job dir should have been removed, stat err = %v", err)
	}
	if _, ok := store.Get(running.ID()); !ok {
		t.Error("running job must survive GC")
	}
}

func contextWithQuickCancel(t *testing.T) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}
