// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"os/exec"
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
	out, err := cmd.CombinedOutput()
	res.Stdout = string(out)
	if ctxErr := ctx.Err(); ctxErr != nil {
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
