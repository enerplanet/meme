// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Defaults applied by the CORS middleware when the corresponding CORSConfig
// list is left empty. Methods cover everything this API serves; the exposed
// Content-Disposition lets browser JS read the zip filename from
// GET /jobs/{id}.
var (
	defaultAllowedMethods = []string{http.MethodGet, http.MethodPost, http.MethodOptions}
	defaultAllowedHeaders = []string{"Content-Type", "Authorization"}
	defaultExposeHeaders  = []string{"Content-Disposition"}
)

// defaultMaxAge caps preflight caching when CORSConfig.MaxAge is zero.
// 10 minutes is Chromium's upper bound, so a larger default buys nothing.
const defaultMaxAge = 10 * time.Minute

// CORSConfig controls cross-origin resource sharing for browser clients. The
// zero value disables CORS entirely: no CORS headers are emitted and OPTIONS
// requests keep their historical behavior (405 from the method-scoped mux
// patterns) — fully backward compatible, secure by default. CORS is enabled
// when AllowedOrigins is non-empty or AllowOriginFunc is set; every other
// field then falls back to a sensible default.
//
// Server owners should call Validate at startup: NewServer returns no error,
// so it cannot reject a misconfiguration itself (the middleware still refuses
// to combine the "*" origin with credentials, per the Fetch spec).
//
// Note CORS is a browser-side policy, not access control — non-browser
// clients ignore it entirely; API_KEY remains the actual authentication.
type CORSConfig struct {
	// AllowedOrigins lists origins allowed to read responses: exact origins
	// ("https://app.example.com"), the wildcard "*" (any origin), or a
	// subdomain wildcard ("https://*.example.com", matching any subdomain
	// depth — https://a.example.com, https://a.b.example.com — but neither
	// https://example.com itself nor https://evil-example.com). Matching is
	// case-insensitive. The literal origin "null" (sandboxed iframes,
	// file:// pages) is only allowed when listed explicitly or via "*".
	AllowedOrigins []string

	// AllowOriginFunc is a programmatic escape hatch, consulted with the raw
	// Origin header value when the origin matches nothing in AllowedOrigins.
	// Origins it approves are echoed back verbatim.
	AllowOriginFunc func(origin string) bool

	// AllowedMethods a cross-origin request may use. Empty means
	// GET, POST and OPTIONS — everything this API serves. A literal "*" is
	// emitted as-is and only acts as a wildcard on credentialless requests;
	// Validate rejects it in combination with AllowCredentials.
	AllowedMethods []string

	// AllowedHeaders a preflight may request. Empty means Content-Type (JSON
	// POSTs need it) and Authorization (for auth proxies in front of the
	// API). The single entry "*" echoes whatever the preflight asks for — a
	// literal "*" would be read as a header named "*" on credentialed
	// requests.
	AllowedHeaders []string

	// ExposeHeaders lists response headers browser JS may read beyond the
	// CORS-safelisted set. Empty means Content-Disposition, so clients can
	// recover the zip filename from GET /jobs/{id}. Like AllowedMethods, a
	// literal "*" is only a wildcard without credentials; Validate rejects
	// the credentialed combination.
	ExposeHeaders []string

	// AllowCredentials permits cookies and TLS client certificates on
	// cross-origin requests. Forbidden together with the "*" origin:
	// Validate rejects the combination, and the middleware echoes the
	// specific origin rather than "*" regardless.
	AllowCredentials bool

	// MaxAge bounds how long browsers may cache a preflight answer. The zero
	// value means 10 minutes (Chromium's cap); a negative value omits the
	// header entirely (browsers then fall back to their 5-second default).
	// Emitted as whole seconds.
	MaxAge time.Duration

	// AllowPrivateNetwork answers Chrome's Private Network Access preflights
	// (Access-Control-Request-Private-Network) affirmatively — needed when a
	// public page calls an API on a LAN or localhost. Off by default.
	AllowPrivateNetwork bool
}

// enabled reports whether the CORS middleware should be installed at all.
func (c CORSConfig) enabled() bool {
	return len(c.AllowedOrigins) > 0 || c.AllowOriginFunc != nil
}

// Validate rejects configurations that are forbidden by the Fetch spec or —
// like a trailing slash on an origin — silently match nothing. It returns nil
// on the zero value. Call it once at startup; the middleware itself accepts
// whatever it is given.
func (c CORSConfig) Validate() error {
	if c.AllowCredentials {
		// On credentialed requests browsers read "*" in these headers as a
		// literal token, not a wildcard — the config would silently not do
		// what it says. (AllowedHeaders "*" is exempt: it is echo mode.)
		for _, m := range c.AllowedMethods {
			if m == "*" {
				return fmt.Errorf("cors: AllowedMethods \"*\" cannot be combined with AllowCredentials (browsers treat it as a literal method name on credentialed requests)")
			}
		}
		for _, h := range c.ExposeHeaders {
			if h == "*" {
				return fmt.Errorf("cors: ExposeHeaders \"*\" cannot be combined with AllowCredentials (browsers treat it as a literal header name on credentialed requests)")
			}
		}
	}
	for _, o := range c.AllowedOrigins {
		if o == "" {
			return fmt.Errorf("cors: AllowedOrigins contains an empty entry")
		}
		if o == "*" {
			if c.AllowCredentials {
				return fmt.Errorf("cors: the \"*\" origin cannot be combined with AllowCredentials (the Fetch spec forbids wildcard origins on credentialed requests)")
			}
			continue
		}
		if strings.EqualFold(o, "null") {
			continue
		}
		scheme, host, ok := strings.Cut(o, "://")
		if !ok || scheme == "" || host == "" {
			return fmt.Errorf("cors: origin %q must be \"*\", \"null\", or scheme://host[:port]", o)
		}
		if strings.Contains(host, "/") {
			return fmt.Errorf("cors: origin %q must not contain a path (origins are scheme://host[:port], no trailing slash)", o)
		}
		if strings.Contains(scheme, "*") ||
			(strings.Contains(host, "*") && (!strings.HasPrefix(host, "*.") || len(host) < len("*.x") || strings.Contains(host[1:], "*"))) {
			return fmt.Errorf("cors: origin %q: \"*\" is only valid on its own or as a subdomain wildcard like https://*.example.com", o)
		}
	}
	return nil
}

// wildcard matches origins of the form prefix + <at least one character> +
// suffix; https://*.example.com becomes {"https://", ".example.com"}.
type wildcard struct{ prefix, suffix string }

func (w wildcard) match(origin string) bool {
	return len(origin) > len(w.prefix)+len(w.suffix) &&
		strings.HasPrefix(origin, w.prefix) && strings.HasSuffix(origin, w.suffix)
}

// corsHandler wraps the API mux with CORS handling. All list-shaped config is
// normalized into ready-to-emit header values at construction time, so the
// per-request work is a map lookup plus header writes.
type corsHandler struct {
	cfg  CORSConfig
	next http.Handler

	allowAll  bool            // "*" listed
	exact     map[string]bool // lowercased exact origins (may include "null")
	wildcards []wildcard      // lowercased subdomain patterns

	methods    string // Access-Control-Allow-Methods value
	headers    string // Access-Control-Allow-Headers value ("" with headersAny)
	headersAny bool   // AllowedHeaders is "*": echo the requested headers
	expose     string // Access-Control-Expose-Headers value
	maxAge     string // Access-Control-Max-Age in seconds; "" omits the header
}

func newCORSHandler(cfg CORSConfig, next http.Handler) *corsHandler {
	h := &corsHandler{cfg: cfg, next: next, exact: map[string]bool{}}
	for _, o := range cfg.AllowedOrigins {
		o = strings.ToLower(o)
		if o == "*" {
			h.allowAll = true
		} else if i := strings.Index(o, "://*."); i >= 0 {
			h.wildcards = append(h.wildcards, wildcard{prefix: o[:i+len("://")], suffix: o[i+len("://*"):]})
		} else {
			h.exact[o] = true
		}
	}

	join := func(vals, fallback []string, canon func(string) string) string {
		if len(vals) == 0 {
			vals = fallback
		}
		out := make([]string, len(vals))
		for i, v := range vals {
			out[i] = canon(v)
		}
		return strings.Join(out, ", ")
	}
	h.methods = join(cfg.AllowedMethods, defaultAllowedMethods, strings.ToUpper)
	if len(cfg.AllowedHeaders) == 1 && cfg.AllowedHeaders[0] == "*" {
		h.headersAny = true
	} else {
		h.headers = join(cfg.AllowedHeaders, defaultAllowedHeaders, http.CanonicalHeaderKey)
	}
	h.expose = join(cfg.ExposeHeaders, defaultExposeHeaders, http.CanonicalHeaderKey)

	switch {
	case cfg.MaxAge < 0: // omit
	case cfg.MaxAge == 0:
		h.maxAge = strconv.Itoa(int(defaultMaxAge / time.Second))
	default:
		h.maxAge = strconv.Itoa(int(cfg.MaxAge / time.Second))
	}
	return h
}

func (h *corsHandler) originAllowed(origin string) bool {
	if h.allowAll {
		return true
	}
	o := strings.ToLower(origin)
	if h.exact[o] {
		return true
	}
	for _, w := range h.wildcards {
		if w.match(o) {
			return true
		}
	}
	return h.cfg.AllowOriginFunc != nil && h.cfg.AllowOriginFunc(origin)
}

// allowOriginValue is the Access-Control-Allow-Origin value for an allowed
// origin: the literal "*" when every origin is allowed, otherwise the request
// origin echoed back. Credentialed responses always echo the specific origin —
// the Fetch spec rejects "*" there — even if the caller skipped Validate.
func (h *corsHandler) allowOriginValue(origin string) string {
	if h.allowAll && !h.cfg.AllowCredentials {
		return "*"
	}
	return origin
}

func (h *corsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
		h.preflight(w, r, origin)
		return
	}

	// Actual request (a non-preflight OPTIONS falls through to the mux and
	// keeps its 405). Headers are set before the handler runs so that every
	// outcome carries them: error envelopes, 413s, the streamed zip. Vary is
	// emitted even without an Origin so caches never mix the variants.
	hdr := w.Header()
	hdr.Add("Vary", "Origin")
	if origin != "" && h.originAllowed(origin) {
		hdr.Set("Access-Control-Allow-Origin", h.allowOriginValue(origin))
		if h.cfg.AllowCredentials {
			hdr.Set("Access-Control-Allow-Credentials", "true")
		}
		if h.expose != "" {
			hdr.Set("Access-Control-Expose-Headers", h.expose)
		}
	}
	h.next.ServeHTTP(w, r)
}

// preflight answers a CORS preflight without consulting the mux (whose
// method-scoped patterns would 405 it) or authentication (preflights carry no
// payload, hence no api_key — the browser sends the credentialless OPTIONS on
// its own). A denied origin gets the Vary headers and nothing else; the
// browser then blocks the actual request. The configured method/header lists
// are emitted as-is: the Fetch spec has the browser compare the request
// against them and fail the fetch itself.
func (h *corsHandler) preflight(w http.ResponseWriter, r *http.Request, origin string) {
	hdr := w.Header()
	hdr.Add("Vary", "Origin")
	hdr.Add("Vary", "Access-Control-Request-Method")
	hdr.Add("Vary", "Access-Control-Request-Headers")
	if origin != "" && h.originAllowed(origin) {
		hdr.Set("Access-Control-Allow-Origin", h.allowOriginValue(origin))
		hdr.Set("Access-Control-Allow-Methods", h.methods)
		if h.headersAny {
			if req := r.Header.Get("Access-Control-Request-Headers"); req != "" {
				hdr.Set("Access-Control-Allow-Headers", req)
			}
		} else if h.headers != "" {
			hdr.Set("Access-Control-Allow-Headers", h.headers)
		}
		if h.cfg.AllowCredentials {
			hdr.Set("Access-Control-Allow-Credentials", "true")
		}
		if h.maxAge != "" {
			hdr.Set("Access-Control-Max-Age", h.maxAge)
		}
		if h.cfg.AllowPrivateNetwork && r.Header.Get("Access-Control-Request-Private-Network") == "true" {
			hdr.Set("Access-Control-Allow-Private-Network", "true")
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
