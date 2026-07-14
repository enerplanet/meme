// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package target_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/enerplanet/meme/internal/scenarios"
	"github.com/enerplanet/meme/internal/target"
	_ "github.com/enerplanet/meme/internal/target/all"
)

// verdict is one cell of the validation matrix: how a target judges a payload.
type verdict struct {
	Verdict  string   `json:"verdict"` // "ok" or "error: <exact message>"
	Warnings []string `json:"warnings,omitempty"`
}

// TestValidationMatrixGolden freezes the full capability-gate contract: for
// every scenario x target (plus the per-target adapted sample payload) the
// exact accept/reject verdict and warning texts are compared against one
// golden JSON. Accidentally loosening a gate (a payload that must 422 now
// passing) or tightening one shows up as a one-line diff here — the
// capability-coverage meta-test checks that claims have scenarios, this test
// checks that verdicts stay stable. Shares -update with the emit goldens.
func TestValidationMatrixGolden(t *testing.T) {
	matrix := map[string]map[string]verdict{}

	for _, name := range scenarios.Names() {
		row := map[string]verdict{}
		for _, tgt := range target.Names() {
			j, err := scenarios.Load(name)
			if err != nil {
				t.Fatalf("load %s: %v", name, err)
			}
			warns, err := target.ValidateFor(&j, tgt)
			v := verdict{Verdict: "ok", Warnings: warns}
			if err != nil {
				v = verdict{Verdict: "error: " + err.Error()}
			}
			row[string(tgt)] = v
		}
		matrix[name] = row
	}

	// The shared sample payload, adapted per target the way a real client would.
	row := map[string]verdict{}
	for _, tgt := range target.Names() {
		j, err := scenarios.SampleFor(tgt)
		if err != nil {
			t.Fatalf("SampleFor(%s): %v", tgt, err)
		}
		warns, err := target.ValidateFor(&j, tgt)
		v := verdict{Verdict: "ok", Warnings: warns}
		if err != nil {
			v = verdict{Verdict: "error: " + err.Error()}
		}
		row[string(tgt)] = v
	}
	matrix["sample_payload (adapted per target)"] = row

	got, err := json.MarshalIndent(matrix, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	path := filepath.Join(goldenRoot, "validation_matrix.json")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden matrix (run: go test ./internal/target -update): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("validation matrix changed:\n%s\n(run: go test ./internal/target -update to accept)", firstDiff(got, want))
	}
}
