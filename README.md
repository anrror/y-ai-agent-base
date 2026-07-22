# y-ai-agent-base

**企业级 AI Agent 框架** — 用 Go 为生产环境构建的编译型、强类型、高并发 Agent 运行时。

不是 LangChain 的 Go 移植版。从零为 **Go 技术栈、高并发微服务、单一静态二进制部署**设计。

---

## 为什么选择 y-ai-agent-base

### 语言与技术选型

**Go 而不是 Python**

AI Agent 框架生态几乎被 Python 主导（LangChain、CrewAI、AutoGen）。但生产环境的差异巨大：

| 维度 | Go（本框架） | Python 生态 |
|------|:---:|:---:|
| **编译与部署** | 单一静态二进制，`scp` 即用，scratch 容器 < 20MB | 依赖 Python 版本 + pip + venv + 系统库，容器 > 800MB |
| **并发模型** | goroutine-per-call，单进程万级并发，栈内存 KB 级 | asyncio/线程池，GIL 限制，上下文切换开销大 |
| **类型安全** | 编译期类型检查，泛型，`interface{}` 零泄漏 | 运行时 duck typing，类型错误到生产才暴露 |
| **冷启动** | 毫秒级 | 秒级（解释器加载 + 包导入） |
| **可观测性** | 零依赖结构化日志（`slog`），内置 Metrics 钩子 | 需额外集成 OpenTelemetry，配置链重 |
| **运维** | 单一进程，无外部依赖，裸机/容器/边缘无差别 | 需 uvicorn/gunicorn + nginx + 进程管理器 |

**选型理由**：AI Agent 框架本质是编排系统——编排 LLM 调用、工具执行、记忆检索、中间件链。Go 的 goroutine 并发模型天然适合这种 I/O 密集型编排。一个 Agent Chat() 调用背后涉及 3-10 次 LLM API 调用、N 次工具执行、M 次记忆检索，这些在 Go 中通过 goroutine + channel 可以零成本并发出海量请求，而 Python 的 asyncio 需要显式 await 每步。

**Gin 而不是 net/http**

选择 Gin 是因为：
- 生产级中间件链（Recovery、Logger、CORS、Rate-Limit 开箱即用）
- 路由组 + 参数绑定 + 校验 — 减少 40% HTTP handler 样板代码
- 社区最大，问题可搜，生态丰富
- `gin.Context` 的 `Request`/`Response` 抽象在流式 SSE 场景下依然干净

**pgvector + PostgreSQL 而不是专用向量库**

- 无需引入 Pinecone/Weaviate/Qdrant 等外部向量数据库，减少运维组件
- PostgreSQL 的 MVCC + HNSW 索引在万级文档规模下延迟 < 50ms
- 单一数据源：业务数据和向量存在同一数据库，无一致性问题
- pgvector 的 HNSW 索引支持 `ef_search` 参数调节精度/性能

**Redis 而不是内存 Session**

虽然框架提供 InMemory 实现，但生产环境 Session/记忆存储走 Redis：
- 进程重启不丢会话
- 多副本共享状态
- TTL 自动过期，无需 GC 调优
- redigo 的池化连接在 Goroutine 并发下表现优异

**Viper 而不是自定义配置**

- 支持 YAML / JSON / TOML / 环境变量 / 命令行参数，优先级链清晰
- 内置 `fsnotify` 热重载支持
- 自动的 `mapstructure` 结构体绑定
- 环境变量命名约定（`YAI_*`）统一了整个配置体系

### 架构选型

**四层架构**

```
Module 层 (业务插件)
  ↓
Agent 层 (编排 + 中间件)
  ↓
Provider 层 (LLM/Embedding/Guard)
  ↓
LLM API
```

每层职责清晰、可独立扩展、可替换实现。Module 层的插件系统让业务代码与框架完全解耦——业务模块只需要实现 `Module` 接口，自动获得路由、健康检查、生命周期管理。

**中间件管道 (Pipeline)**

参考 Gin 和 Redux 的洋葱模型：

```
Request → Middleware1 → Middleware2 → Core(Provider.ChatStream) → Middleware2 → Middleware1 → Response
```

每层中间件可以：
- 修改请求/响应（Context 注入）
- 短路（返回错误，跳过后续）
- 记录/监控（无侵入）
- 超时控制（context.WithTimeout）

流式场景下，中间件链的 pre-hooks 同步执行，`ChatStream` 在独立 goroutine 运行，post-hooks 在流结束后异步执行。这种设计保证了：
- 中间件链线性可读
- 流式数据的零拷贝传递
- Post-hook 不阻塞用户响应

**多 Provider 设计**

```
ProviderSet:
  Chat      LLMProvider     (必须)
  Embedding EmbeddingProvider (可选)
  Guard     GuardProvider   (可选)
```

三个角色独立配置、独立实例化、可混合不同厂商：
- Chat 用 OpenAI，Embedding 用 DeepSeek，Guard 用 Anthropic
- 每个角色有自己的 API Key / BaseURL / Model
- 任意角色为 nil 时自动禁用对应功能
- 全局默认 + 角色覆盖的配置合并策略

这意味着只需修改一行配置即可从 OpenAI 切换到 DeepSeek，而不改任何代码。

**流式设计**

`StreamEvent` 结构化通道：

```go
type StreamEvent struct {
    Type    string // "text" | "tool_call" | "done" | "error"
    Content string
    Done    bool
    Error   error
}
```

- Provider 返回 `<-chan StreamEvent`（只读，由 Provider goroutine 写入和关闭）
- Pipeline 中间件不可读/写 Stream 字段（零竞态设计）
- HTTP 层直接消费通道转发为 SSE
- Post-hook 在流结束后以 `context.Background()` 异步执行

这种设计保证了：**中间件永远不会竞态访问流数据**，因为流通道的拥有者（Provider goroutine）和消费者（HTTP writer）是明确的。

---

## 快速开始

```bash
# 设置 API Key
export YAI_PROVIDERS_API_KEY="sk-your-key"
export YAI_PROVIDERS_BASE_URL="https://api.openai.com/v1"
export YAI_AUTH_JWT_SECRET="my-secret"

# 启动（内置 3 个 Demo Agent + 1 个 Demo Team）
go run ./cmd/server/

# 测试聊天
curl -X POST http://localhost:8080/api/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"assistant","messages":[{"role":"user","content":"你好!"}],"stream":false}'

# 多 Agent 团队协作
curl -X POST http://localhost:8080/api/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"team:project-team","messages":[{"role":"user","content":"设计一个 REST API"}],"stream":false}'

# 零配置启动（一切只需环境变量）
YAI_PROVIDERS_API_KEY="sk-xxx" YAI_AUTH_JWT_SECRET="my-secret" go run ./cmd/server/
```

启动后服务提供：

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

## 核心架构

### 四层设计

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
│              │  + Knowledge + MCP       │                     │
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
| **Agent 层** | Agent 编排、Pipeline 中间件、工具调用、记忆、技能挂载、知识检索、MCP 集成 | 实现 `component.Component` 注入中间件 |
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
  │   └─ Rate Limit (令牌桶)
  │
  ├─ Handler.ChatCompletions
  │   ├─ 解析请求 → ChatInput
  │   │
  │   ├─ Agent.Chat / Agent.RunStream
  │   │   ├─ Pipeline Middleware Chain
  │   │   │   ├─ Guard.Input (安全检查)
  │   │   │   ├─ Knowledge (知识注入)
  │   │   │   ├─ Timeout (上下文超时)
  │   │   │   ├─ Metrics (记录 token/延迟)
  │   │   │   │
  │   │   │   ├─ [核心] → Provider.ChatStream
  │   │   │   │   ├─ 断路器检查
  │   │   │   │   ├─ HTTP POST /v1/chat/completions (SSE)
  │   │   │   │   ├─ 逐 token 解析 SSE 事件
  │   │   │   │   └─ 返回 StreamEvent 通道
  │   │   │   │
  │   │   │   ├─ Tool Call 循环 (最多 N 轮)
  │   │   │   │   ├─ LLM 返回 tool_calls
  │   │   │   │   ├─ 执行工具（本地/MCP）
  │   │   │   │   ├─ 追加结果到消息列表
  │   │   │   │   └─ 再次调用 Provider
  │   │   │   │
  │   │   │   ├─ Guard.Output (输出检查)
  │   │   │   └─ Post-Hooks (异步持久化)
  │   │   │
  │   │   └─ StreamEvent → SSE/JSON 响应
  │   │
  │   └─ HTTP Response
  │
  └─ TelemetryHook (全链路记录)
```

---

## Provider 配置

### 设计理念

**全局默认 + 角色覆盖**：每个角色（Chat/Embedding/Guard）拥有独立的 Provider 实例。三者共享全局默认配置（API Key、Base URL），但可各自覆盖。

这意味着：
- 一个 API Key 用所有角色 → 只需设置全局默认
- Chat 和 Embedding 用不同模型 → 各自配置 Model
- Chat 和 Embedding 用不同厂商 → 各自配置 BaseURL + APIKey
- Guard 不启用 → 整个块不配置，自动 nil

### 配置结构

```yaml
# config/config.yaml
providers:
  # ── 全局默认（所有角色继承） ──
  api_key: "${YAI_PROVIDERS_API_KEY}"
  base_url: "https://api.openai.com/v1"

  # ── Chat（必须配置） ──
  chat:
    model: gpt-4o

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

# 场景 3：多 API Key 隔离
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

> DeepSeek API 兼容 OpenAI 格式，`NewOpenAIProvider` 可直接使用。其他不兼容 OpenAI 格式的厂商，实现 `provider.LLMProvider` 接口即可。

### ProviderSet

```go
// pkg/provider/provider.go
type ProviderSet struct {
    Chat      LLMProvider       // 必须（至少需要一个 Chat 模型）
    Embedding EmbeddingProvider // 可选，nil = 不启用向量检索
    Guard     GuardProvider     // 可选，nil = 不启用安全检查
}
```

框架在 `server.buildCore()` 中为每个角色构建独立的 Provider 实例：

```go
ps := &provider.ProviderSet{}
ps.Chat = openai.NewOpenAIProvider(&provider.ProviderConfig{
    Type: "openai", APIKey: "...", BaseURL: "...", Model: "gpt-4o",
})
ps.Embedding = openai.NewOpenAIProvider(&provider.ProviderConfig{
    Type: "openai", APIKey: "...", BaseURL: "...", Model: "text-embedding-3",
})
```

---

## Agent 系统

Agent 是框架的核心实体，封装了 LLM 调用、工具执行、记忆管理、MCP 集成和扩展系统。

### 结构

```go
type Agent struct {
    Config            Config
    Provider          provider.LLMProvider
    Pipeline          pipeline.Pipeline
    Tools             []tool.Tool
    Memory            memory.Store
    Skills            []Skill
    MCPRegistry       *mcp.Registry     // MCP 服务器注册表（可选）
    Extensions        map[string]Extension
    ComponentRegistry *component.Registry
}
```

Agent 支持热重载 — `ReloadConfig()` 和 `ReloadProvider()` 在运行时原子替换配置和 Provider，不影响进行中的请求。

### 配置

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
    MCP         MCPConfig            // MCP 服务器引用
}

type MCPConfig struct {
    Enabled bool     // 启用 MCP 工具
    Servers []string // MCP 服务器名称列表（来自 Registry）
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
    WithKnowledge(knowledge.New(myStore, knowledge.DefaultConfig())).
    Build()
```

`WithKnowledge()` 将知识组件挂载到 Agent 上，构建后可通过 `Agent.Knowledge()` 获取。未调用则 Agent 没有知识能力。

MCP 工具通过 `WithMCPRegistry()` 和 Config.MCP 配置：

```go
cfg := agent.Config{
    AgentID: "assistant",
    MCP: agent.MCPConfig{
        Enabled: true,
        Servers: []string{"filesystem", "weather"},
    },
}

ag, _ := cfg.ToBuilder().
    WithProvider(prov).
    WithPipeline(pipe).
    WithMCPRegistry(mcpRegistry).
    Build()
```

MCP 工具自动以 `<server>/<tool>` 格式注册到 Agent，可在 Chat() 中与普通工具一样被 LLM 调用。每条会话可通过 `types.MCPSessionConfig` 覆盖 MCP 配置。

### 工具调用循环

Agent 内置工具调用循环（最多可配置轮次）。当 LLM 返回 `tool_calls` 时，Agent 自动执行对应工具并将结果追加到对话中：

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

内置 `InMemoryStore` 实现，支持前缀匹配和子串搜索。可对接外部存储（pgvector、Redis）实现持久化。

### 扩展系统

Agent 通过 `Extension` 接口支持外部模块挂载：

```go
type Extension interface {
    ID() string
    Close() error
}
```

Extension 支持两个可选子接口：
- **`MiddlewareProvider`** — 扩展贡献中间件，构建时自动注入 Agent Pipeline
- **`ToolProvider`** — 扩展贡献 Agent 可调用工具，构建时自动注册到 Agent 工具列表

知识系统（Knowledge）同时实现了这两个接口：当 `AutoInject` 开启时贡献中间件，当配置了 WebSearchRetriever / WebFetchRetriever 时自动注册 `search_web` / `fetch_url` / `search_knowledge` 工具。

---

## 知识系统

框架内置**可插拔的知识系统**（`pkg/knowledge/`），每个 Agent 可独立选择是否挂载知识能力。

### 核心抽象

| 抽象 | 职责 | 内置实现 |
|------|------|----------|
| **Store** | 知识存储后端（增删查） | `InMemoryStore`（关键词/语义搜索） |
| **Retriever** | 单源检索策略 | `StoreRetriever` / `WebSearchRetriever` / `WebFetchRetriever` |
| **Knowledge** | 组件封装 + 多源聚合 + 工具注册 | 组合多个 Retriever，自动导出 Tool |

### 架构

```
┌─────────────────────────────────────────────────────────┐
│                    Knowledge 组件                         │
│  ┌──────────┐  ┌──────────────┐  ┌─────────────────┐   │
│  │ Store    │  │WebSearch     │  │ WebFetch        │   │
│  │Retriever │  │Retriever     │  │ Retriever       │   │
│  │(本地文档) │  │(互联网搜索)   │  │(网页抓取)       │   │
│  └────┬─────┘  └──────┬───────┘  └────────┬────────┘   │
│       └───────┬───────┘───────────────────┘            │
│               ▼                                        │
│      HybridRetriever (扇出→合并→去重→排序)              │
│               │                                        │
│        ┌──────┴──────┐                                 │
│        │  AutoInject  │  ← MiddlewareProvider           │
│        │  (中间件)     │                                  │
│        ├─────────────┤                                  │
│        │  search_web │                                  │
│        │  fetch_url  │  ← ToolProvider                  │
│        │search_knowl.│  (LLM 自主调用)                   │
│        └─────────────┘                                  │
└─────────────────────────────────────────────────────────┘
```

### 集成模式

**1. Auto-Inject 模式（中间件自动注入）**

开启后，每次 Chat() 前自动检索相关文档并注入系统提示词：

```go
cfg := knowledge.Config{AutoInject: true, TopK: 5}
kn := knowledge.New(myStore, cfg)

ag, _ := ac.ToBuilder().
    WithProvider(prov).
    WithPipeline(pipe).
    WithKnowledge(kn).
    Build()
```

**2. 手动模式（LLM 自主调用 Tool）**

配置 WebSearchRetriever 后，LLM 在对话中自主决定是否搜索互联网：

```go
webSearch := knowledge.NewWebSearchRetriever(mySearchAPIFunc)
webFetch := knowledge.NewWebFetchRetriever(myFetchFunc)

kn := knowledge.NewWithRetrievers(knowledge.DefaultConfig(),
    knowledge.NewStoreRetriever("docs", myStore),
    webSearch,
    webFetch,
)

ag, _ := ac.ToBuilder().
    WithProvider(prov).
    WithPipeline(pipe).
    WithKnowledge(kn).
    Build()
```

LLM 可在对话中调用的工具：

| 工具名 | 功能 | 条件 |
|--------|------|------|
| `search_knowledge` | 搜索所有注册的 Retriever | 始终可用 |
| `search_web` | 搜索互联网（调用 searchFn） | 需配置 WebSearchRetriever |
| `fetch_url` | 抓取指定 URL 的文本内容 | 需配置 WebFetchRetriever |

### Retriever 接口

```go
type Retriever interface {
    ID() string
    Retrieve(ctx context.Context, query string, opts ...SearchOption) ([]*Result, error)
}
```

内置实现：

| Retriever | 用途 | 构造方式 |
|-----------|------|----------|
| `StoreRetriever` | 包装任意 Store 为 Retriever | `NewStoreRetriever(id, store)` |
| `WebSearchRetriever` | 互联网搜索（可注入搜索函数） | `NewWebSearchRetriever(searchFn)` |
| `WebFetchRetriever` | URL 页面抓取（可注入 fetch 函数） | `NewWebFetchRetriever(fetchFn)` |
| `HybridRetriever` | 多 Retriever 并行扇出 + 去重合并 | `NewHybridRetriever(id, rs...)` |

### HybridRetriever 合并策略

`HybridRetriever.Retrieve()` 的执行流程：
1. **扇出** — 对所有子 Retriever 并行调用 `Retrieve()`
2. **合并去重** — 按 Result.ID 去重，同一 ID 保留最高得分
3. **阈值过滤** — 丢弃低于 `Threshold` 的低分结果
4. **降序排序** — 按 Score 从高到低排列
5. **TopK 截断** — 保留前 K 条结果

### 自定义 Store

实现 `Store` 接口即可接入任何后端（pgvector、Elasticsearch、MongoDB 等）：

```go
type MyVectorStore struct { ... }

func (s *MyVectorStore) Store(ctx context.Context, docs ...*knowledge.Document) error { ... }
func (s *MyVectorStore) Search(ctx context.Context, query string, opts ...knowledge.SearchOption) ([]*knowledge.Result, error) { ... }
func (s *MyVectorStore) Delete(ctx context.Context, ids ...string) error { ... }
func (s *MyVectorStore) Close() error { ... }
```

---

## MCP 集成

框架提供原生 **MCP（Model Context Protocol）集成**（`pkg/mcp/`），让 Agent 能够发现并调用外部 MCP 服务器暴露的工具。

### 设计目标

MCP 集成层解决的核心问题是：**Agent 如何调用外部工具系统**。无论是本地文件系统、远程 API、数据库，还是浏览器自动化，只要对方实现了 MCP 协议，Agent 就能无缝调用。

### 三层抽象

| 抽象 | 职责 | 实现 |
|------|------|------|
| **Client** | MCP 协议客户端（连接到实际 MCP 服务器） | StdioClient / SSEClient / WSClient / ReconnectClient / FuncClient |
| **Server + Registry** | Server 注册与管理 | 框架提供，宿主系统配置，Agent 引用 |
| **ToolAdapter** | 将 MCP 工具桥接为 framework `tool.Tool` | 框架内部 |

### 架构

```
┌─────────────────────────────────────────────────────┐
│                 Agent Chat                           │
│  解析 MCP 配置 → 从 Registry 解析工具 → 注入工具列表 │
└──────────────────────┬──────────────────────────────┘
                       │
┌──────────────────────▼──────────────────────────────┐
│                 MCP Registry                        │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐         │
│  │ Server A │  │ Server B │  │ Server C │         │
│  │(stdio)   │  │(SSE)     │  │(WebSocket)│         │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘         │
│       │             │             │                │
└───────┼─────────────┼─────────────┼────────────────┘
        │             │             │
   ┌────▼──┐    ┌────▼──┐    ┌────▼──┐
   │ Stdio │    │ SSE   │    │ WS    │
   │Client │    │Client │    │Client │
   └────┬──┘    └────┬──┘    └────┬──┘
        │             │             │
   (subprocess)  (HTTP SSE)   (WebSocket)
```

### Client 接口

```go
type Client interface {
    ListTools(ctx context.Context) ([]*ToolInfo, error)
    CallTool(ctx context.Context, name string, args json.RawMessage) (*CallResult, error)
    Close() error
}
```

接口设计为最小化——三个方法覆盖 MCP 协议的全部交互场景。宿主系统只需实现这三个方法即可集成任意 MCP 服务器。

### 传输实现

框架内置三种传输实现，覆盖主流 MCP 连接方式：

| Client | 传输方式 | 适用场景 |
|--------|----------|----------|
| **StdioClient** | 子进程 stdin/stdout | 本地 MCP 服务器（如 npx 安装的包） |
| **SSEClient** | HTTP SSE + POST | 远程 MCP 服务器（HTTP 可穿透） |
| **WSClient** | WebSocket | 远程 MCP 服务器（双向实时） |

**StdioClient** — 通过执行子进程并读写其 stdin/stdout 进行 JSON-RPC 通信：

```go
client := mcp.NewStdioClient("npx", "-y", "@modelcontextprotocol/server-filesystem", "/tmp")
client.Start(ctx)
defer client.Close()

// 惰性初始化（默认）：第一次 ListTools/CallTool 时自动完成 MCP 握手
// 急切初始化：
// client := mcp.NewStdioClientOpts("npx", []string{"-y", ...}, mcp.WithStdioLazyInit(false))
```

- 支持惰性/急切两种初始化模式
- 并发请求管理（`sync.Mutex` + `map[int]chan`）
- 10MB 最大消息缓冲区
- Windows 兼容的进程终止

**SSEClient** — 通过 HTTP Server-Sent Events 接收服务器推送，POST 发送请求：

```go
client := mcp.NewSSEClient("http://localhost:3001/mcp", nil)
client.Start(ctx)
defer client.Close()
```

- 自动解析 `endpoint` 事件获取 POST URL
- 异步 SSE 事件驱动响应分发
- 支持相对 URL 自动解析

**WSClient** — 通过 WebSocket 进行全双工通信：

```go
// 自定义 HTTP 头（用于鉴权）
header := http.Header{}
header.Set("Authorization", "Bearer token")

client := mcp.NewWSClient("ws://localhost:3001/mcp", nil, header)
client.Start(ctx)
defer client.Close()
```

- 支持 ws:// 和 wss://
- 自定义 HTTP 头（鉴权/跟踪）
- 并发请求管理

### ReconnectClient — 自动重连

包装任意 Client，在连接断开时自动重连：

```go
base := mcp.NewWSClient("ws://localhost:3001/mcp", nil, nil)
base.Start(ctx)

rc := mcp.NewReconnectClient(base, reconnectFn,
    mcp.WithMaxRetries(5),
    mcp.WithBaseDelay(100*time.Millisecond),
    mcp.WithMaxDelay(30*time.Second),
    mcp.WithStateHook(func(s mcp.ConnState) {
        log.Printf("mcp state: %s", s)
    }),
)
```

特性：
- 指数退避重连（`baseDelay * 2^attempt`，上限 `maxDelay`）
- 可配置最大重试次数（默认无限）
- `IsRetriable()` 判定 — 识别网络级临时错误（DNS 失败、TCP RST、连接超时、ECONNREFUSED 等）
- 状态机：`Connected → Reconnecting → Disconnected`
- 状态变更回调钩子

### 错误类型

```go
var (
    ErrConnectionClosed  = errors.New("mcp: connection closed")
    ErrTimeout           = errors.New("mcp: timeout")
    ErrServerError       = errors.New("mcp: server error")
    ErrMalformedResponse = errors.New("mcp: malformed response")
    ErrNotInitialized    = errors.New("mcp: not initialized")
    ErrShutdown          = errors.New("mcp: client is shutting down")
)

// IsRetriable(err) 判定该错误是否适合重试
func IsRetriable(err error) bool
```

`IsRetriable()` 覆盖 Go 标准库和系统级错误（`os.ErrDeadlineExceeded`、`syscall.ECONNREFUSED`、`syscall.ECONNRESET`、`syscall.ETIMEDOUT` 等），以及 Windows 平台特有错误（`connectex`、`No connection could be made`）。

### 使用 MCP 工具

```go
// 1. 创建 Client
client := mcp.NewStdioClient("npx", "-y", "@modelcontextprotocol/server-filesystem", "/tmp")
client.Start(ctx)

// 2. 注册为 Server
reg := mcp.NewRegistry()
reg.Add(mcp.NewServer("filesystem", client))

// 3. Agent 引用
cfg := agent.Config{
    AgentID: "assistant",
    MCP: agent.MCPConfig{Enabled: true, Servers: []string{"filesystem"}},
}

ag, _ := cfg.ToBuilder().
    WithProvider(prov).
    WithPipeline(pipe).
    WithMCPRegistry(reg).   // 传入宿主系统的 Registry
    Build()
```

**会话级 MCP 覆盖** — 每条对话可独立配置：

```go
// 会话 A：禁用 MCP
input1 := &types.ChatInput{
    Messages: []types.Message{{Role: "user", Content: "Hello"}},
    MCP: &types.MCPSessionConfig{Enabled: ptr(false)},
}

// 会话 B：仅使用 filesystem
input2 := &types.ChatInput{
    Messages: []types.Message{{Role: "user", Content: "List files"}},
    MCP: &types.MCPSessionConfig{Enabled: ptr(true), Servers: []string{"filesystem"}},
}
```

会话 MCP 配置优先级：

| 场景 | 行为 |
|------|------|
| `MCP = nil` | 使用 Agent 默认 MCPConfig |
| `MCP.Enabled = nil` | 继承 Agent 的 Enabled 标志 |
| `MCP.Enabled = &false` | 强制禁用 |
| `MCP.Servers = nil` | 继承 Agent 的 Servers |
| `MCP.Servers = []string{}` | 空列表 = 不加载任何 Server |

### 工具命名规则

MCP 工具通过 `ToolAdapter` 桥接为 `tool.Tool`，命名格式为 `<server>/<tool>`：

```
server: "filesystem", tool: "read_file" → Agent 工具名: "filesystem/read_file"
```

Agent 的 Tool Call 循环解析完整名，匹配 MCP 工具时自动调用对应 Server Client 的 `CallTool()`。

---

## 技能系统

技能是**可复用的能力包**——将工具和指令组合为自包含的 Skill，按需挂载到 Agent 上。

### 设计理念

技能系统的诞生源于一个观察：大多数 Agent 工具不是独立存在的，它们总是搭配"怎么用"的指令。例如天气工具需要告诉 LLM "调用此工具获取天气时，需要先确认用户所在城市"。

技能 = 工具集合 + 使用说明 + 元数据。

### 接口

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

### 内置技能

| 技能 | 工具 | 说明 |
|------|------|------|
| `TimeSkill` | `get_current_time` | 获取当前时间/日期 |
| `EchoSkill` | `echo` | 回显输入 |
| `WeatherSkill` | `get_weather` | 模拟天气查询 |

### 使用

```go
reg := skills.NewRegistry()
reg.Register(builtin.TimeSkill())
reg.Register(builtin.WeatherSkill())

// 语义匹配 — 自动找最相关的技能
best := reg.Match("what time is it")  // → TimeSkill

// 挂载到 Agent
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

框架内置安全守卫，通过 Guard 接口实现输入/输出内容过滤。

```go
type Guard struct {
    Provider provider.GuardProvider  // nil = 跳过所有检查
    Config   types.SafetyConfig
}

func (g *Guard) Check(ctx context.Context, text string) (bool, error)
func (g *Guard) CheckInput(ctx context.Context, text string) error
func (g *Guard) CheckOutput(ctx context.Context, text string) error
```

- `Guard.Provider` 为 nil → 所有 `Check` 返回 `(true, nil)`（安全默认）
- `SafetyConfig.Enabled` 为 false → 跳过所有检查
- `CheckInput` / `CheckOutput` 各自有独立开关

---

## 模块系统

### Module 接口

```go
type Module interface {
    ID() string                                // 唯一标识，用于配置和路由前缀
    Config() any                               // 配置结构体指针
    Init(ctx *Context) error                   // 初始化（注册路由/健康检查/中间件）
    Start(ctx context.Context) error           // 启动后台协程
    Stop(ctx context.Context) error            // 优雅关闭
}
```

### 生命周期

```
1. Load config (框架 + 模块配置)
2. Create ProviderSet
3. Create pipeline, stores, registry
4. Seed agents + teams
5. For each module: Init(ctx) → 注册路由、健康检查、中间件
6. Setup Gin engine
7. For each module: Start(ctx)
8. Serve HTTP (阻塞)
9. SIGINT/SIGTERM:
   a. For each module (逆序): Stop(ctx)
   b. HTTP server shutdown, flush post-hooks, close providers
```

### InitContext

```go
type Context struct {
    ModuleConfig  any
    Logger        *slog.Logger
    Router        func() gin.IRouter
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
    Enabled  bool   `mapstructure:"enabled"`
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

```go
srv, err := server.New(cfg,
    server.WithSeed(seedAgents),
    server.WithTeam(seedTeam),
    server.WithModule(&crm.Module{}),
)
```

---

## 多 Agent 团队

框架提供原生多智能体团队支持，通过 **Supervisor-Worker** 模式实现 Agent 间的任务委派和协作。

### 核心概念

| 概念 | 说明 |
|------|------|
| **Agent.AsTool()** | 将 Agent 包装为可被其他 Agent 调用的 Tool |
| **Team** | Supervisor（编排者）+ Members（工作者）组成的团队 |
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

### 使用

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

## 配置系统

配置加载优先级（高 → 低）：

| 优先级 | 来源 | 示例 |
|--------|------|------|
| 1 | 命令行参数 | `--providers.chat.api-key=sk-xxx` |
| 2 | 环境变量 | `YAI_PROVIDERS_CHAT_API_KEY=sk-xxx` |
| 3 | YAML 文件 | `config/config.yaml` |
| 4 | `.env` 文件 | 自动加载，缺失不报错 |

### 热重载

内置 `fsnotify` 热重载，检测到 `config/config.yaml` 变更时自动执行：
1. 比较 Chat Provider 配置是否变化
2. 若有变化：创建新 Provider + Pipeline
3. 下发新配置到所有已注册 Agent（`ReloadConfig` + `ReloadProvider`）
4. 原子交换（`sync.RWMutex` 写锁）
5. 旧 Provider 在无请求引用后自动关闭

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
    "stream": true,             // SSE 流式 / JSON
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

---

## 包结构

```
cmd/
  server/             HTTP 服务器入口（种子 Agent/Team 构建）
internal/
  config/             服务端配置结构体
  handler/            HTTP 处理器
  middleware/         Gin 中间件（JWT、CORS、限流、日志、Telemetry）
  router/             路由注册
  server/             Server 构建器 + 模块生命周期 + 热重载
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
  knowledge/          知识系统（Store/Retriever 接口 + 多源检索 + 工具注册）
  mcp/                MCP 集成（Client 接口 + 三种传输实现 + 重连 + Registry + ToolAdapter）
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
  store/              持久化存储（AgentStore 接口 + MySQL/PG/Redis/InMemory 实现）
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
  06-mcp-client/      MCP 客户端示例（stdio/SSE/WS/Reconnect）
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

---

## 许可证

MIT
