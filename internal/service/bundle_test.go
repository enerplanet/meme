// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package service_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/service"
)

// TestBundleSkipsSymlinks: a symlink planted in the job tree (pointing outside
// it) must not have its target's content copied into the downloadable zip;
// regular files still land.
func TestBundleSkipsSymlinks(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("outside the job dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	input := filepath.Join(dir, "run_0", "input")
	if err := os.MkdirAll(input, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, "real.csv"), []byte("a,b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(input, "link.txt")); err != nil {
		t.Fatal(err)
	}

	store := service.NewJobStore()
	rec := store.Create(model.TargetPyPSA, dir, nil)
	rec.Start()
	rec.Finish(nil, nil, nil)

	names := bundleNames(t, rec)
	if names["files/run_0/input/link.txt"] {
		t.Error("bundle must not follow symlinks out of the job tree")
	}
	if !names["files/run_0/input/real.csv"] {
		t.Error("regular files must survive the symlink filter")
	}
}
