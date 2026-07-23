// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package emit

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/enerplanet/meme/internal/model"
)

// Shared time-series helpers used by every emitter that materializes the inline
// series registry into files (PyPSA CSVs, Calliope data tables). They depend
// only on the model, so they live here at the emit layer where both the
// emitters and the orchestrator (which imports emit) can reach them.

// SnapshotLabels builds n timestep labels: the explicit Time.Timesteps when
// given (extended past their end by resolution-step arithmetic from the last
// timestamp), else timestamps from Time.Start stepping by Time.Resolution
// (hours), else integer indices.
func SnapshotLabels(tc model.TimeConfig, n int) []string {
	out := make([]string, n)
	if len(tc.Timesteps) > 0 {
		copy(out, tc.Timesteps)
		if n > len(tc.Timesteps) {
			extendTimestepLabels(out, tc)
		}
		return out
	}
	start, ok := ParseDate(tc.Start)
	step := ParseHours(tc.Resolution)
	if !ok || step == 0 {
		for i := range out {
			out[i] = strconv.Itoa(i)
		}
		return out
	}
	for i := range out {
		out[i] = start.Add(time.Duration(i) * step).Format("2006-01-02 15:04:05")
	}
	return out
}

// extendTimestepLabels fills out[len(tc.Timesteps):] by continuing the
// explicit timestep list at the model resolution. A series longer than the
// list must still yield a homogeneous datetime index — the old integer-index
// padding mixed bare ints into a datetime column, which breaks pandas
// to_datetime downstream. The continuation keeps the layout of the last
// explicit timestep unless that layout cannot express the step (date-only at
// sub-daily resolution). An unparseable last timestep leaves nothing to
// extrapolate from; integer indices remain as the last resort there.
func extendTimestepLabels(out []string, tc model.TimeConfig) {
	k := len(tc.Timesteps)
	last, layout, ok := parseDateLayout(tc.Timesteps[k-1])
	if !ok {
		for i := k; i < len(out); i++ {
			out[i] = strconv.Itoa(i)
		}
		return
	}
	step := ParseHours(tc.Resolution)
	if layout == "2006-01-02" && step%(24*time.Hour) != 0 {
		layout = "2006-01-02 15:04:05"
	}
	for i := k; i < len(out); i++ {
		out[i] = last.Add(time.Duration(i-k+1) * step).Format(layout)
	}
}

// SnapshotCount is the number of timesteps the emitted files should index: the
// longest inline series if any exists (all tables must agree), else the
// explicit timestep list, else the count implied by the time range, else 24.
func SnapshotCount(m *model.Model) int {
	n := 0
	for _, ts := range m.Timeseries {
		if ts.Source == "inline" && len(ts.Values) > n {
			n = len(ts.Values)
		}
	}
	if n > 0 {
		return n
	}
	if len(m.Time.Timesteps) > 0 {
		return len(m.Time.Timesteps)
	}
	start, ok1 := ParseDate(m.Time.Start)
	end, ok2 := ParseDate(m.Time.End)
	step := ParseHours(m.Time.Resolution)
	if ok1 && ok2 && step > 0 && end.After(start) {
		return int(end.Sub(start)/step) + 1
	}
	return 24
}

// SeriesValues returns the inline values of a time series by id.
func SeriesValues(m *model.Model, id string) ([]float64, bool) {
	ts, ok := m.Timeseries[id]
	if !ok || ts.Source != "inline" {
		return nil, false
	}
	return ts.Values, true
}

// SeriesAt reads index i from a series, padding past the end with the last value.
func SeriesAt(vals []float64, i int) float64 {
	if len(vals) == 0 {
		return 0
	}
	if i >= len(vals) {
		return vals[len(vals)-1]
	}
	return vals[i]
}

// MaxSeriesLen is the longest inline series referenced by any of the col sets.
func MaxSeriesLen(m *model.Model, colSets ...map[string]string) int {
	n := 0
	for _, cols := range colSets {
		for _, id := range cols {
			if v, ok := SeriesValues(m, id); ok && len(v) > n {
				n = len(v)
			}
		}
	}
	return n
}

// dateLayouts are the accepted ISO 8601 layouts (date-only through RFC 3339).
// Keep the list in sync with model.parseableDate, which duplicates it because
// emit imports model.
var dateLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05", "2006-01-02 15:04:05",
	"2006-01-02T15:04", "2006-01-02 15:04", // minute precision (no seconds)
	"2006-01-02",
}

// ParseDate parses s under the accepted ISO 8601 layouts.
func ParseDate(s string) (time.Time, bool) {
	t, _, ok := parseDateLayout(s)
	return t, ok
}

// parseDateLayout additionally reports which layout matched, so continuation
// labels can be formatted in the same spelling as their explicit neighbors.
func parseDateLayout(s string) (time.Time, string, bool) {
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, layout, true
		}
	}
	return time.Time{}, "", false
}

// ParseResolution parses a temporal-resolution string into a duration. It
// accepts pandas-style offset aliases — a count (integer or decimal, default
// 1) followed by a unit: "H"/"h" hours, "min"/"T"/"t" minutes, "S"/"s"
// seconds, "D"/"d" days — and simple ISO 8601 durations ("PT1H", "PT30M",
// "P1D", "PT1H30M"). Anything else is an error; the old digit-scrape silently
// misread "0.5H" as 5 hours, "30min" as 30 hours, and defaulted "D" to 1 hour.
func ParseResolution(res string) (time.Duration, error) {
	s := strings.TrimSpace(res)
	if s == "" {
		return 0, fmt.Errorf("empty resolution")
	}
	var d time.Duration
	var err error
	if s[0] == 'P' || s[0] == 'p' {
		d, err = parseISODuration(s)
	} else {
		d, err = parseOffsetAlias(s)
	}
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("resolution %q must be positive", res)
	}
	return d, nil
}

// ParseHours is the lenient companion of ParseResolution for the call sites
// that predate its error path: empty or unrecognized input falls back to one
// hour (validation is expected to reject bad resolutions before emission).
func ParseHours(res string) time.Duration {
	if res == "" {
		return time.Hour
	}
	d, err := ParseResolution(res)
	if err != nil {
		return time.Hour
	}
	return d
}

// parseOffsetAlias parses a pandas-style offset alias: optional count, unit.
func parseOffsetAlias(s string) (time.Duration, error) {
	numStr, unit := splitNumber(s)
	count := 1.0
	if numStr != "" {
		f, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return 0, fmt.Errorf("unrecognized resolution %q", s)
		}
		count = f
	}
	var base time.Duration
	switch strings.ToLower(unit) {
	case "h":
		base = time.Hour
	case "":
		// A bare count means hours: hours are the canonical unit and the
		// pre-grammar parser accepted "3" as 3H. (s is non-empty here, so an
		// empty unit implies the count parsed.)
		base = time.Hour
	case "min", "t":
		base = time.Minute
	case "s":
		base = time.Second
	case "d":
		base = 24 * time.Hour
	case "m":
		// pandas "M" is month-end, ISO 8601 "M" outside PT is months; a
		// calendar-dependent resolution cannot become a fixed duration, and
		// guessing minutes would corrupt monthly models (and vice versa).
		return 0, fmt.Errorf("ambiguous resolution unit in %q: use \"min\" for minutes", s)
	default:
		return 0, fmt.Errorf("unrecognized resolution %q", s)
	}
	return time.Duration(count * float64(base)), nil
}

// parseISODuration parses the subset of ISO 8601 durations that map to a
// fixed length: P[nD][T[nH][nM][nS]], with decimal counts allowed.
func parseISODuration(s string) (time.Duration, error) {
	rest, inTime, seen := s[1:], false, false
	var d time.Duration
	for len(rest) > 0 {
		if rest[0] == 'T' || rest[0] == 't' {
			if inTime {
				return 0, fmt.Errorf("unrecognized resolution %q", s)
			}
			inTime = true
			rest = rest[1:]
			continue
		}
		numStr, tail := splitNumber(rest)
		if numStr == "" || tail == "" {
			return 0, fmt.Errorf("unrecognized resolution %q", s)
		}
		f, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return 0, fmt.Errorf("unrecognized resolution %q", s)
		}
		unit := tail[0]
		rest = tail[1:]
		var base time.Duration
		switch {
		case !inTime && (unit == 'D' || unit == 'd'):
			base = 24 * time.Hour
		case inTime && (unit == 'H' || unit == 'h'):
			base = time.Hour
		case inTime && (unit == 'M' || unit == 'm'):
			base = time.Minute
		case inTime && (unit == 'S' || unit == 's'):
			base = time.Second
		default:
			// Y and M date designators are calendar-dependent; everything
			// else is malformed.
			return 0, fmt.Errorf("unrecognized resolution %q", s)
		}
		d += time.Duration(f * float64(base))
		seen = true
	}
	if !seen {
		return 0, fmt.Errorf("unrecognized resolution %q", s)
	}
	return d, nil
}

// splitNumber splits a leading decimal number (digits with at most one dot)
// off s, returning it and the remainder.
func splitNumber(s string) (num, rest string) {
	i, dot := 0, false
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.' && !dot) {
		if s[i] == '.' {
			dot = true
		}
		i++
	}
	return s[:i], s[i:]
}

// SeriesLengthMismatches reports the silent-padding hazards in the inline
// series registry: inline series shorter than the snapshot count the emitters
// will use (SnapshotCount — the longest inline series) are padded by
// repeating their last value at read time (SeriesAt), and an explicit
// timestep list shorter than that count is extended by resolution arithmetic
// (SnapshotLabels). Both are legitimate for constant tails but usually flag a
// truncated profile, so callers surface these as validation warnings.
// Messages are sorted for deterministic output.
func SeriesLengthMismatches(m *model.Model) []string {
	n := 0
	for _, ts := range m.Timeseries {
		if ts.Source == "inline" && len(ts.Values) > n {
			n = len(ts.Values)
		}
	}
	if n == 0 {
		return nil
	}
	var out []string
	for id, ts := range m.Timeseries {
		if ts.Source == "inline" && len(ts.Values) < n {
			out = append(out, fmt.Sprintf(
				"timeseries %q: %d values but the emitted time index has %d steps; the last value will be repeated",
				id, len(ts.Values), n))
		}
	}
	sort.Strings(out)
	if k := len(m.Time.Timesteps); k > 0 && k < n {
		out = append(out, fmt.Sprintf(
			"time.timesteps lists %d labels but the emitted time index has %d steps; labels will be extended at the model resolution",
			k, n))
	}
	return out
}
