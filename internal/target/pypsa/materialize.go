// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa

import (
	"math"

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
// storage_units-inflow.csv, and in a power flow the fixed dispatch series as
// generators-/storage_units-/links-p_set.csv. This completes the emitter's
// writeTimeVarying hook — a series-valued parameter never silently collapses
// to a scalar.
func MaterializeTimeSeries(m *model.Model, dir string) error {
	return materializeTimeSeries(m, dir, false)
}

// materializeTimeSeries is MaterializeTimeSeries with the power-flow switch:
// in mode power_flow the fixed dispatch, transfer and reactive series become
// the components' p_set / q_set files; an optimization run ignores those
// inputs (ValidateFor says so) and gets none of the files.
func materializeTimeSeries(m *model.Model, dir string, pfMode bool) error {
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
			// Power-flow inputs: a fixed dispatch is the component's p_set;
			// reactive power is a q_set series as given, or derived from a
			// series active set-point at the power factor (the scale is Q per
			// unit of P; writeSeriesCSV applies it).
			if eff.Dispatch.IsSeries() && pfMode {
				if file, ok := dispatchFile[eff.Role]; ok {
					add(file, name, eff.Dispatch.SeriesID())
				}
			}
			if file, ok := qFile[eff.Role]; ok && pfMode {
				if e := eff.Electrical; e != nil {
					if e.ReactiveDispatch.IsSeries() {
						add(file, name, e.ReactiveDispatch.SeriesID())
					} else if p := activeSetpoint(eff); e.PowerFactor != nil && p.IsSeries() {
						addScaled(file, name, p.SeriesID(), e.ReactiveFactor())
					}
				}
			}
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
				if eff.Storage != nil && eff.Storage.SOCProfile.IsSeries() {
					add("storage_units-state_of_charge_set.csv", name, eff.Storage.SOCProfile.SeriesID())
				}
				// A Store's variable cost sits on its discharger link.
				if eff.Storage.IsStore() {
					if c, ok := eff.Costs[model.PrimaryCostClass]; ok && c.VariableOM.IsSeries() {
						add("links-marginal_cost.csv", name+"_discharge", c.VariableOM.SeriesID())
					}
				}
			}
		}
	}
	// A controlled link's transfer series (power flow) is its p_set.
	for id, l := range m.Transmission {
		if pfMode && l.Dispatch.IsSeries() && l.Electrical == nil {
			add("links-p_set.csv", id, l.Dispatch.SeriesID())
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

	// The snapshot index spans the longest series, else the horizon
	// model.time describes (emit.SnapshotCount, the same calendar Calliope's
	// data tables use). A scalar-only model used to get no snapshots.csv at
	// all, leaving PyPSA on its single default snapshot while the other
	// targets solved the whole time range — the same payload, a different
	// problem.
	n := 0
	for _, cols := range files {
		for _, ref := range cols {
			if v, ok := emit.SeriesValues(m, ref.id); ok && len(v) > n {
				n = len(v)
			}
		}
	}
	if n == 0 {
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
			v := ref.scale * emit.SeriesAt(vals, i)
			if ref.scale != 1 && ref.scale != -1 {
				v = math.Round(v*1e12) / 1e12 // a derived value, without float noise
			}
			row = append(row, emit.Ftoa(v))
		}
		out = append(out, row)
	}
	return emit.WriteCSV(dir, file, out)
}
