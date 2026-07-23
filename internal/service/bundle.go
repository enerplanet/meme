// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package service

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

// WriteBundle streams the finished job's result zip: metadata.json (the
// JobView), log.txt, results.json, the byte-identical initial config, and the
// whole emitted files/ tree (per-target subtrees for multi-target jobs). The
// bundle layout is product contract; keeping it here lets it be tested without
// HTTP.
func WriteBundle(w io.Writer, rec *JobRecord) error {
	view := rec.View(false)
	zw := zip.NewWriter(w)
	defer zw.Close()

	addJSON(zw, "metadata.json", view)
	addBytes(zw, "log.txt", []byte(rec.LogText()))
	addJSON(zw, "results.json", view.Runs)
	if cfg, err := os.ReadFile(filepath.Join(rec.Dir(), "config.json")); err == nil {
		addBytes(zw, "config.json", cfg)
	}
	addTree(zw, rec.Dir(), "files")
	return zw.Close()
}

func addBytes(zw *zip.Writer, name string, data []byte) {
	if f, err := zw.Create(name); err == nil {
		_, _ = f.Write(data)
	}
}

func addJSON(zw *zip.Writer, name string, v any) {
	if b, err := json.MarshalIndent(v, "", "  "); err == nil {
		addBytes(zw, name, b)
	}
}

// addTree adds every regular file under root to the zip under prefix/<relpath>.
func addTree(zw *zip.Writer, root, prefix string) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		// Skip symlinks: following one would copy content from outside the
		// job tree into a downloadable artifact.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		in, oerr := os.Open(path)
		if oerr != nil {
			return nil
		}
		defer in.Close()
		if out, cerr := zw.Create(filepath.ToSlash(filepath.Join(prefix, rel))); cerr == nil {
			_, _ = io.Copy(out, in)
		}
		return nil
	})
}
