# VibeRouter

A Go-based AI model API gateway that proxies and load-balances requests between OpenAI and Anthropic APIs with automatic protocol transformation.

## Features

- **Dual API Compatibility** - Supports both OpenAI and Anthropic API styles
- **Automatic Protocol Transformation** - Converts requests/responses when client style differs from backend provider
- **Load Balancing** - Round-robin and random strategies with automatic failover retry
- **Circuit Breaker** - Automatic model circuit breaking after consecutive failures
- **Streaming Support** - SSE streaming for both OpenAI and Anthropic endpoints
- **Smart Detection** - Auto-detects API style from headers and request body
- **Zero Database Dependency** - All configuration via `config.yaml`

## Quick Start

### Prerequisites

- Go 1.21+
- API keys for OpenAI and/or Anthropic

### Installation

```bash
git clone https://github.com/yourusername/viberouter.git
cd viberouter
go mod tidy
go build -o viberouter .
```

### Configuration

1. Copy the example config:
   ```bash
   cp config.yaml.example config.yaml
   ```

2. Edit `config.yaml` with your settings:
   ```yaml
   server:
     address: ":8080"
     mode: "release"

   api_keys:
     - key: sk-your-gateway-key
       username: your-username
       is_active: true

   backend_models:
     - provider: openai
       display_name: GPT-4
       technical_name: gpt-4
       base_url: https://api.openai.com/v1
       api_key: sk-your-openai-api-key
       is_active: true
     - provider: anthropic
       display_name: Claude-3-Sonnet
       technical_name: claude-3-sonnet-20240229
       base_url: https://api.anthropic.com
       api_key: sk-ant-your-anthropic-api-key
       is_active: true
   ```

### Run

```bash
./viberouter
```

## API Endpoints

### OpenAI Compatible

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/chat/completions` | POST | Chat completions (SSE streaming supported) |
| `/v1/completions` | POST | Text completions |
| `/v1/embeddings` | POST | Embeddings |
| `/v1/models` | GET | List models (public, no auth required) |
| `/v1/models/*` | GET | Get model info (public, no auth required) |

### Anthropic Compatible

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/messages` | POST | Messages API (SSE streaming supported) |
| `/v1/models` | GET | List models (public, no auth required) |

## Authentication

Two authentication methods are supported:

- **OpenAI Style**: `Authorization: Bearer sk-your-key`
- **Anthropic Style**: `x-api-key: sk-your-key`

## Auto Model Selection

Use `model: "auto"` to let the system intelligently select the backend:

```json
{
  "model": "auto",
  "messages": [{"role": "user", "content": "Hello"}]
}
```

## Protocol Transformation

When client API style differs from backend provider, automatic transformation occurs:

| Aspect | OpenAI | Anthropic |
|--------|--------|-----------|
| Auth Header | `Authorization: Bearer` | `x-api-key` |
| System Message | `messages[0].role="system"` | `system` parameter |
| Stop Sequences | `stop` field | `stop_sequences` |

## Configuration Reference

| Setting | Default | Description |
|---------|---------|-------------|
| `server.address` | `:8080` | Listen address |
| `server.mode` | `release` | Gin mode (debug/release) |
| `log.retention_days` | 30 | Log retention in days |
| `circuit_breaker.threshold` | 5 | Failures before circuit opens |
| `circuit_breaker.timeout_sec` | 30 | Recovery timeout |
| `retry.max_attempts` | 2 | Max retry attempts |

## Project Structure

```
viberouter/
├── main.go
├── go.mod / go.sum
├── config.yaml.example
├── internal/
│   ├── config/
│   ├── handler/
│   │   ├── anthropic_handler.go
│   │   └── openai_handler.go
│   ├── middleware/
│   │   └── auth.go
│   ├── model/
│   ├── router/
│   └── service/
│       ├── apistyle.go
│       ├── filelogger.go
│       ├── loadbalancer.go
│       └── transformer.go
└── test/
    └── sdk/
```

## License

MIT License
