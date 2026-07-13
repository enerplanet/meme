// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package emit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// Small filesystem/map helpers shared by the target emitters and the service
// layer (absorbed from the former util package).

// Keys returns the sorted keys of a string-keyed map (deterministic output).
func Keys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Mkdirs creates a directory tree.
func Mkdirs(path string) error { return os.MkdirAll(path, 0o755) }

// WriteJSON writes an indented JSON file (HTML escaping off for readable output).
func WriteJSON(dir, name string, v any) error {
	if err := Mkdirs(dir); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644)
}
