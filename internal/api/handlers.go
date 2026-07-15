// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package api

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/enerplanet/meme/internal/model"
	"github.com/enerplanet/meme/internal/service"
	"github.com/enerplanet/meme/internal/target"
)

func (s Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	_, job, targets, ok := s.decodeJobRaw(w, r)
	if !ok {
		return
	}
	if len(targets) == 1 {
		warnings, err := target.ValidateFor(&job, targets[0])
		if err != nil {
			writeJSONResp(w, http.StatusUnprocessableEntity, map[string]any{"valid": false, "error": err.Error()})
			return
		}
		writeJSONResp(w, http.StatusOK, map[string]any{"valid": true, "warnings": warnings})
		return
	}
	// Multi-target: one verdict per target, overall valid only when all are.
	perTarget := map[string]any{}
	allValid := true
	for _, t := range targets {
		warnings, err := target.ValidateFor(&job, t)
		if err != nil {
			perTarget[string(t)] = map[string]any{"valid": false, "error": err.Error()}
			allValid = false
		} else {
			perTarget[string(t)] = map[string]any{"valid": true, "warnings": warnings}
		}
	}
	code := http.StatusOK
	if !allValid {
		code = http.StatusUnprocessableEntity
	}
	writeJSONResp(w, code, map[string]any{"valid": allValid, "targets": perTarget})
}

func (s Server) handleConvert(w http.ResponseWriter, r *http.Request) {
	_, job, targets, ok := s.decodeJobRaw(w, r)
	if !ok {
		return
	}
	// Validate every requested target before writing anything.
	allWarnings := map[string]any{}
	for _, t := range targets {
		warnings, err := target.ValidateFor(&job, t)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("%s: %s", t, err))
			return
		}
		allWarnings[string(t)] = warnings
	}
	dir, err := s.workDir("convert")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	entrypoints := map[string]any{}
	for _, t := range targets {
		impl, _ := target.For(t)
		out := filepath.Join(dir, "input")
		if len(targets) > 1 {
			out = filepath.Join(dir, string(t), "input")
		}
		entrypoint, err := impl.Emit(&job, out)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("%s: emit failed: %s", t, err))
			return
		}
		entrypoints[string(t)] = entrypoint
	}
	if len(targets) == 1 {
		t := string(targets[0])
		writeJSONResp(w, http.StatusOK, map[string]any{"target": t, "entrypoint": entrypoints[t], "warnings": allWarnings[t]})
		return
	}
	writeJSONResp(w, http.StatusOK, map[string]any{"targets": targetNames(targets), "entrypoints": entrypoints, "warnings": allWarnings})
}

// handleSubmit validates synchronously for EVERY requested target (so callers
// learn about hard errors immediately), then schedules one job that emits and
// runs each target in turn. The raw payload is preserved as config.json in the
// job dir so the result bundle carries the initial config verbatim.
func (s Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	raw, job, targets, ok := s.decodeJobRaw(w, r)
	if !ok {
		return
	}
	var warnings []string
	for _, t := range targets {
		warns, err := target.ValidateFor(&job, t)
		if err != nil {
			writeJSONResp(w, http.StatusUnprocessableEntity, map[string]any{
				"scheduled": false, "target": string(t), "error": err.Error(), "warnings": warnings,
			})
			return
		}
		for _, wmsg := range warns {
			if len(targets) > 1 {
				wmsg = string(t) + ": " + wmsg
			}
			warnings = append(warnings, wmsg)
		}
	}
	dir, err := s.workDir("job")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The persisted config (and thus the result bundle) must never carry the
	// credential; payloads without a key stay byte-identical.
	if job.APIKey != "" {
		raw = redactAPIKey(raw)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "persist config: "+err.Error())
		return
	}
	rec := s.Store.Create(model.Target(strings.Join(targetNames(targets), ",")), dir, warnings)
	go s.Executor.Execute(s.JobContext, rec, job, targets)

	writeJSONResp(w, http.StatusAccepted, map[string]any{
		"scheduled":  true,
		"id":         rec.ID(),
		"state":      service.StateQueued,
		"target":     strings.Join(targetNames(targets), ","),
		"warnings":   warnings,
		"status_url": "/jobs/" + rec.ID() + "/status",
		"result_url": "/jobs/" + rec.ID(),
	})
}

func (s Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.Store.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSONResp(w, http.StatusOK, rec.View(true))
}

// handleResult streams the zip bundle once the job is done.
func (s Server) handleResult(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.Store.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	view := rec.View(false)
	if !view.State.Done() {
		writeJSONResp(w, http.StatusConflict, map[string]any{
			"id": view.ID, "state": view.State, "message": "job not finished",
		})
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename="+view.ID+".zip")
	_ = service.WriteBundle(w, rec)
}

// decodeJobRaw reads the (size-capped) Job body and the ?target= query param:
// one target, a comma-separated list, or "all". It enforces the configured
// API key (a 401 when the payload's api_key is missing or wrong; no check
// when the server has none). Returns the raw body so the caller can persist
// the initial config.
func (s Server) decodeJobRaw(w http.ResponseWriter, r *http.Request) ([]byte, model.Job, []model.Target, bool) {
	targets, err := parseTargets(r.URL.Query().Get("target"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, model.Job{}, nil, false
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		code := http.StatusBadRequest
		if _, tooBig := err.(*http.MaxBytesError); tooBig {
			code = http.StatusRequestEntityTooLarge
		}
		writeError(w, code, "read body: "+err.Error())
		return nil, model.Job{}, nil, false
	}
	var job model.Job
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&job); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return nil, model.Job{}, nil, false
	}
	if s.APIKey != "" && subtle.ConstantTimeCompare([]byte(job.APIKey), []byte(s.APIKey)) != 1 {
		writeError(w, http.StatusUnauthorized, "invalid or missing api_key in payload")
		return nil, model.Job{}, nil, false
	}
	return raw, job, targets, true
}

// redactAPIKey strips the top-level api_key field from a raw payload before it
// is persisted as config.json. Only called when a key is present, so keyless
// configs round-trip byte-identical into the bundle.
func redactAPIKey(raw []byte) []byte {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return raw // decodeJobRaw already accepted it; keep the original on surprise
	}
	delete(doc, "api_key")
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return raw
	}
	return out
}

// parseTargets resolves the ?target= parameter: "pypsa", "pypsa,calliope", or
// "all" (every registered target). Duplicates collapse, order is preserved.
func parseTargets(q string) ([]model.Target, error) {
	if q == "all" {
		return target.Names(), nil
	}
	seen := map[model.Target]bool{}
	var out []model.Target
	for _, part := range strings.Split(q, ",") {
		t := model.Target(strings.TrimSpace(part))
		if !target.Known(t) {
			return nil, fmt.Errorf("unknown target %q (use one of %v, a comma-separated list, or \"all\")", t, target.Names())
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out, nil
}

func targetNames(targets []model.Target) []string {
	out := make([]string, len(targets))
	for i, t := range targets {
		out[i] = string(t)
	}
	return out
}
