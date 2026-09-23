# VibeRouter

[English](README.md) | [简体中文](README.zh-CN.md)

<p>
  <strong>A self-hosted AI API gateway: one address, three protocols, smart routing across all your upstream models.</strong>
</p>

VibeRouter is written in Go and exposes a single adaptive API endpoint — no matter which SDK style your client uses (OpenAI Chat Completions, OpenAI Responses, or Anthropic Messages), they all connect to the same address. VibeRouter auto-detects the protocol, picks an upstream model according to your routing policy, performs bidirectional protocol conversion when necessary (streaming included), and supports failover, balance awareness, and complexity-based dispatching.

- **Single-binary delivery**: the admin frontend (React + shadcn/ui) is embedded via `go:embed` — no Node, no database, no separate frontend service
- **No database**: all configuration lives in `config.yaml`, hot-reloaded on every change made in the admin UI; admin credentials live in `.env`

---

## Table of Contents

- [Features](#features)
- [Quick Start (5 minutes)](#quick-start-5-minutes)
- [Deployment](#deployment)
  - [Option 1: Run the binary directly](#option-1-run-the-binary-directly)
  - [Option 2: Linux systemd service](#option-2-linux-systemd-service)
  - [Option 3: Build from source (frontend included)](#option-3-build-from-source-frontend-included)
  - [Firewall and ports](#firewall-and-ports)
  - [Upgrade and backup](#upgrade-and-backup)
- [Configuration reference (config.yaml)](#configuration-reference-configyaml)
- [Admin UI guide](#admin-ui-guide)
- [Client integration examples](#client-integration-examples)
- [Routing behavior in depth](#routing-behavior-in-depth)
- [Logging](#logging)
- [Balance / quota queries](#balance--quota-queries)
- [FAQ](#faq)
- [Testing](#testing)

---

## Features

| Capability | Description |
|---|---|
| Adaptive 3-protocol entry | One service auto-detects `POST /v1/chat/completions`, `POST /v1/responses`, and `POST /v1/messages` |
| Automatic protocol conversion | When the entry protocol differs from the upstream protocol, requests are converted bidirectionally through a canonical intermediate representation, **including streaming SSE**; same-protocol traffic passes through with zero overhead |
| Direct mode | Clients use a model's public name directly |
| Load balancing | Groups + priority-based failover (unreachable / timeout / 5xx / 429 / 402 insufficient balance / 403 auto-switch), round-robin or random within the same priority |
| Smart load balancing | A dispatcher model first classifies complexity → routes to the advanced or basic tier, then applies priority failover within the tier; dispatcher failure degrades to the basic tier |
| Capability-aware routing | Multimodal filtering and long-context filtering (configurable threshold); auto-escalates to the advanced tier when the basic tier lacks a capable model |
| Balance / quota queries | Built-in probes for DeepSeek, SiliconFlow, OpenRouter, Novita, StepFun, Zhipu, MiniMax, and Kimi Coding |
| Multi-key management | `vr-`-prefixed access keys with full CRUD and enable/disable |
| Access logging | JSON Lines files rotated daily, 7-day retention by default (configurable); filterable by key / model / status code / time in the admin UI |
| Admin UI | shadcn/ui-based interface in Chinese and English (follows the browser language, manually switchable with the choice remembered), embedded in and served by the Go binary |

25 provider presets ship built-in (DeepSeek, Zhipu, MiniMax, Kimi, SiliconFlow, OpenRouter, Volcano Ark, Alibaba Bailian, ModelScope, PPIO, etc., sourced from cc-switch), pre-fillable with one click in the admin UI.

---

## Quick Start (5 minutes)

> Want to pre-configure things? Copy `.env.example` → `.env` and `config.example.yaml` → `config.yaml` from the repository root and edit per the inline comments. You can also skip this — sensible defaults are generated on first launch.

1. Start the service (binary `viberouter`, or `viberouter.exe` on Windows):

   ```bash
   ./viberouter          # reads config.yaml and .env from the working directory by default
   ```

   On first launch it will:
   - generate a default `config.yaml`
   - seed an **admin / admin** account into `.env` (the log will tell you; change it soon after login)

2. Open the admin UI at `http://localhost:8080` and log in with `admin / admin`.

3. Add a provider: Providers page → Add provider → pick a preset (e.g. DeepSeek) → fill in the API key → save.

4. Add a model: Models page → Add model, for example:
   - Route name `ds-flash` (this is the name clients use)
   - Provider `deepseek`, upstream model `deepseek-v4-flash`
   - Tick Multimodal / Long context as needed and set the context limit

5. Create an access key: Keys page → Create key, and you get a `vr-xxxxxxxx`.

6. Connect with any SDK (OpenAI SDK as an example):

   ```python
   from openai import OpenAI
   client = OpenAI(base_url="http://localhost:8080/v1", api_key="vr-xxxxxxxx")
   print(client.chat.completions.create(
       model="ds-flash",
       messages=[{"role": "user", "content": "Hello"}],
   ).choices[0].message.content)
   ```

---

## Deployment

### Option 1: Run the binary directly

VibeRouter is a **single-file binary** with the frontend embedded and no other runtime dependencies.

**Windows:**

```powershell
mkdir D:\apps\viberouter
cd D:\apps\viberouter
# place viberouter.exe here
.\viberouter.exe
```

**Linux:**

```bash
sudo mkdir -p /opt/viberouter
sudo cp viberouter-linux-amd64 /opt/viberouter/viberouter
sudo chmod +x /opt/viberouter/viberouter
cd /opt/viberouter && sudo ./viberouter
```

Launch flags (all have defaults; usually unnecessary):

| Flag | Default | Description |
|---|---|---|
| `-config` | `config.yaml` | Path to the config file |
| `-env` | `.env` | Path to the admin credentials file |

> Always launch from a fixed working directory: `config.yaml`, `.env`, and `logs/` are resolved relative to the **current working directory**.

### Option 2: Linux systemd service

Create `/etc/systemd/system/viberouter.service`:

```ini
[Unit]
Description=VibeRouter AI API Gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/opt/viberouter
ExecStart=/opt/viberouter/viberouter
Restart=on-failure
RestartSec=3
# To run as non-root, create the user first and grant the directory:
# User=viberouter
# Group=viberouter

[Install]
WantedBy=multi-user.target
```

Enable:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now viberouter
sudo systemctl status viberouter
journalctl -u viberouter -f      # follow the logs
```

### Option 3: Build from source (frontend included)

Requires Go ≥ 1.26 and Node.js ≥ 20 (+ pnpm).

```bash
git clone <repo> viberouter && cd viberouter

# 1. Build the frontend (output goes to internal/server/web, embedded via go:embed)
cd web
pnpm install
pnpm build
cd ..

# 2. Build the backend (frontend is embedded automatically)
go build -o viberouter .

# 3. Cross-compile a Linux build (from Windows/macOS)
GOOS=linux GOARCH=amd64 go build -o viberouter-linux-amd64 .
```

You can also just run `make build` (equivalent to steps 1+2 above).

> Note: build the frontend before the backend, otherwise the binary embeds a placeholder page.

### Firewall and ports

The default listener is `:8080`; change `server.addr` in `config.yaml` (restart required).

Linux with firewalld:

```bash
sudo firewall-cmd --add-port=8080/tcp
# add --permanent && sudo firewall-cmd --reload to persist across reboots
```

With ufw:

```bash
sudo ufw allow 8080/tcp
```

**Production advice**: do not expose port 8080 directly to the public internet. Put Nginx/Caddy in front with HTTPS:

```nginx
server {
    listen 443 ssl;
    server_name ai.example.com;
    # ssl_certificate / ssl_certificate_key omitted

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        # required for streaming SSE
        proxy_buffering off;
        proxy_cache off;
        proxy_set_header Connection "";
        proxy_read_timeout 600s;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
    }
}
```

### Upgrade and backup

- **Upgrade**: stop the service → replace the binary → start. `config.yaml` / `.env` / `logs/` are fully compatible and kept as-is.
- **Backup**: back up just `config.yaml` and `.env` from the working directory — they contain all providers, models, groups, and keys.

### Change the admin password

`ADMIN_PASSWORD_HASH` in `.env` is a bcrypt hash. Generate a new one:

```bash
# on any machine with Python
python -c "import bcrypt; print(bcrypt.hashpw(b'your-new-password', bcrypt.gensalt()).decode())"
# or with Go: a small go run golang.org/x/crypto/bcrypt helper
```

Replace `ADMIN_PASSWORD_HASH` in `.env` and restart the service.

---

## Configuration reference (config.yaml)

Every change made in the admin UI is written back to this file and hot-applied; manual editing works too (routing/model config is re-read per request; `server.addr` and the log file base name require a restart).

```yaml
server:
  addr: ":8080"                    # listen address
  disable_captcha: false           # turn off the login captcha (tests / trusted networks only)

routing:
  long_context_threshold: 200000   # estimated input tokens above this → only long-context models
  dispatcher_timeout_sec: 5        # dispatcher model timeout (seconds); timeout/failure degrades to the basic tier
  strategy: round_robin            # same-priority strategy: round_robin | random

providers:                         # providers
  - id: deepseek                   # unique ID
    name: DeepSeek
    protocol: chat_completions     # chat_completions | responses | anthropic
    base_url: https://api.deepseek.com
    api_key: sk-xxxx               # provider-level default key (models may override)
    balance:
      type: deepseek               # balance probe type (see "Balance / quota queries")

models:                            # models
  - name: ds-flash                 # public route name (clients use this)
    provider: deepseek
    upstream_model: deepseek-v4-flash   # real upstream model name
    # api_key / base_url / protocol  # optional: override provider defaults
    multimodal: false              # supports image input
    long_context: false            # supports very long contexts
    max_context_tokens: 128000

groups:                            # groups (group name = public model name)
  - name: lb                       # load-balancing group
    mode: balance
    strategy: round_robin          # optional: override the global strategy
    members:
      - {model: ds-flash, priority: 1}   # lower number = tried first; unreachable/insufficient balance → next member
      - {model: ds-pro,   priority: 2}

  - name: auto                     # smart load-balancing group
    mode: smart
    dispatcher_model: ds-flash     # dispatcher model (a cheap, fast one is recommended)
    # dispatcher_prompt: |         # optional: custom complexity-classification prompt
    #   ...
    advanced:                      # advanced tier
      - {model: ds-pro, priority: 1}
    basic:                         # basic tier
      - {model: ds-flash, priority: 1}

keys:                              # access keys (best managed in the admin UI)
  - {id: k1, key: vr-xxxx, name: my-client, enabled: true, created_at: "..."}

logs:
  file: logs/router.log            # log base name (actual file: router-2026-08-15.log)
  retention_days: 7                # days to keep logs, default 7
```

`.env` (admin credentials — never commit this file):

```ini
ADMIN_USERNAME=admin
ADMIN_PASSWORD_HASH=<bcrypt hash>
ADMIN_SESSION_SECRET=<random hex; old sessions are invalidated when it changes>
```

---

## Admin UI guide

| Page | Features |
|---|---|
| Providers | One-click preset pre-fill; edit/delete; **Check balance** button (cash balance or quota percentage) |
| Models | Full-field CRUD; multimodal / long-context toggle badges |
| Group routing | Balance groups: members + priorities; smart groups: dispatcher model, **editable dispatcher prompt** (with a restore-default button), advanced/basic sub-groups |
| Keys | Create (`vr-` prefix auto-generated), copy, rename, enable/disable, delete |
| Logs | Filter by status code / model / **key name** / count; expand each row for error details, provider, and failover count |
| Settings | Long-context threshold, dispatcher timeout, default strategy, **log retention days**, log file base name — hot-applied on save |

---

## Client integration examples

All three SDKs connect to the **same address**; the router auto-detects the protocol. Use a model name or a group name as the model.

**OpenAI Chat Completions:**

```python
from openai import OpenAI
client = OpenAI(base_url="http://<host>:8080/v1", api_key="vr-xxxx")
client.chat.completions.create(model="auto", messages=[...], stream=True)
```

**OpenAI Responses:**

```python
client.responses.create(model="auto", input="Hello", stream=True)
```

**Anthropic:**

```python
from anthropic import Anthropic
client = Anthropic(base_url="http://<host>:8080", api_key="vr-xxxx")
client.messages.create(model="auto", max_tokens=1024, messages=[...])
```

**Claude Code / other CLIs**: point `ANTHROPIC_BASE_URL` at `http://<host>:8080` and set `ANTHROPIC_AUTH_TOKEN` to `vr-xxxx`.

**Force a smart-group tier** (skips the dispatcher model):

```
X-VibeRouter-Tier: advanced    # or basic
```

---

## Routing behavior in depth

**Decision order** (common to all three modes):

1. Model name resolution: match a model first, then a group
2. Modality filter: request contains images → only models with `multimodal: true`
3. Context filter: estimated tokens (text ≈ chars/4, images counted as 1000) above the threshold → only models with `long_context: true`
4. Smart groups add: the dispatcher model classifies complexity into a tier; if the chosen tier has no capability match, **auto-escalate to the advanced tier**
5. Within the group, members are sorted by priority; same priority is round-robin/random per strategy

**Failover triggers** (transparent to the client; the next candidate is tried automatically):

- Network error / connection timeout
- HTTP 5xx, 429 (rate limit), 402 (insufficient balance, e.g. DeepSeek out of credit), 403 (quota/permission)

4xx client errors (e.g. 400 bad request) are not failed over; the response is relayed verbatim.

**Smart dispatching details**:

- Only the text of the last user message (truncated to 4000 chars) is sent to the dispatcher — no history, no images, `temperature=0`
- Dispatcher timeout is 5s (configurable); failure/unparseable output → **degrade to the basic tier**, with the reason logged
- Pick a cheap, fast dispatcher model; reasoning models (e.g. deepseek-v4) also work (a sufficient token budget is reserved internally)

---

## Logging

- Storage: `logs/router-YYYY-MM-DD.log`, rotated daily, JSON Lines format
- Retention: 7 days by default, adjustable in Settings; expired files are deleted automatically
- Fields per entry: time, **key name**, client protocol, requested model, routed model, upstream model, provider, group/tier, streaming flag, status code, latency, input/output tokens, failover count, error details, dispatcher degradation reason

Analyze logs directly from the command line:

```bash
# today's traffic for a given key
grep '"key_name":"my-client"' logs/router-$(date +%F).log | jq .
```

---

## Balance / quota queries

Click "Check balance" on the Providers page. Different providers return a cash balance or a quota percentage:

| Probe type | Provider | Returns |
|---|---|---|
| `deepseek` | DeepSeek | Cash balance (CNY) |
| `siliconflow` | SiliconFlow CN/global (currency auto-detected) | Cash balance |
| `openrouter` | OpenRouter | Remaining credits (USD) |
| `novita` | Novita AI | Available balance (USD) |
| `stepfun` | StepFun | Cash balance (CNY) |
| `zhipu` | Zhipu GLM (bigmodel.cn / z.ai) | Token quota remaining % |
| `kimi_coding` | Kimi For Coding | Weekly quota remaining % |
| `minimax` | MiniMax CN/global | Coding Plan remaining % |

If a custom provider is API-compatible with any of the above, choose the matching `balance.type` when creating it to reuse the probe.

---

## FAQ

**Q: Port already in use on startup?**
A: Change `server.addr` in `config.yaml` (e.g. `":18080"`) and restart.

**Q: Do config changes in the admin UI require a restart?**
A: No — everything hot-applies. Only manual edits to `server.addr` or the log file base name require a restart.

**Q: Smart dispatching always lands in the basic tier?**
A: Open the entry in the Logs page and check `dispatcher_error`: common causes are a reasoning-model dispatcher timing out or returning empty (a 1024-token budget is reserved internally and usually suffices), or the dispatcher model itself being unavailable (invalid key, etc.). The `X-VibeRouter-Tier` header can force a tier to help debug.

**Q: Multimodal request fails with "does not support multimodal input"?**
A: No model in the target model/group has `multimodal: true`. Tick multimodal on a model that supports images.

**Q: Is upstream `reasoning_content` (reasoning trace) preserved?**
A: Preserved verbatim in same-protocol pass-through; cross-protocol conversion keeps only the final answer (content) — the reasoning trace does not survive conversion.

**Q: Is streaming supported?**
A: Yes, for all three protocols, including cross-protocol streaming conversion (e.g. Anthropic SDK `messages.stream()` against a chat_completions upstream).

**Q: How do I change / reset the admin password?**
A: Delete the `ADMIN_PASSWORD_HASH` line in `.env` and restart → admin/admin is regenerated (noted in the log); log in and change it as described above.

---

## Testing

```bash
go vet ./... && go test ./...                 # unit + integration tests (protocol conversion / routing matrix / admin flows)

# Mock upstreams for all three protocols + official-SDK compatibility suite (39 cases)
go build -o mockupstream ./cmd/mockupstream
python tests/e2e/run_tests.py ./viberouter ./mockupstream

# Linux two-host cross-network acceptance (requires host and credential setup)
python tests/e2e/deploy_linux.py
```

## License

MIT
