// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope

import (
	"fmt"
	"strconv"
	"strings"
)

// yamlNode is an insertion-ordered YAML mapping. It exists so the Calliope
// emitter can produce deterministic, readable model.yaml files without an
// external YAML dependency.
type yamlNode struct {
	keys []string
	vals map[string]any
}

func newYAML() *yamlNode { return &yamlNode{vals: map[string]any{}} }

// set adds or replaces a key, preserving first-insertion order. A nil value
// emits a bare "key:" (YAML null) — used for "tech is enabled, no override".
func (n *yamlNode) set(k string, v any) *yamlNode {
	if _, ok := n.vals[k]; !ok {
		n.keys = append(n.keys, k)
	}
	n.vals[k] = v
	return n
}

func (n *yamlNode) len() int { return len(n.keys) }

// yamlDoc serializes a yamlNode to a YAML document string.
func yamlDoc(n *yamlNode) string {
	var sb strings.Builder
	writeYAMLMap(&sb, n, 0)
	return sb.String()
}

func writeYAMLMap(sb *strings.Builder, n *yamlNode, indent int) {
	pad := strings.Repeat("  ", indent)
	for _, k := range n.keys {
		v := n.vals[k]
		switch val := v.(type) {
		case nil:
			fmt.Fprintf(sb, "%s%s:\n", pad, k)
		case *yamlNode:
			if val == nil {
				fmt.Fprintf(sb, "%s%s:\n", pad, k)
			} else if val.len() == 0 {
				fmt.Fprintf(sb, "%s%s: {}\n", pad, k)
			} else {
				fmt.Fprintf(sb, "%s%s:\n", pad, k)
				writeYAMLMap(sb, val, indent+1)
			}
		case []any:
			if len(val) == 0 {
				fmt.Fprintf(sb, "%s%s: []\n", pad, k)
			} else if isScalarSeq(val) {
				fmt.Fprintf(sb, "%s%s: [%s]\n", pad, k, joinScalars(val))
			} else {
				fmt.Fprintf(sb, "%s%s:\n", pad, k)
				for _, item := range val {
					if sub, ok := item.(*yamlNode); ok {
						fmt.Fprintf(sb, "%s- \n", strings.Repeat("  ", indent+1))
						writeYAMLMap(sb, sub, indent+2)
					} else {
						fmt.Fprintf(sb, "%s- %s\n", strings.Repeat("  ", indent+1), yamlScalar(item))
					}
				}
			}
		default:
			fmt.Fprintf(sb, "%s%s: %s\n", pad, k, yamlScalar(v))
		}
	}
}

func isScalarSeq(s []any) bool {
	for _, v := range s {
		if _, ok := v.(*yamlNode); ok {
			return false
		}
	}
	return true
}

func joinScalars(s []any) string {
	parts := make([]string, len(s))
	for i, v := range s {
		parts[i] = yamlScalar(v)
	}
	return strings.Join(parts, ", ")
}

// yamlFloat forces float rendering: an integral value keeps a ".0" suffix so it
// stays a float in YAML. Used for coordinates, where Calliope requires latitude
// and longitude to have matching types (49.0 must not collapse to int-like 49
// next to a fractional 13.2).
type yamlFloat float64

func yamlScalar(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case yamlFloat:
		s := strconv.FormatFloat(float64(x), 'g', -1, 64)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		return s
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case string:
		return yamlString(x)
	case []any:
		// Nested inline list, e.g. a multi-dim index entry [co2, gas].
		return "[" + joinScalars(x) + "]"
	default:
		return yamlString(fmt.Sprint(x))
	}
}

// yamlString quotes a string only when needed to stay valid YAML.
func yamlString(s string) string {
	if s == "" {
		return `""`
	}
	safe := true
	for _, r := range s {
		if !(r == '_' || r == '-' || r == '.' || r == '/' || r == ':' && false ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			safe = false
			break
		}
	}
	// A bare token that parses as a number/bool must be quoted to stay a string.
	if safe {
		if _, err := strconv.ParseFloat(s, 64); err == nil {
			safe = false
		}
		switch strings.ToLower(s) {
		case "true", "false", "null", "yes", "no", "on", "off", "~":
			// YAML 1.1 boolean/null literals (any casing) — always quote.
			safe = false
		}
	}
	if safe {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
