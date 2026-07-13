// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package emit

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/enerplanet/meme/internal/model"
)

// Shared value/format helpers used by every target emitter.

// Ftoa formats a float in Go's shortest round-trip representation.
func Ftoa(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// BoolPy renders a bool in Python literal spelling (True/False), as the PyPSA
// CSV reader expects.
func BoolPy(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// WriteCSV writes rows as an RFC 4180 CSV file under dir.
func WriteCSV(dir, name string, rows [][]string) error {
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.WriteAll(rows); err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}

// PerfEfficiency returns the efficiency Value honoring a constant Performance
// block (which may override the bare Efficiency field).
func PerfEfficiency(t model.Technology) *model.Value {
	if t.Performance != nil && t.Performance.Kind() == model.PerfConstant && t.Performance.Efficiency != nil {
		return t.Performance.Efficiency
	}
	return t.Efficiency
}

// ValScalar renders a Value's scalar projection, falling back to def when the
// value is absent or not reducible (a series or a multi-entry indexed
// parameter).
func ValScalar(v *model.Value, def float64) string {
	if f, ok := v.Reduce(); ok {
		return Ftoa(f)
	}
	return Ftoa(def)
}

// ScalarOr is equivalent to ValScalar and kept for existing call sites; prefer
// ValScalar in new code.
func ScalarOr(v *model.Value, def float64) string { return ValScalar(v, def) }

// OptFloat renders *p, or def when p is nil (def carries non-numeric
// spellings such as "inf").
func OptFloat(p *float64, def string) string {
	if p != nil {
		return Ftoa(*p)
	}
	return def
}
