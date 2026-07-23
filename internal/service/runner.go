// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/enerplanet/meme/internal/target"
)

// Runner executes (or plans) a RunPlan. The context bounds the process's
// lifetime: cancellation (server shutdown) or the runner's own timeout kills
// the solver instead of hanging the job forever.
type Runner interface {
	Run(ctx context.Context, p target.RunPlan) RunResult
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

// CommandRunner shells out to the plan's command and captures output. Timeout
// (if > 0) bounds each run; an expired deadline or a cancelled context kills
// the process group and surfaces as a non-zero run.
type CommandRunner struct {
	Timeout time.Duration
}

// Run implements Runner.
func (c CommandRunner) Run(ctx context.Context, p target.RunPlan) RunResult {
	res := RunResult{Plan: p}
	if len(p.Command) == 0 {
		res.Error = "empty command"
		res.ExitCode = -1
		return res
	}
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
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
	// The entrypoint (e.g. python run.py) spawns solver children that inherit
	// the output pipes: killing only the direct child would leave a grandchild
	// holding the pipe and block Wait forever. Run the command in its own
	// process group and kill the whole group on cancel/timeout; WaitDelay is
	// the backstop for anything that escaped the group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 3 * time.Second
	err := cmd.Run()
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
