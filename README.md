# VibeRouter

> 兼容 OpenAI 与 Anthropic 双协议的 AI 模型路由网关 —— 分层路由、长上下文感知、协议自动转换、可登录 Web 管理后台，全程无数据库。

VibeRouter 把多个后端大模型（OpenAI / Anthropic 风格均可）统一成一套入口，按**任务复杂度**与**上下文长度**自动路由到「高级 / 普通」分层，并在分层内按**优先级**做严格故障转移。客户端用 OpenAI 或 Anthropic 任一风格的 SDK 都能访问，后端 provider 与客户端风格不一致时自动转换协议。

---

## 目录

- [特性一览](#特性一览)
- [快速开始](#快速开始)
- [配置文件](#配置文件)
- [路由机制](#路由机制)
- [API 接口](#api-接口)
- [认证](#认证)
- [Web 管理后台](#web-管理后台)
- [协议转换](#协议转换)
- [熔断与重试](#熔断与重试)
- [日志](#日志)
- [测试](#测试)
- [跨平台编译](#跨平台编译)
- [项目结构](#项目结构)
- [相关文档](#相关文档)

---

## 特性一览

- **双协议兼容**：入口 / 出口同时兼容 OpenAI 与 Anthropic 两种 API 风格，自动协议转换（system / tools / tool_choice / stop / 流式 SSE / 响应与错误结构）
- **分层路由**：后端模型分「高级（advanced）/ 普通（basic）」两层，按任务复杂度自动选择
- **长上下文感知**：模型可标记是否支持长上下文及容量上限，超阈值请求自动路由到长上下文模型，分层内无可用时升级到高级层
- **优先级故障转移**：分层内多模型按优先级严格降级，主模型熔断 / 失败才用下一级；同优先级间走轮询 / 加权 / 随机
- **熔断器**：连续失败自动隔离，超时半开探测恢复
- **可登录 Web 后台**：配置模型 / 分层 / 路由规则 / API Key、查询调用日志，中英双语
- **配置文件存储**：所有数据存 `config.yaml`，Web 改动写回并热加载；**无数据库**
- **JSON Lines 日志**：每条调用一行 JSON，Web 端可筛选查询
- **自动风格检测**：`model: "auto"` 自动识别客户端 API 风格并路由

---

## 快速开始

### 1. 编译

```bash
# Windows
go build -o viberouter.exe .

# 或直接运行
go run .
```

> 需要预编译的 Linux 二进制？见 [跨平台编译](#跨平台编译)。

### 2. 运行

```bash
./viberouter.exe
```

首次运行会自动：
- 在可执行文件同目录读取 / 创建 `config.yaml`
- 把旧版扁平 `backend_models`（若存在）迁移进 `basic` 分层
- 种子一个默认管理员账号 **`admin / admin`**（bcrypt，请尽快在 Web 后台修改）

启动后：
- 代理服务监听 `server.address`（默认 `:8080`）
- Web 后台：`http://localhost:8080/`（用 `admin / admin` 登录）
- 健康检查：`GET /health`

### 3. 最小配置示例

在可执行文件同目录放一个 `config.yaml`：

```yaml
server:
  address: :8080
  mode: release

tiers:
  basic:
    models:
      - name: gpt-mini
        provider: openai
        technical_name: gpt-4o-mini
        base_url: https://api.openai.com/v1
        api_key: sk-xxx
        priority: 1
        enabled: true
  advanced:
    models:
      - name: gpt-pro
        provider: openai
        technical_name: gpt-4o
        base_url: https://api.openai.com/v1
        api_key: sk-xxx
        priority: 1
        long_context: true
        max_context_tokens: 128000
        enabled: true

api_keys:
  - key: sk-your-client-key
    username: alice
    is_active: true
```

### 4. 发起一次调用

```bash
# OpenAI 风格客户端
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-your-client-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"auto","messages":[{"role":"user","content":"hi"}]}'
```

---

## 配置文件

完整字段如下（带默认值与说明）：

```yaml
server:
  address: :8080          # 监听地址，支持 :8080 / 127.0.0.1:8080 / 0.0.0.0:8080
  mode: release           # gin 模式：release / debug

log:
  retention_days: 30      # 日志保留天数
  file: ./logs/viberouter.jsonl   # JSON Lines 日志路径

circuit_breaker:
  threshold: 5            # 连续失败几次后熔断
  timeout_sec: 30         # 熔断多久后半开探测

retry:
  max_attempts: 2         # 非流式最大重试（跨候选模型故障转移）
  timeout_ms: 30000       # 上游请求超时

load_balance:
  strategy: round_robin   # 同优先级模型间策略：round_robin / weighted_round_robin / random

routing:
  complexity:
    default_tier: basic           # 无法判定复杂度时的兜底分层
    rules:                        # 满足任一即判为复杂任务 → advanced
      - { field: message_turns,    op: gte, value: 8 }
      - { field: est_input_tokens, op: gte, value: 8000 }
      - { field: has_tools,        op: eq,  value: 1 }
      - { field: has_code,         op: eq,  value: 1 }
  long_context_threshold: 32000    # 输入 token 超过此值 → 需要长上下文模型
  override:
    model_alias:                   # model 字段别名 → 分层（客户端强制指定）
      auto-advanced: advanced
      auto-basic: basic
    header: "X-VibeRouter-Tier"    # 也可用请求头直接指定 advanced/basic

tiers:
  advanced:                        # 高级模型（复杂任务）
    models:
      - name: claude-opus          # 显示名（分层内唯一）
        provider: anthropic        # openai / anthropic
        technical_name: claude-opus-4-xxx   # 上游真实模型名
        base_url: https://api.anthropic.com
        api_key: sk-ant-xxx
        priority: 1                # 数值越小越优先（严格故障转移）
        long_context: true         # 是否支持长上下文
        max_context_tokens: 200000 # 上下文容量上限
        enabled: true
      - name: gpt-4o-pro
        provider: openai
        technical_name: gpt-4o
        base_url: https://api.openai.com/v1
        api_key: sk-xxx
        priority: 2                # 主模型熔断 / 失败时降级到这里
        long_context: true
        max_context_tokens: 128000
        enabled: true
  basic:                           # 普通模型（普通任务）
    models:
      - name: claude-haiku
        provider: anthropic
        technical_name: claude-haiku-4-xxx
        base_url: https://api.anthropic.com
        api_key: sk-ant-xxx
        priority: 1
        enabled: true

admin:                             # Web 后台
  session:
    secret: change-me              # 会话密钥（占位）
    max_age_sec: 86400             # 会话有效期
  users:
    - username: admin
      password_hash: "$2a$..."     # bcrypt 哈希（首次运行自动种子 admin/admin）
      role: super_admin

api_keys:                          # 客户端访问代理用的密钥
  - key: sk-your-client-key
    username: alice                # 用于日志归属
    is_active: true
```

> **注意**：通过 Web 后台保存配置时，`config.yaml` 会被重新序列化，**注释不会保留**。

### 复杂度规则字段

| `field` | 含义 | 单位 |
|---|---|---|
| `message_turns` | 对话消息轮数 | 条 |
| `est_input_tokens` | 输入 token 估算（≈ body 长度 / 4） | token |
| `prompt_length` | 同上 | token |
| `has_tools` | 是否含工具调用 | 0 / 1 |
| `has_code` | 是否含代码块（\`\`\`） | 0 / 1 |

`op` 支持 `gte` / `gt` / `eq`。满足**任一**规则即判为复杂任务。

---

## 路由机制

每个请求经四步漏斗确定目标模型：

```
① 直连       客户端指定具体模型名（在配置中存在）→ 直连该模型，跳过路由
② 复杂度→分层  X-VibeRouter-Tier 头 > auto-advanced/auto-basic 别名 > 复杂度规则 > default_tier
③ 长上下文    估算输入 > long_context_threshold → 只留 long_context 且容量足够的模型
              分层内没有 → 升级到 advanced 的长上下文模型 → 仍没有 → 报错
④ 优先级      剔除熔断中的模型 → 按 priority 升序排序（严格故障转移）
              同优先级多个模型 → 按 load_balance.strategy 选择
```

### 客户端如何控制路由

| `model` 字段 | 行为 |
|---|---|
| `auto` | 自动检测 API 风格 + 完整路由（复杂度 / 长上下文 / 优先级） |
| `auto-advanced` | 锁定高级分层，仍走长上下文过滤与优先级故障转移 |
| `auto-basic` | 锁定普通分层 |
| 具体模型名（如 `gpt-4o`） | 直连该模型，跳过分层 |

也可在任意请求上加请求头 `X-VibeRouter-Tier: advanced|basic` 强制分层（优先级最高）。

### 严格故障转移示例

advanced 分层：`claude-opus` (priority 1) → `gpt-4o-pro` (priority 2)

- 正常：始终用 `claude-opus`
- `claude-opus` 熔断或返回 5xx/超时：降级到 `gpt-4o-pro`
- `claude-opus` 恢复（熔断器半开探测成功）：回到 `claude-opus`

---

## API 接口

### 代理端点（客户端调用，需 API Key）

**OpenAI 兼容**

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/v1/chat/completions` | 聊天补全（支持 SSE 流式） |
| POST | `/v1/completions` | 文本补全（legacy） |
| POST | `/v1/embeddings` | 向量嵌入 |
| GET | `/v1/models` | 模型列表（**公开**） |
| GET | `/v1/models/:model` | 模型信息（**公开**） |

**Anthropic 兼容**

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/v1/messages` | 消息接口（支持 SSE 流式） |
| GET | `/v1/models` | 模型列表（**公开**） |
| GET | `/v1/models/:model` | 模型信息（**公开**） |

### 管理端点（Web 后台使用，需管理员会话）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/auth/login` | 登录（用户名 + 密码） |
| POST | `/auth/logout` | 登出 |
| GET | `/auth/me` | 当前登录用户 |
| GET | `/admin/config` | 完整配置概览 |
| GET / POST / PUT / DELETE | `/admin/models` | 后端模型增删改查 |
| GET / PUT | `/admin/routing` | 路由规则读写 |
| GET / POST / PUT / DELETE | `/admin/keys` | API Key 管理 |
| GET | `/admin/logs` | 调用日志查询（支持筛选） |

---

## 认证

### 客户端 → 代理（API Key）

从 `config.yaml` 的 `api_keys` 配置，两种头部任选：

```
Authorization: Bearer sk-your-client-key     # OpenAI 风格
x-api-key: sk-your-client-key                # Anthropic 风格
```

`/v1/models` 与 `/v1/models/*` 为公开端点，无需认证。

### 管理员 → Web 后台（会话）

- 账号存 `config.yaml` 的 `admin.users`，密码 bcrypt 哈希
- 首次运行自动种子 `admin / admin`
- 登录后下发 `viberouter_session` Cookie（HttpOnly），后续管理请求凭 Cookie鉴权

---

## Web 管理后台

浏览器打开 `http://localhost:8080/`，默认 `admin / admin` 登录。四个标签页：

- **后端模型**：按高级 / 普通分组展示，可视化增删改、设置分层归属、优先级、长上下文、provider、密钥
- **路由规则**：调整复杂度判定阈值、默认分层、长上下文阈值、覆盖请求头
- **API 密钥**：管理客户端访问密钥与归属用户名
- **调用日志**：按用户 / 模型 / 分层 / 风格 / 状态码筛选，查看耗时与路径

语言：按 `navigator.language` 自动选择中文 / 英文。

所有改动写回 `config.yaml` 并热加载，无需重启。

---

## 协议转换

客户端风格与后端 provider 不一致时自动双向转换：

| 项目 | OpenAI | Anthropic |
|---|---|---|
| 认证头 | `Authorization: Bearer` | `x-api-key` |
| system 消息 | `messages[0].role=system` | `system` 参数 |
| 工具调用 | `tools` / `tool_choice` | `tools` / `tool_choice`（格式不同） |
| 停止序列 | `stop` | `stop_sequences` |
| 响应内容 | `choices[0].message.content`（字符串） | `content: [{type:text,...}]`（数组） |
| 停止原因 | `finish_reason: stop/length/...` | `stop_reason: end_turn/max_tokens/tool_use` |
| 错误结构 | `{"error":{"type","message"}}` | `{"type":"error","error":{...}}` |

流式响应逐 SSE 事件转换。

---

## 熔断与重试

- **熔断器**按模型（`<tier>:<name>`）记录状态：`closed → open`（连续失败达 `threshold` 次）→ `half_open`（`timeout_sec` 后探测）→ `closed`/`open`
- **故障转移**：非流式请求在候选模型间按优先级依次尝试，5xx / 401 / 404 / 网络错误触发降级
- **流式请求不重试**（一旦开始流式输出即锁定该模型）

---

## 日志

- **控制台**：彩色实时输出（用户名 / provider / 模型 / 状态码 / 延迟 / 分层 / 长上下文标记按颜色区分）
- **文件**：JSON Lines，每行一条调用记录，路径 `log.file`（默认 `./logs/viberouter.jsonl`），按 `log.retention_days` 滚动清理

每条记录包含：`timestamp / username / client_ip / provider / model_name / model_display_name / tier / is_long_context / api_style / request_path / method / status_code / latency_ms / error_message`

Web 后台的「调用日志」页即基于该文件筛选查询。

---

## 测试

### 路由单元测试（快，无网络）

```bash
go test ./internal/service/
# 单个用例
go test ./internal/service/ -run TestRoute_LongContextFilter -v
```

覆盖：复杂度判定、分层覆盖（头 / 别名）、长上下文过滤与升级、无可用模型、直连模型、优先级排序、熔断跳过、空分层报错。

### SDK 集成测试（需运行中的服务 + 可达后端）

```bash
./viberouter.exe &          # 启动服务
go test ./test/sdk/         # 用官方 openai-go 与 anthropic-sdk-go 端到端测试
```

覆盖：流式 / 非流式、system / temperature、虚拟模型列表、`auto-basic` 别名、`X-VibeRouter-Tier` 覆盖、Anthropic↔OpenAI 跨协议转换一致性、长上下文探测、工具调用。

---

## 跨平台编译

纯 Go 实现（`CGO_ENABLED=0`），可交叉编译为全静态二进制，扔到目标机器直接运行，无外部依赖。

> 提供 [Makefile](Makefile)：`make build` / `make build-linux` / `make build-linux-arm64` / `make build-windows` / `make build-darwin` / `make build-all` / `make test` / `make run` 等（Windows 需先安装 make，Linux/macOS 原生可用）。

```bash
# Windows
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o viberouter-windows-amd64.exe .

# Linux x86_64
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o viberouter-linux-amd64 .

# Linux ARM64（如 ARM 服务器 / 树莓派）
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o viberouter-linux-arm64 .

# macOS
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o viberouter-darwin-arm64 .
```

部署：把二进制 + `config.yaml`（首次运行会自动生成）放到同一目录，`./viberouter` 即可。日志目录与 `config.yaml` 默认在可执行文件同目录下。

---

## 项目结构

```
viberouter/
├── main.go                     # 入口：加载配置 / 初始化 / 启动
├── config.yaml                 # 配置（运行时生成 / Web 后台写回）
├── internal/
│   ├── config/config.go        # 配置加载 / 保存 / 热加载 / 默认管理员种子
│   ├── model/models.go         # 数据结构（BackendModel / CallLog，无 ORM）
│   ├── service/
│   │   ├── loadbalancer.go     # 路由核心：分层 / 复杂度 / 长上下文 / 优先级 / 熔断
│   │   ├── transformer.go      # OpenAI ↔ Anthropic 协议转换
│   │   ├── apistyle.go         # API 风格检测
│   │   ├── filelogger.go       # JSON Lines 日志 + Web 查询
│   │   └── admin_service.go    # 调用日志服务
│   ├── handler/
│   │   ├── openai_handler.go   # /v1/chat/completions 等代理 + 流式
│   │   ├── anthropic_handler.go# /v1/messages 代理 + 流式
│   │   └── admin.go            # Web 管理 API
│   ├── middleware/auth.go      # API Key 鉴权 + 管理员会话鉴权
│   └── router/router.go        # Gin 路由装配
├── web/static/index.html       # Vue 3 单页管理后台
├── test/sdk/                   # 官方 SDK 集成测试
└── logs/                       # JSON Lines 日志（运行时生成）
```

---

## 相关文档

- [需求.md](需求.md) —— 当前需求规格（权威）
- [CLAUDE.md](CLAUDE.md) —— 给 AI 编程助手的架构指引
- ~~[docs/archive/SPEC.md](docs/archive/SPEC.md)~~ —— 旧版规格（分层路由之前的扁平模型时代，**已归档，勿信**）
