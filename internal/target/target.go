// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

// Package target defines the framework-target abstraction and its registry.
//
// A Target owns everything framework-specific: its capability profile, its
// validation gates, its emitter, and its run plan (including any generated
// driver script). The registry orchestrates the generic, capability-driven
// validation (mode/feature gating, performance emittability, native-block
// warnings) so an implementation only supplies what is genuinely its own.
//
// Implementations self-register from their package init(); importing
// target/all (or any implementation package) makes them available. This keeps
// the dependency graph acyclic: model <- emit <- target <- {pypsa, calliope,
// adoptnet0} <- service <- api.
package target

import (
	"fmt"
	"sort"
	"sync"

	"github.com/enerplanet/meme/internal/model"
)

// RunPlan describes how to execute one emitted run of a target.
type RunPlan struct {
	Target     model.Target `json:"target"`
	Entrypoint string       `json:"entrypoint"`
	Command    []string     `json:"command"`
	WorkDir    string       `json:"work_dir"`
}

// RunDirs are the per-run directories a Plan may use: RunDir holds the driver
// script (and is the working directory), InputDir the emitted native model,
// OutDir the solver results.
type RunDirs struct {
	RunDir   string
	InputDir string
	OutDir   string
}

// Target is one supported framework backend.
type Target interface {
	// Name is the wire identifier ("pypsa", "calliope", "adopt-net0").
	Name() model.Target

	// Capabilities is the feature profile ValidateFor gates against. Every
	// claimed feature must be honored end-to-end (the capability-coverage test
	// enforces a runnable scenario per claim).
	Capabilities() map[model.Feature]bool

	// ValidateJob holds the target-specific gates beyond the generic
	// capability gating: anything this target's emitter would silently drop
	// must be rejected (error) or surfaced (warning) here.
	ValidateJob(j *model.Job) (warnings []string, err error)

	// Emit writes the complete, runnable native model under outDir and
	// returns the entrypoint the simulator is pointed at.
	Emit(j *model.Job, outDir string) (entrypoint string, err error)

	// Plan builds the command for one emitted run, writing any driver script
	// into dirs.RunDir.
	Plan(j *model.Job, entrypoint string, dirs RunDirs) (RunPlan, error)
}

var (
	mu       sync.RWMutex
	registry = map[model.Target]Target{}
)

// Register adds an implementation; called from implementation init().
func Register(t Target) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[t.Name()]; dup {
		panic(fmt.Sprintf("target %q registered twice", t.Name()))
	}
	registry[t.Name()] = t
}

// For resolves a registered target by name.
func For(name model.Target) (Target, error) {
	mu.RLock()
	defer mu.RUnlock()
	t, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown target %q (known: %v)", name, names())
	}
	return t, nil
}

// Known reports whether a target name is registered.
func Known(name model.Target) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := registry[name]
	return ok
}

// Names lists the registered target names, sorted.
func Names() []model.Target {
	mu.RLock()
	defer mu.RUnlock()
	return names()
}

func names() []model.Target {
	out := make([]model.Target, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Supports reports whether a registered target claims a feature.
func Supports(name model.Target, f model.Feature) bool {
	t, err := For(name)
	if err != nil {
		return false
	}
	return t.Capabilities()[f]
}

// CapabilityMatrix aggregates every registered target's profile for the
// /capabilities endpoint (target -> feature -> supported).
func CapabilityMatrix() map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, name := range Names() {
		t, _ := For(name)
		m := map[string]bool{}
		for f, ok := range t.Capabilities() {
			m[string(f)] = ok
		}
		out[string(name)] = m
	}
	return out
}
