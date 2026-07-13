// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package scenarios_test

import (
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/scenarios"
)

// TestCorpusIntegrity: every embedded scenario decodes into a Job carrying its
// target prefix in the file name, and the shared sample loads for every
// target adaptation.
func TestCorpusIntegrity(t *testing.T) {
	names := scenarios.Names()
	if len(names) < 30 {
		t.Fatalf("corpus suspiciously small: %d scenarios", len(names))
	}
	for _, name := range names {
		j, err := scenarios.Load(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if j.Model.Metadata.Name == "" {
			t.Errorf("%s: metadata.name empty", name)
		}
		if !strings.HasPrefix(name, "pypsa_") && !strings.HasPrefix(name, "calliope_") && !strings.HasPrefix(name, "adopt_") {
			t.Errorf("%s: file name must carry its target prefix", name)
		}
	}
	for _, tgt := range []model.Target{model.TargetPyPSA, model.TargetCalliope, model.TargetAdOpt} {
		if _, err := scenarios.SampleFor(tgt); err != nil {
			t.Errorf("SampleFor(%s): %v", tgt, err)
		}
	}
}
