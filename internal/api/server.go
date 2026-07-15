// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

// Package api is the HTTP transport layer: it decodes requests, resolves the
// requested targets, and adapts the target registry and the service layer to
// REST. It owns no domain logic — validation lives with the target registry,
// execution with the service Executor, and the bundle layout with the service
// package.
package api

import (
	"context"
	"net/http"
	"os"

	"github.com/enerplanet/meme/internal/emit"
	"github.com/enerplanet/meme/internal/service"
	"github.com/enerplanet/meme/internal/target"
	_ "github.com/enerplanet/meme/internal/target/all" // register built-in targets
)

// maxBodyBytes caps request payloads (canonical models are small; this guards
// against accidental or hostile multi-GB bodies).
const maxBodyBytes = 32 << 20 // 32 MiB

// Server wires the target registry, executor, and async job store behind an
// HTTP API. Executor defaults to a dry-run executor (no Python toolchain
// needed); WorkRoot defaults to a temp dir; Store defaults to a fresh
// in-memory JobStore; JobContext (the lifetime handed to running solvers)
// defaults to context.Background().
//
// APIKey, when non-empty, must be carried by every payload as a top-level
// "api_key" field or the request is rejected with 401. When empty (the
// default, i.e. no API_KEY in the .env), no key is required and any key a
// client happens to send is accepted. The GET endpoints carry no payload;
// job results are addressed by their unguessable 128-bit random ids.
type Server struct {
	Executor   *service.Executor
	WorkRoot   string
	Store      *service.JobStore
	JobContext context.Context
	APIKey     string
}

// NewServer returns an http.Handler exposing (also aliased under /v1/):
//
//	GET  /healthz                 liveness
//	GET  /capabilities            capability matrix (target -> feature -> bool)
//	POST /validate?target=        validate a Job (warnings or 4xx)
//	POST /convert?target=         emit native files synchronously
//	POST /simulate?target=        submit an async job -> 202 {id, ...}
//	GET  /jobs/{id}/status        job state + console log + per-run errors
//	GET  /jobs/{id}               zipped result bundle once finished
//
// ?target= accepts a single target ("pypsa"), a comma-separated list
// ("pypsa,calliope"), or "all": one request validates/emits/runs every
// requested framework; multi-target bundles nest per-target under
// files/<target>/.
func NewServer(s Server) http.Handler {
	if s.Store == nil {
		s.Store = service.NewJobStore()
	}
	if s.Executor == nil {
		s.Executor = service.NewExecutor(nil, 0) // DryRunner
	}
	if s.JobContext == nil {
		s.JobContext = context.Background()
	}
	mux := http.NewServeMux()
	route := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, h)
		method, path, ok := splitPattern(pattern)
		if ok {
			mux.HandleFunc(method+" /v1"+path, h)
		}
	}
	route("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSONResp(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	route("GET /capabilities", func(w http.ResponseWriter, r *http.Request) {
		writeJSONResp(w, http.StatusOK, map[string]any{
			"targets":  target.Names(),
			"features": target.CapabilityMatrix(),
		})
	})
	route("POST /validate", s.handleValidate)
	route("POST /convert", s.handleConvert)
	route("POST /simulate", s.handleSubmit)
	route("GET /jobs/{id}/status", s.handleStatus)
	route("GET /jobs/{id}", s.handleResult)
	return mux
}

// splitPattern splits a "METHOD /path" route pattern.
func splitPattern(pattern string) (method, path string, ok bool) {
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == ' ' {
			return pattern[:i], pattern[i+1:], true
		}
	}
	return "", "", false
}

func (s Server) workDir(prefix string) (string, error) {
	if s.WorkRoot == "" {
		return os.MkdirTemp("", prefix+"-")
	}
	if err := emit.Mkdirs(s.WorkRoot); err != nil {
		return "", err
	}
	return os.MkdirTemp(s.WorkRoot, prefix+"-")
}
