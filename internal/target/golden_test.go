// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package target_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/scenarios"
	"github.com/enerplanet/meme/internal/target"
	_ "github.com/enerplanet/meme/internal/target/all"
)

// update rewrites the golden trees instead of comparing against them:
//
//	go test ./internal/target -update
var update = flag.Bool("update", false, "rewrite golden files instead of comparing")

const goldenRoot = "testdata/golden"

// TestEmitGoldens snapshots the complete emitted tree (native model files plus
// the run plan and any generated driver script) for every scenario x target
// combination the validator accepts, and compares it byte-for-byte against
// testdata/golden/<target>/<scenario>/. Any emitter change shows up here as a
// plain text diff in milliseconds — long before a solver would see it.
func TestEmitGoldens(t *testing.T) {
	for _, name := range scenarios.Names() {
		stem := strings.TrimSuffix(name, ".json")
		for _, tgt := range target.Names() {
			t.Run(string(tgt)+"/"+stem, func(t *testing.T) {
				j, err := scenarios.Load(name)
				if err != nil {
					t.Fatalf("load: %v", err)
				}
				if _, err := target.ValidateFor(&j, tgt); err != nil {
					t.Skipf("not accepted by %s (frozen in validation_matrix.json): %v", tgt, err)
				}
				snap := emitSnapshot(t, &j, tgt)
				compareGolden(t, filepath.Join(goldenRoot, string(tgt), stem), snap)
			})
		}
	}
}

// emitSnapshot runs Emit + Plan into a temp dir and returns every produced
// file keyed by its path relative to the run dir, with the absolute temp base
// normalized to ${BASE}. The run plan itself is included as _plan.json.
func emitSnapshot(t *testing.T, j *model.Job, tgt model.Target) map[string][]byte {
	t.Helper()
	impl, err := target.For(tgt)
	if err != nil {
		t.Fatalf("For(%s): %v", tgt, err)
	}
	base := t.TempDir()
	dirs := target.RunDirs{
		RunDir:   base,
		InputDir: filepath.Join(base, "input"),
		OutDir:   filepath.Join(base, "output"),
	}
	entry, err := impl.Emit(j, dirs.InputDir)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if err := os.MkdirAll(dirs.OutDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := impl.Plan(j, entry, dirs)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	snap := map[string][]byte{}
	err = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return err
		}
		snap[filepath.ToSlash(rel)] = normalizeBase(b, base)
		return nil
	})
	if err != nil {
		t.Fatalf("walk emitted tree: %v", err)
	}

	planDoc, err := json.MarshalIndent(struct {
		Target     model.Target `json:"target"`
		Entrypoint string       `json:"entrypoint"`
		Command    []string     `json:"command"`
		WorkDir    string       `json:"work_dir"`
	}{
		Target:     plan.Target,
		Entrypoint: string(normalizeBase([]byte(plan.Entrypoint), base)),
		Command:    normalizeAll(plan.Command, base),
		WorkDir:    string(normalizeBase([]byte(plan.WorkDir), base)),
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	snap["_plan.json"] = append(planDoc, '\n')
	return snap
}

func normalizeBase(b []byte, base string) []byte {
	return bytes.ReplaceAll(b, []byte(base), []byte("${BASE}"))
}

func normalizeAll(ss []string, base string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = string(normalizeBase([]byte(s), base))
	}
	return out
}

// compareGolden checks the snapshot against the golden tree at dir: same file
// set, same bytes. With -update it rewrites the tree instead.
func compareGolden(t *testing.T, dir string, snap map[string][]byte) {
	t.Helper()
	if *update {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		for rel, b := range snap {
			p := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}

	golden := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		golden[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		t.Fatalf("no golden tree at %s (run: go test ./internal/target -update): %v", dir, err)
	}

	for rel := range golden {
		if _, ok := snap[rel]; !ok {
			t.Errorf("golden file %s is no longer emitted (run -update if intended)", rel)
		}
	}
	for rel, got := range snap {
		want, ok := golden[rel]
		if !ok {
			t.Errorf("new emitted file %s has no golden (run -update if intended)", rel)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from golden:\n%s\n(run: go test ./internal/target -update to accept)", rel, firstDiff(got, want))
		}
	}
}

// firstDiff renders the first differing line of two texts.
func firstDiff(got, want []byte) string {
	g := strings.Split(string(got), "\n")
	w := strings.Split(string(want), "\n")
	n := len(g)
	if len(w) > n {
		n = len(w)
	}
	for i := 0; i < n; i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return fmt.Sprintf("  line %d:\n    got:  %q\n    want: %q", i+1, gl, wl)
		}
	}
	return "  (contents differ only in trailing bytes)"
}
