// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope_test

import (
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/scenarios"
	"github.com/enerplanet/meme/internal/target/calliope"
)

// Operate mode on Calliope needs at least one fixed size; without one the solve
// succeeds and postprocessing aborts on the missing flow_cap.
func TestValidateJobOperateNeedsAFixedSize(t *testing.T) {
	j, err := scenarios.Load("calliope_operate_trade.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (calliope.Calliope{}).ValidateJob(&j); err != nil {
		t.Fatalf("an import limit is a fixed size: %v", err)
	}

	grid := j.Model.Trade["grid"]
	grid.Import.Limit = nil
	j.Model.Trade["grid"] = grid
	line := j.Model.Transmission["line"]
	line.Capacity = nil
	j.Model.Transmission["line"] = line

	_, err = (calliope.Calliope{}).ValidateJob(&j)
	if err == nil || !strings.Contains(err.Error(), `mode "operate"`) {
		t.Fatalf("error = %v, want a rejection naming operate mode", err)
	}
}
