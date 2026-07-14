// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package calliope

import (
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

// FuzzYAMLString: the writer must never panic and must quote anything that is
// not a plain identifier — the guard for the hand-rolled (dependency-free)
// YAML writer.
func FuzzYAMLString(f *testing.F) {
	for _, seed := range []string{"", "plain", "a: b", "x #y", "- z", "0.5", "ä ö", "\n", `"`} {
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
