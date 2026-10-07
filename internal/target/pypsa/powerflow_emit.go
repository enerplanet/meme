// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa

import (
	"os"

	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/model"
)

// emitPowerFlow writes a self-contained PyPSA CSV import folder for a
// power-flow job: buses (with v_nom), carriers, lines, transformers,
// generators and loads (with per-snapshot p_set series) — all from the
// top-level model.PowerFlow block. It REPLACES the normal dispatch emission
// for such a job: the power-flow network is authoritative (lines carry real
// r/x/s_nom, unlike the transport Links the dispatch path emits), and run.py's
// `power_flow` mode imports exactly this folder and runs lpf()/pf().
//
// The p_set series are written as per-component per-snapshot CSVs
// (generators-p_set.csv, loads-p_set.csv) plus snapshots.csv, using the same
// row layout the MaterializeTimeSeries emitter writes (snapshot-label columns),
// so run.py can read them as fixed injections per snapshot.
func emitPowerFlow(j *model.Job, outDir string) error {
	pf := j.PowerFlow
	if pf == nil {
		return nil
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	if err := emit.WriteCSV(outDir, "network.csv", [][]string{
		{"name", "pypsa_version"},
		{j.Model.Metadata.Name, pypsaTargetVersion},
	}); err != nil {
		return err
	}

	// carriers (constant) ------------------------------------------------------------------
	// PyPSA classifies a sub-network as electric purely by the BUS CARRIER NAME:
	// networks.py: "if carrier not in ['AC','DC'] ..." and power_flow.py:
	// "if sub_networks.carrier != 'AC'" (no Y built otherwise -> pf() fails with
	// "'SubNetwork' object has no attribute 'Y'"). The carrier must therefore be
	// NAMED "AC"; the `type` column is not consulted for this determination.
	if err := emit.WriteCSV(outDir, "carriers.csv", [][]string{
		{"name", "co2_emissions"},
		{"AC", "0"},
	}); err != nil {
		return err
	}

	// buses with v_nom ------------------------------------------------------
	rows := [][]string{{"name", "carrier", "v_nom"}}
	for _, b := range pf.Buses {
		rows = append(rows, []string{b.Name, "AC", emit.Ftoa(b.VNomKV)})
	}
	if err := emit.WriteCSV(outDir, "buses.csv", rows); err != nil {
		return err
	}

	// lines (literal r/x/s_nom) --------------------------------------------
	if len(pf.Lines) > 0 {
		rows := [][]string{{"name", "bus0", "bus1", "length", "r", "x", "s_nom", "num_parallel"}}
		for _, l := range pf.Lines {
			rows = append(rows, []string{
				l.Name, l.Bus0, l.Bus1,
				emit.Ftoa(l.LengthKm), emit.Ftoa(l.ROhm), emit.Ftoa(l.XOhm),
				emit.Ftoa(l.SNomMVA), emit.Ftoa(l.NumParallel),
			})
		}
		if err := emit.WriteCSV(outDir, "lines.csv", rows); err != nil {
			return err
		}
	}

	// transformers (literal s_nom + small per-unit impedance) ----------------
	if len(pf.Transformers) > 0 {
		rows := [][]string{{"name", "bus0", "bus1", "s_nom", "r", "x", "num_parallel"}}
		for _, t := range pf.Transformers {
			rows = append(rows, []string{
				t.Name, t.Bus0, t.Bus1,
				emit.Ftoa(t.SNomMVA),
				emit.Ftoa(0.001), emit.Ftoa(0.004), // per-unit defaults for a solvable solve
				emit.Ftoa(t.NumParallel),
			})
		}
		if err := emit.WriteCSV(outDir, "transformers.csv", rows); err != nil {
			return err
		}
	}

	// generators + loads (static attrs) + the p_set series -------------------
	genRows := [][]string{{"name", "bus", "carrier", "control", "p_nom"}}
	loadRows := [][]string{{"name", "bus", "p_set"}}
	nSnaps := 0
	for _, g := range pf.Generators {
		// p_nom: a conservative generator rating — the max dispatch, so the
		// injection is always within nameplate (PF fixes p; p_nom is unused but
		// keeps the CSV import consistent).
		r := 1.0
		if l := maxFloat(g.PSetMW); l > r {
			r = l
		}
		genRows = append(genRows, []string{g.Name, g.Bus, "AC", g.Control, emit.Ftoa(r)})
		if len(g.PSetMW) > nSnaps {
			nSnaps = len(g.PSetMW)
		}
	}
	for _, l := range pf.Loads {
		loadRows = append(loadRows, []string{l.Name, l.Bus, "0"})
		if len(l.PSetMW) > nSnaps {
			nSnaps = len(l.PSetMW)
		}
	}
	if err := emit.WriteCSV(outDir, "generators.csv", genRows); err != nil {
		return err
	}
	if err := emit.WriteCSV(outDir, "loads.csv", loadRows); err != nil {
		return err
	}

	// snapshots + per-component p_set series ---------------------------------
	labels := emit.SnapshotLabels(j.Model.Time, nSnaps)
	if err := emit.WriteCSV(outDir, "snapshots.csv", snapshotRows(labels)); err != nil {
		return err
	}
	genPSets := map[string][]float64{}
	for _, g := range pf.Generators {
		if len(g.PSetMW) > 0 {
			genPSets[g.Name] = g.PSetMW
		}
	}
	loadPSets := map[string][]float64{}
	for _, l := range pf.Loads {
		if len(l.PSetMW) > 0 {
			loadPSets[l.Name] = l.PSetMW
		}
	}
	if err := writePSetCSV(outDir, "generators-p_set.csv", labels, genPSets); err != nil {
		return err
	}
	if err := writePSetCSV(outDir, "loads-p_set.csv", labels, loadPSets); err != nil {
		return err
	}
	return nil
}

// snapshotRows renders snapshots.csv (name column only) from the labels.
func snapshotRows(labels []string) [][]string {
	out := [][]string{{"name"}}
	for _, l := range labels {
		out = append(out, []string{l})
	}
	return out
}

// writePSetCSV writes a snapshot-indexed CSV (snapshot column + one column per
// component) from a name->series map, matching MaterializeTimeSeries's
// writeSeriesCSV row layout.
func writePSetCSV(dir, file string, labels []string, cols map[string][]float64) error {
	if len(cols) == 0 {
		return nil
	}
	names := emit.Keys(cols)
	header := append([]string{"snapshot"}, names...)
	out := [][]string{header}
	for i, label := range labels {
		row := []string{label}
		for _, name := range names {
			row = append(row, emit.Ftoa(atSeries(cols[name], i)))
		}
		out = append(out, row)
	}
	return emit.WriteCSV(dir, file, out)
}

func atSeries(vals []float64, i int) float64 {
	if i < len(vals) {
		return vals[i]
	}
	if len(vals) > 0 {
		return vals[len(vals)-1]
	}
	return 0
}

func maxFloat(vals []float64) float64 {
	m := 0.0
	for _, v := range vals {
		if v > m {
			m = v
		}
	}
	return m
}
