// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package emit

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/enerplanet/meme/internal/model"
)

func f64(f float64) *float64 { return &f }

func TestAnnualizedCapex(t *testing.T) {
	cases := []struct {
		name                        string
		overnight, rate, life, want float64
	}{
		{"no lifetime passes through", 1000, 0.05, 0, 1000},
		{"negative lifetime passes through", 1000, 0.05, -3, 1000},
		{"zero interest is straight-line", 1000, 0, 20, 50},
		{"CRF at 5% over 20y", 1000, 0.05, 20, 1000 * 0.05 * math.Pow(1.05, 20) / (math.Pow(1.05, 20) - 1)},
		{"one-year lifetime repays principal plus interest", 1000, 0.05, 1, 1050},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AnnualizedCapex(c.overnight, c.rate, c.life)
			if math.Abs(got-c.want) > 1e-9 {
				t.Errorf("AnnualizedCapex(%v, %v, %v) = %v, want %v", c.overnight, c.rate, c.life, got, c.want)
			}
		})
	}
}

func TestFormatHelpers(t *testing.T) {
	if got := Ftoa(0.1); got != "0.1" {
		t.Errorf("Ftoa(0.1) = %q", got)
	}
	if got := Ftoa(1e21); got != "1e+21" {
		t.Errorf("Ftoa(1e21) = %q", got)
	}
	if BoolPy(true) != "True" || BoolPy(false) != "False" {
		t.Error("BoolPy must render Python-style booleans")
	}
	if got := OptFloat(f64(2.5), "inf"); got != "2.5" {
		t.Errorf("OptFloat(2.5) = %q", got)
	}
	if got := OptFloat(nil, "inf"); got != "inf" {
		t.Errorf("OptFloat(nil) = %q, want default", got)
	}
	if got := ValScalar(&model.Value{Scalar: f64(3)}, 1); got != "3" {
		t.Errorf("ValScalar(scalar 3) = %q", got)
	}
	if got := ValScalar(nil, 1); got != "1" {
		t.Errorf("ValScalar(nil) = %q, want default", got)
	}
	// A series-valued parameter must NOT silently collapse to its default via
	// ValScalar without the caller handling the series — Reduce fails, so the
	// default is returned; emitters guard series cases before calling this.
	if got := ScalarOr(&model.Value{Ref: "ts:load"}, 7); got != "7" {
		t.Errorf("ScalarOr(series) = %q, want default", got)
	}
}

func TestAnyToString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"x", "x"},
		{true, "True"},
		{false, "False"},
		{float64(2.5), "2.5"},
		{json.Number("42"), "42"},
		{[]any{1.0, "a"}, `[1,"a"]`},
	}
	for _, c := range cases {
		if got := AnyToString(c.in); got != c.want {
			t.Errorf("AnyToString(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSnapshotLabels(t *testing.T) {
	tc := model.TimeConfig{Start: "2030-01-01", Resolution: "1H"}
	got := SnapshotLabels(tc, 3)
	want := []string{"2030-01-01 00:00:00", "2030-01-01 01:00:00", "2030-01-01 02:00:00"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SnapshotLabels 1H = %v, want %v", got, want)
		}
	}
	got = SnapshotLabels(model.TimeConfig{Start: "2030-01-01", Resolution: "3H"}, 2)
	if got[1] != "2030-01-01 03:00:00" {
		t.Errorf("3H step: got %v", got)
	}
	// Unparseable start falls back to integer indices, never fails.
	got = SnapshotLabels(model.TimeConfig{Start: "someday"}, 2)
	if got[0] != "0" || got[1] != "1" {
		t.Errorf("fallback labels = %v, want [0 1]", got)
	}
}

func TestParseDate(t *testing.T) {
	for _, ok := range []string{
		"2030-01-01", "2030-01-01T06:00", "2030-01-01 06:00",
		"2030-01-01T06:00:00", "2030-01-01 06:00:00", "2030-01-01T06:00:00Z",
	} {
		if _, parsed := ParseDate(ok); !parsed {
			t.Errorf("ParseDate(%q) failed, must parse", ok)
		}
	}
	for _, bad := range []string{"", "01.01.2030", "not a date"} {
		if _, parsed := ParseDate(bad); parsed {
			t.Errorf("ParseDate(%q) parsed, must fail", bad)
		}
	}
}

func TestParseHours(t *testing.T) {
	cases := map[string]float64{
		"":     1, // default 1H
		"1H":   1,
		"3H":   3,
		"24H":  24,
		"PT1H": 1, // digit scrape also accepts ISO8601-style durations
		"xyz":  1, // no digits -> default
		"0H":   1, // zero -> default
	}
	for in, wantH := range cases {
		if got := ParseHours(in).Hours(); got != wantH {
			t.Errorf("ParseHours(%q) = %vh, want %vh", in, got, wantH)
		}
	}
}

func TestSeriesHelpers(t *testing.T) {
	m := &model.Model{Timeseries: map[string]model.TimeSeries{
		"load":  {Source: "inline", Values: []float64{1, 2, 3}},
		"file1": {Source: "file", Path: "x.csv"},
	}}
	if v, ok := SeriesValues(m, "load"); !ok || len(v) != 3 {
		t.Errorf("SeriesValues(load) = %v, %v", v, ok)
	}
	if _, ok := SeriesValues(m, "file1"); ok {
		t.Error("SeriesValues must reject non-inline series")
	}
	if _, ok := SeriesValues(m, "missing"); ok {
		t.Error("SeriesValues must reject unknown ids")
	}
	if SeriesAt(nil, 0) != 0 {
		t.Error("SeriesAt on empty series must be 0")
	}
	if SeriesAt([]float64{1, 2}, 5) != 2 {
		t.Error("SeriesAt past the end must pad with the last value")
	}
	n := MaxSeriesLen(m, map[string]string{"a": "load"}, map[string]string{"b": "missing"})
	if n != 3 {
		t.Errorf("MaxSeriesLen = %d, want 3", n)
	}
}

func TestWriteCSV(t *testing.T) {
	dir := t.TempDir()
	rows := [][]string{{"name", "note"}, {"a", `with "quotes", and comma`}}
	if err := WriteCSV(dir, "t.csv", rows); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "t.csv"))
	if err != nil {
		t.Fatal(err)
	}
	want := "name,note\na,\"with \"\"quotes\"\", and comma\"\n"
	if string(b) != want {
		t.Errorf("WriteCSV output %q, want %q", b, want)
	}
	if err := WriteCSV(filepath.Join(dir, "nope"), "t.csv", rows); err == nil {
		t.Error("WriteCSV into a missing directory must error")
	}
}

func TestMergeNativeMap(t *testing.T) {
	dst := map[string]any{
		"keep":    1.0,
		"nested":  map[string]any{"a": 1.0, "b": 2.0},
		"clobber": map[string]any{"x": 1.0},
	}
	raw := json.RawMessage(`{"nested":{"b":9,"c":3},"clobber":"flat","new":true}`)
	if err := MergeNativeMap(dst, raw); err != nil {
		t.Fatal(err)
	}
	if dst["keep"] != 1.0 || dst["new"] != true || dst["clobber"] != "flat" {
		t.Errorf("merge result wrong: %#v", dst)
	}
	nested := dst["nested"].(map[string]any)
	if nested["a"] != 1.0 || nested["b"] != 9.0 || nested["c"] != 3.0 {
		t.Errorf("nested objects must merge recursively, scalars overwrite: %#v", nested)
	}

	if err := MergeNativeMap(dst, nil); err != nil {
		t.Errorf("empty raw must be a no-op, got %v", err)
	}
	if err := MergeNativeMap(dst, json.RawMessage(`[1,2]`)); err == nil {
		t.Error("non-object native block must be rejected")
	}
}

func TestKeysSorted(t *testing.T) {
	m := map[string]int{"c": 1, "a": 2, "b": 3}
	got := Keys(m)
	if got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("Keys not sorted: %v", got)
	}
	got2 := SortedKeys(map[string]any{"z": nil, "y": nil})
	if got2[0] != "y" || got2[1] != "z" {
		t.Errorf("SortedKeys not sorted: %v", got2)
	}
}

// FuzzMergeNativeMap: arbitrary JSON must never panic (the merge feeds
// user-supplied native blocks into nested emitter state).
func FuzzMergeNativeMap(f *testing.F) {
	f.Add([]byte(`{"a":{"b":1}}`))
	f.Add([]byte(`{"a":[1,2,{"c":null}]}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(`{"a":{"a":{"a":{"a":{}}}}}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		dst := map[string]any{"seed": map[string]any{"x": 1.0}}
		_ = MergeNativeMap(dst, raw)
	})
}
