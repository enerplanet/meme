// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/target"
)

// Orchestrator runs one target of one job: it validates, expands parameter
// sweeps into independent runs, and for each run asks the target to emit its
// native files and build its run plan, then hands the plan to a Runner.
type Orchestrator struct {
	Runner Runner       // nil -> DryRunner (plan only, no execution)
	Log    func(string) // optional progress logger (per-step + run output)
}

func (o Orchestrator) logf(format string, a ...any) {
	if o.Log != nil {
		o.Log(fmt.Sprintf(format, a...))
	}
}

// RunResult is the outcome of one run (one sweep point of one target).
type RunResult struct {
	Target   string             `json:"target"`
	Index    int                `json:"index"`
	InputDir string             `json:"input_dir"`
	Plan     target.RunPlan     `json:"plan"`
	Assigned map[string]float64 `json:"assigned,omitempty"`
	Stdout   string             `json:"stdout,omitempty"`
	Stderr   string             `json:"stderr,omitempty"`
	ExitCode int                `json:"exit_code"`
	Error    string             `json:"error,omitempty"`
}

// Simulate validates, expands the sweep, and for each run emits + materializes +
// runs. It returns per-run results plus validation warnings.
func (o Orchestrator) Simulate(ctx context.Context, j *model.Job, name model.Target, baseDir string) (results []RunResult, warnings []string, err error) {
	warnings, err = target.ValidateFor(j, name)
	if err != nil {
		return nil, warnings, err
	}
	impl, err := target.For(name)
	if err != nil {
		return nil, warnings, err
	}
	runner := o.Runner
	if runner == nil {
		runner = DryRunner{}
	}

	runs, err := ExpandRuns(j)
	if err != nil {
		return nil, warnings, err
	}
	o.logf("validated ok (%d warning(s)); expanded into %d run(s)", len(warnings), len(runs))
	for i, run := range runs {
		runDir := filepath.Join(baseDir, fmt.Sprintf("run_%d", i))
		dirs := target.RunDirs{
			RunDir:   runDir,
			InputDir: filepath.Join(runDir, "input"),
			OutDir:   filepath.Join(runDir, "output"),
		}
		o.logf("run %d: emitting %s files", i, name)
		entrypoint, err := impl.Emit(&run.Job, dirs.InputDir)
		if err != nil {
			return results, warnings, fmt.Errorf("run %d emit: %w", i, err)
		}
		if err := emit.Mkdirs(dirs.OutDir); err != nil {
			return results, warnings, fmt.Errorf("run %d output dir: %w", i, err)
		}
		plan, err := impl.Plan(&run.Job, entrypoint, dirs)
		if err != nil {
			return results, warnings, fmt.Errorf("run %d plan: %w", i, err)
		}
		o.logf("run %d: %s", i, strings.Join(plan.Command, " "))
		res := runner.Run(ctx, plan)
		res.Target = string(name)
		res.Index = i
		res.InputDir = dirs.InputDir
		res.Assigned = run.Assignment
		o.logf("run %d: exit=%d", i, res.ExitCode)
		if s := strings.TrimSpace(res.Stdout); s != "" {
			o.logf("run %d output:\n%s", i, s)
		}
		if res.Error != "" {
			o.logf("run %d error: %s", i, res.Error)
		}
		results = append(results, res)
	}
	o.logf("all runs complete")
	return results, warnings, nil
}

// ---------------------------------------------------------------------------
// Sweep expansion.
// ---------------------------------------------------------------------------

type expandedRun struct {
	Job        model.Job
	Assignment map[string]float64
}

// ExpandRuns turns a Job with a parameter sweep into one Job per sweep point,
// each a deep copy with the assigned parameters applied. No sweep -> a single
// run with the base Job.
func ExpandRuns(j *model.Job) ([]expandedRun, error) {
	assigns := j.Experiment.ExpandSweep()
	if len(assigns) == 0 {
		return []expandedRun{{Job: *j}}, nil
	}
	out := make([]expandedRun, 0, len(assigns))
	for _, a := range assigns {
		clone, err := cloneJob(j)
		if err != nil {
			return nil, err
		}
		for path, val := range a {
			if err := applyPath(&clone.Model, path, val); err != nil {
				return nil, fmt.Errorf("sweep %q: %w", path, err)
			}
		}
		out = append(out, expandedRun{Job: *clone, Assignment: a})
	}
	return out, nil
}

func cloneJob(j *model.Job) (*model.Job, error) {
	b, err := json.Marshal(j)
	if err != nil {
		return nil, err
	}
	var c model.Job
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// applyPath sets a dotted parameter path on the model. Supported roots:
// technologies.<id>.capacity.{max,min,existing}, technologies.<id>.costs.<class>.{investment_per_capacity,variable_om},
// technologies.<id>.efficiency, carriers.<id>.co2_intensity.
func applyPath(m *model.Model, path string, v float64) error {
	seg := splitPath(path)
	if len(seg) < 3 {
		return fmt.Errorf("path too short")
	}
	switch seg[0] {
	case "technologies":
		t, ok := m.Technologies[seg[1]]
		if !ok {
			return fmt.Errorf("tech %q not found", seg[1])
		}
		switch seg[2] {
		case "capacity":
			if t.Capacity == nil {
				t.Capacity = &model.Capacity{}
			}
			switch seg[3] {
			case "max":
				t.Capacity.Max = &v
			case "min":
				t.Capacity.Min = &v
			case "existing":
				t.Capacity.Existing = v
			default:
				return fmt.Errorf("unknown capacity field %q", seg[3])
			}
		case "efficiency":
			t.Efficiency = &model.Value{Scalar: &v}
		case "costs":
			if len(seg) < 5 {
				return fmt.Errorf("costs path needs class and field")
			}
			if t.Costs == nil {
				t.Costs = map[string]model.CostClass{}
			}
			c := t.Costs[seg[3]]
			switch seg[4] {
			case "investment_per_capacity":
				c.InvestmentPerCapacity = &v
			case "variable_om":
				c.VariableOM = &model.Value{Scalar: &v}
			default:
				return fmt.Errorf("unknown cost field %q", seg[4])
			}
			t.Costs[seg[3]] = c
		default:
			return fmt.Errorf("unknown tech field %q", seg[2])
		}
		m.Technologies[seg[1]] = t
	case "carriers":
		c, ok := m.Carriers[seg[1]]
		if !ok {
			return fmt.Errorf("carrier %q not found", seg[1])
		}
		if seg[2] == "co2_intensity" {
			c.CO2Intensity = &v
		} else {
			return fmt.Errorf("unknown carrier field %q", seg[2])
		}
		m.Carriers[seg[1]] = c
	default:
		return fmt.Errorf("unsupported path root %q", seg[0])
	}
	return nil
}

func splitPath(p string) []string {
	var out []string
	cur := ""
	for _, r := range p {
		if r == '.' {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(r)
		}
	}
	return append(out, cur)
}

// WriteRunResults dumps the run results as JSON for the caller/orchestrator log.
func WriteRunResults(dir string, results []RunResult) error {
	return emit.WriteJSON(dir, "results.json", results)
}
