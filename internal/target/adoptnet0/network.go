// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package adoptnet0

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"

	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/model"
)

// Transmission entries become arcs of adopt_net0 database networks
// (native.adopt-net0.network, CO2_Pipeline by default for CO2captured). The
// network template data is patched once per network by the run step, so arcs
// sharing a network must agree on it.

var defaultNetwork = map[string]string{"CO2captured": "CO2_Pipeline"}

// adoptNetworks groups the active transmission ids by network and returns the
// template patch of each network.
func adoptNetworks(m *model.Model) (map[string][]string, map[string]map[string]any, error) {
	arcs := map[string][]string{}
	patches := map[string]map[string]any{}
	for _, id := range emit.Keys(m.Transmission) {
		l := m.Transmission[id]
		if !l.IsActive() {
			continue
		}
		if l.Distance == nil || *l.Distance <= 0 {
			return nil, nil, fmt.Errorf("transmission %q: adopt-net0 needs a positive distance", id)
		}
		if l.Capacity != nil && (!l.Capacity.Expandable || l.Capacity.Existing > 0) {
			return nil, nil, fmt.Errorf("transmission %q: only new (expandable) arcs are supported for adopt-net0", id)
		}
		native := map[string]any{}
		if raw := l.Native.For(model.TargetAdOpt); len(raw) > 0 {
			if err := json.Unmarshal(raw, &native); err != nil {
				return nil, nil, fmt.Errorf("transmission %q: native.adopt-net0: %w", id, err)
			}
		}
		net, _ := native["network"].(string)
		delete(native, "network")
		if net == "" {
			net = defaultNetwork[l.Carrier]
		}
		if net == "" {
			return nil, nil, fmt.Errorf("transmission %q: set native.adopt-net0.network (no default for carrier %q)", id, l.Carrier)
		}

		perf := map[string]any{"bidirectional_network": 0}
		if l.Bidirectional {
			perf["bidirectional_network"] = 1
		}
		if l.LossPerDistance != nil {
			perf["loss"] = *l.LossPerDistance
		}
		patch := map[string]any{"Performance": perf}
		if c, ok := l.Costs[model.PrimaryCostClass]; ok && c.InvestmentPerCapacityDistance != nil {
			// linear CAPEX; a fixed part would make each arc a binary decision
			patch["Economics"] = map[string]any{"gamma1": 0.0, "gamma2": 0.0, "gamma3": 0.0,
				"gamma4": *c.InvestmentPerCapacityDistance}
		}
		if raw, _ := json.Marshal(native); len(native) > 0 {
			if err := emit.MergeNativeMap(patch, raw); err != nil {
				return nil, nil, err
			}
		}
		if prev, ok := patches[net]; ok && !reflect.DeepEqual(prev, patch) {
			return nil, nil, fmt.Errorf("transmission %q: arcs of network %q differ in network-level data", id, net)
		}
		patches[net] = patch
		arcs[net] = append(arcs[net], id)
	}
	return arcs, patches, nil
}

// emitNetworks writes Networks.json and network_topology/new/<network>/ for one
// period: connection, distance and (when set) size_max_arcs node x node
// matrices, rows = from, columns = to.
func emitNetworks(m *model.Model, period string) (map[string]map[string]any, error) {
	arcs, patches, err := adoptNetworks(m)
	if err != nil {
		return nil, err
	}
	nodes := emit.Keys(m.Nodes)
	for _, net := range emit.Keys(arcs) {
		conn, dist, size := map[[2]string]float64{}, map[[2]string]float64{}, map[[2]string]float64{}
		for _, id := range arcs[net] {
			l := m.Transmission[id]
			keys := [][2]string{{l.From, l.To}}
			if l.Bidirectional {
				keys = append(keys, [2]string{l.To, l.From})
			}
			for _, k := range keys {
				conn[k], dist[k] = 1, *l.Distance
				if l.Capacity != nil && l.Capacity.Max != nil {
					size[k] = *l.Capacity.Max
				}
			}
		}
		dir := filepath.Join(period, "network_topology", "new", net)
		files := map[string]map[[2]string]float64{"connection.csv": conn, "distance.csv": dist}
		if len(size) > 0 {
			files["size_max_arcs.csv"] = size
		}
		for name, cell := range files {
			rows := [][]string{append([]string{""}, nodes...)}
			for _, from := range nodes {
				row := []string{from}
				for _, to := range nodes {
					row = append(row, emit.Ftoa(cell[[2]string{from, to}]))
				}
				rows = append(rows, row)
			}
			if err := writeSemicolonCSV(dir, name, rows); err != nil {
				return nil, err
			}
		}
	}
	// copy_network_data concatenates existing + new: both must be lists.
	if err := emit.WriteJSON(period, "Networks.json", map[string]any{
		"existing": []string{}, "new": emit.Keys(arcs),
	}); err != nil {
		return nil, err
	}
	if len(arcs) > 0 {
		// copy_network_data uses shutil.copy, which needs the target dir.
		if err := emit.Mkdirs(filepath.Join(period, "network_data")); err != nil {
			return nil, err
		}
	}
	return patches, nil
}
