// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package model

// Target identifies a downstream simulator. A framework may be registered
// under more than one name when several of its releases are supported: the
// bare name is the default version, a "-<major.minor>" suffix pins another one
// (TargetCalliope06). The target registry (internal/target) also knows
// version-explicit aliases of the defaults ("calliope-0.7" -> "calliope").
type Target string

const (
	TargetCalliope   Target = "calliope"     // Calliope 0.7 (the default Calliope target)
	TargetCalliope06 Target = "calliope-0.6" // Calliope 0.6 (version variant of TargetCalliope)
	TargetPyPSA      Target = "pypsa"
	TargetAdOpt      Target = "adopt-net0"
)
