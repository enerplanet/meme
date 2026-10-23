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
//
// A framework may be registered more than once when several of its releases
// are supported, one Target per version. The bare name is the default version
// (what "all" and the default-target list expand to); the others are version
// Variants, selected only by their explicit name (e.g. "calliope-0.6"). A
// version-explicit alias of a default ("calliope-0.7" -> "calliope") keeps a
// pinned name valid when the default moves on.
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

	// Version is the framework release this target's emitter and run plan are
	// validated against — the pin the environment/ image installs.
	Version() string

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

// Variant is implemented by a target that is an alternative version of a
// default target (calliope-0.6 of calliope). A variant is selected only by its
// explicit name: "all" and Defaults expand to the default targets, so one
// request never runs two versions of the same framework unless asked to.
type Variant interface {
	VariantOf() model.Target
}

var (
	mu       sync.RWMutex
	registry = map[model.Target]Target{}
	aliases  = map[model.Target]model.Target{} // alias -> registered name
)

// Register adds an implementation; called from implementation init().
func Register(t Target) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[t.Name()]; dup {
		panic(fmt.Sprintf("target %q registered twice", t.Name()))
	}
	if _, dup := aliases[t.Name()]; dup {
		panic(fmt.Sprintf("target %q is already an alias", t.Name()))
	}
	registry[t.Name()] = t
}

// RegisterAlias makes alias resolve to the registered target canonical (e.g.
// "calliope-0.7" -> "calliope"), so a version-explicit name stays valid when
// the default version moves on. Called from implementation init() after
// Register.
func RegisterAlias(alias, canonical model.Target) {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := registry[canonical]; !ok {
		panic(fmt.Sprintf("alias %q: target %q is not registered", alias, canonical))
	}
	if _, dup := registry[alias]; dup {
		panic(fmt.Sprintf("alias %q collides with a registered target", alias))
	}
	if prev, dup := aliases[alias]; dup && prev != canonical {
		panic(fmt.Sprintf("alias %q registered twice (%q, %q)", alias, prev, canonical))
	}
	aliases[alias] = canonical
}

// Resolve canonicalizes a target name through the alias table; a name that is
// not an alias passes through unchanged (For reports unknown ones).
func Resolve(name model.Target) model.Target {
	mu.RLock()
	defer mu.RUnlock()
	return resolve(name)
}

func resolve(name model.Target) model.Target {
	if canonical, ok := aliases[name]; ok {
		return canonical
	}
	return name
}

// For resolves a registered target by name or alias.
func For(name model.Target) (Target, error) {
	mu.RLock()
	defer mu.RUnlock()
	t, ok := registry[resolve(name)]
	if !ok {
		return nil, fmt.Errorf("unknown target %q (known: %v)", name, names())
	}
	return t, nil
}

// Known reports whether a target name (or alias) is registered.
func Known(name model.Target) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := registry[resolve(name)]
	return ok
}

// Names lists every registered target name (defaults and version variants,
// not aliases), sorted.
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

// Defaults lists the registered targets that are not version variants — one
// per framework, what "all" expands to — sorted.
func Defaults() []model.Target {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]model.Target, 0, len(registry))
	for n, t := range registry {
		if _, variant := t.(Variant); !variant {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Aliases returns a copy of the alias table (alias -> registered name).
func Aliases() map[model.Target]model.Target {
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[model.Target]model.Target, len(aliases))
	for a, c := range aliases {
		out[a] = c
	}
	return out
}

// Versions maps every registered target name to the framework version it
// drives (target -> version), for the /capabilities endpoint.
func Versions() map[string]string {
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]string, len(registry))
	for n, t := range registry {
		out[string(n)] = t.Version()
	}
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
