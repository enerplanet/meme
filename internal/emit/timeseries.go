// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package emit

import (
	"strconv"
	"time"

	"github.com/enerplanet/meme/internal/model"
)

// Shared time-series helpers used by every emitter that materializes the inline
// series registry into files (PyPSA CSVs, Calliope data tables). They depend
// only on the model, so they live here at the emit layer where both the
// emitters and the orchestrator (which imports emit) can reach them.

// SnapshotLabels builds n timestep labels: the explicit Time.Timesteps when
// given (padded with integer indices past their end), else timestamps from
// Time.Start stepping by Time.Resolution (hours), else integer indices.
func SnapshotLabels(tc model.TimeConfig, n int) []string {
	out := make([]string, n)
	if len(tc.Timesteps) > 0 {
		for i := range out {
			if i < len(tc.Timesteps) {
				out[i] = tc.Timesteps[i]
			} else {
				out[i] = strconv.Itoa(i)
			}
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

// ParseDate parses s under the accepted ISO 8601 layouts (date-only through
// RFC 3339). Keep the layout list in sync with model.parseableDate, which
// duplicates it because emit imports model.
func ParseDate(s string) (time.Time, bool) {
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05", "2006-01-02 15:04:05",
		"2006-01-02T15:04", "2006-01-02 15:04", // minute precision (no seconds)
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ParseHours extracts the hour count from a pandas-style frequency alias
// ("1H", "3h"); unparseable or zero input defaults to one hour.
func ParseHours(res string) time.Duration {
	if res == "" {
		return time.Hour
	}
	num := ""
	for _, r := range res {
		if r >= '0' && r <= '9' {
			num += string(r)
		}
	}
	h, err := strconv.Atoi(num)
	if err != nil || h == 0 {
		return time.Hour
	}
	return time.Duration(h) * time.Hour
}
