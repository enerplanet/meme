// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package api_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/api"
)

// withKey injects a top-level api_key field into a raw Job payload.
func withKey(t *testing.T, payload []byte, key string) []byte {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatal(err)
	}
	doc["api_key"], _ = json.Marshal(key)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func postStatus(t *testing.T, url string, body []byte) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestAPIKeyEnforced: with API_KEY configured, every payload-bearing endpoint
// rejects a missing or wrong api_key with a 401 envelope and accepts the
// right one; the GET endpoints (no payload) stay reachable.
func TestAPIKeyEnforced(t *testing.T) {
	srv := httptest.NewServer(api.NewServer(api.Server{APIKey: "s3cret", WorkRoot: t.TempDir()}))
	defer srv.Close()
	payload := sampleBytes(t)

	for _, path := range []string{"/validate?target=pypsa", "/convert?target=pypsa", "/simulate?target=pypsa"} {
		if code, body := postStatus(t, srv.URL+path, payload); code != http.StatusUnauthorized || body["error"] == nil {
			t.Errorf("%s without key: status %d body %v, want 401 envelope", path, code, body)
		}
		if code, _ := postStatus(t, srv.URL+path, withKey(t, payload, "wrong")); code != http.StatusUnauthorized {
			t.Errorf("%s with wrong key: status %d, want 401", path, code)
		}
	}
	if code, body := postStatus(t, srv.URL+"/validate?target=pypsa", withKey(t, payload, "s3cret")); code != http.StatusOK || body["valid"] != true {
		t.Errorf("correct key must pass: status %d body %v", code, body)
	}
	for _, path := range []string{"/healthz", "/capabilities"} {
		if resp, _ := http.Get(srv.URL + path); resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s must stay reachable, status %d", path, resp.StatusCode)
		}
	}
}

// TestAPIKeyDisabled: without API_KEY configured, no key is requested and any
// key a client sends is accepted.
func TestAPIKeyDisabled(t *testing.T) {
	srv := newTestServer(t) // no APIKey
	payload := sampleBytes(t)

	if code, body := postStatus(t, srv.URL+"/validate?target=pypsa", payload); code != http.StatusOK || body["valid"] != true {
		t.Errorf("keyless request must pass without auth: %d %v", code, body)
	}
	if code, body := postStatus(t, srv.URL+"/validate?target=pypsa", withKey(t, payload, "anything-goes")); code != http.StatusOK || body["valid"] != true {
		t.Errorf("any key must be accepted without auth: %d %v", code, body)
	}
}

// TestAPIKeyRedactedFromBundle: the credential never lands in the result
// bundle — config.json carries the model but no api_key field.
func TestAPIKeyRedactedFromBundle(t *testing.T) {
	srv := httptest.NewServer(api.NewServer(api.Server{APIKey: "s3cret", WorkRoot: t.TempDir()}))
	defer srv.Close()

	code, sub := postStatus(t, srv.URL+"/simulate?target=pypsa", withKey(t, sampleBytes(t), "s3cret"))
	if code != http.StatusAccepted {
		t.Fatalf("submit with key: status %d %v", code, sub)
	}
	id, _ := sub["id"].(string)
	if status := awaitDone(t, srv.URL, id); status["state"] != "succeeded" {
		t.Fatalf("job did not succeed: %v", status)
	}

	resp, err := http.Get(srv.URL + "/jobs/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("zip open: %v", err)
	}
	for _, f := range zr.File {
		if f.Name != "config.json" {
			continue
		}
		rc, _ := f.Open()
		cfg, _ := io.ReadAll(rc)
		rc.Close()
		if strings.Contains(string(cfg), "api_key") || strings.Contains(string(cfg), "s3cret") {
			t.Error("bundle config.json must not carry the api_key")
		}
		var doc map[string]any
		if err := json.Unmarshal(cfg, &doc); err != nil || doc["model"] == nil {
			t.Errorf("redacted config.json must stay a valid Job payload: err=%v", err)
		}
		return
	}
	t.Error("bundle has no config.json")
}
