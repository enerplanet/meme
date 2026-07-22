// Copyright (c) 2026 BigGeoData & Spatial AI, Technische Hochschule Deggendorf
// SPDX-License-Identifier: MIT

package api_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/enerplanet/meme/internal/api"
)

func corsServer(t *testing.T, s api.Server) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(api.NewServer(s))
	t.Cleanup(srv.Close)
	return srv
}

func doRequest(t *testing.T, method, url string, hdr map[string]string, body []byte) *http.Response {
	t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, url, err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// preflight sends OPTIONS with Origin and Access-Control-Request-Method: POST.
func preflight(t *testing.T, url, origin string, extra map[string]string) *http.Response {
	t.Helper()
	h := map[string]string{"Origin": origin, "Access-Control-Request-Method": "POST"}
	for k, v := range extra {
		h[k] = v
	}
	return doRequest(t, http.MethodOptions, url, h, nil)
}

func getWithOrigin(t *testing.T, url, origin string) *http.Response {
	t.Helper()
	return doRequest(t, http.MethodGet, url, map[string]string{"Origin": origin}, nil)
}

// vary joins all Vary field lines (the middleware emits one per value, which
// is RFC-legal, so Header.Get would only see the first).
func vary(resp *http.Response) string {
	return strings.Join(resp.Header.Values("Vary"), ", ")
}

// TestCORSDisabledByDefault: the zero CORSConfig changes nothing — no CORS
// headers, no Vary, and preflight OPTIONS keeps the historical 405 from the
// method-scoped mux patterns.
func TestCORSDisabledByDefault(t *testing.T) {
	srv := newTestServer(t)

	resp := getWithOrigin(t, srv.URL+"/healthz", "https://app.example.com")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status %d", resp.StatusCode)
	}
	if v := resp.Header.Get("Access-Control-Allow-Origin"); v != "" {
		t.Errorf("disabled CORS must not emit Allow-Origin, got %q", v)
	}
	if v := vary(resp); v != "" {
		t.Errorf("disabled CORS must not emit Vary, got %q", v)
	}

	pf := preflight(t, srv.URL+"/validate?target=pypsa", "https://app.example.com", nil)
	if pf.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("preflight with CORS disabled: status %d, want 405", pf.StatusCode)
	}
}

// TestCORSPreflight: an allowed origin gets a full 204 preflight answer — echoed
// origin, default methods/headers, the 10-minute max-age, and all three Vary
// values — on the bare route and its /v1 alias alike.
func TestCORSPreflight(t *testing.T) {
	srv := corsServer(t, api.Server{CORS: api.CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}})

	for _, path := range []string{"/validate?target=pypsa", "/v1/validate?target=pypsa"} {
		resp := preflight(t, srv.URL+path, "https://app.example.com", nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("%s: preflight status %d, want 204", path, resp.StatusCode)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
			t.Errorf("%s: Allow-Origin %q, want echoed origin", path, got)
		}
		if got := resp.Header.Get("Access-Control-Allow-Methods"); got != "GET, POST, OPTIONS" {
			t.Errorf("%s: Allow-Methods %q", path, got)
		}
		if got := resp.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Content-Type") {
			t.Errorf("%s: Allow-Headers %q missing Content-Type", path, got)
		}
		if got := resp.Header.Get("Access-Control-Max-Age"); got != "600" {
			t.Errorf("%s: Max-Age %q, want 600", path, got)
		}
		for _, want := range []string{"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"} {
			if !strings.Contains(vary(resp), want) {
				t.Errorf("%s: Vary %q missing %s", path, vary(resp), want)
			}
		}
	}
}

// TestCORSPreflightDeniedOrigin: an unlisted origin still gets the 204 and the
// Vary headers (cache correctness) but zero Access-Control headers — the
// browser then blocks the actual request.
func TestCORSPreflightDeniedOrigin(t *testing.T) {
	srv := corsServer(t, api.Server{CORS: api.CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}})

	resp := preflight(t, srv.URL+"/validate?target=pypsa", "https://evil.test", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("denied preflight status %d, want 204", resp.StatusCode)
	}
	if !strings.Contains(vary(resp), "Origin") {
		t.Errorf("denied preflight must keep Vary, got %q", vary(resp))
	}
	for _, h := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Methods", "Access-Control-Allow-Headers", "Access-Control-Max-Age"} {
		if v := resp.Header.Get(h); v != "" {
			t.Errorf("denied preflight must not emit %s, got %q", h, v)
		}
	}
}

// TestCORSActualRequest: allowed origins get the CORS headers on top of the
// normal response — including on error envelopes like a 404, so browser JS can
// read them — while denied origins get the body but no CORS headers.
func TestCORSActualRequest(t *testing.T) {
	srv := corsServer(t, api.Server{CORS: api.CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}})
	origin := "https://app.example.com"

	get := getWithOrigin(t, srv.URL+"/capabilities", origin)
	if get.StatusCode != http.StatusOK || get.Header.Get("Access-Control-Allow-Origin") != origin {
		t.Errorf("GET: status %d Allow-Origin %q", get.StatusCode, get.Header.Get("Access-Control-Allow-Origin"))
	}
	if !strings.Contains(vary(get), "Origin") {
		t.Errorf("GET: Vary %q missing Origin", vary(get))
	}

	post := doRequest(t, http.MethodPost, srv.URL+"/validate?target=pypsa",
		map[string]string{"Origin": origin, "Content-Type": "application/json"}, sampleBytes(t))
	if post.StatusCode != http.StatusOK || post.Header.Get("Access-Control-Allow-Origin") != origin {
		t.Errorf("POST: status %d Allow-Origin %q", post.StatusCode, post.Header.Get("Access-Control-Allow-Origin"))
	}

	notFound := getWithOrigin(t, srv.URL+"/jobs/nope/status", origin)
	if notFound.StatusCode != http.StatusNotFound || notFound.Header.Get("Access-Control-Allow-Origin") != origin {
		t.Errorf("404 envelope: status %d Allow-Origin %q, want CORS headers on errors too",
			notFound.StatusCode, notFound.Header.Get("Access-Control-Allow-Origin"))
	}

	denied := getWithOrigin(t, srv.URL+"/capabilities", "https://evil.test")
	if denied.StatusCode != http.StatusOK {
		t.Errorf("denied origin still gets the body: status %d", denied.StatusCode)
	}
	if v := denied.Header.Get("Access-Control-Allow-Origin"); v != "" {
		t.Errorf("denied origin must not get Allow-Origin, got %q", v)
	}
}

// TestCORSVaryWithoutOrigin: with CORS enabled, even same-origin requests
// (no Origin header) carry Vary: Origin so shared caches never serve a
// header-less variant to a CORS client.
func TestCORSVaryWithoutOrigin(t *testing.T) {
	srv := corsServer(t, api.Server{CORS: api.CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}})

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if !strings.Contains(vary(resp), "Origin") {
		t.Errorf("Vary %q missing Origin", vary(resp))
	}
	if v := resp.Header.Get("Access-Control-Allow-Origin"); v != "" {
		t.Errorf("no Origin header must mean no Allow-Origin, got %q", v)
	}
}

// TestCORSWildcardOrigin: "*" allows any origin and is emitted literally
// (credentials are off, so the literal wildcard is the spec-preferred form).
func TestCORSWildcardOrigin(t *testing.T) {
	srv := corsServer(t, api.Server{CORS: api.CORSConfig{AllowedOrigins: []string{"*"}}})

	resp := getWithOrigin(t, srv.URL+"/capabilities", "https://anything.test")
	if v := resp.Header.Get("Access-Control-Allow-Origin"); v != "*" {
		t.Errorf("Allow-Origin %q, want literal *", v)
	}
	if v := resp.Header.Get("Access-Control-Allow-Credentials"); v != "" {
		t.Errorf("credentials header must be absent, got %q", v)
	}
}

// TestCORSSubdomainWildcard: https://*.example.com matches any subdomain depth
// case-insensitively, but never the apex domain, other schemes, or lookalike
// hosts like evil-example.com.
func TestCORSSubdomainWildcard(t *testing.T) {
	srv := corsServer(t, api.Server{CORS: api.CORSConfig{AllowedOrigins: []string{"https://*.example.com"}}})

	for _, origin := range []string{"https://app.example.com", "https://a.b.example.com", "HTTPS://APP.EXAMPLE.COM"} {
		resp := getWithOrigin(t, srv.URL+"/healthz", origin)
		if v := resp.Header.Get("Access-Control-Allow-Origin"); v != origin {
			t.Errorf("%s: Allow-Origin %q, want match echoed", origin, v)
		}
	}
	for _, origin := range []string{"https://evil-example.com", "https://example.com", "http://app.example.com", "https://.example.com"} {
		resp := getWithOrigin(t, srv.URL+"/healthz", origin)
		if v := resp.Header.Get("Access-Control-Allow-Origin"); v != "" {
			t.Errorf("%s must not match, got Allow-Origin %q", origin, v)
		}
	}
}

// TestCORSNullOrigin: the literal "null" origin (sandboxed iframes, file://)
// is denied unless listed explicitly.
func TestCORSNullOrigin(t *testing.T) {
	denying := corsServer(t, api.Server{CORS: api.CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}})
	if v := getWithOrigin(t, denying.URL+"/healthz", "null").Header.Get("Access-Control-Allow-Origin"); v != "" {
		t.Errorf("unlisted null origin must be denied, got %q", v)
	}

	allowing := corsServer(t, api.Server{CORS: api.CORSConfig{AllowedOrigins: []string{"null"}}})
	if v := getWithOrigin(t, allowing.URL+"/healthz", "null").Header.Get("Access-Control-Allow-Origin"); v != "null" {
		t.Errorf("listed null origin must be echoed, got %q", v)
	}
}

// TestCORSCredentials: with AllowCredentials the specific origin is echoed —
// never "*", even in the misconfigured wildcard case that skipped Validate —
// and Allow-Credentials: true rides on preflights and actual responses.
func TestCORSCredentials(t *testing.T) {
	srv := corsServer(t, api.Server{CORS: api.CORSConfig{
		AllowedOrigins:   []string{"https://app.example.com"},
		AllowCredentials: true,
	}})
	origin := "https://app.example.com"

	pf := preflight(t, srv.URL+"/validate?target=pypsa", origin, nil)
	if pf.Header.Get("Access-Control-Allow-Origin") != origin || pf.Header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("preflight: Allow-Origin %q Allow-Credentials %q",
			pf.Header.Get("Access-Control-Allow-Origin"), pf.Header.Get("Access-Control-Allow-Credentials"))
	}
	actual := getWithOrigin(t, srv.URL+"/healthz", origin)
	if actual.Header.Get("Access-Control-Allow-Origin") != origin || actual.Header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("actual: Allow-Origin %q Allow-Credentials %q",
			actual.Header.Get("Access-Control-Allow-Origin"), actual.Header.Get("Access-Control-Allow-Credentials"))
	}

	// Defense-in-depth: "*" + credentials fails Validate, but if wired anyway
	// the middleware echoes the origin instead of the forbidden literal "*".
	loose := corsServer(t, api.Server{CORS: api.CORSConfig{AllowedOrigins: []string{"*"}, AllowCredentials: true}})
	if v := getWithOrigin(t, loose.URL+"/healthz", origin).Header.Get("Access-Control-Allow-Origin"); v != origin {
		t.Errorf("wildcard+credentials must echo the origin, got %q", v)
	}
}

// TestCORSValidate: Validate accepts the zero value and well-formed configs
// and rejects the Fetch-spec-forbidden or silently-dead ones.
func TestCORSValidate(t *testing.T) {
	if err := (api.CORSConfig{}).Validate(); err != nil {
		t.Errorf("zero value must validate, got %v", err)
	}
	good := api.CORSConfig{AllowedOrigins: []string{"https://app.example.com", "https://*.example.org", "http://localhost:5173", "null", "*"}}
	if err := good.Validate(); err != nil {
		t.Errorf("well-formed config must validate, got %v", err)
	}

	bad := []api.CORSConfig{
		{AllowedOrigins: []string{"*"}, AllowCredentials: true}, // Fetch spec forbids
		{AllowedOrigins: []string{"https://foo.*.com"}},         // mid-host wildcard
		{AllowedOrigins: []string{"*.example.com"}},             // missing scheme
		{AllowedOrigins: []string{"https://app.example.com/"}},  // trailing path
		{AllowedOrigins: []string{""}},                          // empty entry
	}
	for _, cfg := range bad {
		if err := cfg.Validate(); err == nil {
			t.Errorf("config %v must be rejected", cfg.AllowedOrigins)
		}
	}
}

// TestCORSExposeHeaders: the zip download exposes Content-Disposition by
// default so browser JS can read the bundle filename.
func TestCORSExposeHeaders(t *testing.T) {
	srv := corsServer(t, api.Server{CORS: api.CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}})

	sub := post(t, srv.URL+"/simulate?target=pypsa", sampleBytes(t))
	id, _ := sub["id"].(string)
	if id == "" {
		t.Fatalf("no job id: %v", sub)
	}
	awaitDone(t, srv.URL, id)

	resp := getWithOrigin(t, srv.URL+"/jobs/"+id, "https://app.example.com")
	if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("Content-Type %q, want application/zip", ct)
	}
	if v := resp.Header.Get("Access-Control-Expose-Headers"); !strings.Contains(v, "Content-Disposition") {
		t.Errorf("Expose-Headers %q missing Content-Disposition", v)
	}
}

// TestCORSPreflightWithAPIKey: preflights carry no payload and therefore no
// api_key — they must be answered before authentication, never 401.
func TestCORSPreflightWithAPIKey(t *testing.T) {
	srv := corsServer(t, api.Server{
		APIKey: "s3cret",
		CORS:   api.CORSConfig{AllowedOrigins: []string{"https://app.example.com"}},
	})

	resp := preflight(t, srv.URL+"/simulate?target=pypsa", "https://app.example.com", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("preflight with auth enabled: status %d, want 204", resp.StatusCode)
	}
	if v := resp.Header.Get("Access-Control-Allow-Origin"); v != "https://app.example.com" {
		t.Errorf("preflight with auth enabled: Allow-Origin %q", v)
	}
}

// TestCORSAllowOriginFunc: a func-only config enables the middleware and is
// consulted after the (empty) list; approved origins are echoed verbatim.
func TestCORSAllowOriginFunc(t *testing.T) {
	srv := corsServer(t, api.Server{CORS: api.CORSConfig{
		AllowOriginFunc: func(origin string) bool { return origin == "https://ok.test" },
	}})

	if v := getWithOrigin(t, srv.URL+"/healthz", "https://ok.test").Header.Get("Access-Control-Allow-Origin"); v != "https://ok.test" {
		t.Errorf("approved origin: Allow-Origin %q", v)
	}
	if v := getWithOrigin(t, srv.URL+"/healthz", "https://no.test").Header.Get("Access-Control-Allow-Origin"); v != "" {
		t.Errorf("rejected origin: Allow-Origin %q, want none", v)
	}
}

// TestCORSMaxAge: positive durations are emitted as whole seconds, zero means
// the 10-minute default, negative omits the header.
func TestCORSMaxAge(t *testing.T) {
	cases := []struct {
		maxAge time.Duration
		want   string // "" = header absent
	}{
		{5 * time.Minute, "300"},
		{0, "600"},
		{-1, ""},
	}
	for _, c := range cases {
		srv := corsServer(t, api.Server{CORS: api.CORSConfig{
			AllowedOrigins: []string{"https://app.example.com"},
			MaxAge:         c.maxAge,
		}})
		resp := preflight(t, srv.URL+"/validate?target=pypsa", "https://app.example.com", nil)
		if got := resp.Header.Get("Access-Control-Max-Age"); got != c.want {
			t.Errorf("MaxAge=%v: header %q, want %q", c.maxAge, got, c.want)
		}
	}
}

// TestCORSNonPreflightOptions: OPTIONS without Access-Control-Request-Method
// is not a preflight — it falls through to the mux (405) but still carries the
// actual-request CORS headers.
func TestCORSNonPreflightOptions(t *testing.T) {
	srv := corsServer(t, api.Server{CORS: api.CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}})

	resp := doRequest(t, http.MethodOptions, srv.URL+"/validate?target=pypsa",
		map[string]string{"Origin": "https://app.example.com"}, nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("non-preflight OPTIONS: status %d, want 405", resp.StatusCode)
	}
	if v := resp.Header.Get("Access-Control-Allow-Origin"); v != "https://app.example.com" {
		t.Errorf("non-preflight OPTIONS: Allow-Origin %q", v)
	}
	if !strings.Contains(vary(resp), "Origin") {
		t.Errorf("non-preflight OPTIONS: Vary %q missing Origin", vary(resp))
	}
}

// TestCORSPrivateNetwork: Chrome's Access-Control-Request-Private-Network
// preflight is answered affirmatively iff AllowPrivateNetwork is set.
func TestCORSPrivateNetwork(t *testing.T) {
	pna := map[string]string{"Access-Control-Request-Private-Network": "true"}

	off := corsServer(t, api.Server{CORS: api.CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}})
	resp := preflight(t, off.URL+"/validate?target=pypsa", "https://app.example.com", pna)
	if v := resp.Header.Get("Access-Control-Allow-Private-Network"); v != "" {
		t.Errorf("PNA off: header must be absent, got %q", v)
	}

	on := corsServer(t, api.Server{CORS: api.CORSConfig{
		AllowedOrigins:      []string{"https://app.example.com"},
		AllowPrivateNetwork: true,
	}})
	resp = preflight(t, on.URL+"/validate?target=pypsa", "https://app.example.com", pna)
	if v := resp.Header.Get("Access-Control-Allow-Private-Network"); v != "true" {
		t.Errorf("PNA on: header %q, want true", v)
	}
}
