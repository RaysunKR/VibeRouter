# VibeRouter 规格说明书

## 1. 项目概述

- **项目名称**: VibeRouter
- **项目类型**: Go语言AI模型接口路由服务
- **核心功能**: 兼容OpenAI和Anthropic两种风格接口的AI模型统一路由网关，支持多模型负载均衡、协议转换、自动风格检测
- **目标用户**: 需要统一管理多个AI模型密钥的运维人员和企业

---

## 2. 技术栈

| 组件 | 技术选型 |
|------|----------|
| 语言 | Go 1.21+ |
| 框架 | Gin |
| 配置 | YAML (config.yaml) |
| 日志 | 文件 + 彩色控制台输出 |

**无数据库依赖** - 所有配置通过 `config.yaml` 管理

---

## 3. 功能特性

### 3.1 接口兼容层

#### OpenAI兼容接口
| 端点 | 方法 | 说明 |
|------|------|------|
| `/v1/chat/completions` | POST | ChatGPT聊天补全，**支持SSE流式响应** |
| `/v1/completions` | POST | 文本补全（legacy） |
| `/v1/embeddings` | POST | 向量嵌入 |
| `/v1/models` | GET | 模型列表（公开，无需认证） |
| `/v1/models/*` | GET | 特定模型信息（公开，无需认证） |

#### Anthropic兼容接口
| 端点 | 方法 | 说明 |
|------|------|------|
| `/v1/messages` | POST | Claude消息接口，**支持SSE流式响应** |
| `/v1/models` | GET | 模型列表（公开，无需认证） |
| `/v1/models/*` | GET | 特定模型信息（公开，无需认证） |

### 3.2 认证机制

**无数据库认证** - 使用 `config.yaml` 中配置的 API Keys

```yaml
api_keys:
  - key: sk-your-api-key-1
    username: user1
    is_active: true
  - key: sk-your-api-key-2
    username: user2
    is_active: true
```

| 认证方式 | 说明 |
|---------|------|
| `x-api-key` Header | Anthropic风格 |
| `Authorization: Bearer` | OpenAI风格 |

**公开端点（无需认证）：**
- `GET /v1/models`
- `GET /v1/models/*`

### 3.3 模型配置

```yaml
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

### 3.4 负载均衡与熔断

| 功能 | 说明 |
|------|------|
| 负载均衡策略 | 支持轮询（round_robin）、随机（random） |
| 快速失败重试 | 请求失败时自动切换模型重试（默认2次） |
| 熔断器 | 连续失败达到阈值（默认5次）后熔断，30秒后自动恢复 |

### 3.5 协议转换

当客户端API风格与后端Provider不匹配时，自动进行双向转换：

| 转换项 | OpenAI格式 | Anthropic格式 |
|--------|-----------|--------------|
| 认证头 | `Authorization: Bearer` | `x-api-key` |
| system消息 | `messages[0].role="system"` | `system` 参数 |
| 工具调用 | `tools: [...]` | `tools: [...]` |
| stop | `stop` 字段 | `stop_sequences` |

### 3.6 自动风格检测

`model: "auto"` 智能选择后端：

```json
{
  "model": "auto",
  "messages": [{"role": "user", "content": "Hello"}]
}
```

系统根据请求头和body内容自动检测API风格并选择对应后端。

### 3.7 日志系统

**彩色控制台输出：**
- 用户名（青色）
- Provider（绿色=OpenAI，黄色=Anthropic）
- 模型名（蓝色）
- 状态码（绿色=成功，黄色=客户端错误，红色=服务端错误）
- 延迟（绿色<1s，黄色<5s，红色>5s）

**文件日志：**
- 路径: `logs/api-YYYY-MM-DD.log`
- 格式: 管道分隔符，便于解析

---

## 4. 配置项

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| `server.address` | :8080 | 监听地址 |
| `server.mode` | release | Gin运行模式 |
| `log.retention_days` | 30 | 日志保留天数 |
| `circuit_breaker.threshold` | 5 | 熔断阈值 |
| `circuit_breaker.timeout_sec` | 30 | 熔断恢复时间 |
| `retry.max_attempts` | 2 | 最大重试次数 |

---

## 5. 项目结构

```
viberouter/
├── main.go
├── go.mod / go.sum
├── config.yaml           # 配置文件
├── logs/                 # 日志目录（自动创建）
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
└── viberouter.exe
```

---

## 6. API路由设计

```
公开端点（无需认证）:
  GET    /v1/models           - 模型列表
  GET    /v1/models/*        - 特定模型信息

认证端点:
  POST   /v1/chat/completions - OpenAI聊天补全
  POST   /v1/completions      - 文本补全
  POST   /v1/embeddings       - 向量嵌入
  POST   /v1/messages         - Anthropic消息
```

---

## 7. 错误响应格式

| 客户端风格 | 错误格式 |
|-----------|---------|
| OpenAI | `{"error":{"type":"...","message":"..."}}` |
| Anthropic | `{"type":"error","error":{"type":"...","message":"..."}}` |

---

## 8. 验收标准

- [x] 配置文件（config.yaml）管理所有设置，无数据库依赖
- [x] API Keys 配置用户名，日志记录正确的用户
- [x] 彩色控制台输出，提高日志可读性
- [x] `GET /v1/models` 公开无需认证
- [x] `GET /v1/models/*` 公开无需认证
- [x] OpenAI `/v1/chat/completions` 非流式请求正常转发
- [x] OpenAI `/v1/chat/completions` SSE流式请求正常转发
- [x] Anthropic `/v1/messages` 非流式请求正常转发
- [x] Anthropic `/v1/messages` SSE流式请求正常转发
- [x] `model: "auto"` 自动风格检测和后端选择
- [x] 客户端风格与后端Provider不匹配时自动协议转换
- [x] 错误响应格式遵循客户端API风格
- [x] 熔断器：模型连续失败后自动熔断和恢复
- [x] 调用日志正确记录（用户、模型、延迟、状态）
