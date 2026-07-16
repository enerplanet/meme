// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/target"
	_ "github.com/enerplanet/meme/internal/target/all"
)

// TestE2EExamplesSolve backs the examples/ README claim that every published
// payload "runs to succeeded against the real API with real solvers": each
// example is submitted to its intended target(s) (the shared ones with
// ?target=all) and the job must finish succeeded. Full tier only.
func TestE2EExamplesSolve(t *testing.T) {
	requireFull(t)
	cases := map[string]string{
		"pypsa_full.json":    "pypsa",
		"calliope_full.json": "calliope",
		"adopt_full.json":    "adopt-net0",
		"shared_full.json":   "all",
		"payload_full.json":  "all",
	}
	for file, tgt := range cases {
		file, tgt := file, tgt
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			payload, err := os.ReadFile(filepath.Join("..", "..", "examples", file))
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			srv, _ := newE2EServer(t)
			sub := submit(t, srv.URL, model.Target(tgt), payload)
			status := awaitJob(t, srv.URL, sub["id"].(string))
			if status["state"] != "succeeded" {
				t.Fatalf("example %s on %s: state=%v error=%v\nlog:\n%v",
					file, tgt, status["state"], status["error"], status["log"])
			}
		})
	}
}

// TestExamplePayloadsValidate pins the documented API surface: every payload
// in examples/ must decode into a Job and pass ValidateFor on its intended
// target(s). Unlike the rest of this package it needs no framework execution,
// so it always runs. A model or validation change that breaks a published
// example fails here, not at a user.
func TestExamplePayloadsValidate(t *testing.T) {
	cases := map[string][]model.Target{
		"shared_full.json":   {model.TargetPyPSA, model.TargetCalliope, model.TargetAdOpt},
		"payload_full.json":  {model.TargetPyPSA},
		"pypsa_full.json":    {model.TargetPyPSA},
		"calliope_full.json": {model.TargetCalliope},
		"adopt_full.json":    {model.TargetAdOpt},
	}
	for file, tgts := range cases {
		b, err := os.ReadFile(filepath.Join("..", "..", "examples", file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, tgt := range tgts {
			t.Run(file+"/"+string(tgt), func(t *testing.T) {
				var j model.Job
				if err := json.Unmarshal(b, &j); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if _, err := target.ValidateFor(&j, tgt); err != nil {
					t.Fatalf("example rejected: %v", err)
				}
			})
		}
	}
}
