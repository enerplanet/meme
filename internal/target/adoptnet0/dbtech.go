// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package adoptnet0

import (
	"encoding/json"

	"github.com/enerplanet/meme/internal/model"
)

// AdoptDBTech maps a canonical technology to an adopt_net0 database technology
// name. adopt_net0 is database-driven: a tech must name a library entry that
// copy_technology_data can materialize. Only physics techs and storage have a
// canonical counterpart; anything else needs a native.adopt-net0 block naming a
// real database tech (validateAdoptTechs enforces that). Shared by the AdOpT
// emitter and by ValidateFor, so the two can never disagree.
func dbTech(t model.Technology) (string, bool) {
	if name, _ := nativeTech(t)["technology"].(string); name != "" {
		return name, true
	}
	if t.Performance != nil && t.Performance.Kind() == model.PerfPhysics {
		switch t.Performance.Model {
		case "pv":
			return "Photovoltaic", true
		case "wind":
			return "WindTurbine_Onshore_1500", true
		case "heat_pump":
			return "HeatPump_AirSourced", true
		}
	}
	if t.Role == model.RoleStorage {
		return "Storage_Battery", true
	}
	return "", false
}

// nativeTech decodes a tech's native.adopt-net0 block; its "technology" key
// names the database entry, the rest is patched onto it.
func nativeTech(t model.Technology) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(t.Native.For(model.TargetAdOpt), &m)
	return m
}
