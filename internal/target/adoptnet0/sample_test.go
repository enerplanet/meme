// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package adoptnet0_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/target/adoptnet0"
)

func TestAdOptEmitSample(t *testing.T) {
	j := loadSampleFor(t, model.TargetAdOpt)
	if _, err := validateFor(&j, model.TargetAdOpt); err != nil {
		t.Fatalf("validate: %v", err)
	}
	dir := t.TempDir()
	root, err := (adoptnet0.AdOptNET0{}).Emit(&j, dir)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	topo := readFileAbs(t, filepath.Join(root, "Topology.json"))
	if !strings.Contains(topo, "electricity") || !strings.Contains(topo, "investment_periods") {
		t.Errorf("Topology.json incomplete:\n%s", topo)
	}
	if !strings.Contains(topo, `"resolution": "1h"`) {
		t.Errorf("resolution must be lowercase 1h:\n%s", topo)
	}
	techs := readFileAbs(t, filepath.Join(root, "period1/node_data/n1/Technologies.json"))
	if !strings.Contains(techs, "existing") || !strings.Contains(techs, "new") {
		t.Errorf("Technologies.json missing existing/new:\n%s", techs)
	}
	if !strings.Contains(techs, "Storage_Battery") {
		t.Errorf("storage tech not mapped to Storage_Battery:\n%s", techs)
	}
	elec := readFileAbs(t, filepath.Join(root, "period1/node_data/n1/carrier_data/electricity.csv"))
	if !strings.Contains(elec, ";Demand;") {
		t.Errorf("carrier CSV not semicolon-delimited:\n%s", elec)
	}
	cfg := readFileAbs(t, filepath.Join(root, "ConfigModel.json"))
	if !strings.Contains(cfg, "glpk") {
		t.Errorf("ConfigModel should select glpk:\n%s", cfg)
	}
}
