# VibeRouter

[English](README.md) | [简体中文](README.zh-CN.md)

<p>
  <strong>一个自托管的 AI API 网关：一个地址，三种协议，智能调度所有上游模型。</strong>
</p>

VibeRouter 用 Golang 实现，对外暴露统一的自适应 API 端点——客户端无论使用 OpenAI Chat Completions、OpenAI Responses 还是 Anthropic Messages 哪种 SDK 风格，都连接同一个地址；VibeRouter 自动识别协议、按路由策略选中上游模型、必要时完成协议双向转换（含流式），并支持故障转移、余额感知与复杂度调度。

- **单二进制交付**：管理前端（React + shadcn/ui）通过 `go:embed` 内嵌，无需 Node、无需数据库、无需单独起前端服务
- **无数据库**：全部配置存于 `config.yaml`，管理端修改热生效；管理员凭证在 `.env`

---

## 目录

- [功能特性](#功能特性)
- [快速开始（5 分钟）](#快速开始5-分钟)
- [部署教程](#部署教程)
  - [方式一：下载/构建二进制直接运行](#方式一下载构建二进制直接运行)
  - [方式二：Linux systemd 常驻服务](#方式二linux-systemd-常驻服务)
  - [方式三：源码构建（含前端）](#方式三源码构建含前端)
  - [防火墙与端口](#防火墙与端口)
  - [升级与备份](#升级与备份)
- [配置详解 config.yaml](#配置详解-configyaml)
- [管理端使用指南](#管理端使用指南)
- [客户端接入示例](#客户端接入示例)
- [路由行为详解](#路由行为详解)
- [日志](#日志)
- [余额/配额查询](#余额配额查询)
- [常见问题 FAQ](#常见问题-faq)
- [测试](#测试)

---

## 功能特性

| 能力 | 说明 |
|---|---|
| 三协议自适应入口 | 同一服务自动识别 `POST /v1/chat/completions`、`POST /v1/responses`、`POST /v1/messages` |
| 协议自动转换 | 入口与上游协议不一致时经 canonical 中间表示双向转换，**含流式 SSE**；同协议则直通（零损耗） |
| 直选模式 | 客户端直接使用模型对外名称 |
| 负载均衡 | 分组 + 优先级故障转移（不可达/超时/5xx/429/402 余额不足/403 自动切换），同优先级轮询或随机 |
| 智能负载均衡 | 调度模型先判复杂度 → 强模型组/普通模型组，组内再按优先级故障转移；调度失败自动降级普通组 |
| 能力感知 | 多模态过滤、长上下文过滤（阈值可配）；普通组能力不足自动升级强模型组 |
| 余额/配额查询 | 内置 DeepSeek、SiliconFlow、OpenRouter、Novita、StepFun、智谱、MiniMax、Kimi Coding 探针 |
| 多 Key 管理 | `vr-` 前缀访问 Key，随时增删改查、启用/禁用 |
| 访问日志 | JSON Lines 按天切分文件，默认保留 7 天（可调），管理端按 Key/模型/状态码/时间过滤 |
| 管理界面 | shadcn/ui 界面，中英双语可切换（跟随浏览器语言，可手动切换并记忆），由 Go 二进制内嵌托管 |

内置 25 个供应商预设（DeepSeek、智谱、MiniMax、Kimi、SiliconFlow、OpenRouter、火山方舟、阿里百炼、ModelScope、PPIO 等，取自 cc-switch），管理端一键预填。

---

## 快速开始（5 分钟）

> 想预先自定义配置？可从仓库根目录复制 `.env.example` → `.env`、`config.example.yaml` → `config.yaml`，按文件内注释修改。也可以跳过这一步，首次启动会自动生成默认值。

1. 启动服务（假设二进制为 `viberouter`，Windows 为 `viberouter.exe`）：

   ```bash
   ./viberouter          # 默认读取当前目录 config.yaml 与 .env
   ```

   首次启动会：
   - 生成默认 `config.yaml`
   - 在 `.env` 中生成管理员账号 **admin / admin**（日志中会提示，请登录后尽快修改）

2. 打开管理端 `http://localhost:8080`，用 `admin / admin` 登录。

3. 添加供应商：供应商页 → 添加供应商 → 选择预设（如 DeepSeek）→ 填入 API Key → 保存。

4. 添加模型：模型页 → 添加模型，例如：
   - 路由名称 `ds-flash`（客户端访问用这个名字）
   - 供应商 `deepseek`，上游模型 `deepseek-v4-flash`
   - 按需勾选 多模态 / 长上下文，填上下文上限

5. 创建访问 Key：Key 管理页 → 创建 Key，得到 `vr-xxxxxxxx`。

6. 用任意 SDK 接入（以 OpenAI SDK 为例）：

   ```python
   from openai import OpenAI
   client = OpenAI(base_url="http://localhost:8080/v1", api_key="vr-xxxxxxxx")
   print(client.chat.completions.create(
       model="ds-flash",
       messages=[{"role": "user", "content": "你好"}],
   ).choices[0].message.content)
   ```

---

## 部署教程

### 方式一：下载/构建二进制直接运行

VibeRouter 是**单文件二进制**，前端已内嵌，无其他运行时依赖。

**Windows：**

```powershell
mkdir D:\apps\viberouter
cd D:\apps\viberouter
# 放入 viberouter.exe
.\viberouter.exe
```

**Linux：**

```bash
sudo mkdir -p /opt/viberouter
sudo cp viberouter-linux-amd64 /opt/viberouter/viberouter
sudo chmod +x /opt/viberouter/viberouter
cd /opt/viberouter && sudo ./viberouter
```

启动参数（均有默认值，通常不用指定）：

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-config` | `config.yaml` | 配置文件路径 |
| `-env` | `.env` | 管理员凭证文件路径 |

> 建议始终在固定工作目录下启动：`config.yaml`、`.env`、`logs/` 均相对于**当前工作目录**解析。

### 方式二：Linux systemd 常驻服务

创建 `/etc/systemd/system/viberouter.service`：

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
# 如需以非 root 运行，先创建用户并授权目录：
# User=viberouter
# Group=viberouter

[Install]
WantedBy=multi-user.target
```

启用：

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now viberouter
sudo systemctl status viberouter
journalctl -u viberouter -f      # 查看运行日志
```

### 方式三：源码构建（含前端）

需要 Go ≥ 1.26 与 Node.js ≥ 20（+ pnpm）。

```bash
git clone <repo> viberouter && cd viberouter

# 1. 构建前端（产物输出到 internal/server/web，将被 go:embed 嵌入）
cd web
pnpm install
pnpm build
cd ..

# 2. 构建后端（自动内嵌前端）
go build -o viberouter .

# 3. 交叉编译 Linux 版本（在 Windows/macOS 上构建 Linux 部署包）
GOOS=linux GOARCH=amd64 go build -o viberouter-linux-amd64 .
```

也可以直接 `make build`（等价于上述 1+2）。

> 注意：必须先构建前端再构建后端，否则二进制内嵌的是占位页面。

### 防火墙与端口

默认监听 `:8080`，在 `config.yaml` 的 `server.addr` 修改（改后需重启）。

Linux 若启用 firewalld：

```bash
sudo firewall-cmd --add-port=8080/tcp
# 需要开机保留则加 --permanent && sudo firewall-cmd --reload
```

若启用 ufw：

```bash
sudo ufw allow 8080/tcp
```

**生产环境建议**：不要直接把 8080 暴露到公网。用 Nginx/Caddy 反向代理并启用 HTTPS：

```nginx
server {
    listen 443 ssl;
    server_name ai.example.com;
    # ssl_certificate / ssl_certificate_key 略

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        # 流式 SSE 必需
        proxy_buffering off;
        proxy_cache off;
        proxy_set_header Connection "";
        proxy_read_timeout 600s;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
    }
}
```

### 升级与备份

- **升级**：停止服务 → 替换二进制 → 启动。`config.yaml` / `.env` / `logs/` 完全兼容保留。
- **备份**：只需备份工作目录下的 `config.yaml` 与 `.env` 两个文件，即包含全部供应商、模型、分组、Key 配置。

### 修改管理员密码

`.env` 中 `ADMIN_PASSWORD_HASH` 是 bcrypt 哈希。生成新哈希：

```bash
# 任意一台有 Python 的机器
python -c "import bcrypt; print(bcrypt.hashpw(b'你的新密码', bcrypt.gensalt()).decode())"
# 或用 Go: go run golang.org/x/crypto/bcrypt 小工具
```

替换 `.env` 中的 `ADMIN_PASSWORD_HASH` 后重启服务。

---

## 配置详解 config.yaml

管理端的所有修改都会写回此文件并热生效；手工编辑亦可（路由/模型等每次请求实时读取，`server.addr` 与日志文件基名需重启）。

```yaml
server:
  addr: ":8080"                    # 监听地址
  disable_captcha: false           # 关闭登录验证码（仅建议测试/受信内网环境使用）

routing:
  long_context_threshold: 200000   # 估算输入 token 超过此值 → 只选"长上下文"模型
  dispatcher_timeout_sec: 5        # 智能调度模型超时（秒），超时/失败降级到普通组
  strategy: round_robin            # 同优先级负载均衡策略: round_robin | random

providers:                         # 供应商
  - id: deepseek                   # 唯一 ID
    name: DeepSeek
    protocol: chat_completions     # chat_completions | responses | anthropic
    base_url: https://api.deepseek.com
    api_key: sk-xxxx               # 供应商级默认 Key（模型可覆盖）
    balance:
      type: deepseek               # 余额探针类型（见"余额/配额查询"）

models:                            # 模型
  - name: ds-flash                 # 对外路由名称（客户端用它）
    provider: deepseek
    upstream_model: deepseek-v4-flash   # 上游真实模型名
    # api_key / base_url / protocol  # 可选：覆盖供应商默认值
    multimodal: false              # 是否支持图片输入
    long_context: false            # 是否支持超长上下文
    max_context_tokens: 128000

groups:                            # 分组（分组名即对外模型名）
  - name: lb                       # 负载均衡组
    mode: balance
    strategy: round_robin          # 可选，覆盖全局策略
    members:
      - {model: ds-flash, priority: 1}   # 数字小者优先；不可达/余额不足自动切下一个
      - {model: ds-pro,   priority: 2}

  - name: auto                     # 智能负载均衡组
    mode: smart
    dispatcher_model: ds-flash     # 调度模型（建议用便宜快速的模型）
    # dispatcher_prompt: |         # 可选：自定义复杂度判定提示词
    #   ...
    advanced:                      # 强模型组
      - {model: ds-pro, priority: 1}
    basic:                         # 普通模型组
      - {model: ds-flash, priority: 1}

keys:                              # 访问 Key（建议只在管理端操作）
  - {id: k1, key: vr-xxxx, name: 我的客户端, enabled: true, created_at: "..."}

logs:
  file: logs/router.log            # 日志文件基名（实际写入 router-2026-08-15.log）
  retention_days: 7                # 日志保存天数，默认 7
```

`.env`（管理员凭证，切勿提交到版本库）：

```ini
ADMIN_USERNAME=admin
ADMIN_PASSWORD_HASH=<bcrypt 哈希>
ADMIN_SESSION_SECRET=<随机十六进制，重启后旧登录态失效>
```

---

## 管理端使用指南

| 页面 | 功能 |
|---|---|
| 供应商 | 预设模板一键预填；编辑/删除；**查余额**按钮（现金余额或配额百分比） |
| 模型 | 全字段增删改查；多模态/长上下文开关徽章 |
| 分组路由 | 负载均衡组：成员+优先级；智能组：调度模型、**调度提示词可编辑**（可一键填入默认提示词）、强/普通两个子组 |
| Key 管理 | 创建（`vr-` 前缀自动生成）、复制、改名、启用/禁用、删除 |
| 日志 | 按状态码/模型/**Key 名称**/条数过滤；每行可展开查看错误详情、供应商、故障转移次数 |
| 设置 | 长上下文阈值、调度超时、默认策略、**日志保存天数**、日志文件基名——保存即热生效 |

---

## 客户端接入示例

三种 SDK 连**同一个地址**，路由自动识别协议。模型名填路由中的模型名或分组名。

**OpenAI Chat Completions：**

```python
from openai import OpenAI
client = OpenAI(base_url="http://<host>:8080/v1", api_key="vr-xxxx")
client.chat.completions.create(model="auto", messages=[...], stream=True)
```

**OpenAI Responses：**

```python
client.responses.create(model="auto", input="你好", stream=True)
```

**Anthropic：**

```python
from anthropic import Anthropic
client = Anthropic(base_url="http://<host>:8080", api_key="vr-xxxx")
client.messages.create(model="auto", max_tokens=1024, messages=[...])
```

**Claude Code / 其他 CLI**：将 `ANTHROPIC_BASE_URL` 指向 `http://<host>:8080`、`ANTHROPIC_AUTH_TOKEN` 设为 `vr-xxxx` 即可。

**强制指定智能分组层级**（跳过调度模型判断）：

```
X-VibeRouter-Tier: advanced    # 或 basic
```

---

## 路由行为详解

**路由决策顺序**（三种模式通用）：

1. 模型名解析：先匹配模型，再匹配分组
2. 模态过滤：请求含图片 → 只在 `multimodal: true` 的模型中选择
3. 上下文过滤：估算 token（文本 ≈ 字符数/4，图片按 1000 计）超过阈值 → 只在 `long_context: true` 的模型中选择
4. 智能组额外步骤：调度模型判复杂度选层级；所选层级无能力匹配模型时**自动升级到强模型组**
5. 组内按优先级排序，同优先级按策略轮询/随机

**故障转移触发条件**（对客户端透明，自动尝试下一个候选）：

- 网络错误 / 连接超时
- HTTP 5xx、429（限流）、402（余额不足，如 DeepSeek 余额耗尽）、403（配额/权限）

4xx 客户端错误（如 400 参数错误）不转移，原样返回。

**智能调度细节**：

- 只把最后一条 user 消息的文本（截断 4000 字符）发给调度模型，不带历史、不带图片，`temperature=0`
- 调度超时 5 秒（可配）；调度失败/输出无法解析 → **降级到普通模型组**，日志记录降级原因
- 调度模型建议选便宜快速的模型；推理型模型（如 deepseek-v4）也可作调度模型（内部已预留足够 token 预算）

---

## 日志

- 存储：`logs/router-YYYY-MM-DD.log`，按天切分，JSON Lines 格式
- 保留：默认 7 天，设置页可调；过期文件自动删除
- 每条字段：时间、**Key 名称**、客户端协议、请求模型、实际路由模型、上游模型、供应商、分组/层级、是否流式、状态码、延迟、输入/输出 token、故障转移次数、错误详情、调度降级原因

命令行直接分析日志示例：

```bash
# 某 Key 今天的访问
grep '"key_name":"我的客户端"' logs/router-$(date +%F).log | jq .
```

---

## 余额/配额查询

管理端供应商页点"查余额"。不同供应商返回现金余额或配额百分比：

| 探针类型 | 供应商 | 返回 |
|---|---|---|
| `deepseek` | DeepSeek | 现金余额 (CNY) |
| `siliconflow` | SiliconFlow 国内/国际站（自动识别币种） | 现金余额 |
| `openrouter` | OpenRouter | 剩余额度 (USD) |
| `novita` | Novita AI | 可用余额 (USD) |
| `stepfun` | 阶跃星辰 | 现金余额 (CNY) |
| `zhipu` | 智谱 GLM（bigmodel.cn / z.ai） | token 配额剩余百分比 |
| `kimi_coding` | Kimi For Coding | 周配额剩余百分比 |
| `minimax` | MiniMax 国内/国际站 | Coding Plan 剩余百分比 |

自定义供应商如果兼容以上任一 API 形态，创建时选择对应 `balance.type` 即可复用探针。

---

## 常见问题 FAQ

**Q: 启动提示端口被占用？**
A: 改 `config.yaml` 的 `server.addr`（如 `":18080"`）后重启。

**Q: 管理端修改配置需要重启吗？**
A: 不需要，全部热生效。只有手工修改 `server.addr` 或日志文件基名后需重启。

**Q: 智能调度总是进普通组？**
A: 查日志页展开对应记录看 `dispatcher_error`：常见原因是调度模型是推理型模型且回答超时/为空（内部已预留 1024 token 预算，通常够用），或调度模型本身不可用（Key 无效等）。也可用 `X-VibeRouter-Tier` 请求头强制层级排查。

**Q: 多模态请求报错 "does not support multimodal input"？**
A: 目标模型/分组内没有任何 `multimodal: true` 的模型。给支持图片的模型勾上多模态。

**Q: 上游返回的 `reasoning_content`（推理过程）会保留吗？**
A: 同协议直通时原样保留；跨协议转换时只取最终回答（content），推理过程不参与转换。

**Q: 支持 streaming 吗？**
A: 三种协议全部支持，包括跨协议流式转换（如 Anthropic SDK 的 `messages.stream()` 接 chat_completions 上游）。

**Q: 如何改管理员密码 / 忘记密码？**
A: 删除 `.env` 中 `ADMIN_PASSWORD_HASH` 一行并重启 → 会重新生成 admin/admin（日志有提示），登录后再按上文方法改。

---

## 测试

```bash
go vet ./... && go test ./...                 # 单元 + 集成测试（协议转换/路由矩阵/管理端流程）

# 三协议 mock 上游 + 官方 SDK 兼容性套件（39 项）
go build -o mockupstream ./cmd/mockupstream
python tests/e2e/run_tests.py ./viberouter ./mockupstream

# Linux 双主机跨网络验收（需配置主机与凭证）
python tests/e2e/deploy_linux.py
```

## License

MIT
