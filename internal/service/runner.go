// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/enerplanet/meme/internal/target"
)

// Runner executes (or plans) a RunPlan. The context bounds the process's
// lifetime: cancellation (server shutdown) or the runner's own timeout kills
// the solver instead of hanging the job forever.
type Runner interface {
	Run(ctx context.Context, p target.RunPlan) RunResult
}

// StreamingRunner is a Runner that can also hand the process output to a sink
// line by line while it runs, so a long solve's progress reaches the job log
// before the run ends.
type StreamingRunner interface {
	Runner
	RunStreaming(ctx context.Context, p target.RunPlan, line func(string)) RunResult
}

// DryRunner returns the plan without executing anything — the default, so the
// service works with no Python toolchain installed.
type DryRunner struct{}

// Run implements Runner.
func (DryRunner) Run(_ context.Context, p target.RunPlan) RunResult {
	return RunResult{Plan: p, ExitCode: 0, Stdout: "dry-run: not executed"}
}

// Output capture caps. A runaway solver can print gigabytes; the captured
// string lives on the JobRecord for up to the job TTL and is re-serialized on
// every status poll, so only the head (startup banner) and the tail (the
// status/objective lines consumers grep for) are kept.
const (
	captureHead = 512 << 10
	captureTail = 512 << 10
)

func truncationMarker(dropped int64) string {
	return fmt.Sprintf("\n...[truncated %d bytes]...\n", dropped)
}

// cappedBuffer is an io.Writer retaining the first headMax and the last
// tailMax bytes written; everything in between is dropped and only counted.
// Writes are mutex-guarded because stdout and stderr pipes are drained by
// separate goroutines into a shared combined capture.
type cappedBuffer struct {
	mu      sync.Mutex
	headMax int
	tailMax int
	head    []byte
	ring    []byte // last tailMax bytes, ring-buffered
	ringPos int    // next write offset into ring
	ringLen int    // valid bytes in ring
	total   int64  // everything ever written
}

func newCappedBuffer(headMax, tailMax int) *cappedBuffer {
	return &cappedBuffer{headMax: headMax, tailMax: tailMax}
}

// Write implements io.Writer; it never fails.
func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	b.total += int64(n)
	if room := b.headMax - len(b.head); room > 0 {
		take := min(room, len(p))
		b.head = append(b.head, p[:take]...)
		p = p[take:]
	}
	if len(p) == 0 {
		return n, nil
	}
	if b.ring == nil {
		b.ring = make([]byte, b.tailMax)
	}
	if len(p) >= b.tailMax {
		copy(b.ring, p[len(p)-b.tailMax:])
		b.ringPos = 0
		b.ringLen = b.tailMax
		return n, nil
	}
	first := copy(b.ring[b.ringPos:], p)
	copy(b.ring, p[first:])
	b.ringPos = (b.ringPos + len(p)) % b.tailMax
	b.ringLen = min(b.ringLen+len(p), b.tailMax)
	return n, nil
}

// String renders head + truncation marker + tail (marker only if bytes were
// actually dropped).
func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	overflow := b.total - int64(len(b.head))
	if overflow <= 0 {
		return string(b.head)
	}
	tail := make([]byte, b.ringLen)
	start := (b.ringPos - b.ringLen + b.tailMax) % b.tailMax
	n := copy(tail, b.ring[start:])
	copy(tail[n:], b.ring[:b.ringLen-n])
	dropped := overflow - int64(len(tail))
	if dropped <= 0 {
		return string(b.head) + string(tail)
	}
	return string(b.head) + truncationMarker(dropped) + string(tail)
}

// streamCap bounds how much output one run streams into the job log; past it
// the sink gets a single notice and the head+tail capture takes over.
const streamCap = 64 << 20

// lineWriter splits written bytes into lines for a sink. Partial lines are
// held until their newline (or Flush); the stdout and stderr pipe goroutines
// share it, hence the mutex.
type lineWriter struct {
	mu        sync.Mutex
	sink      func(string)
	buf       []byte
	sent      int
	truncated bool
}

// Write implements io.Writer; it never fails.
func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.emit(w.buf[:i])
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

// Flush emits a trailing partial line.
func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.emit(w.buf)
		w.buf = nil
	}
}

func (w *lineWriter) emit(line []byte) {
	if w.truncated {
		return
	}
	if w.sent+len(line) > streamCap {
		w.truncated = true
		w.sink(fmt.Sprintf("...[live output capped at %d MiB; the run's head and tail follow when it ends]...", streamCap>>20))
		return
	}
	w.sent += len(line) + 1
	w.sink(string(bytes.TrimRight(line, "\r")))
}

// CommandRunner shells out to the plan's command and captures output. Timeout
// (if > 0) bounds each run unless the job asks for its own budget (see
// WithRunTimeout), which MaxTimeout (if > 0) caps; an expired deadline or a
// cancelled context kills the process group and surfaces as a non-zero run.
type CommandRunner struct {
	Timeout    time.Duration
	MaxTimeout time.Duration
}

type runTimeoutKey struct{}

// WithRunTimeout attaches a job's own per-run time budget (d > 0) to ctx,
// replacing the runner's default Timeout for runs started with it.
func WithRunTimeout(ctx context.Context, d time.Duration) context.Context {
	if d <= 0 {
		return ctx
	}
	return context.WithValue(ctx, runTimeoutKey{}, d)
}

// timeout is the budget for a run started with ctx: the job's own (capped at
// MaxTimeout) or the default.
func (c CommandRunner) timeout(ctx context.Context) time.Duration {
	d, ok := ctx.Value(runTimeoutKey{}).(time.Duration)
	if !ok {
		return c.Timeout
	}
	if c.MaxTimeout > 0 && d > c.MaxTimeout {
		return c.MaxTimeout
	}
	return d
}

// Run implements Runner.
func (c CommandRunner) Run(ctx context.Context, p target.RunPlan) RunResult {
	return c.RunStreaming(ctx, p, nil)
}

// RunStreaming implements StreamingRunner: as Run, and additionally every
// output line (stdout and stderr, as printed) goes to line while the process
// runs. A nil line streams nothing. RunResult.Streamed reports whether the
// whole output reached the sink.
func (c CommandRunner) RunStreaming(ctx context.Context, p target.RunPlan, line func(string)) RunResult {
	res := RunResult{Plan: p}
	if len(p.Command) == 0 {
		res.Error = "empty command"
		res.ExitCode = -1
		return res
	}
	if d := c.timeout(ctx); d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, p.Command[0], p.Command[1:]...)
	cmd.Dir = p.WorkDir
	// Stdout stays the combined stream (the job log and downstream consumers
	// grep it for solver markers); stderr is additionally teed into its own
	// capture so RunResult.Stderr carries just the error stream.
	combined := newCappedBuffer(captureHead, captureTail)
	stderr := newCappedBuffer(captureHead, captureTail)
	cmd.Stdout = combined
	cmd.Stderr = io.MultiWriter(combined, stderr)
	var live *lineWriter
	if line != nil {
		live = &lineWriter{sink: line}
		cmd.Stdout = io.MultiWriter(combined, live)
		cmd.Stderr = io.MultiWriter(combined, stderr, live)
	}
	// The entrypoint (e.g. python run.py) spawns solver children that inherit
	// the output pipes: killing only the direct child would leave a grandchild
	// holding the pipe and block Wait forever. Run the command in its own
	// process group and kill the whole group on cancel/timeout; WaitDelay is
	// the backstop for anything that escaped the group.
	configureProcGroup(cmd)
	cmd.WaitDelay = 3 * time.Second
	err := cmd.Run()
	if live != nil {
		live.Flush()
		res.Streamed = !live.truncated
	}
	res.Stdout = combined.String()
	res.Stderr = stderr.String()
	if ctxErr := ctx.Err(); ctxErr != nil {
		res.TimedOut = errors.Is(ctxErr, context.DeadlineExceeded)
		res.Error = "run aborted: " + ctxErr.Error()
		res.ExitCode = -1
		return res
	}
	if err != nil {
		res.Error = err.Error()
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
		} else {
			res.ExitCode = -1
		}
	}
	return res
}
