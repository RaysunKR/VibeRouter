# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

VibeRouter is a Go-based AI model API gateway that proxies and load-balances requests between OpenAI and Anthropic APIs. It supports protocol transformation when the client API style differs from the backend provider. Backend models are organized into **tiers** (advanced / basic), routed by task complexity and context length, and managed through a login-protected web UI. **There is no database** — all state lives in `config.yaml` plus JSON Lines log files.

## Build & Test Commands

```bash
go build -o viberouter.exe .          # Build the server binary
go run .                              # Run locally (reads ./config.yaml; logs to stdout + log.file)
go vet ./internal/...                 # Lint (skip ./opensource — stale snapshot)
go mod tidy                           # Update dependencies
go test ./internal/service/           # Routing unit tests (fast, no network)
go test ./internal/service/ -run TestRoute_LongContextFilter -v   # Run a single test
go test ./test/sdk/                   # SDK integration tests (needs a running server + reachable backends; slow)
```

`config.yaml` is created/upgraded automatically on first run (legacy flat `backend_models` migrate into the `basic` tier; a default `admin/admin` user is seeded). The binary reads/writes `config.yaml` next to the executable.

## Architecture

### Request Flow
1. Client request → Middleware (API-key `AuthMiddleware` for `/v1/*`, or session `AdminAuthMiddleware` for `/admin/*`) → Handler
2. Handler builds a `RouteRequest` (model name, message turns, est. tokens, has_tools, has_code) from the parsed body
3. `LoadBalancer.Route()` resolves an **ordered failover list**: complexity → tier → long-context filter → priority ordering
4. Handler iterates the list; for each candidate: `RequestTransformer` converts request if client style ≠ provider, proxies upstream, transforms response back, logs the call
5. On 5xx/401/404 or network error → record failure, fail over to next candidate (non-streaming only)

### Routing Decision (`internal/service/loadbalancer.go` `Route()`)
1. **Direct** — if the client names a concrete configured model (technical or display name), route straight to it, skipping tier logic.
2. **Tier** — resolve via, in order: `X-VibeRouter-Tier` header → `auto-advanced`/`auto-basic` model alias → complexity rules → `routing.complexity.default_tier`.
3. **Long context** — if `est_input_tokens > routing.long_context_threshold`, keep only models with `long_context: true` and `max_context_tokens ≥ est`. If the tier has none, **escalate to the advanced tier's** long-context models; if still none → error.
4. **Priority** — drop circuit-open models, then order by `priority` ascending (strict failover). Same-priority models are tie-broken by `load_balance.strategy` (round_robin rotates a persistent counter; random shuffles).

Complexity rules (`routing.complexity.rules`) match on `message_turns`, `est_input_tokens`/`prompt_length`, `has_tools`, `has_code` with `gte`/`gt`/`eq`; any match ⇒ advanced.

### Key Components

**`internal/config/config.go`** — Loads/saves `config.yaml` via goccy/go-yaml. `GetConfig()` returns an immutable snapshot (RCU); `SaveConfig()` atomically writes and swaps. `EnsureDefaultAdmin()` seeds `admin/admin` (bcrypt) on first run. Legacy flat `backend_models` are auto-migrated into the `basic` tier. After save the admin handler calls `LoadBalancer.Reload()` for hot reload.

**`internal/model/models.go`** — Plain structs, **no GORM**: `BackendModel` (tier/key are runtime-only; carries provider, technical_name, base_url, api_key, priority, long_context, max_context_tokens, enabled), `CallLog` (one JSON Lines row, includes tier + is_long_context), plus `Provider`/`Tier`/`CircuitState` enums.

**`internal/service/loadbalancer.go`** — Singleton `LoadBalancer`. `Route()` (see above), `GetAllModels()`/`FindModel()` for listings, per-model `CircuitBreaker` keyed by stable `<tier>:<name>`. `RecordSuccess`/`RecordFailure` update breaker state.

**`internal/service/transformer.go`** — `RequestTransformer` converts OpenAI↔Anthropic: request (system, tools, tool_choice, stop), response (content blocks, stop_reason mapping, usage), and error format. Streaming conversion is chunk-by-chunk in the handlers.

**`internal/service/apistyle.go`** — Detects API style by URL path, headers (Authorization/x-api-key), and body fields.

**`internal/handler/`** — `openai_handler.go` & `anthropic_handler.go` proxy client requests through the failover list, handle streaming SSE, and format errors per client style. `admin.go` serves the management API (auth, models, routing, keys, logs).

**`internal/middleware/auth.go`** — API-key validation (client traffic) and admin session-cookie auth (web UI). Session tokens are crypto-random hex, held in an in-memory `sync.Map` with TTL from `admin.session.max_age_sec`.

**`internal/router/router.go`** — Gin setup: `/static` + `/` (SPA), `/health`, `/auth/*`, `/admin/*` (session-protected), `/v1/*` (API-key protected). `/v1/models` is public.

### Circuit Breaker
- State stored in-memory per model key (`<tier>:<name>`): `closed` → `open` (after `circuit_breaker.threshold` failures) → `half_open` (after `timeout_sec`) → `closed`/`open`.
- `failure_count` increments on 5xx or network error; success resets to `closed`.
- `Route()` excludes open models from the candidate pool (so failover skips them proactively).

### Frontend

Single-page Vue 3 + Tailwind app in `web/static/index.html`. Login → tabbed dashboard: **Models** (grouped by tier, with priority/long-context), **Routing** (complexity rules + long-context threshold), **API Keys**, **Logs** (filtered query over JSON Lines). Language: `navigator.language` starts with `zh` → Simplified Chinese, else English. Default login is `admin/admin`.

## Configuration (`config.yaml`)

See `config.yaml.example` for a full worked example. Top-level keys: `server`, `log` (incl. `file` → JSON Lines path), `circuit_breaker`, `retry`, `load_balance.strategy`, `routing` (complexity rules + `long_context_threshold` + override), `tiers` (`advanced`/`basic` → `models`), `admin` (session + bcrypt `users`), `api_keys`.

Web UI edits write back to this file. **Comments are not preserved** on save (the file is re-marshalled).

## Logging

`internal/service/filelogger.go` writes each call as one JSON Lines row to `log.file` (default `./logs/viberouter.jsonl`) asynchronously, with colored console output. `FileLogger.Query(LogFilter)` powers the web log view (newest-first, filterable by user/model/tier/style/status).

## Error Response Formatting

All errors (VibeRouter's own or backend's) are formatted to the client's API style:
- OpenAI: `{"error":{"type":"...","message":"..."}}`
- Anthropic: `{"type":"error","error":{"type":"...","message":"..."}}`

Handlers use `transformer.FormatError()` for VibeRouter errors, `transformer.DetectAndTransformError()` for backend errors.

## Notes

- **No database** — config in `config.yaml`, logs in JSON Lines. The `internal/model/models.go` structs carry no ORM tags.
- **Spec docs**: this CLAUDE.md plus `config.yaml.example` are the current spec. The old `SPEC.md` (pre-tier-era: flat `backend_models`, pipe-separated logs, no web UI) is **stale and archived** at `docs/archive/SPEC.md` — do not trust it. Module requires Go 1.25 (`go.mod`), not the 1.21 that spec claims.
- `server.address` accepts `:8080`, `127.0.0.1:8080`, `0.0.0.0:8080`.
- Client-facing virtual model names: `auto` (full routing), `auto-advanced` / `auto-basic` (lock tier), or any concrete configured model name (direct).
- Streaming requests are **not retried** (only non-streaming fails over across candidates).
- `opensource/` is an old self-contained snapshot of the codebase, not the live application — build/vet of the main app targets `.` and `./internal/...`.
