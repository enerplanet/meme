// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package api_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enerplanet/meme/internal/api"
	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/service"
	"github.com/enerplanet/meme/internal/target"
)

// TestMethodNotAllowed: routes are method-scoped — the wrong verb is a 405,
// not a handler running on garbage input.
func TestMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t)
	if resp, _ := http.Get(srv.URL + "/validate?target=pypsa"); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /validate: status %d, want 405", resp.StatusCode)
	}
	if resp, _ := http.Post(srv.URL+"/capabilities", "application/json", nil); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /capabilities: status %d, want 405", resp.StatusCode)
	}
}

// TestUnknownJob: status and result of a non-existent job are 404s carrying the
// JSON error envelope.
func TestUnknownJob(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/jobs/deadbeef/status", "/jobs/deadbeef"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, resp.StatusCode)
		}
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out["error"] == nil {
			t.Errorf("%s: body must be the {\"error\":...} envelope, got err=%v body=%v", path, err, out)
		}
		resp.Body.Close()
	}
}

// TestTargetParamEdgeCases: an empty ?target= is a 400, and duplicates in the
// list collapse to a single target.
func TestTargetParamEdgeCases(t *testing.T) {
	srv := newTestServer(t)
	resp, _ := http.Post(srv.URL+"/validate", "application/json", bytes.NewReader(sampleBytes(t)))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing target param: status %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()

	r := post(t, srv.URL+"/convert?target=pypsa,pypsa", sampleBytes(t))
	if r["target"] != "pypsa" || r["entrypoint"] == nil {
		t.Errorf("duplicate targets must collapse to one: %v", r)
	}
}

// blockingRunner blocks every run until release is closed, so a test can
// observe the job in its running state.
type blockingRunner struct{ release chan struct{} }

func (b blockingRunner) Run(ctx context.Context, p target.RunPlan) service.RunResult {
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return service.RunResult{Plan: p, ExitCode: 0, Stdout: "released"}
}

// TestResultLifecycle: downloading the bundle of an unfinished job is a 409;
// once finished the zip's config.json is byte-identical to the submitted body.
func TestResultLifecycle(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(api.NewServer(api.Server{
		Executor: service.NewExecutor(blockingRunner{release: release}, 1),
		WorkRoot: t.TempDir(),
	}))
	defer srv.Close()

	payload := sampleBytes(t)
	sub := post(t, srv.URL+"/simulate?target=pypsa", payload)
	id, _ := sub["id"].(string)
	if id == "" {
		t.Fatalf("no job id: %v", sub)
	}

	// The runner is blocked, so the job cannot be finished yet.
	resp, err := http.Get(srv.URL + "/jobs/" + id)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("result before finish: status %d, want 409", resp.StatusCode)
	}
	var conflict map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&conflict)
	resp.Body.Close()
	if s, _ := conflict["state"].(string); s != "queued" && s != "running" {
		t.Errorf("409 body must name the live state, got %v", conflict)
	}

	close(release)
	if status := awaitDone(t, srv.URL, id); status["state"] != "succeeded" {
		t.Fatalf("job did not succeed after release: %v", status)
	}

	resp, err = http.Get(srv.URL + "/jobs/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("result after finish: status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("zip open: %v", err)
	}
	var cfg []byte
	for _, f := range zr.File {
		if f.Name == "config.json" {
			rc, _ := f.Open()
			cfg, _ = io.ReadAll(rc)
			rc.Close()
		}
	}
	if !bytes.Equal(cfg, payload) {
		t.Errorf("config.json in the bundle must be byte-identical to the submitted body (got %d bytes, want %d)", len(cfg), len(payload))
	}
}

// TestSubmitRejectsBeforeScheduling: a payload one requested target cannot
// honor is a 422 at submit time naming that target — a multi-target job never
// silently drops a framework.
func TestSubmitRejectsBeforeScheduling(t *testing.T) {
	srv := newTestServer(t)
	// The raw sample uses UC extras + constraints AdOpT rejects.
	resp, err := http.Post(srv.URL+"/simulate?target=all", "application/json", bytes.NewReader(sampleBytes(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("submit with a rejecting target: status %d, want 422", resp.StatusCode)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out["scheduled"] != false || out["target"] == "" || out["error"] == nil {
		t.Errorf("422 must name the rejecting target and reason: %v", out)
	}
	if _, ok := out["id"]; ok {
		t.Error("a rejected submit must not create a job")
	}
	_ = model.TargetAdOpt // (documents which target rejects the raw sample)
}

// TestValidateMultiTargetMixedVerdicts: when one requested target accepts the
// payload and another rejects it, /validate answers 422 with overall
// valid=false and one verdict per target — the accepting target keeps its
// valid=true, the rejecting one carries the reason.
func TestValidateMultiTargetMixedVerdicts(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Post(srv.URL+"/validate?target=pypsa,adopt-net0", "application/json", bytes.NewReader(sampleBytes(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("mixed verdicts: status %d, want 422", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["valid"] != false {
		t.Errorf("overall valid must be false, got %v", out["valid"])
	}
	verdicts, _ := out["targets"].(map[string]any)
	if pv, _ := verdicts["pypsa"].(map[string]any); pv["valid"] != true {
		t.Errorf("pypsa verdict must stay valid, got %v", pv)
	}
	av, _ := verdicts["adopt-net0"].(map[string]any)
	if av["valid"] != false {
		t.Errorf("adopt-net0 verdict must be invalid, got %v", av)
	}
	if reason, _ := av["error"].(string); reason == "" {
		t.Errorf("invalid verdict must carry the reason, got %v", av)
	}
}

// TestConvertRejectsInvalidPayload: /convert validates every requested target
// before writing anything — a rejecting target is a 422 error envelope naming
// that target, and the work root stays empty.
func TestConvertRejectsInvalidPayload(t *testing.T) {
	work := t.TempDir()
	srv := httptest.NewServer(api.NewServer(api.Server{WorkRoot: work}))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/convert?target=pypsa,adopt-net0", "application/json", bytes.NewReader(sampleBytes(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("rejecting target: status %d, want 422", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if msg, _ := out["error"].(string); !strings.HasPrefix(msg, "adopt-net0: ") {
		t.Errorf("422 must name the rejecting target, got %q", msg)
	}
	entries, err := os.ReadDir(work)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("a rejected convert must not write anything, found %v", entries)
	}
}

// TestWorkRootUnusable: when the work root cannot be created (here: nested
// under a regular file), /convert and /simulate answer a 500 error envelope —
// after validation, before any job exists — instead of panicking.
func TestWorkRootUnusable(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.NewServer(api.Server{WorkRoot: filepath.Join(blocker, "work")}))
	defer srv.Close()

	for _, path := range []string{"/convert?target=pypsa", "/simulate?target=pypsa"} {
		resp, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(sampleBytes(t)))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("%s: status %d, want 500", path, resp.StatusCode)
		}
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out["error"] == nil {
			t.Errorf("%s: 500 body must be the {\"error\":...} envelope, got err=%v body=%v", path, err, out)
		}
		resp.Body.Close()
	}
}
