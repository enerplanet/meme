// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa

import (
	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/model"
)

// seriesRef is one materialized column: a registry series id and a multiplier
// (export trade limits enter the p_min_pu file negated).
type seriesRef struct {
	id    string
	scale float64
}

// MaterializeTimeSeries writes snapshots.csv and the per-attribute time-varying
// files PyPSA reads from the inline time-series registry: loads-p_set.csv,
// generators-p_max_pu.csv / p_min_pu.csv (availability and series trade
// limits), generators-marginal_cost.csv (series variable_om and trade prices),
// links-marginal_cost.csv (series variable_om on conversions) and
// storage_units-inflow.csv. This completes the emitter's writeTimeVarying
// hook — a series-valued parameter never silently collapses to a scalar.
func MaterializeTimeSeries(m *model.Model, dir string) error {
	files := map[string]map[string]seriesRef{} // file -> component -> series
	addScaled := func(file, component, seriesID string, scale float64) {
		if files[file] == nil {
			files[file] = map[string]seriesRef{}
		}
		files[file][component] = seriesRef{id: seriesID, scale: scale}
	}
	add := func(file, component, seriesID string) { addScaled(file, component, seriesID, 1) }

	for id, t := range m.Technologies {
		for _, node := range t.Node {
			name := id
			if len(t.Node) > 1 {
				name = id + "@" + node
			}
			eff := t.At(node)
			switch eff.Role {
			case model.RoleDemand:
				if eff.DemandProfile.IsSeries() {
					add("loads-p_set.csv", name, eff.DemandProfile.SeriesID())
				}
			case model.RoleSupply:
				if eff.Operation != nil && eff.Operation.EqualsPU.IsSeries() {
					// Fixed dispatch: the same series pins both bounds.
					add("generators-p_max_pu.csv", name, eff.Operation.EqualsPU.SeriesID())
					add("generators-p_min_pu.csv", name, eff.Operation.EqualsPU.SeriesID())
				} else if eff.Operation != nil && eff.Operation.MaxPU.IsSeries() {
					add("generators-p_max_pu.csv", name, eff.Operation.MaxPU.SeriesID())
				} else if eff.Performance != nil && eff.Performance.Kind() == model.PerfPhysics && eff.Performance.Precomputed.IsSeries() {
					add("generators-p_max_pu.csv", name, eff.Performance.Precomputed.SeriesID())
				}
				if c, ok := eff.Costs[model.PrimaryCostClass]; ok && c.VariableOM.IsSeries() {
					add("generators-marginal_cost.csv", name, c.VariableOM.SeriesID())
				}
			case model.RoleConversion:
				if c, ok := eff.Costs[model.PrimaryCostClass]; ok && c.VariableOM.IsSeries() {
					add("links-marginal_cost.csv", name, c.VariableOM.SeriesID())
				}
			case model.RoleStorage:
				if eff.Storage != nil && eff.Storage.Inflow.IsSeries() {
					add("storage_units-inflow.csv", name, eff.Storage.Inflow.SeriesID())
				}
			}
		}
	}
	// Trade market generators: a series price becomes a time-varying marginal
	// cost (import: cost of buying; export: revenue via negative dispatch); a
	// series limit becomes the dispatch bound of the fixed-p_nom=1 generator
	// (import: p_max_pu; export: p_min_pu = -limit, mirroring the static -1).
	for id, tr := range m.Trade {
		if tr.Import != nil {
			if tr.Import.Price.IsSeries() {
				add("generators-marginal_cost.csv", id+"_import", tr.Import.Price.SeriesID())
			}
			if tr.Import.Limit.IsSeries() {
				add("generators-p_max_pu.csv", id+"_import", tr.Import.Limit.SeriesID())
			}
		}
		if tr.Export != nil {
			if tr.Export.Price.IsSeries() {
				add("generators-marginal_cost.csv", id+"_export", tr.Export.Price.SeriesID())
			}
			if tr.Export.Limit.IsSeries() {
				addScaled("generators-p_min_pu.csv", id+"_export", tr.Export.Limit.SeriesID(), -1)
			}
		}
	}

	if len(files) == 0 && m.Time.Weights == nil {
		return nil
	}
	n := 0
	for _, cols := range files {
		for _, ref := range cols {
			if v, ok := emit.SeriesValues(m, ref.id); ok && len(v) > n {
				n = len(v)
			}
		}
	}
	if n == 0 {
		if m.Time.Weights == nil {
			return nil
		}
		// Weights alone still need snapshots.csv; size it from the horizon.
		n = emit.SnapshotCount(m)
	}
	labels := emit.SnapshotLabels(m.Time, n)

	// snapshots.csv: the time index, plus the snapshot weightings when set
	// (PyPSA reads objective/generators/stores as extra columns).
	header := []string{"name"}
	var weights []float64
	if m.Time.Weights != nil {
		header = append(header, "objective", "generators", "stores")
		if vals, ok := emit.SeriesValues(m, m.Time.Weights.SeriesID()); ok {
			weights = vals
		} else if f, ok := m.Time.Weights.Reduce(); ok {
			weights = []float64{f} // SeriesAt pads with the last value
		}
	}
	snaps := [][]string{header}
	for i, l := range labels {
		row := []string{l}
		if weights != nil {
			w := emit.Ftoa(emit.SeriesAt(weights, i))
			row = append(row, w, w, w)
		}
		snaps = append(snaps, row)
	}
	if err := emit.WriteCSV(dir, "snapshots.csv", snaps); err != nil {
		return err
	}
	for _, file := range emit.Keys(files) {
		if err := writeSeriesCSV(dir, file, labels, files[file], m); err != nil {
			return err
		}
	}
	return nil
}

func writeSeriesCSV(dir, file string, labels []string, cols map[string]seriesRef, m *model.Model) error {
	if len(cols) == 0 {
		return nil
	}
	names := emit.Keys(cols)
	header := append([]string{"snapshot"}, names...)
	out := [][]string{header}
	for i, label := range labels {
		row := []string{label}
		for _, name := range names {
			ref := cols[name]
			vals, _ := emit.SeriesValues(m, ref.id)
			row = append(row, emit.Ftoa(ref.scale*emit.SeriesAt(vals, i)))
		}
		out = append(out, row)
	}
	return emit.WriteCSV(dir, file, out)
}
