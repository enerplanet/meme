# Configuration

**Requirements.** Host build: Go ≥ 1.22 and `make`. Real solver execution:
Docker with compose v2 — the [`environment/`](https://github.com/enerplanet/meme/tree/main/environment/) image carries
Python 3.12, the three frameworks (Calliope twice: 0.7.0 and 0.6.8, each in
its own venv) and their solvers (the framework versions are exact-pinned and
load-bearing; the solvers ride on `highspy`/apt without pins — see
the [environment guide](../operations/environment.md)).

**Configuration** — precedence **CLI flag > `.env` > built-in default**
(copy [`.env.example`](https://github.com/enerplanet/meme/blob/main/.env.example) to `.env`, or point elsewhere with
`-env-file` / `MEME_ENV_FILE`):

| `.env` key | Flag | Default | Meaning |
|---|---|---|---|
| `PORT` / `ADDR` | `-addr` | `:8080` | listen port (`ADDR=host:port` wins over `PORT`) |
| `WORK` | `-work` | OS temp | root dir for emitted files and job dirs |
| `EXEC` | `-exec` | `false` | run real solvers vs. dry-run |
| `API_KEY` | `-api-key` | *(empty)* | require this key in every payload; empty = auth off |
| `CORS_ORIGINS` | `-cors-origins` | *(empty)* | comma-separated browser origins allowed via CORS; empty = CORS off |

**Version.** `meme -version` prints the build version and exits, and
`GET /healthz` reports the same value next to the liveness status. `make build`
stamps it from the git tag (`0.1.0-alpha`, or `0.1.0-alpha-3-g1a2b3c4-dirty`
once the tree has moved on), a `go install …@version` binary carries its
module version, and a plain `go build` reports `dev`.

**Authentication.** Set `API_KEY` and every payload-bearing request
(`/validate`, `/convert`, `/simulate`) must carry it as a top-level
`"api_key"` field in the JSON body or it is rejected with a 401
`{"error":…}`. When `API_KEY` is not set (the default), no key is requested
and any key a client sends is simply accepted. The key is compared in
constant time, never logged, and **redacted from the `config.json` persisted
into the result bundle**; keyless payloads still round-trip byte-identical.
The `GET /jobs/{id}` endpoints carry no payload — they are addressed by
unguessable 128-bit random job ids known only to the submitter.

The example payloads in [`examples/`](https://github.com/enerplanet/meme/tree/main/examples/) already ship a top-level
`"api_key": "s3cret"`, so they post verbatim against a server started with the
matching key. For a different key, edit the field (e.g. `jq '.api_key="…"'`);
when `API_KEY` is unset the field is ignored.

```bash
echo 'API_KEY=s3cret' >> .env && make run
curl -s -X POST 'localhost:8080/validate?target=pypsa' -d @examples/pypsa_full.json
```

**CORS.** Off by default: no `Access-Control-*` headers are emitted and
cross-origin browser calls stay blocked. Set `CORS_ORIGINS` to a
comma-separated allow list — exact origins (`https://app.example.com`), the
wildcard `*`, or subdomain wildcards (`https://*.example.com`) — and the
server answers preflight `OPTIONS` on every route (204, before
authentication: preflights carry no payload and hence no `api_key`), echoes
the allowed origin on responses, and exposes `Content-Disposition` so
browser JS can read the zip filename from `GET /jobs/{id}`. Combining `*`
with credentials is rejected at startup, as are malformed origins (e.g. a
trailing slash). Embedders constructing `api.Server` directly get the full
knob set via `api.CORSConfig`: custom methods/headers, credentials,
preflight max-age, Chrome private-network preflights, and an
`AllowOriginFunc` escape hatch. Note CORS is browser policy, not access
control — non-browser clients ignore it; `API_KEY` remains the
authentication.

```bash
echo 'CORS_ORIGINS=http://localhost:5173' >> .env && make run
curl -si -X OPTIONS 'localhost:8080/validate?target=pypsa' \
  -H 'Origin: http://localhost:5173' -H 'Access-Control-Request-Method: POST'
# HTTP/1.1 204 No Content, Access-Control-Allow-Origin: http://localhost:5173, …
```

