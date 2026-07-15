// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/scenarios"
	"github.com/enerplanet/meme/internal/service"
	"github.com/enerplanet/meme/internal/target"
)

func loadSample(t *testing.T) model.Job {
	t.Helper()
	j, err := scenarios.LoadSample()
	if err != nil {
		t.Fatalf("load sample: %v", err)
	}
	return j
}

func TestExpandRunsSweep(t *testing.T) {
	j := loadSample(t)
	j.Experiment.Sweep = []model.SweepAxis{
		{Parameter: "technologies.pv.capacity.max", Values: []float64{200, 400}},
	}
	runs, err := service.ExpandRuns(&j)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}
	got := map[float64]bool{}
	for _, r := range runs {
		got[*r.Job.Model.Technologies["pv"].Capacity.Max] = true
	}
	if !got[200] || !got[400] {
		t.Errorf("sweep values not applied: %v", got)
	}
}

func TestCommandRunner(t *testing.T) {
	res := service.CommandRunner{}.Run(context.Background(), target.RunPlan{Command: []string{"echo", "hello-orchestrator"}})
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "hello-orchestrator") {
		t.Errorf("command runner failed: %+v", res)
	}
}
