// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package adoptnet0_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/scenarios"
	"github.com/enerplanet/meme/internal/target"
	_ "github.com/enerplanet/meme/internal/target/all"
)

func loadSampleFor(t *testing.T, tgt model.Target) model.Job {
	t.Helper()
	j, err := scenarios.SampleFor(tgt)
	if err != nil {
		t.Fatalf("load sample for %s: %v", tgt, err)
	}
	return j
}

func validateFor(j *model.Job, tgt model.Target) ([]string, error) {
	return target.ValidateFor(j, tgt)
}

func readFileAbs(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(readFileAbs(t, path)), &m); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return m
}
