// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package model

// Target identifies a downstream simulator.
type Target string

const (
	TargetCalliope Target = "calliope"
	TargetPyPSA    Target = "pypsa"
	TargetAdOpt    Target = "adopt-net0"
)
