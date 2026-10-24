// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package main

import "testing"

// The stamped release wins; without one the binary still names a version.
func TestBuildVersion(t *testing.T) {
	defer func(v string) { version = v }(version)

	version = "0.1.0-test"
	if got := buildVersion(); got != "0.1.0-test" {
		t.Errorf("stamped version: got %q, want %q", got, "0.1.0-test")
	}

	version = ""
	if got := buildVersion(); got == "" {
		t.Error("unstamped binary must still report a version (module version or \"dev\")")
	}
}
