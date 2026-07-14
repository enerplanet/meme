// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa_test

import (
	"encoding/csv"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
)

// The multi-carrier CHP in the sample declares its carriers only through
// `flows`. Every bus a link references (bus0/bus1/bus2/…) must exist in
// buses.csv, or PyPSA 1.x raises a ConsistencyError on unknown buses.
func TestPyPSALinkBusesAreDefined(t *testing.T) {
	dir := emitSample(t)

	defined := map[string]bool{}
	for _, rec := range parseCSV(t, readFile(t, dir, "buses.csv")) {
		defined[rec["name"]] = true
	}
	if !defined["n1::gas"] {
		t.Errorf("expected gas bus n1::gas to be materialized from chp flows; buses=%v", defined)
	}

	for _, rec := range parseCSV(t, readFile(t, dir, "links.csv")) {
		for col, val := range rec {
			if !strings.HasPrefix(col, "bus") || val == "" {
				continue
			}
			if !defined[val] {
				t.Errorf("link %q references undefined bus %q (col %s)", rec["name"], val, col)
			}
		}
	}
}

// Costs stored under a class other than the primary "monetary" class are
// silently dropped by the emitters; ValidateFor should warn about it.
func TestCostClassWarning(t *testing.T) {
	j := loadSample(t)

	// Re-file some technology's costs under a non-primary class name.
	var victim string
	for id, tech := range j.Model.Technologies {
		if len(tech.Costs) == 0 {
			continue
		}
		if c, ok := tech.Costs[model.PrimaryCostClass]; ok {
			tech.Costs = map[string]model.CostClass{"capex": c}
			j.Model.Technologies[id] = tech
			victim = id
			break
		}
	}
	if victim == "" {
		t.Skip("sample has no technology with monetary costs to rewrite")
	}

	warns, err := validateFor(&j, model.TargetPyPSA)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w, victim) && strings.Contains(w, model.PrimaryCostClass) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a cost-class warning naming %q and %q; got %v", victim, model.PrimaryCostClass, warns)
	}
}

// A well-formed payload (costs under the primary class) must NOT warn about costs.
func TestNoCostClassWarningWhenPrimary(t *testing.T) {
	j := loadSample(t)
	warns, err := validateFor(&j, model.TargetPyPSA)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, w := range warns {
		if strings.Contains(w, "class") && strings.Contains(w, "ignored") {
			t.Errorf("unexpected cost-class warning for valid payload: %q", w)
		}
	}
}

func parseCSV(t *testing.T, content string) []map[string]string {
	t.Helper()
	r := csv.NewReader(strings.NewReader(content))
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(rows) == 0 {
		return nil
	}
	head := rows[0]
	out := make([]map[string]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		rec := map[string]string{}
		for i, h := range head {
			if i < len(row) {
				rec[h] = row[i]
			}
		}
		out = append(out, rec)
	}
	return out
}
