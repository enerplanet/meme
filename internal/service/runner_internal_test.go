// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package service

import "testing"

func TestCappedBuffer(t *testing.T) {
	tests := []struct {
		name             string
		headMax, tailMax int
		writes           []string
		want             string
	}{
		{"under head cap", 8, 4, []string{"hello"}, "hello"},
		{"head+tail exactly, no marker", 4, 4, []string{"12345678"}, "12345678"},
		{"drop in the middle", 4, 4, []string{"0123456789"}, "0123" + truncationMarker(2) + "6789"},
		{"single write beyond tail", 2, 3, []string{"abcdefghij"}, "ab" + truncationMarker(5) + "hij"},
		{"byte writes wrap the ring", 1, 3, []string{"a", "b", "c", "d", "e", "f", "g"}, "a" + truncationMarker(3) + "efg"},
		{"empty", 4, 4, nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := newCappedBuffer(tc.headMax, tc.tailMax)
			for _, w := range tc.writes {
				n, err := b.Write([]byte(w))
				if n != len(w) || err != nil {
					t.Fatalf("Write(%q) = (%d, %v)", w, n, err)
				}
			}
			if got := b.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}
