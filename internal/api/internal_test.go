// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

// White-box tests for unexported helpers whose fallback branches are
// unreachable through the HTTP surface.

package api

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestSplitPattern: route patterns split on the first space; a pattern without
// one reports ok=false, so it would get no /v1 alias.
func TestSplitPattern(t *testing.T) {
	method, path, ok := splitPattern("GET /jobs/{id}")
	if !ok || method != "GET" || path != "/jobs/{id}" {
		t.Errorf(`splitPattern("GET /jobs/{id}") = %q, %q, %v`, method, path, ok)
	}
	if _, _, ok := splitPattern("/healthz"); ok {
		t.Error("a pattern without a method must report ok=false")
	}
}

// TestRedactAPIKey: only the top-level api_key is dropped — every other field,
// nested content included, survives — and input that is not a JSON object
// passes through unchanged rather than being corrupted.
func TestRedactAPIKey(t *testing.T) {
	out := redactAPIKey([]byte(`{"api_key":"s3cret","name":"grid","nested":{"api_key":"inner"}}`))
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("redacted output is not JSON: %v", err)
	}
	if _, ok := doc["api_key"]; ok {
		t.Error("top-level api_key must be removed")
	}
	if string(doc["name"]) != `"grid"` {
		t.Errorf("sibling fields must survive, got %s", out)
	}
	if !bytes.Contains(out, []byte(`"inner"`)) {
		t.Errorf("nested content must survive untouched, got %s", out)
	}
	if bytes.Contains(out, []byte("s3cret")) {
		t.Errorf("credential must not survive, got %s", out)
	}

	raw := []byte(`[1,2,3]`)
	if got := redactAPIKey(raw); !bytes.Equal(got, raw) {
		t.Errorf("non-object input must pass through unchanged, got %s", got)
	}
}
