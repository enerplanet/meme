// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope

import (
	"math"
	"regexp"
	"strings"
	"testing"
)

// TestYAMLStringQuoting pins the writer's quoting rules for the classic YAML
// hazard characters: anything that could be misparsed must be double-quoted,
// bare identifiers must stay bare (Calliope reads params like `base_tech:
// supply` unquoted).
func TestYAMLStringQuoting(t *testing.T) {
	quoted := []string{
		"", "a: b", "a #comment", "yes", "no", "true", "null", "on",
		"[bracket", "{brace", "- dash", "0.5", "1e6", "with space",
		"tab\tchar", "newline\nchar", `has "quotes"`,
		// YAML 1.1 non-finite float literals (any casing): bare, these would
		// load as floats and collide with the writer's own float spellings.
		".inf", ".Inf", ".nan", ".NAN",
	}
	for _, s := range quoted {
		got := yamlString(s)
		if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
			t.Errorf("yamlString(%q) = %s — must be quoted", s, got)
		}
	}
	bare := []string{"electricity", "base_tech", "n1", "flow_cap_max", "a-b.c/d", "PV25"}
	for _, s := range bare {
		if got := yamlString(s); got != s {
			t.Errorf("yamlString(%q) = %s — safe identifier must stay bare", s, got)
		}
	}
}

// TestYAMLDocShape: nested nodes indent, floats keep a decimal point when
// forced (Calliope's lat/lon type check), lists render inline.
func TestYAMLDocShape(t *testing.T) {
	root := newYAML()
	root.set("name", "toy model") // needs quoting
	sub := newYAML()
	sub.set("latitude", yamlFloat(49))
	sub.set("tags", []any{"a b", "c"})
	root.set("node", sub)

	doc := yamlDoc(root)
	for _, want := range []string{
		"name: \"toy model\"\n",
		"node:\n",
		"  latitude: 49.0\n",
		"  tags: [\"a b\", c]\n",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("yamlDoc missing %q:\n%s", want, doc)
		}
	}
}

// yaml11Float is the YAML 1.1 base-10 float resolution pattern (PyYAML's
// resolver, sans base-60): the mantissa must contain a decimal point and the
// exponent must carry a sign. Calliope loads model.yaml under these rules, so
// anything the writer intends as a float must match it — Go's bare "1e+06"
// does not and would load as a string.
var yaml11Float = regexp.MustCompile(
	`^(?:[-+]?[0-9][0-9_]*\.[0-9_]*(?:[eE][-+][0-9]+)?` +
		`|[-+]?\.[0-9_]+(?:[eE][-+][0-9]+)?` +
		`|[-+]?\.(?:inf|Inf|INF)` +
		`|\.(?:nan|NaN|NAN))$`)

// TestYAMLFloatStr pins the float spellings and proves each parses back as a
// float under YAML 1.1 resolution (round numbers >= 1e6 and tiny values used
// to render as "1e+06"-style strings).
func TestYAMLFloatStr(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{1e6, "1.0e+06"},
		{2e6, "2.0e+06"},
		{1e21, "1.0e+21"},
		{1e-7, "1.0e-07"},
		{2.5e6, "2.5e+06"},
		{-1e6, "-1.0e+06"},
		{1234.5, "1234.5"},
		{0.001, "0.001"},
		{math.Inf(1), ".inf"},
		{math.Inf(-1), "-.inf"},
		{math.NaN(), ".nan"},
	}
	for _, c := range cases {
		got := yamlScalar(c.in)
		if got != c.want {
			t.Errorf("yamlScalar(%v) = %q, want %q", c.in, got, c.want)
		}
		if !yaml11Float.MatchString(got) {
			t.Errorf("yamlScalar(%v) = %q does not resolve as a YAML 1.1 float", c.in, got)
		}
	}
	// The forced-float wrapper shares the fix (bare "1e+06" was possible
	// there too) and keeps its ".0" suffix for integral values.
	if got := yamlScalar(yamlFloat(1e6)); got != "1.0e+06" {
		t.Errorf("yamlScalar(yamlFloat(1e6)) = %q, want 1.0e+06", got)
	}
	if got := yamlScalar(yamlFloat(49)); got != "49.0" {
		t.Errorf("yamlScalar(yamlFloat(49)) = %q, want 49.0", got)
	}
	// Integral float64s keep rendering int-like — YAML resolves them as ints,
	// which Calliope coerces; only the exponential form was mistyped.
	if got := yamlScalar(float64(42)); got != "42" {
		t.Errorf("yamlScalar(42.0) = %q, want 42", got)
	}
}

// FuzzYAMLString: the writer must never panic and must quote anything that is
// not a plain identifier — the guard for the hand-rolled (dependency-free)
// YAML writer.
func FuzzYAMLString(f *testing.F) {
	for _, seed := range []string{
		"", "plain", "a: b", "x #y", "- z", "0.5", "ä ö", "\n", `"`,
		// Number-shaped and YAML-1.1-float-shaped strings must stay quoted so
		// they cannot collide with the writer's own float spellings.
		"1e+06", "1.0e+06", ".inf", "-.inf", ".NaN",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := yamlString(s)
		if got == "" {
			t.Fatalf("yamlString(%q) produced empty output", s)
		}
		if strings.HasPrefix(got, `"`) {
			return // quoted form: writer took the safe path
		}
		// Bare form: every rune must be from the safe identifier set.
		for _, r := range got {
			safe := r == '_' || r == '-' || r == '.' || r == '/' ||
				(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
			if !safe {
				t.Fatalf("yamlString(%q) = %q left unsafe rune %q unquoted", s, got, r)
			}
		}
	})
}
