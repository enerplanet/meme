// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope_test

import (
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
)

// Calliope 0.7 operate mode sizes nothing: flow_cap, storage_cap, source_cap
// and area_use are input parameters (default infinite) and *_max bounds have no
// variable left to bound (issue #8). Every element with a capacity must
// therefore arrive as a fixed input — technologies did, trade imports and
// transmission lines did not — and a model in which nothing defines flow_cap
// must be rejected up front, because Calliope's postprocessing aborts on the
// missing array only after the solve.

// block returns the YAML block of one tech entry (from "  <id>:" to the next
// two-space-indented key).
func block(doc, id string) string {
	start := strings.Index(doc, "\n  "+id+":\n")
	if start < 0 {
		return ""
	}
	rest := doc[start+1:]
	end := strings.Index(rest[1:], "\n  ")
	for end >= 0 && strings.HasPrefix(rest[1+end+1:], "   ") { // deeper indent: keep scanning
		next := strings.Index(rest[1+end+1:], "\n  ")
		if next < 0 {
			return rest
		}
		end = end + 1 + next
	}
	if end < 0 {
		return rest
	}
	return rest[:1+end+1]
}

// The reporter's case: in operate mode the import is fixed at its limit and
// the line at its existing capacity, so both bind.
func TestCalliopeOperateFixesImportAndLine(t *testing.T) {
	j := loadScenario(t, "calliope_operate_limits.json")
	warns, err := validateFor(&j, model.TargetCalliope)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, w := range warns {
		if strings.Contains(w, "operate mode") {
			t.Errorf("fixed capacities must not warn, got %q", w)
		}
	}
	doc := emitCalliope(t, j)
	wantAll(t, block(doc, "market_import"), "flow_cap_max: 50", "flow_cap: 50")
	wantAll(t, block(doc, "line"), "flow_cap_max: 40", "flow_cap_min: 40", "flow_cap: 40")
	wantAll(t, block(doc, "gen1"), "flow_cap: 1000")
}

// Plan mode: a line's existing capacity is a bound too (it used to be ignored,
// leaving a fixed line unbounded).
func TestCalliopeTransmissionExistingCapacityBound(t *testing.T) {
	j := loadScenario(t, "calliope_transmission.json")
	l := j.Model.Transmission["line"]
	l.Capacity = &model.Capacity{Existing: 40, Expandable: false}
	j.Model.Transmission["line"] = l
	if _, err := validateFor(&j, model.TargetCalliope); err != nil {
		t.Fatalf("validate: %v", err)
	}
	line := block(emitCalliope(t, j), "line")
	wantAll(t, line, "flow_cap_max: 40", "flow_cap_min: 40")
	if strings.Contains(line, "flow_cap:") {
		t.Errorf("plan mode must not fix flow_cap:\n%s", line)
	}
}

// An expandable tech or line in operate mode is installed at capacity.max,
// and the payload is told so (PyPSA rejects the same case; here the choice
// is explicit, not silent).
func TestCalliopeOperateExpandableIsInstalledAtMaxWithWarning(t *testing.T) {
	j := loadScenario(t, "calliope_operate_limits.json")
	line := j.Model.Transmission["line"]
	line.Capacity = &model.Capacity{Expandable: true, Max: fp(40)}
	j.Model.Transmission["line"] = line
	gen := j.Model.Technologies["gen1"]
	gen.Capacity = &model.Capacity{Expandable: true, Max: fp(1000)}
	j.Model.Technologies["gen1"] = gen
	warns, err := validateFor(&j, model.TargetCalliope)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	joined := strings.Join(warns, "\n")
	for _, want := range []string{`transmission "line"`, `tech "gen1"`, "capacity.max"} {
		if !strings.Contains(joined, want) {
			t.Errorf("want an operate-mode warning mentioning %s, got %v", want, warns)
		}
	}
	doc := emitCalliope(t, j)
	wantAll(t, block(doc, "line"), "flow_cap: 40")
	wantAll(t, block(doc, "gen1"), "flow_cap: 1000")
}

// An import without a limit means unlimited import: no flow_cap (Calliope's
// operate default is infinite) and no warning.
func TestCalliopeOperateUnlimitedImportStaysUnbounded(t *testing.T) {
	j := loadScenario(t, "calliope_operate_limits.json")
	tr := j.Model.Trade["market"]
	tr.Import.Limit = nil
	j.Model.Trade["market"] = tr
	warns, err := validateFor(&j, model.TargetCalliope)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, w := range warns {
		if strings.Contains(w, "market") {
			t.Errorf("an unlimited import is not a dropped feature, got warning %q", w)
		}
	}
	imp := block(emitCalliope(t, j), "market_import")
	if strings.Contains(imp, "flow_cap") {
		t.Errorf("unlimited import must carry no flow_cap:\n%s", imp)
	}
}

// Nothing fixed anywhere (an unlimited import serving a demand) would solve
// and then abort in postprocessing on the missing flow_cap array; validation
// rejects it first.
func TestCalliopeOperateNeedsAFixedCapacity(t *testing.T) {
	j := loadScenario(t, "calliope_operate_limits.json")
	delete(j.Model.Technologies, "gen1")
	delete(j.Model.Technologies, "gen2")
	delete(j.Model.Technologies, "load2")
	j.Model.Transmission = nil
	tr := j.Model.Trade["market"]
	tr.Import.Limit = nil
	j.Model.Trade["market"] = tr
	_, err := validateFor(&j, model.TargetCalliope)
	if err == nil || !strings.Contains(err.Error(), "at least one fixed capacity") {
		t.Fatalf("operate mode without any fixed capacity should be rejected, got %v", err)
	}
	// The same model is fine in plan mode.
	j.Experiment.Mode = model.ModePlan
	if _, err := validateFor(&j, model.TargetCalliope); err != nil {
		t.Errorf("plan mode should accept it: %v", err)
	}
}

// The other two sizes operate mode turns into inputs: source.cap becomes a
// fixed source_cap, area.max a fixed area_use (installed area, with a
// warning like capacity.max).
func TestCalliopeOperateSourceCapAndArea(t *testing.T) {
	j := loadScenario(t, "calliope_source_native.json")
	j.Experiment.Mode = model.ModeOperate
	pv := j.Model.Technologies["pv"]
	pv.Capacity = &model.Capacity{Existing: 400, Expandable: false}
	pv.Source.Cap = fp(500)
	j.Model.Technologies["pv"] = pv
	warns, err := validateFor(&j, model.TargetCalliope)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if joined := strings.Join(warns, "\n"); !strings.Contains(joined, "area.max") {
		t.Errorf("installing area.max in operate mode must warn, got %v", warns)
	}
	wantAll(t, block(emitCalliope(t, j), "pv"), "flow_cap: 400", "source_cap: 500", "area_use: 5000")
}
