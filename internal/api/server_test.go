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
	"os"
	"strings"
	"testing"
	"time"

	"github.com/enerplanet/meme/internal/api"
	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/scenarios"
)

func sampleBytes(t *testing.T) []byte {
	t.Helper()
	return scenarios.Sample()
}

// calliopeSampleBytes is the sample adapted to Calliope's gates, re-marshalled
// for HTTP round-trips.
func calliopeSampleBytes(t *testing.T) []byte {
	t.Helper()
	j, err := scenarios.SampleFor(model.TargetCalliope)
	if err != nil {
		t.Fatalf("adapt sample: %v", err)
	}
	b, err := json.Marshal(j)
	if err != nil {
		t.Fatalf("marshal calliope sample: %v", err)
	}
	return b
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(api.NewServer(api.Server{})) // dry-run executor default
	t.Cleanup(srv.Close)
	return srv
}

func TestServerAsyncJobLifecycle(t *testing.T) {
	srv := newTestServer(t)

	// capabilities
	if resp, _ := http.Get(srv.URL + "/capabilities"); resp.StatusCode != 200 {
		t.Fatalf("capabilities status %d", resp.StatusCode)
	}
	// convert stays synchronous
	if r := post(t, srv.URL+"/convert?target=calliope", calliopeSampleBytes(t)); r["entrypoint"] == nil {
		t.Errorf("convert missing entrypoint: %v", r)
	}

	// submit async job
	sub := post(t, srv.URL+"/simulate?target=pypsa", sampleBytes(t))
	if sub["scheduled"] != true {
		t.Fatalf("expected scheduled=true, got %v", sub)
	}
	id, _ := sub["id"].(string)
	if id == "" {
		t.Fatal("no job id returned")
	}

	status := awaitDone(t, srv.URL, id)
	if status["state"] != "succeeded" {
		t.Fatalf("job did not succeed: %v", status)
	}
	if log, _ := status["log"].(string); !strings.Contains(log, "run 0") {
		t.Errorf("status log missing run output: %q", log)
	}

	// fetch zipped result bundle
	resp, err := http.Get(srv.URL + "/jobs/" + id)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("expected application/zip, got %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("zip open: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{"metadata.json", "log.txt", "results.json", "config.json"} {
		if !names[want] {
			t.Errorf("zip missing %s (have %v)", want, keysOf(names))
		}
	}
	hasFiles := false
	for n := range names {
		if strings.HasPrefix(n, "files/") {
			hasFiles = true
		}
	}
	if !hasFiles {
		t.Errorf("zip missing emitted files/ tree (have %v)", keysOf(names))
	}
}

// TestMultiTargetEndpoints drives ?target=all through validate, convert and the
// async lifecycle (dry-run): one shared config, three framework models.
func TestMultiTargetEndpoints(t *testing.T) {
	srv := newTestServer(t)
	payload := readExample(t, "shared_full.json")

	// validate: per-target verdicts, overall valid.
	v := post(t, srv.URL+"/validate?target=all", payload)
	if v["valid"] != true {
		t.Fatalf("shared payload must validate for all targets: %v", v)
	}
	verdicts, _ := v["targets"].(map[string]any)
	for _, tgt := range []string{"pypsa", "calliope", "adopt-net0"} {
		tv, _ := verdicts[tgt].(map[string]any)
		if tv["valid"] != true {
			t.Errorf("target %s not valid: %v", tgt, tv)
		}
	}

	// payload_full (the maximal all-target payload, carrying pypsa+calliope+
	// adopt native blocks together) must also validate everywhere.
	if fv := post(t, srv.URL+"/validate?target=all", readExample(t, "payload_full.json")); fv["valid"] != true {
		t.Fatalf("payload_full must validate for all targets: %v", fv)
	}

	// convert: one entrypoint per target.
	c := post(t, srv.URL+"/convert?target=pypsa,calliope,adopt-net0", payload)
	eps, _ := c["entrypoints"].(map[string]any)
	if len(eps) != 3 {
		t.Fatalf("expected 3 entrypoints, got %v", c)
	}

	// simulate (dry-run): 3 targets x 2 sweep points = 6 planned runs.
	sub := post(t, srv.URL+"/simulate?target=all", payload)
	if sub["scheduled"] != true || sub["target"] != "adopt-net0,calliope,pypsa" {
		t.Fatalf("multi-target submit: %v", sub)
	}
	status := awaitDone(t, srv.URL, sub["id"].(string))
	if status["state"] != "succeeded" {
		t.Fatalf("multi-target dry job did not succeed: %v", status)
	}
	runs, _ := status["runs"].([]any)
	if len(runs) != 6 {
		t.Errorf("expected 6 runs (3 targets x 2 sweep points), got %d", len(runs))
	}
	perTarget := map[string]int{}
	for _, r := range runs {
		m, _ := r.(map[string]any)
		perTarget[m["target"].(string)]++
	}
	for _, tgt := range []string{"pypsa", "calliope", "adopt-net0"} {
		if perTarget[tgt] != 2 {
			t.Errorf("target %s: %d runs, want 2", tgt, perTarget[tgt])
		}
	}

	// bad target list still 400s.
	resp, _ := http.Post(srv.URL+"/simulate?target=pypsa,nonsense", "application/json", bytes.NewReader(payload))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown target in list: status %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestErrorEnvelope: every non-2xx body is the JSON {"error": "..."} envelope —
// bad target (400), invalid JSON (400), and validation rejection (422 keeps its
// richer shape but stays JSON with an error field).
func TestErrorEnvelope(t *testing.T) {
	srv := newTestServer(t)

	check := func(url string, body []byte, wantCode int) map[string]any {
		t.Helper()
		resp, err := http.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != wantCode {
			t.Fatalf("%s: status %d, want %d", url, resp.StatusCode, wantCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("%s: Content-Type %q, want JSON", url, ct)
		}
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("%s: error body is not JSON: %v", url, err)
		}
		if _, ok := out["error"]; !ok {
			t.Errorf("%s: error body missing \"error\" field: %v", url, out)
		}
		return out
	}

	check(srv.URL+"/convert?target=nonsense", sampleBytes(t), http.StatusBadRequest)
	check(srv.URL+"/convert?target=pypsa", []byte("{not json"), http.StatusBadRequest)
	check(srv.URL+"/validate?target=adopt-net0", sampleBytes(t), http.StatusUnprocessableEntity)
}

// TestBodySizeLimit: oversized payloads are rejected with 413, not read to the
// end.
func TestBodySizeLimit(t *testing.T) {
	srv := newTestServer(t)
	big := bytes.Repeat([]byte("x"), 33<<20) // > 32 MiB cap
	resp, err := http.Post(srv.URL+"/validate?target=pypsa", "application/json", bytes.NewReader(big))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: status %d, want 413", resp.StatusCode)
	}
}

// TestV1Alias: every route is also served under /v1/.
func TestV1Alias(t *testing.T) {
	srv := newTestServer(t)
	if resp, _ := http.Get(srv.URL + "/v1/capabilities"); resp.StatusCode != 200 {
		t.Errorf("/v1/capabilities status %d", resp.StatusCode)
	}
	if r := post(t, srv.URL+"/v1/validate?target=pypsa", sampleBytes(t)); r["valid"] != true {
		t.Errorf("/v1/validate failed: %v", r)
	}
}

// --- helpers ------------------------------------------------------------------

func awaitDone(t *testing.T, base, id string) map[string]any {
	t.Helper()
	var status map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/jobs/" + id + "/status")
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		status = map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()
		if s, _ := status["state"].(string); s == "succeeded" || s == "failed" {
			return status
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish: %v", id, status)
	return nil
}

func readExample(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../../examples/" + name)
	if err != nil {
		t.Fatalf("read example %s: %v", name, err)
	}
	return b
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func post(t *testing.T, url string, body []byte) map[string]any {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return out
}
