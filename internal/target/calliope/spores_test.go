// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/target"
	"github.com/enerplanet/meme/internal/target/calliope"
)

// A SPORES schedule with "minimise" stages (Lombardi et al. 2020: explicitly
// minimise the capacity of a tech / group after the explore iterations):
// the native loop runs the explore stages; the exclusion objective and its
// parameters are emitted as extra math, and the run plan hands the schedule to
// a SPORES driver instead of the bare `calliope run` CLI.
func sporesScheduleJob(t *testing.T) model.Job {
	t.Helper()
	j := loadSampleFor(t, model.TargetCalliope)
	j.Experiment.Mode = model.ModeAlternatives
	slack := 0.1
	j.Experiment.Alternatives = &model.AlternativesOptions{
		Slack: &slack, ScoringAlgorithm: "relative_deployment",
		Stages: []model.AlternativesStage{
			{Type: "explore", Count: 2},
			{Type: "minimise", Targets: [][]string{{"pv"}, {"pv", "wind"}}, CountEach: 3},
		},
		Weights: &model.AlternativesWeights{Excl: 10, Nos: 1},
	}
	return j
}

func TestCalliopeSporesScheduleMath(t *testing.T) {
	j := sporesScheduleJob(t)
	if _, err := validateFor(&j, model.TargetCalliope); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := emitCalliopeDir(t, j)
	y := readFile(t, dir, "model.yaml")
	// native explore iterations = the explore stages
	for _, w := range []string{"mode: spores", "number: 2", "additional_math.yaml"} {
		if !strings.Contains(y, w) {
			t.Errorf("model.yaml: expected %q; got:\n%s", w, y)
		}
	}
	m := readFile(t, dir, "additional_math.yaml")
	for _, w := range []string{"tempo_excl_score:", "tempo_w_nos:", "tempo_w_excl:", "tempo_min_spores:",
		"sum(flow_cap * spores_score, over=[nodes, techs, carriers])",
		"sum(flow_cap * tempo_excl_score, over=[nodes, techs, carriers])"} {
		if !strings.Contains(m, w) {
			t.Errorf("additional_math.yaml: expected %q; got:\n%s", w, m)
		}
	}
}

func TestCalliopeSporesSchedulePlan(t *testing.T) {
	j := sporesScheduleJob(t)
	run := t.TempDir()
	dirs := target.RunDirs{RunDir: run, InputDir: filepath.Join(run, "input"), OutDir: filepath.Join(run, "output")}
	plan, err := (calliope.Calliope{}).Plan(&j, filepath.Join(dirs.InputDir, "model.yaml"), dirs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.Command, " ") != "python run.py" {
		t.Errorf("command: %v", plan.Command)
	}
	var sched struct {
		Stages []struct {
			Type      string     `json:"type"`
			Targets   [][]string `json:"targets"`
			CountEach int        `json:"count_each"`
		} `json:"stages"`
		Weights struct{ Excl, Nos float64 } `json:"weights"`
	}
	b, err := os.ReadFile(filepath.Join(run, "spores_schedule.json"))
	if err != nil {
		t.Fatalf("schedule not written: %v", err)
	}
	if err := json.Unmarshal(b, &sched); err != nil {
		t.Fatal(err)
	}
	if len(sched.Stages) != 2 || sched.Stages[1].CountEach != 3 || len(sched.Stages[1].Targets) != 2 || sched.Weights.Excl != 10 {
		t.Errorf("schedule: %+v", sched)
	}
	if _, err := os.Stat(filepath.Join(run, "spores_driver.py")); err != nil {
		t.Errorf("spores driver not written: %v", err)
	}
	if r := readFile(t, run, "run.py"); !strings.Contains(r, `"spores_schedule"`) {
		t.Errorf("run.py CFG lacks spores_schedule:\n%s", r[:200])
	}
}

func TestCalliopeSporesScheduleValidation(t *testing.T) {
	j := sporesScheduleJob(t)
	j.Experiment.Alternatives.Stages = append(j.Experiment.Alternatives.Stages, model.AlternativesStage{Type: "explore", Count: 1})
	if _, err := validateFor(&j, model.TargetCalliope); err == nil {
		t.Errorf("explore after minimise must be rejected (the native loop runs explore first)")
	}
	j = sporesScheduleJob(t)
	j.Experiment.Alternatives.Stages[1].Targets = nil
	if _, err := validateFor(&j, model.TargetCalliope); err == nil {
		t.Errorf("minimise stage without targets must be rejected")
	}
}
