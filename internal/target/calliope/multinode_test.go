// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope_test

import (
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
)

// A technology listed at several nodes is one copy of the technology per node
// (issue #7). Its node-indexed inputs — the demand profile, a supply
// availability series, a source series — must therefore reach EVERY listed
// node: one data table per (tech, parameter) whose CSV carries one column per
// node, each column seen through that node's overrides. Before the fix the
// table was written for the first node only, so the other nodes had no
// demand to meet and nothing warned.

// twoNodeTransmission is the issue's reproduction: calliope_transmission.json
// with gen and load both placed at n1 and n2.
func twoNodeTransmission(t *testing.T) model.Job {
	t.Helper()
	j := loadScenario(t, "calliope_transmission.json")
	for _, id := range []string{"gen", "load"} {
		tech := j.Model.Technologies[id]
		tech.Node = model.StringList{"n1", "n2"}
		j.Model.Technologies[id] = tech
	}
	return j
}

// setSeries registers an inline time series on the job (the scenario may carry
// no timeseries registry at all).
func setSeries(j *model.Job, id string, values ...float64) {
	if j.Model.Timeseries == nil {
		j.Model.Timeseries = map[string]model.TimeSeries{}
	}
	j.Model.Timeseries[id] = model.TimeSeries{Source: "inline", Values: values}
}

// dataTableFile returns the CSV file the data table `key` in model.yaml points
// at (the `table:` entry under data_tables.<key>), or "" when absent.
func dataTableFile(doc, key string) string {
	lines := strings.Split(doc, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != key+":" {
			continue
		}
		for _, next := range lines[i+1:] {
			if strings.HasPrefix(strings.TrimSpace(next), "table: ") {
				return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(next), "table: "))
			}
			if !strings.HasPrefix(next, "    ") {
				break
			}
		}
	}
	return ""
}

// csvHeader returns the two header rows (techs, nodes) and the first data row
// of a data-table CSV.
func csvHeader(t *testing.T, dir, file string) (techs, nodes, first string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(readFile(t, dir, file)), "\n")
	if len(lines) < 3 {
		t.Fatalf("%s: expected two header rows and data, got %q", file, lines)
	}
	return lines[0], lines[1], lines[2]
}

// A demand profile placed at two nodes is written for both nodes, in one data
// table with one column per node, so each node draws the full profile.
func TestCalliopeMultiNodeDemandProfile(t *testing.T) {
	j := twoNodeTransmission(t)
	if _, err := validateFor(&j, model.TargetCalliope); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := emitCalliopeDir(t, j)
	doc := readFile(t, dir, "model.yaml")
	file := dataTableFile(doc, "load__sink_use_equals")
	if file == "" {
		t.Fatalf("no load__sink_use_equals data table in model.yaml:\n%s", doc)
	}
	if strings.Count(doc, "load__sink_use_equals:") != 1 {
		t.Errorf("want exactly one data table for the load profile, got:\n%s", doc)
	}
	techs, nodes, first := csvHeader(t, dir, file)
	if techs != "techs,load,load" || nodes != "nodes,n1,n2" {
		t.Errorf("header = %q / %q, want techs,load,load / nodes,n1,n2 (one column per placed node)", techs, nodes)
	}
	if !strings.HasPrefix(first, "2025-01-01 00:00:00,100,100") {
		t.Errorf("first row = %q, want the profile at both nodes", first)
	}
	// Both nodes still list the tech.
	for _, want := range []string{"  n1:\n", "  n2:\n"} {
		if !strings.Contains(doc, want) {
			t.Errorf("model.yaml missing node %q", strings.TrimSpace(want))
		}
	}
}

// A supply availability series (operation.max_pu) placed at two nodes is
// written for both nodes as well.
func TestCalliopeMultiNodeSupplyAvailability(t *testing.T) {
	j := twoNodeTransmission(t)
	setSeries(&j, "cf", 0, 0.2, 0.6, 0.9)
	gen := j.Model.Technologies["gen"]
	gen.Operation = &model.Operation{MaxPU: model.Series("cf")}
	j.Model.Technologies["gen"] = gen
	if _, err := validateFor(&j, model.TargetCalliope); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := emitCalliopeDir(t, j)
	doc := readFile(t, dir, "model.yaml")
	file := dataTableFile(doc, "gen__source_use_max")
	if file == "" {
		t.Fatalf("no gen__source_use_max data table in model.yaml:\n%s", doc)
	}
	techs, nodes, first := csvHeader(t, dir, file)
	if techs != "techs,gen,gen" || nodes != "nodes,n1,n2" {
		t.Errorf("header = %q / %q, want techs,gen,gen / nodes,n1,n2", techs, nodes)
	}
	if !strings.HasPrefix(first, "2025-01-01 00:00:00,0,0") {
		t.Errorf("first row = %q, want the availability at both nodes", first)
	}
	if !strings.Contains(doc, "source_unit: per_cap") {
		t.Errorf("availability must stay per unit of flow_cap:\n%s", doc)
	}
}

// A per-node demand_profile override lands in that node's column; the other
// node keeps the base profile.
func TestCalliopeNodeOverrideDemandProfile(t *testing.T) {
	j := twoNodeTransmission(t)
	setSeries(&j, "dem2", 7, 8, 9)
	load := j.Model.Technologies["load"]
	load.NodeOverrides = map[string]model.NodeOverride{"n2": {DemandProfile: model.Series("dem2")}}
	j.Model.Technologies["load"] = load
	if _, err := validateFor(&j, model.TargetCalliope); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := emitCalliopeDir(t, j)
	doc := readFile(t, dir, "model.yaml")
	file := dataTableFile(doc, "load__sink_use_equals")
	if file == "" {
		t.Fatalf("no load__sink_use_equals data table in model.yaml:\n%s", doc)
	}
	_, nodes, first := csvHeader(t, dir, file)
	if nodes != "nodes,n1,n2" {
		t.Errorf("nodes header = %q, want nodes,n1,n2", nodes)
	}
	if !strings.HasPrefix(first, "2025-01-01 00:00:00,100,7") {
		t.Errorf("first row = %q, want base profile at n1 and the override at n2", first)
	}
}

// Two techs referencing the same series must not share one CSV: each table
// gets its own file, named after tech and parameter, with its own headers.
func TestCalliopeSharedSeriesSeparateTables(t *testing.T) {
	j := loadScenario(t, "calliope_node_override.json") // l1 at n1, l2 at n2
	for _, id := range []string{"l1", "l2"} {
		tech := j.Model.Technologies[id]
		tech.DemandProfile = model.Series("dem")
		j.Model.Technologies[id] = tech
	}
	j.Model.Timeseries = map[string]model.TimeSeries{"dem": {Source: "inline", Values: []float64{10, 20, 30}}}
	if _, err := validateFor(&j, model.TargetCalliope); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := emitCalliopeDir(t, j)
	doc := readFile(t, dir, "model.yaml")
	f1, f2 := dataTableFile(doc, "l1__sink_use_equals"), dataTableFile(doc, "l2__sink_use_equals")
	if f1 == "" || f2 == "" || f1 == f2 {
		t.Fatalf("each tech needs its own data-table file, got l1=%q l2=%q", f1, f2)
	}
	if _, nodes, _ := csvHeader(t, dir, f1); nodes != "nodes,n1" {
		t.Errorf("l1 nodes header = %q, want nodes,n1", nodes)
	}
	if _, nodes, _ := csvHeader(t, dir, f2); nodes != "nodes,n2" {
		t.Errorf("l2 nodes header = %q, want nodes,n2", nodes)
	}
}
