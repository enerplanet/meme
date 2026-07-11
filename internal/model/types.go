// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package model

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Value: a number, a time-series reference, an inline per-timestep array, OR
// an n-dimensional indexed parameter.
//
// In JSON a parameter is one of:
//   - a bare number:            0.9
//   - a time-series id string:  "pv_cf"  (optionally "ts:pv_cf")
//   - an inline array:          [0.1, 0.4, 0.7]  (per-timestep values)
//   - an indexed object:        {"data": [10, 20], "index": [["monetary"],
//     ["co2"]], "dims": ["costs"]}  (Calliope's {data, index, dims} model)
//
// Go has no sum type, so Value carries all four slots and custom (un)marshalling
// collapses the JSON shapes. Emitters that need a scalar call Reduce/Select to
// project an indexed parameter down; they error if that projection is ambiguous.
// Inline arrays never reach the emitters: Model.InternInlineSeries (called by
// target.ValidateFor) moves them into the Timeseries registry and rewrites the
// Value to a reference, so every series-handling path sees exactly one shape.
// ---------------------------------------------------------------------------

// Value carries one parameter in exactly one of its four shapes: a scalar, a
// time-series reference, an inline per-timestep array, or an indexed
// parameter. The zero Value marshals as JSON null.
type Value struct {
	Scalar  *float64      // set when the JSON value was a number
	Ref     string        // set when the JSON value was a time-series id
	Inline  []float64     // set when the JSON value was an array (interned to Ref before emit)
	Indexed *IndexedParam // set when the JSON value was an indexed object
}

// IndexedParam is a parameter defined over one or more dimensions: Data[i] is
// the value at coordinate Index[i] (a tuple over Dims).
type IndexedParam struct {
	Data  []float64  `json:"data"`
	Index [][]string `json:"index"`
	Dims  []string   `json:"dims"`
}

// Num builds a scalar Value.
func Num(f float64) *Value { return &Value{Scalar: &f} }

// Series builds a Value referencing the time series with the given id.
func Series(id string) *Value { return &Value{Ref: id} }

// IsSeries reports whether this Value points at a time series.
func (v *Value) IsSeries() bool { return v != nil && v.Ref != "" }

// IsInline reports whether this Value carries an inline per-timestep array
// (not yet interned into the Timeseries registry).
func (v *Value) IsInline() bool { return v != nil && len(v.Inline) > 0 }

// IsIndexed reports whether this Value is an n-dimensional indexed parameter.
func (v *Value) IsIndexed() bool { return v != nil && v.Indexed != nil }

// SeriesID returns the referenced TimeSeries key with any "ts:" prefix stripped.
func (v *Value) SeriesID() string { return strings.TrimPrefix(v.Ref, "ts:") }

// Reduce collapses the value to a single scalar when unambiguous: a plain scalar,
// or an indexed parameter with exactly one data entry. Series and multi-entry
// indexed parameters return ok=false (the caller must handle them another way).
func (v *Value) Reduce() (float64, bool) {
	if v == nil {
		return 0, false
	}
	if v.Scalar != nil {
		return *v.Scalar, true
	}
	if v.Indexed != nil && len(v.Indexed.Data) == 1 {
		return v.Indexed.Data[0], true
	}
	return 0, false
}

// Select returns the scalar entry of an indexed parameter matching (dim, key),
// e.g. Select("costs", "monetary"). A plain scalar matches any selector.
func (v *Value) Select(dim, key string) (float64, bool) {
	if v == nil {
		return 0, false
	}
	if v.Indexed == nil {
		if v.Scalar != nil {
			return *v.Scalar, true
		}
		return 0, false
	}
	di := -1
	for i, d := range v.Indexed.Dims {
		if d == dim {
			di = i
			break
		}
	}
	if di == -1 {
		return 0, false
	}
	for i, tup := range v.Indexed.Index {
		if di < len(tup) && tup[di] == key {
			return v.Indexed.Data[i], true
		}
	}
	return 0, false
}

// StructuralError reports an internal inconsistency in an indexed parameter.
func (v *Value) StructuralError() error {
	if v == nil || v.Indexed == nil {
		return nil
	}
	ip := v.Indexed
	if len(ip.Data) != len(ip.Index) {
		return fmt.Errorf("indexed param: %d data entries but %d index tuples", len(ip.Data), len(ip.Index))
	}
	for i, tup := range ip.Index {
		if len(tup) != len(ip.Dims) {
			return fmt.Errorf("indexed param: index tuple %d has %d coords but %d dims", i, len(tup), len(ip.Dims))
		}
	}
	return nil
}

// MarshalJSON renders the populated slot in its canonical JSON shape
// (precedence: indexed, reference, inline array, scalar; empty Value -> null).
func (v Value) MarshalJSON() ([]byte, error) {
	switch {
	case v.Indexed != nil:
		return json.Marshal(v.Indexed)
	case v.Ref != "":
		return json.Marshal(v.Ref)
	case len(v.Inline) > 0:
		return json.Marshal(v.Inline)
	case v.Scalar != nil:
		return json.Marshal(*v.Scalar)
	default:
		return []byte("null"), nil
	}
}

// UnmarshalJSON dispatches on the leading JSON token: an object is an indexed
// parameter, a string a series reference, an array an inline series (must be
// non-empty), and anything else a number.
func (v *Value) UnmarshalJSON(b []byte) error {
	b = []byte(strings.TrimSpace(string(b)))
	if string(b) == "null" {
		return nil
	}
	switch b[0] {
	case '{': // indexed object
		var ip IndexedParam
		if err := json.Unmarshal(b, &ip); err != nil {
			return fmt.Errorf("indexed parameter: %w", err)
		}
		v.Indexed = &ip
		return nil
	case '"': // time-series id
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		v.Ref = s
		return nil
	case '[': // inline per-timestep array
		var vals []float64
		if err := json.Unmarshal(b, &vals); err != nil {
			return fmt.Errorf("inline series: %w", err)
		}
		if len(vals) == 0 {
			return fmt.Errorf("inline series must not be empty")
		}
		v.Inline = vals
		return nil
	default: // number
		var f float64
		if err := json.Unmarshal(b, &f); err != nil {
			return fmt.Errorf("value must be a number, time-series id, inline array, or indexed object, got %s", b)
		}
		v.Scalar = &f
		return nil
	}
}

// ---------------------------------------------------------------------------
// Period: one investment period. Historically a bare name string; the rich
// object form carries the representative year (PyPSA multi-horizon periods
// must be increasing integer years) and weighting. No emitter consumes
// periods yet — the type preserves wire compatibility of the legacy []string
// form while the multi-horizon feature lands.
// ---------------------------------------------------------------------------

// Period is one investment period; only Name is required. See the section
// comment above for the wire compatibility contract.
type Period struct {
	Name            string   `json:"name"`
	Year            *int     `json:"year,omitempty"`
	LengthYears     *float64 `json:"length_years,omitempty"`     // elapsed years until the next period
	ObjectiveWeight *float64 `json:"objective_weight,omitempty"` // multiplier on this period's costs
}

// UnmarshalJSON accepts the legacy bare-name string as well as the rich
// object form.
func (p *Period) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &p.Name)
	}
	type raw Period // shed the method set to avoid recursion
	return json.Unmarshal(b, (*raw)(p))
}

// MarshalJSON emits the bare-name string whenever no rich field is set, so
// legacy payloads round-trip byte-identically.
func (p Period) MarshalJSON() ([]byte, error) {
	if p.Year == nil && p.LengthYears == nil && p.ObjectiveWeight == nil {
		return json.Marshal(p.Name) // legacy bare-name form round-trips
	}
	type raw Period
	return json.Marshal(raw(p))
}

// ---------------------------------------------------------------------------
// StringList: accepts a single string OR an array of strings, always []string.
// Lets carrier_in/carrier_out/node be written as "electricity" or a list.
// ---------------------------------------------------------------------------

// StringList is a []string that also accepts a single JSON string, so
// carrier_in/carrier_out/node can be written as "electricity" or a list.
type StringList []string

// First returns the first element, or "" for an empty list.
func (s StringList) First() string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// UnmarshalJSON accepts a bare string or an array of strings.
func (s *StringList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*s = StringList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("expected a string or array of strings, got %s", b)
	}
	*s = many
	return nil
}

// ---------------------------------------------------------------------------
// Validated enums.
// ---------------------------------------------------------------------------

// Role classifies a technology's function in the energy balance: supply
// injects a carrier from outside the system, demand removes one, conversion
// transforms between carriers, and storage shifts them over time.
type Role string

const (
	RoleSupply     Role = "supply"
	RoleDemand     Role = "demand"
	RoleConversion Role = "conversion"
	RoleStorage    Role = "storage"
)

// Valid reports whether r is one of the defined roles.
func (r Role) Valid() bool {
	switch r {
	case RoleSupply, RoleDemand, RoleConversion, RoleStorage:
		return true
	}
	return false
}

// CostBasis states whether investment figures are overnight (annuitized via
// lifetime and interest rate) or already annualized. The empty string defaults
// to overnight.
type CostBasis string

const (
	CostOvernight  CostBasis = "overnight"
	CostAnnualized CostBasis = "annualized"
)

// Valid reports whether c is a defined basis or unset.
func (c CostBasis) Valid() bool {
	return c == "" || c == CostOvernight || c == CostAnnualized
}

// Foresight is the planning horizon assumption of a run; accepted and
// reserved, no target maps it yet.
type Foresight string

const (
	ForesightPerfect Foresight = "perfect"
	ForesightMyopic  Foresight = "myopic"
)

// Valid reports whether f is a defined foresight or unset.
func (f Foresight) Valid() bool {
	return f == "" || f == ForesightPerfect || f == ForesightMyopic
}

// Objective selects the optimization goal. A cost objective under an emission
// cap is min_cost plus a Model.EmissionLimits entry; min_emissions is
// capability-gated (FeatObjectiveEmissions).
type Objective string

const (
	ObjectiveMinCost      Objective = "min_cost"
	ObjectiveMinEmissions Objective = "min_emissions"
)

// Valid reports whether o is a defined objective or unset (= min_cost).
func (o Objective) Valid() bool {
	return o == "" || o == ObjectiveMinCost || o == ObjectiveMinEmissions
}
