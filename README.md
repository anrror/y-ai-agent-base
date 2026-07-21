# y-ai-agent-base

[![CI](https://github.com/anrror/y-ai-agent-base/actions/workflows/ci.yml/badge.svg)](https://github.com/anrror/y-ai-agent-base/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/Go-1.24-00ADD8.svg)](https://go.dev/)
[![Coverage](https://img.shields.io/badge/coverage-%3E60%25-brightgreen)]()

**y-ai-agent-base** 是一个用 Go 1.24 构建的企业级 AI Agent 框架，提供从单 Agent 到多 Agent 团队的完整运行时。框架以"即插即用的业务模块"为核心理念——每个业务领域以独立 Module 接入，自带配置、路由、健康检查与生命周期，零框架入侵。

不是 LangChain 的 Go 移植版，而是为生产级 Go 技术栈从零设计的 Agent 框架：编译型、强类型、goroutine 并发、单一静态二进制部署。

---

## 目录

- [为什么选择 y-ai-agent-base](#为什么选择-y-ai-agent-base)
- [核心架构](#核心架构)
- [快速开始](#快速开始)
- [Provider 配置](#provider-配置)
- [API 接口](#api-接口)
- [Agent 系统](#agent-系统)
- [模块系统](#模块系统)
- [多 Agent 协作](#多-agent-协作)
- [技能系统](#技能系统)
- [安全守卫](#安全守卫)
- [配置系统](#配置系统)
- [包结构](#包结构)
- [开发指南](#开发指南)
- [许可证](#许可证)

---

## 为什么选择 y-ai-agent-base

与 LangChain、CrewAI 等 Python 生态框架不同，y-ai-agent-base 专为 **Go 技术栈、高并发、强类型约束的生产环境**设计：

| 维度 | y-ai-agent-base | Python 生态 (LangChain/CrewAI/AutoGen) |
|------|:---:|:---:|
| **语言与性能** | Go — 编译型，goroutine 并发，毫秒级启动，低内存占用 | Python — 解释型，GIL 限制，冷启动秒级 |
| **类型安全** | 编译期类型检查，零 `interface{}` 泄漏，Go 1.24 泛型 | 运行时 duck typing，类型错误到生产才暴露 |
| **部署** | 单一静态二进制，`scp` 即用，scratch 镜像 < 20MB | 依赖 Python 版本 + pip + venv，镜像 > 500MB |
| **并发模型** | goroutine-per-call，单进程万级并发 | asyncio/线程池，上下文切换开销大 |
| **模块化** | Module 插件系统 — 业务模块零耦合、即插即用 | 无内置模块隔离，业务代码散落各处 |
| **Provider 设计** | 多 Provider 独立配置：Chat/Embedding/Guard 各自独立 API Key + BaseURL + Model，可混合不同厂商 | 大多单 Provider 单配置，切换厂商需改代码 |
| **多 Agent 协作** | 原生 Team + Supervisor 模式，Agent 互调通过 Function Calling | 需引入 CrewAI/AutoGen 等第三方编排 |
| **运维友好** | 内置 JWT、限流、CORS、健康检查、Prometheus 指标、热重载、结构化日志 | 需额外集成 Prometheus、nginx、logstash 等 |
| **可观测性** | `slog` 结构化日志 + Metrics 中间件，请求 ID、延迟、Agent ID 全链路跟踪 | OpenTelemetry 配置重，依赖 exporter 部署 |
| **记忆系统** | 多层次记忆存储，支持 TTL 自动过期，可扩展 Store 接口 | LangChain 记忆实现复杂且性能瓶颈明显 |
| **二进制体积** | 单一静态二进制 ~15MB，scratch 容器镜像 | 容器镜像通常 >800MB，层数多，攻击面大 |

---

## 核心架构

框架采用**四层架构**设计，每一层职责清晰、可独立扩展：

```
┌──────────────────────────────────────────────────────────────┐
│                      HTTP Server (Gin)                        │
│   ┌──────────────────┐   ┌────────────────────────────────┐  │
│   │  Framework Routes │   │     Module 层                   │  │
│   │  /health          │   │  ┌──────────┐ ┌──────────┐    │  │
│   │  /metrics         │   │  │ CRM      │ │ Billing  │    │  │
│   │  /api/v1/*        │   │  │ Module   │ │ Module   │    │  │
│   └────────┬──────────┘   └────────┬─────────────────────┘  │
│            │                       │                         │
│            └───────────┬───────────┘                         │
│                        ▼                                      │
│              ┌──────────────────────────┐                     │
│              │     Agent 系统            │                     │
│              │  Registry + Pipeline      │                     │
│              │  + Skills + Tools         │                     │
│              └────────────┬─────────────┘                     │
│                           ▼                                   │
│              ┌──────────────────────────┐                     │
│              │      ProviderSet          │                     │
│              │  ┌──────┬───────┬──────┐ │                     │
│              │  │ Chat │ Embed │ Guard│ │  ← 每角色独立实例    │
│              │  └──┬───┴───┬───┴──┬───┘ │                     │
│              │     │       │      │     │                     │
│              │  OpenAI  DeepSeek  nil   │  ← Guard 可不配置    │
│              └──────┼───────────────────┘                     │
│                     ▼                                         │
│              ┌──────────────────────────┐                     │
│              │   LLM API (HTTP/SSE)     │                     │
│              └──────────────────────────┘                     │
└──────────────────────────────────────────────────────────────┘
```

各层职责：

| 层 | 职责 | 可扩展方式 |
|----|------|-----------|
| **Module 层** | 业务模块插件系统 — 每个模块自带路由、配置、健康检查、生命周期 | 实现 `module.Module` 接口 |
| **Agent 层** | Agent 编排、Pipeline 中间件、工具调用、记忆、技能挂载 | 实现 `component.Component` 注入中间件 |
| **Provider 层** | LLM/Embedding/Guard 三合一管理，每个角色独立配置 | 实现 `provider.LLMProvider` / `EmbeddingProvider` / `GuardProvider` 接口 |
| **HTTP 层** | Gin HTTP 服务器，JWT/限流/CORS 中间件，SSE 流式，热重载 | Module 注册路由和中间件 |

### 请求生命周期

```
HTTP Request
  │
  ├─ Gin Middleware Chain
  │   ├─ Recovery
  │   ├─ CORS
  │   ├─ RequestID (注入 X-Request-ID)
  │   ├─ JWT Auth (Bearer token 验证)
  │   ├─ Rate Limit (令牌桶)
  │   └─ Logging (请求 ID + 延迟 + 状态码)
  │
  ├─ Handler.ChatCompletions
  │   ├─ 解析请求 → ChatInput
  │   │
  │   ├─ Agent.Chat / Agent.RunStream
  │   │   ├─ Pipeline Middleware Chain (洋葱模型)
  │   │   │   ├─ Timeout (上下文超时控制)
  │   │   │   ├─ Metrics (记录 token/延迟)
  │   │   │   ├─ [核心] → Provider.ChatStream
  │   │   │   │   ├─ 断路器检查
  │   │   │   │   ├─ HTTP POST /v1/chat/completions (SSE)
  │   │   │   │   ├─ 逐 token 解析 SSE 事件
  │   │   │   │   └─ 返回 StreamEvent 通道
  │   │   │   ├─ Tool Call 循环 (最多 10 轮)
  │   │   │   └─ Post-Hooks (异步持久化)
  │   │   │
  │   │   └─ StreamEvent → SSE/JSON 响应
  │   │
  │   └─ HTTP Response
  │
  └─ TelemetryHook (全链路记录)
```

---

## 快速开始

```bash
# 1. 设置 API Key（全局默认，所有 Provider 继承）
export YAI_PROVIDERS_API_KEY="sk-your-key"
export YAI_PROVIDERS_BASE_URL="https://api.openai.com/v1"
export YAI_AUTH_JWT_SECRET="my-secret"

# 2. 启动（内置 3 个 Demo Agent + 1 个 Demo Team）
go run ./cmd/server/

# 3. 测试
curl -X POST http://localhost:8080/api/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"assistant","messages":[{"role":"user","content":"你好!"}],"stream":false}'

# 4. 多 Agent 团队协作
curl -X POST http://localhost:8080/api/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"project-team","messages":[{"role":"user","content":"设计一个 REST API 并写一篇介绍文章"}],"stream":false}'

# 5. 零配置启动（仅需必要环境变量）
YAI_PROVIDERS_API_KEY="sk-xxx" YAI_AUTH_JWT_SECRET="my-secret" go run ./cmd/server/
```

启动后服务器提供：

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/health` | 健康检查（含模块聚合） |
| GET | `/metrics` | 框架指标 |
| POST | `/api/v1/chat/completions` | OpenAI 兼容聊天（SSE 流式 + JSON） |
| POST | `/api/v1/agents` | 注册 Agent |
| GET | `/api/v1/agents` | 列出所有 Agent |
| GET | `/api/v1/agents/:id` | 获取 Agent 详情 |
| DELETE | `/api/v1/agents/:id` | 删除 Agent |
| GET | `/api/v1/teams` | 列出多智能体团队 |
| GET | `/api/v1/teams/:id` | 获取团队详情 |

---

## Provider 配置

框架采用**全局默认 + 角色覆盖**设计，每个角色（Chat/Embedding/Guard）拥有独立的 Provider 实例。

### 配置结构

```yaml
# config/config.yaml
providers:
  # ── 全局默认（所有角色继承） ──
  api_key: "${YAI_PROVIDERS_API_KEY}"
  base_url: "https://api.openai.com/v1"

  # ── Chat（必须配置） ──
  chat:
    model: gpt-4o                     # 继承全局 api_key/base_url

  # ── Embedding（可选，nil = 不启用向量检索） ──
  embedding:
    model: text-embedding-3-large

  # ── Guard（可选，nil = 跳过全部安全检查） ──
  guard:
    model: gpt-4o-mini
```

**配置合并逻辑**（`ProvidersConfig.Resolve()` 自动执行）：
1. 如果某角色的 `APIKey` 为空，继承全局 `providers.api_key`
2. 如果某角色的 `BaseURL` 为空，继承全局 `providers.base_url`
3. 如果某角色整个配置块不存在（如 `guard:` 省略），则该角色的 Provider 为 nil，对应功能禁用

### ProviderSet

框架内置 `ProviderSet` 结构，为每个角色持有独立的 Provider 实例：

```go
// pkg/provider/provider.go
type ProviderSet struct {
    Chat      LLMProvider       // 必须（至少需要一个 Chat 模型）
    Embedding EmbeddingProvider // 可选，nil = 不启用向量检索
    Guard     GuardProvider     // 可选，nil = 不启用安全检查
}
```

框架在 `server.buildCore()` 中为每个角色构建独立的 Provider 实例，每个角色通过自己的 `*ProviderConfig` 初始化：

```go
// internal/server/server.go
ps := &provider.ProviderSet{}
ps.Chat = openai.NewOpenAIProvider(&provider.ProviderConfig{
    Type: "openai", APIKey: "...", BaseURL: "...", Model: "gpt-4o",
})
ps.Embedding = openai.NewOpenAIProvider(&provider.ProviderConfig{
    Type: "openai", APIKey: "...", BaseURL: "...", Model: "text-embedding-3",
})
```

### 环境变量

| 变量 | 作用 | 优先级 |
|------|------|--------|
| `YAI_PROVIDERS_API_KEY` | 全局默认 API Key | 低（被角色级覆盖） |
| `YAI_PROVIDERS_BASE_URL` | 全局默认 Base URL | 低（被角色级覆盖） |
| `YAI_PROVIDERS_CHAT_API_KEY` | Chat 专用 Key | 高 |
| `YAI_PROVIDERS_CHAT_MODEL` | Chat 模型名 | 最高 |
| `YAI_PROVIDERS_CHAT_BASE_URL` | Chat 专用 URL | 高 |
| `YAI_PROVIDERS_EMBEDDING_API_KEY` | Embedding 专用 Key | 高 |
| `YAI_PROVIDERS_EMBEDDING_MODEL` | Embedding 模型名 | — |
| `YAI_PROVIDERS_GUARD_API_KEY` | Guard 专用 Key | 高 |
| `YAI_PROVIDERS_GUARD_MODEL` | Guard 模型名 | — |

### 常见配置场景

```yaml
# 场景 1：纯 OpenAI
providers:
  api_key: "${YAI_OPENAI_KEY}"
  base_url: "https://api.openai.com/v1"
  chat:
    model: gpt-4o

# 场景 2：OpenAI Chat + DeepSeek Embedding（混合厂商）
providers:
  api_key: "${YAI_OPENAI_KEY}"
  base_url: "https://api.openai.com/v1"
  chat:
    model: gpt-4o
  embedding:
    base_url: "https://api.deepseek.com/v1"
    api_key: "${YAI_DEEPSEEK_KEY}"
    model: deepseek-embedding

# 场景 3：多 API Key 隔离（Chat 和 Embedding 不同 Key）
providers:
  chat:
    api_key: "${YAI_CHAT_KEY}"
    base_url: "https://api.openai.com/v1"
    model: gpt-4o
  embedding:
    api_key: "${YAI_EMBED_KEY}"
    base_url: "https://api.openai.com/v1"
    model: text-embedding-3-large
  # guard 不配置 → 禁用安全检查

# 场景 4：切换 DeepSeek 只需改两行
providers:
  api_key: "${YAI_DEEPSEEK_API_KEY}"
  base_url: "https://api.deepseek.com/v1"
  chat:
    model: deepseek-chat
```

> DeepSeek API 兼容 OpenAI 格式，`NewOpenAIProvider` 可直接使用。如使用其他不兼容 OpenAI 格式的厂商，实现 `provider.LLMProvider` 接口即可。

---

## API 接口

### 聊天补全

```
POST /api/v1/chat/completions
Authorization: Bearer <jwt>

{
    "model": "assistant",       // Agent ID 或 team:<id>
    "messages": [
        {"role": "user", "content": "Hello!"}
    ],
    "stream": true,             // true = SSE 流式, false = JSON
    "temperature": 0.7,
    "max_tokens": 2048
}
```

流式响应逐 token 推送 SSE chunk：

```
data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"}}]}

data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"你好"}}]}

data: [DONE]
```

### Agent 管理

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/agents` | 注册新 Agent |
| GET | `/api/v1/agents` | 列出所有 Agent |
| GET | `/api/v1/agents/:id` | 获取 Agent 详情 |
| DELETE | `/api/v1/agents/:id` | 删除 Agent |

### 健康检查

```
GET /health

{
    "status": "ok",
    "timestamp": "2026-07-13T10:00:00Z",
    "checks": {
        "database": "ok",
        "provider": "ok",
        "crm": {"status": "ok", "timestamp": "..."}
    }
}
```

健康检查自动聚合框架组件（数据库、Provider）和所有已注册模块的健康状态。

---

## Agent 系统

Agent 是框架的核心实体，封装了 LLM 调用、工具执行、记忆管理和扩展系统。

### Agent 结构

```go
type Agent struct {
    Config            Config
    Provider          provider.LLMProvider
    Pipeline          pipeline.Pipeline
    Tools             []tool.Tool
    Memory            memory.Store
    Skills            []Skill
    Extensions        map[string]Extension
    ComponentRegistry *component.Registry
}
```

Agent 支持热重载 — `ReloadConfig()` 和 `ReloadProvider()` 在运行时原子替换配置和 Provider，不影响进行中的请求。

### Agent 配置

```go
type Config struct {
    AgentID     string               // 唯一标识
    Identity    *personality.Identity // 身份描述
    Personality personality.OCEAN    // 大五人格
    LLMConfig   types.ModelConfig    // 模型配置
    PromptTmpl  string               // 系统提示词
    SafetyConfig types.SafetyConfig  // 安全配置
    Status      AgentStatus          // 状态
    Memory      *types.MemoryConfig  // 记忆配置
}
```

### 构建 Agent

```go
ac := agent.Config{
    AgentID: "coder",
    Identity: &personality.Identity{
        Name: "Code Expert", Role: "senior software engineer",
        Description: "A pragmatic senior engineer who writes clean code.",
        Tone: "professional", Verbosity: "concise",
    },
    LLMConfig: types.ModelConfig{
        Model: "gpt-4o", Temperature: 0.7, MaxTokens: 4096,
    },
    PromptTmpl: "You are a senior software engineer...",
    Status: agent.StatusReady,
}
ac.FillDefaults()

ag, err := ac.ToBuilder().
    WithProvider(prov).
    WithPipeline(pipe).
    WithSkills(timeSkill, weatherSkill).
    Build()
```

### 工具调用

Agent 内置工具调用循环（最多 10 轮迭代）。当 LLM 返回 `tool_calls` 时，Agent 自动执行对应工具并将结果追加到对话中：

```
Agent.Chat() 循环:
  1. 设置工具 → Provider.ChatStream
  2. 收集流事件
  3. 检查是否有 tool_calls
  4. 有 → 执行工具 → 追加结果 → 回到 1
  5. 无 → 返回最终文本响应
```

### 记忆系统

```go
// pkg/memory
type Store interface {
    Add(ctx context.Context, entry *Entry) error
    Search(ctx context.Context, query string, limit int) ([]*Entry, error)
    Delete(ctx context.Context, id string) error
    Close() error
}

type Entry struct {
    ID         string
    Content    string
    Importance float64
    CreatedAt  time.Time
    Metadata   map[string]any
}
```

内置 `InMemoryStore` 实现，支持前缀匹配和子串搜索。可对接外部存储实现持久化。

### 扩展系统

Agent 通过 `Extension` 接口支持外部模块（情绪、推理、调度器等）的挂载：

```go
type Extension interface {
    ID() string
    Close() error
}
```

扩展通过 Builder 的 `WithExtensions()` 注入，构建后通过 `GetExtension(id)` 或 `GetComponent(id)` 查找。

---

## 模块系统

### Module 接口

```go
type Module interface {
    ID() string                                // 唯一标识，用于配置 key 和路由前缀
    Config() any                               // 配置结构体指针
    Init(ctx *Context) error                   // 初始化（注册路由/健康检查/中间件）
    Start(ctx context.Context) error           // 启动后台协程
    Stop(ctx context.Context) error            // 优雅关闭
}
```

### 生命周期

```
1. Load config (框架 + 模块配置)
2. Create ProviderSet (Chat/Embedding/Guard 各角色独立构建)
3. Create pipeline, stores, registry
4. Seed agents (server.WithSeed)
5. Build teams (server.WithTeam)
6. For each module: Init(ctx) → 注册路由、健康检查、中间件
7. Setup Gin engine (框架路由 + 模块路由)
8. For each module: Start(ctx)
9. Serve HTTP (阻塞)
10. SIGINT/SIGTERM:
    a. For each module (逆序): Stop(ctx)
    b. HTTP server shutdown, flush post-hooks, close providers
```

### InitContext

```go
type Context struct {
    ModuleConfig  any               // 已解析的模块配置
    Logger        *slog.Logger      // 带 module 标签的 Logger
    Router        func() gin.IRouter    // /api/v1/<id> 路由组
    RegisterRoute func(method, path string, handler gin.HandlerFunc, mws ...gin.HandlerFunc)
    RegisterHealthCheck func(name string, fn HealthCheckFunc)
    RegisterMiddleware  func(mw gin.HandlerFunc)
    AgentRegistry *agent.Registry
    AgentStore    store.AgentStore
    Provider      provider.LLMProvider
    Pipeline      pipeline.Pipeline
}
```

### 编写业务模块

```go
type Config struct {
    Enabled bool   `mapstructure:"enabled"`
    Endpoint string `mapstructure:"endpoint"`
}

type Module struct {
    cfg    *Config
    client *http.Client
}

func (m *Module) ID() string { return "crm" }
func (m *Module) Config() any { return &Config{} }

func (m *Module) Init(ctx *module.Context) error {
    m.cfg = ctx.ModuleConfig.(*Config)
    m.client = &http.Client{Timeout: 10 * time.Second}
    r := ctx.Router()
    r.GET("/contacts", m.listContacts)
    ctx.RegisterHealthCheck("crm", m.healthCheck)
    return nil
}

func (m *Module) Start(ctx context.Context) error {
    if m.cfg.Enabled { go m.syncLoop(ctx) }
    return nil
}

func (m *Module) Stop(ctx context.Context) error {
    m.client.CloseIdleConnections()
    return nil
}
```

注册到服务器：

```go
srv, err := server.New(cfg,
    server.WithSeed(seedAgents),
    server.WithTeam(seedTeam),
    server.WithModule(&crm.Module{}),
)
```

---

## 多 Agent 协作

框架提供原生多智能体团队支持，通过**编排者-工作者（Supervisor-Worker）** 模式实现 Agent 间的任务委派和协作。

### 核心概念

| 概念 | 说明 |
|------|------|
| **Agent.AsTool()** | 将 Agent 包装为可被其他 Agent 调用的 Tool |
| **Team** | 由 Supervisor（编排者）+ Members（工作者）组成的团队 |
| **Supervisor** | 负责接收任务、分解、委派给成员的编排 Agent |
| **delegate_to_\<id\>** | Team 自动为每个成员注册的工具 |

### 团队运作

```
用户请求 → Supervisor
  ├─ 分析任务
  ├─ delegate_to_coder("写一个 REST API")
  ├─ delegate_to_creative("写一篇介绍文章")
  ├─ 等待所有工作者完成
  └─ 整合结果 → 用户
```

### 使用方式

```bash
# 通过 team:<id> 格式访问团队
curl -X POST http://localhost:8080/api/v1/chat/completions \
  -H "Authorization: Bearer <token>" \
  -d '{"model":"team:project-team","messages":[{"role":"user","content":"设计一个 REST API"}],"stream":false}'
```

### 创建团队

```go
team, err := team.New(supervisorCfg, prov, pipe, coderAgent, creativeAgent)
h.TeamRegistry.Register(team)
```

---

## 技能系统

技能是**可复用的能力包**——将工具和指令组合为自包含的 Skill，按需挂载到 Agent 上。

```go
type Skill interface {
    Name() string
    Description() string
    Instructions() string           // 注入 Agent 系统指令
    Tools() []tool.Tool             // 技能关联的工具集
    Metadata() SkillMetadata        // Tags, Category, Version 等
    Match(ctx, query string) float64 // 语义匹配分数
}
```

### 使用内置技能

```go
timeSkill := builtin.TimeSkill()
weatherSkill := builtin.WeatherSkill()

reg := skills.NewRegistry()
reg.Register(timeSkill)
reg.Register(weatherSkill)

// 语义匹配 — 自动找最相关的技能
best := reg.Match("what time is it")  // → timeSkill
```

### 挂载到 Agent

```go
ag, _ := ac.ToBuilder().
    WithProvider(prov).
    WithPipeline(pipe).
    WithSkills(timeSkill, weatherSkill).
    Build()
// 技能的 Instructions 自动注入系统提示词
// 技能的 Tools 自动合并到 Agent 工具列表
```

---

## 安全守卫

框架内置安全守卫，通过 Guard 接口实现输入/输出内容过滤。Guard 为 nil 时全部放行。

```go
type Guard struct {
    Provider provider.GuardProvider  // nil = 跳过所有检查
    Config   types.SafetyConfig
}

func (g *Guard) Check(ctx context.Context, text string) (bool, error)
func (g *Guard) CheckInput(ctx context.Context, text string) error   // 输入检查
func (g *Guard) CheckOutput(ctx context.Context, text string) error  // 输出检查
```

- 当 `Guard.Provider == nil` → 所有 `Check` 返回 `(true, nil)`
- 当 `SafetyConfig.Enabled == false` → 跳过所有检查
- `CheckInput` / `CheckOutput` 各自有独立开关

---

## 配置系统

配置加载优先级（高 → 低）：

| 优先级 | 来源 | 示例 |
|--------|------|------|
| 1 | 命令行参数 | `--providers.chat.api-key=sk-xxx` |
| 2 | 环境变量 | `YAI_PROVIDERS_CHAT_API_KEY=sk-xxx` |
| 3 | YAML 文件 | `config/config.yaml` |
| 4 | `.env` 文件 | 自动加载，缺失不报错 |

### 框架配置结构

```go
type Config struct {
    Server    ServerConfig    `mapstructure:"server"`
    Providers ProvidersConfig `mapstructure:"providers"`
    Database  DatabaseConfig  `mapstructure:"database"`
    Auth      AuthConfig      `mapstructure:"auth"`
    Logging   LoggingConfig   `mapstructure:"logging"`
    Modules   map[string]any  `mapstructure:"modules"`
}

type ProvidersConfig struct {
    APIKey    string          `mapstructure:"api_key"`     // 全局默认
    BaseURL   string          `mapstructure:"base_url"`    // 全局默认
    Chat      *ProviderConfig `mapstructure:"chat"`        // 必须
    Embedding *ProviderConfig `mapstructure:"embedding"`   // 可选
    Guard     *ProviderConfig `mapstructure:"guard"`       // 可选
}

type ProviderConfig struct {
    Type    string `mapstructure:"type"`
    APIKey  string `mapstructure:"api_key"`
    BaseURL string `mapstructure:"base_url"`
    Model   string `mapstructure:"model"`
}
```

### 热重载

框架内置配置文件热重载（`fsnotify`），检测到 `config/config.yaml` 变更时自动执行：

1. 比较 Chat Provider 配置是否变化（Key/URL/Model）
2. 若有变化：创建新 Provider + Pipeline
3. 下发新配置到所有已注册 Agent（`ReloadConfig` + `ReloadProvider`）
4. 原子交换（`sync.RWMutex` 写锁）
5. 旧 Provider 在无请求引用后自动关闭

```go
// watchConfig 简化流程
chatChanged := newCfg.Providers.Chat.APIKey != old.APIKey ||
    newCfg.Providers.Chat.BaseURL != old.BaseURL ||
    newCfg.Providers.Chat.Model != old.Model

if chatChanged {
    newProv := openai.NewOpenAIProvider(&provider.ProviderConfig{...})
    newPipe := pipeline.New(newProv, ...)
    // 通知所有 Agent 热替换
    for _, ag := range reg.List() { ag.ReloadProvider(newProv, newPipe) }
    // 原子交换
    provMu.Lock()
    s.prov = newProv
    s.pipe = newPipe
    provMu.Unlock()
    oldProv.Close()
}
```

---

## 包结构

```
cmd/
  server/             HTTP 服务器入口（种子 Agent/Team 构建）
internal/
  config/             服务端配置结构体
  handler/            HTTP 处理器（Chat Completions、Agent CRUD、Teams）
  middleware/         Gin 中间件（JWT、CORS、限流、日志、Telemetry）
  router/             路由注册
  server/             Server 构建器 + 模块生命周期管理 + 热重载
pkg/
  agent/              Agent 编排、Builder、Registry、热重载
  cache/              缓存（精确匹配 + 语义匹配）
  clock/              可注入时钟（测试用）
  component/          组件接口（中间件注入 + 优先级排序）
  compressor/         Token 压缩
  config/             YAML/环境变量/命令行配置加载 + Validate + Watch
  driver/             LLM 驱动层
  edge/               边缘计算
  inference/          推理路由器
  memory/             记忆存储接口 + InMemoryStore 实现
  module/             模块插件系统（Module 接口 + InitContext + 健康检查）
  pipeline/           中间件管道（Handler/Middleware/Pipeline + 流式 + PostHook）
  provider/           Provider 抽象（LLMProvider/EmbeddingProvider/GuardProvider + ProviderSet）
  provider/openai/    OpenAI/DeepSeek 兼容实现（SSE 流式 + 工具调用 + 断路器）
  reasoning/          推理引擎
  safety/             安全守卫（Guard nil-safe → 不配置即跳过）
  scheduler/          调度器
  session/            会话管理
  skills/             技能系统（Skill 接口 + Registry + 语义匹配 + 内置技能）
  store/              持久化存储（MemoryStore 接口 + InMemoryStore）
  team/               多 Agent 团队（Team + Registry + Supervisor）
  tool/               工具系统（Tool 接口 + Registry + FromFunction + Schema）
  types/              共享类型（Message/ModelConfig/ChatInput/ChatOutput + 错误体系）
  util/               工具函数
examples/
  01-basic-agent/     最小 Agent 示例
  02-custom-tool/     自定义工具（Calculator）
  03-custom-middleware/ 自定义中间件（Timing + Logging）
  04-custom-skill/    自定义技能（Time + Weather）
  05-full-integration/ 全链路集成测试
```

---

## 开发指南

```bash
# 构建
make build

# 运行测试（含竞态检测）
make test

# 覆盖率
make coverage

# Lint 检查
make lint

# 完整 CI 流水线
make ci

# 启动开发服务器
go run ./cmd/server/

# Docker 启动依赖服务
docker compose up -d   # PostgreSQL (pgvector) + Redis
go run ./cmd/server/
```

### 测试准则

| 类型 | 覆盖场景 |
|------|----------|
| 单元测试 | 纯函数正确性（正常 + 边界 + 错误路径） |
| 集成测试 | `httptest` + mock，真实 adapter 对接 |
| E2E 测试 | API 全链路（Chat/Agent CRUD/Health/Auth/RateLimit） |
| 竞态检测 | `go test -race`，并发安全 + goroutine 泄漏检测 |
| 覆盖率 | > 60%（核心包 > 80%） |

---

## 许可证

MIT
