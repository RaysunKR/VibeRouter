# VibeRouter

基于 Go 语言的 AI 模型 API 网关，支持 OpenAI 和 Anthropic API 的代理和负载均衡，并提供自动协议转换功能。

## 功能特性

- **双 API 兼容** - 同时支持 OpenAI 和 Anthropic 两种 API 风格
- **自动协议转换** - 当客户端风格与后端提供商不匹配时自动转换请求/响应格式
- **负载均衡** - 支持轮询和随机策略，自动故障重试
- **熔断器** - 连续失败后自动熔断，保护系统稳定性
- **流式支持** - 支持 OpenAI 和 Anthropic 端点的 SSE 流式响应
- **智能检测** - 根据请求头和请求体自动检测 API 风格
- **零数据库依赖** - 所有配置通过 `config.yaml` 管理

## 快速开始

### 环境要求

- Go 1.21+
- OpenAI 和/或 Anthropic 的 API 密钥

### 安装

```bash
git clone https://github.com/RaysunKR/viberouter.git
cd viberouter
go mod tidy
go build -o viberouter .
```

### 配置

1. 复制示例配置：
   ```bash
   cp config.yaml.example config.yaml
   ```

2. 编辑 `config.yaml`：
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

### 运行

```bash
./viberouter
```

## API 端点

### OpenAI 兼容接口

| 端点 | 方法 | 说明 |
|------|------|------|
| `/v1/chat/completions` | POST | 聊天补全（支持 SSE 流式） |
| `/v1/completions` | POST | 文本补全 |
| `/v1/embeddings` | POST | 向量嵌入 |
| `/v1/models` | GET | 模型列表（公开，无需认证） |
| `/v1/models/*` | GET | 模型详情（公开，无需认证） |

### Anthropic 兼容接口

| 端点 | 方法 | 说明 |
|------|------|------|
| `/v1/messages` | POST | 消息接口（支持 SSE 流式） |
| `/v1/models` | GET | 模型列表（公开，无需认证） |

## 认证方式

支持两种认证方式：

- **OpenAI 风格**: `Authorization: Bearer sk-your-key`
- **Anthropic 风格**: `x-api-key: sk-your-key`

## 自动模型选择

使用 `model: "auto"` 让系统智能选择后端：

```json
{
  "model": "auto",
  "messages": [{"role": "user", "content": "你好"}]
}
```

系统会根据请求头和内容自动检测 API 风格并选择对应的后端模型。

## 协议转换

当客户端 API 风格与后端提供商不匹配时，自动进行双向转换：

| 转换项 | OpenAI 格式 | Anthropic 格式 |
|--------|-------------|----------------|
| 认证头 | `Authorization: Bearer` | `x-api-key` |
| 系统消息 | `messages[0].role="system"` | `system` 参数 |
| 停止序列 | `stop` 字段 | `stop_sequences` |

## 负载均衡与熔断

| 功能 | 说明 |
|------|------|
| 负载均衡策略 | 轮询（round_robin）、随机（random） |
| 故障重试 | 请求失败时自动切换模型重试（默认 2 次） |
| 熔断器 | 连续失败达到阈值（默认 5 次）后熔断，30 秒后自动恢复 |

## 配置参考

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| `server.address` | `:8080` | 监听地址 |
| `server.mode` | `release` | Gin 运行模式（debug/release） |
| `log.retention_days` | 30 | 日志保留天数 |
| `circuit_breaker.threshold` | 5 | 熔断阈值 |
| `circuit_breaker.timeout_sec` | 30 | 熔断恢复时间 |
| `retry.max_attempts` | 2 | 最大重试次数 |

## 项目结构

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

## 日志系统

**彩色控制台输出：**
- 用户名（青色）
- Provider（绿色=OpenAI，黄色=Anthropic）
- 模型名（蓝色）
- 状态码（绿色=成功，黄色=客户端错误，红色=服务端错误）
- 延迟（绿色<1s，黄色<5s，红色>5s）

**文件日志：**
- 路径: `logs/api-YYYY-MM-DD.log`
- 格式: 管道分隔符，便于解析

## 错误响应格式

错误响应会根据客户端 API 风格自动格式化：

| 客户端风格 | 错误格式 |
|-----------|---------|
| OpenAI | `{"error":{"type":"...","message":"..."}}` |
| Anthropic | `{"type":"error","error":{"type":"...","message":"..."}}` |

## 许可证

MIT License
