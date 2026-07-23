// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package emit

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		// Numeric-stability edges: the naive CRF used to blow up here (rate
		// 1e-16 gave +Inf, 1e-12 was 0.19% off, huge lifetimes gave NaN).
		{"rate 1e-16 is straight-line, not +Inf", 1000, 1e-16, 20, 50},
		{"rate 1e-17 is straight-line", 1000, 1e-17, 20, 50},
		// First-order CRF: (1/L)(1 + (L+1)r/2), hand-computed for L=20.
		{"rate 1e-12 keeps first-order accuracy", 1000, 1e-12, 20, 50.000000000525},
		{"rate 1e-8 keeps first-order accuracy", 1000, 1e-8, 20, 50.00000525},
		{"tiny negative rate", 1000, -1e-12, 20, 49.999999999475},
		{"negative rate uses the closed form", 1000, -0.05, 20,
			1000 * -0.05 * math.Pow(0.95, 20) / (math.Pow(0.95, 20) - 1)},
		// (1.05)^175200 overflows; the stable form converges to CRF = r.
		{"huge lifetime converges to the interest rate", 1000, 0.05, 175200, 50},
		// Below -100% no CRF exists; documented straight-line fallback.
		{"rate below -1 falls back to straight-line", 1000, -1.5, 20.5, 1000 / 20.5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AnnualizedCapex(c.overnight, c.rate, c.life)
			// NaN fails every comparison, so guard finiteness explicitly or a
			// NaN result would slip past the tolerance check.
			if math.IsNaN(got) || math.IsInf(got, 0) || math.Abs(got-c.want) > 1e-9 {
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

func TestSnapshotLabelsExtendsTimesteps(t *testing.T) {
	cases := []struct {
		name string
		tc   model.TimeConfig
		n    int
		want []string
	}{
		{
			"exact timesteps pass through",
			model.TimeConfig{Timesteps: []string{"2030-01-01 00:00:00", "2030-01-01 01:00:00"}},
			2,
			[]string{"2030-01-01 00:00:00", "2030-01-01 01:00:00"},
		},
		{
			// The old behavior padded with bare integer indices, producing a
			// mixed datetime/int column that pandas to_datetime rejects.
			"short timesteps extend by resolution arithmetic",
			model.TimeConfig{Resolution: "1H", Timesteps: []string{"2030-01-01 00:00:00", "2030-01-01 01:00:00"}},
			4,
			[]string{"2030-01-01 00:00:00", "2030-01-01 01:00:00", "2030-01-01 02:00:00", "2030-01-01 03:00:00"},
		},
		{
			"extension honors a coarser resolution",
			model.TimeConfig{Resolution: "3H", Timesteps: []string{"2030-01-01 00:00:00"}},
			3,
			[]string{"2030-01-01 00:00:00", "2030-01-01 03:00:00", "2030-01-01 06:00:00"},
		},
		{
			"missing resolution defaults to one hour",
			model.TimeConfig{Timesteps: []string{"2030-01-01 00:00:00"}},
			2,
			[]string{"2030-01-01 00:00:00", "2030-01-01 01:00:00"},
		},
		{
			"extension keeps the minute-precision layout",
			model.TimeConfig{Resolution: "1H", Timesteps: []string{"2030-01-01T00:00"}},
			2,
			[]string{"2030-01-01T00:00", "2030-01-01T01:00"},
		},
		{
			"date-only layout survives daily resolution",
			model.TimeConfig{Resolution: "24H", Timesteps: []string{"2030-01-01"}},
			3,
			[]string{"2030-01-01", "2030-01-02", "2030-01-03"},
		},
		{
			// A date-only layout cannot express hourly steps without emitting
			// duplicate labels; the extension switches to the full layout.
			"date-only layout widens for sub-daily resolution",
			model.TimeConfig{Resolution: "1H", Timesteps: []string{"2030-01-01"}},
			3,
			[]string{"2030-01-01", "2030-01-01 01:00:00", "2030-01-01 02:00:00"},
		},
		{
			// Nothing to extrapolate from: integer indices stay the last resort.
			"unparseable last timestep keeps integer padding",
			model.TimeConfig{Timesteps: []string{"a", "b"}},
			4,
			[]string{"a", "b", "2", "3"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SnapshotLabels(c.tc, c.n)
			if len(got) != len(c.want) {
				t.Fatalf("SnapshotLabels = %v, want %v", got, c.want)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Fatalf("SnapshotLabels = %v, want %v", got, c.want)
				}
			}
		})
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
		"":    1, // default 1H
		"1H":  1,
		"3H":  3,
		"24H": 24,
		"xyz": 1, // unrecognized -> lenient default
		"0H":  1, // zero -> lenient default
		// Grammar fixes: the old digit scrape misread all of these.
		"0.5H":  0.5,  // was 5h
		"30min": 0.5,  // was 30h
		"15T":   0.25, // was 15h
		"PT30M": 0.5,  // was 30h
		"PT1H":  1,
		"D":     24, // was silent 1h default
	}
	for in, wantH := range cases {
		if got := ParseHours(in).Hours(); got != wantH {
			t.Errorf("ParseHours(%q) = %vh, want %vh", in, got, wantH)
		}
	}
}

func TestParseResolution(t *testing.T) {
	ok := []struct {
		in   string
		want time.Duration
	}{
		{"1H", time.Hour},
		{"3h", 3 * time.Hour},
		{"H", time.Hour}, // count defaults to 1, as in pandas
		{"0.5H", 30 * time.Minute},
		{"30min", 30 * time.Minute},
		{"min", time.Minute},
		{"15T", 15 * time.Minute},
		{"45S", 45 * time.Second},
		{"D", 24 * time.Hour},
		{"2d", 48 * time.Hour},
		{"3", 3 * time.Hour}, // bare count means hours (pre-grammar behavior)
		{" 1H ", time.Hour},
		{"PT1H", time.Hour},
		{"PT30M", 30 * time.Minute},
		{"pt30m", 30 * time.Minute},
		{"PT0.5H", 30 * time.Minute},
		{"PT1H30M", 90 * time.Minute},
		{"P1D", 24 * time.Hour},
		{"P1DT12H", 36 * time.Hour},
	}
	for _, c := range ok {
		got, err := ParseResolution(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseResolution(%q) = %v, %v, want %v", c.in, got, err, c.want)
		}
	}
	bad := []string{
		"", "xyz", "0H", "-1H", "1.5.2H", "H3",
		"M", "1M", // month vs minute is ambiguous; must not guess
		"P", "PT", "PTM", "PT1X", "P1M", // calendar-dependent or malformed
	}
	for _, in := range bad {
		if got, err := ParseResolution(in); err == nil {
			t.Errorf("ParseResolution(%q) = %v, want error", in, got)
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

func TestWriteCSVRejectsNonFinite(t *testing.T) {
	// Every spelling Ftoa can produce for a non-finite float must be refused
	// before any bytes hit disk — a "NaN" cell in generators.csv used to
	// round-trip into a successful job.
	for _, cell := range []string{Ftoa(math.NaN()), Ftoa(math.Inf(1)), Ftoa(math.Inf(-1))} {
		dir := t.TempDir()
		rows := [][]string{{"name", "capital_cost"}, {"gen", cell}}
		err := WriteCSV(dir, "t.csv", rows)
		if err == nil {
			t.Fatalf("WriteCSV with %q cell must error", cell)
		}
		if _, statErr := os.Stat(filepath.Join(dir, "t.csv")); !os.IsNotExist(statErr) {
			t.Errorf("WriteCSV must not create a file when rejecting %q", cell)
		}
	}
	// The lowercase "inf" spelling is the deliberate unbounded default
	// (PyPSA p_nom_max via OptFloat) and must keep passing.
	dir := t.TempDir()
	if err := WriteCSV(dir, "t.csv", [][]string{{"p_nom_max"}, {"inf"}}); err != nil {
		t.Errorf("WriteCSV must accept the lowercase inf default: %v", err)
	}
}

func TestSeriesLengthMismatches(t *testing.T) {
	// No inline series: nothing to compare against.
	if got := SeriesLengthMismatches(&model.Model{Timeseries: map[string]model.TimeSeries{
		"f": {Source: "file", Path: "x.csv"},
	}}); got != nil {
		t.Errorf("no inline series must yield nil, got %v", got)
	}
	// Equal lengths: no warnings.
	if got := SeriesLengthMismatches(&model.Model{Timeseries: map[string]model.TimeSeries{
		"a": {Source: "inline", Values: []float64{1, 2}},
		"b": {Source: "inline", Values: []float64{3, 4}},
	}}); len(got) != 0 {
		t.Errorf("equal lengths must yield no warnings, got %v", got)
	}
	// Shorter inline series and a short explicit timestep list are both
	// silently padded by the emitters; each must be reported, sorted, and
	// non-inline series must stay exempt.
	m := &model.Model{
		Time: model.TimeConfig{Timesteps: []string{"2030-01-01 00:00:00"}},
		Timeseries: map[string]model.TimeSeries{
			"long":  {Source: "inline", Values: []float64{1, 2, 3}},
			"zhort": {Source: "inline", Values: []float64{1}},
			"also":  {Source: "inline", Values: []float64{1, 2}},
			"file1": {Source: "file", Path: "x.csv"},
		},
	}
	got := SeriesLengthMismatches(m)
	if len(got) != 3 {
		t.Fatalf("SeriesLengthMismatches = %v, want 3 warnings", got)
	}
	wantSubstr := []string{`"also": 2 values`, `"zhort": 1 values`, "time.timesteps lists 1 labels"}
	for i, sub := range wantSubstr {
		if !strings.Contains(got[i], sub) {
			t.Errorf("warning[%d] = %q, want it to contain %q", i, got[i], sub)
		}
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
