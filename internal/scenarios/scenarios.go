// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

// Package scenarios embeds the canonical test corpus: one runnable Job payload
// per exercised capability, plus the shared sample payload. Embedding makes the
// corpus importable from any package (unit tests, the capability-coverage
// meta-test, the E2E suite) without fragile relative paths.
package scenarios

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"sort"

	"github.com/enerplanet/meme/internal/model"
)

//go:embed testdata
var files embed.FS

// Names lists the scenario file names (sorted, e.g. "pypsa_generators.json").
func Names() []string {
	entries, err := files.ReadDir("testdata/scenarios")
	if err != nil {
		panic(err) // embedded tree; cannot fail at run time
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// Raw returns a scenario payload verbatim.
func Raw(name string) ([]byte, error) {
	return files.ReadFile(path.Join("testdata", "scenarios", name))
}

// Load decodes a scenario payload into a Job.
func Load(name string) (model.Job, error) {
	b, err := Raw(name)
	if err != nil {
		return model.Job{}, err
	}
	var j model.Job
	if err := json.Unmarshal(b, &j); err != nil {
		return model.Job{}, fmt.Errorf("decode %s: %w", name, err)
	}
	return j, nil
}

// Sample returns the shared sample payload (the maximal PyPSA-shaped Job used
// by the unit tests) verbatim.
func Sample() []byte {
	b, err := files.ReadFile("testdata/sample_payload.json")
	if err != nil {
		panic(err)
	}
	return b
}

// LoadSample decodes the shared sample payload.
func LoadSample() (model.Job, error) {
	var j model.Job
	if err := json.Unmarshal(Sample(), &j); err != nil {
		return model.Job{}, err
	}
	return j, nil
}

// SampleFor adapts the shared sample payload to a target. The sample is
// deliberately PyPSA-complete; the per-target gates require removing what a
// target genuinely cannot represent — exactly what a real client would do.
func SampleFor(t model.Target) (model.Job, error) {
	j, err := LoadSample()
	if err != nil {
		return model.Job{}, err
	}
	switch t {
	case model.TargetCalliope:
		// Calliope 0.7 MILP has no start/stop-cost or up/down-time math.
		cc := j.Model.Technologies["ccgt"]
		cc.Operation.StartUpCost = nil
		cc.Operation.MinUptime = nil
		j.Model.Technologies["ccgt"] = cc
	case model.TargetAdOpt:
		// adopt_net0 is database-driven: generic supply/conversion techs,
		// portable linear constraints and transmission networks have no
		// representation.
		delete(j.Model.Technologies, "ccgt")
		delete(j.Model.Technologies, "chp")
		j.Model.Constraints = nil
		j.Model.Transmission = nil
	}
	return j, nil
}
