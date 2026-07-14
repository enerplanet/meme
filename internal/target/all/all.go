// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

// Package all registers every built-in target implementation. Blank-import it
// (the api package does) to populate the target registry.
package all

import (
	_ "github.com/enerplanet/meme/internal/target/adoptnet0"
	_ "github.com/enerplanet/meme/internal/target/calliope"
	_ "github.com/enerplanet/meme/internal/target/pypsa"
)
