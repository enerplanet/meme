// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package pypsa_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
)

func TestNodeOverride(t *testing.T) {
	csv := readFile(t, emitSampleDir(t), "generators.csv")
	// pv is on n1 (base max 500) and n2 (override max 300).
	var n1, n2 string
	for _, line := range strings.Split(csv, "\n") {
		if strings.HasPrefix(line, "pv@n1,") {
			n1 = line
		}
		if strings.HasPrefix(line, "pv@n2,") {
			n2 = line
		}
	}
	if n1 == "" || n2 == "" {
		t.Fatalf("missing pv rows:\n%s", csv)
	}
	if !strings.Contains(n1, "500") {
		t.Errorf("pv@n1 should keep base p_nom_max 500: %s", n1)
	}
	if !strings.Contains(n2, "300") {
		t.Errorf("pv@n2 should use override p_nom_max 300: %s", n2)
	}
}

func TestConstraintGlobalVsSidecar(t *testing.T) {
	dir := emitSampleDir(t)
	gc := readFile(t, dir, "global_constraints.csv")
	// A PyPSA GlobalConstraint always sums over EVERY component of a carrier, so
	// the tech-scoped max_vre_capacity (techs=[pv]) must NOT be projected — it
	// goes to the sidecar, which run.py applies with exact scoping. Only the
	// unscoped emission cap stays a GlobalConstraint.
	if !strings.Contains(gc, "co2_cap") {
		t.Errorf("expected co2_cap as a global constraint:\n%s", gc)
	}
	if strings.Contains(gc, "max_vre_capacity") {
		t.Errorf("tech-scoped max_vre_capacity must not widen into a global constraint:\n%s", gc)
	}
	sc := readFile(t, dir, "_constraints.json")
	for _, want := range []string{"max_vre_capacity", "pv_plus_ccgt_cap"} {
		if !strings.Contains(sc, want) {
			t.Errorf("expected %s in sidecar:\n%s", want, sc)
		}
	}
	if strings.Contains(gc, "pv_plus_ccgt_cap") {
		t.Errorf("mixed constraint should NOT be a global constraint:\n%s", gc)
	}
}

func TestIndexedValueRoundTripAndReduce(t *testing.T) {
	raw := `{"data":[0.42],"index":[["monetary"]],"dims":["costs"]}`
	var v model.Value
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !v.IsIndexed() {
		t.Fatal("expected indexed value")
	}
	if f, ok := v.Reduce(); !ok || f != 0.42 {
		t.Errorf("single-entry indexed should reduce to 0.42, got %v ok=%v", f, ok)
	}
	if f, ok := v.Select("costs", "monetary"); !ok || f != 0.42 {
		t.Errorf("Select(costs,monetary) should be 0.42, got %v ok=%v", f, ok)
	}
	out, err := json.Marshal(&v)
	if err != nil || !strings.Contains(string(out), "\"dims\"") {
		t.Errorf("round-trip marshal failed: %s err=%v", out, err)
	}
}

func TestMultiValuedIndexedRejectedForPyPSA(t *testing.T) {
	indexed := &model.Value{Indexed: &model.IndexedParam{
		Data:  []float64{0.9, 0.95},
		Index: [][]string{{"a"}, {"b"}},
		Dims:  []string{"scenario"},
	}}

	j := loadSample(t)
	pv := j.Model.Technologies["pv"]
	pv.Efficiency = indexed
	j.Model.Technologies["pv"] = pv
	if _, err := validateFor(&j, model.TargetPyPSA); err == nil {
		t.Error("expected pypsa to reject a multi-valued indexed parameter")
	}

	jc := loadSampleFor(t, model.TargetCalliope)
	pvc := jc.Model.Technologies["pv"]
	pvc.Efficiency = indexed
	jc.Model.Technologies["pv"] = pvc
	if _, err := validateFor(&jc, model.TargetCalliope); err != nil {
		t.Errorf("calliope should accept indexed parameters: %v", err)
	}
}

func TestNodeOverrideRejectsUnlistedNode(t *testing.T) {
	j := loadSample(t)
	ccgt := j.Model.Technologies["ccgt"] // ccgt is only on n1
	max := 50.0
	ccgt.NodeOverrides = map[string]model.NodeOverride{"n2": {Capacity: &model.Capacity{Max: &max}}}
	j.Model.Technologies["ccgt"] = ccgt
	if err := j.Model.Validate(); err == nil {
		t.Error("expected override on a node not in the tech's node list to be rejected")
	}
}
