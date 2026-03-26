# Kocort 项目架构

本文从“分层、装配、数据流、运行模式”四个角度描述当前 `kocort` 的架构。

## 1. 系统目标

Kocort 想解决的不是“单个模型调用”，而是把一个桌面级 Agent 系统打包成可本地启动、可 GUI 管理、可多渠道接入的运行时：

- 默认启动 HTTP gateway 与嵌入式前端
- 支持多模型后端和本地模型
- 支持技能、工具、任务、子代理、记忆、审计
- 支持外部渠道接入与桌面壳包装

## 2. 顶层分层

```text
cmd/*                    入口层
  ↓
api/                     接口层（HTTP / RPC / SSE / static）
  ↓
runtime/                 编排层（系统中枢）
  ↓
internal/*               领域层（backend / session / task / tool / ...）
  ↓
utils/                   通用工具
```

分层原则：

- `cmd` 只负责启动和参数决策
- `api` 只负责请求适配和返回格式
- `runtime` 协调所有子系统
- `internal/*` 提供相对独立、可测试的领域实现

## 3. 两个启动模式

### 3.1 CLI / Gateway 模式

`cmd/kocort/main.go` 支持两种运行方式：

- 不带参数：默认进入 `-gateway` 模式，启动 Gin 服务器
- 带 `-message`：直接执行一次 agent run

入口会先：

1. 解析 `configDir` / `stateDir`
2. 加载并 merge 运行时配置
3. 解析相对路径
4. 构建 `runtime.Runtime`
5. 决定启动 API Server 还是执行单次消息

### 3.2 Desktop 模式

`cmd/kocort-desktop/main.go` 会：

- 使用桌面默认配置目录
- 启动同一套 Runtime + API Server
- 交给平台托盘/菜单栏壳进行控制

因此桌面端本质上不是另一套后端，而是同一运行时的不同包装。

## 4. Runtime 是系统中枢

`runtime.Runtime` 聚合了主要服务实例，因此它既是：

- Agent 执行器
- 服务容器
- API 层共享的门面
- 配置热更新落点

它内部包含的关键服务有：

- `Config` / `ConfigStore`
- `Environment` / `Logger` / `Audit`
- `Sessions` / `Memory`
- `Backends` / `Backend`
- `Tools` / `Plugins` / `Approvals`
- `Deliverer` / `EventHub` / `Channels`
- `Tasks` / `Queue` / `ActiveRuns` / `Heartbeats`
- `Subagents` / `Processes` / `ToolLoops`
- `Cerebellum` / `BrainLocal`

## 5. Builder 装配顺序

`runtime/runtime_builder.go` 中的 `Build()` 大体按下面顺序工作：

1. 解析 `stateDir`、默认 agent identity、代理配置
2. 初始化 session store、audit、logger、environment
3. 初始化 deliverer、event hub、channel manager
4. 构建 identity resolver 与 memory manager
5. 注册默认工具与 plugin registry
6. 初始化 `cerebellum` 与 `brainLocal`
7. 组装 `Runtime`
8. 启动任务调度、心跳与渠道后台 runner
9. 记录初始化审计事件
10. 视配置自动启用本地模型 / 小脑相关能力

这意味着大多数系统能力都在 Runtime 构建时一次性就绪。

## 6. 核心运行数据流

### 6.1 WebChat / RPC 请求

```text
HTTP Request
  → api/handlers
  → runtime.ChatSend / runtime.Run
  → pipeline.validate
  → pipeline.resolve
  → pipeline.gateQueue
  → pipeline.loadContext
  → pipeline.buildRunContext
  → pipeline.execute
  → delivery + transcript + event hub
```

### 6.2 外部渠道入站

```text
POST /channels/:channelID
  → channel adapter ServeHTTP
  → runtime inbound / chat path
  → 同一套 Run Pipeline
```

### 6.3 配置修改

```text
API save request
  → api/service.ModifyAndPersist
  → runtime.ApplyConfig
  → logger/env/proxy/channels/plugins/memory/backends 热更新
```

## 7. 模型架构

Kocort 当前支持三类模型角色：

- **Cloud brain**：普通 provider 后端，负责主推理
- **brainLocal**：本地主推理；`brainMode=local` 时接管整个主流程
- **cerebellum**：本地小模型，仅在 cloud 模式下对工具调用做审查或帮助配置

### 7.1 backend registry 的决策

`internal/backend/backend_registry.go` 根据 provider 的 `api` 字段分派到：

- `openai-completions` / 空值 → OpenAI 兼容后端
- `anthropic-messages` → Anthropic 兼容后端
- `command` → 本地命令后端
- `cli` → CLI backend

本地脑模式下则由 `LocalModelBackend` 直接承担运行。

## 8. 会话与状态存储

`internal/session.SessionStore` 负责：

- 解析或创建 session key
- 维护 `sessions.json`
- 维护 transcript JSONL 文件
- reset / fork / freshness / visibility / binding
- 维护 session 级 provider/model/thinking/verbose 等持久状态

会话状态目录默认位于 `stateDir` 下，既服务 WebChat，也服务任务和子代理。

## 9. 异步与后台能力

系统的后台编排主要由四类组件承担：

- `TaskScheduler`：once / every / cron 任务
- `HeartbeatRunner`：周期性唤醒 agent
- `FollowupQueue`：活跃运行冲突后的排队跟进
- `SubagentRegistry`：子代理生命周期与公告释放

这些能力都挂在 Runtime 上，而不是独立服务进程。

## 10. API 与前端架构

API 层是 Gin；前端是 Next.js。构建时：

1. `web` 生成静态资源
2. 资源同步到 `api/static/dist`
3. Go 二进制用 embed 打包这些资源
4. Gateway 统一对外提供 API 与页面

这让项目保持“单二进制 + 嵌入前端”的部署模式。

## 11. 架构上的几个关键判断

### 11.1 Runtime 优先于分布式拆分

当前架构显然偏向单进程内聚：

- 简化桌面和本地部署
- 降低配置分散度
- 便于会话、任务、记忆、审计共享状态

### 11.2 `internal/core` 是低耦合基础层

很多共享类型和协议下沉到 `internal/core`，目的是避免 `runtime ↔ internal/*` 之间形成环状依赖。

### 11.3 API 与领域逻辑显式分离

大量复杂业务没有直接写在 handler 中，而是拆到 `runtime` 与 `api/service`，这是后续继续演进时应当坚持的边界。
