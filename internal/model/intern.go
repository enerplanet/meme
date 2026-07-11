// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package model

import (
	"fmt"
	"strings"
)

// InternInlineSeries moves every inline per-timestep array ([0.1, 0.4, ...]
// in the JSON) out of its Value slot into the Timeseries registry and rewrites
// the Value to a series reference. After this pass the emitters see exactly
// one series shape (a Ref resolving against Model.Timeseries), so none of them
// needs an inline-array branch. Generated ids are deterministic
// ("_inline:<kind>:<id>:<field>") and the prefix is reserved: an "_inline:"
// registry entry that no Value references (a user-defined squatter rather
// than this pass's own output) is rejected. That keeps the pass idempotent —
// re-running over already-interned Values (now Refs) changes nothing.
// target.ValidateFor calls this before validation, so every run path gets it.
func (m *Model) InternInlineSeries() error {
	referenced := map[string]bool{}
	intern := func(id string, v *Value) {
		if v == nil {
			return
		}
		if v.IsSeries() {
			referenced[v.SeriesID()] = true
			return
		}
		if !v.IsInline() {
			return
		}
		if m.Timeseries == nil {
			m.Timeseries = map[string]TimeSeries{}
		}
		m.Timeseries[id] = TimeSeries{Source: "inline", Values: v.Inline}
		*v = Value{Ref: id}
		referenced[id] = true
	}
	key := func(parts ...string) string {
		id := "_inline"
		for _, p := range parts {
			id += ":" + p
		}
		return id
	}

	intern(key("time", "weights"), m.Time.Weights)

	for _, tid := range keysOf(m.Technologies) {
		t := m.Technologies[tid]
		intern(key("tech", tid, "efficiency"), t.Efficiency)
		intern(key("tech", tid, "demand_profile"), t.DemandProfile)
		if t.Operation != nil {
			intern(key("tech", tid, "max_pu"), t.Operation.MaxPU)
			intern(key("tech", tid, "min_pu"), t.Operation.MinPU)
			intern(key("tech", tid, "equals_pu"), t.Operation.EqualsPU)
		}
		if t.Storage != nil {
			intern(key("tech", tid, "inflow"), t.Storage.Inflow)
		}
		if t.Source != nil {
			intern(key("tech", tid, "source_max"), t.Source.Max)
		}
		if t.Performance != nil {
			intern(key("tech", tid, "performance_efficiency"), t.Performance.Efficiency)
			intern(key("tech", tid, "precomputed"), t.Performance.Precomputed)
		}
		for _, class := range keysOf(t.Costs) {
			intern(key("tech", tid, "variable_om", class), t.Costs[class].VariableOM)
			intern(key("tech", tid, "fuel_cost", class), t.Costs[class].FuelCost)
		}
		for _, nid := range keysOf(t.NodeOverrides) {
			ov := t.NodeOverrides[nid]
			intern(key("tech", tid, nid, "efficiency"), ov.Efficiency)
			intern(key("tech", tid, nid, "demand_profile"), ov.DemandProfile)
			if ov.Operation != nil {
				intern(key("tech", tid, nid, "max_pu"), ov.Operation.MaxPU)
				intern(key("tech", tid, nid, "min_pu"), ov.Operation.MinPU)
				intern(key("tech", tid, nid, "equals_pu"), ov.Operation.EqualsPU)
			}
			if ov.Storage != nil {
				intern(key("tech", tid, nid, "inflow"), ov.Storage.Inflow)
			}
			if ov.Source != nil {
				intern(key("tech", tid, nid, "source_max"), ov.Source.Max)
			}
			if ov.Performance != nil {
				intern(key("tech", tid, nid, "performance_efficiency"), ov.Performance.Efficiency)
				intern(key("tech", tid, nid, "precomputed"), ov.Performance.Precomputed)
			}
			for _, class := range keysOf(ov.Costs) {
				intern(key("tech", tid, nid, "variable_om", class), ov.Costs[class].VariableOM)
				intern(key("tech", tid, nid, "fuel_cost", class), ov.Costs[class].FuelCost)
			}
		}
	}

	for _, nid := range keysOf(m.Nodes) {
		clim := m.Nodes[nid].Climate
		for _, col := range keysOf(clim) {
			v := clim[col]
			if v.IsInline() {
				intern(key("node", nid, col), &v)
				clim[col] = v
			}
		}
	}

	for _, trID := range keysOf(m.Trade) {
		tr := m.Trade[trID]
		if tr.Import != nil {
			intern(key("trade", trID, "import_limit"), tr.Import.Limit)
			intern(key("trade", trID, "import_price"), tr.Import.Price)
		}
		if tr.Export != nil {
			intern(key("trade", trID, "export_limit"), tr.Export.Limit)
			intern(key("trade", trID, "export_price"), tr.Export.Price)
		}
	}

	for _, id := range keysOf(m.Timeseries) {
		if strings.HasPrefix(id, "_inline:") && !referenced[id] {
			return fmt.Errorf("timeseries %q: the \"_inline:\" prefix is reserved for interned inline arrays", id)
		}
	}
	return nil
}
