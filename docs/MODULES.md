# Kocort 模块与职责地图

本文把仓库中最重要的目录和包按职责整理成一张“模块地图”，方便在读代码或补功能时快速定位。

## 1. 顶层目录

| 目录 | 作用 |
| --- | --- |
| `cmd/kocort` | CLI / Gateway 主入口；默认无参数时直接启动网关 |
| `cmd/kocort-desktop` | 桌面壳入口；启动 Runtime + API Server，并接平台托盘/菜单栏 |
| `api` | Gin 路由、HTTP handler、API service 组装、静态资源服务 |
| `runtime` | 系统中枢；负责装配各子系统并执行 Agent run pipeline |
| `internal` | 领域实现层；按 backend、session、task、tool、memory 等拆包 |
| `web` | Next.js 前端工程；构建产物嵌入 Go API 提供的静态站点 |
| `local-config` | 本地运行示例配置与运行态数据目录 |
| `defaults` | 默认 catalog / 示例模型与渠道模板 |
| `desktop` | macOS / Windows / Linux 的桌面包装资源 |

## 2. `api` 层

### 2.1 handler 分组

- `handlers/workspace.go`：聊天、历史、媒体、任务管理
- `handlers/engine.go`：brain、capabilities、sandbox、数据源、技能导入安装
- `handlers/system.go`：dashboard、audit、环境变量、网络设置
- `handlers/rpc.go`：兼容型 RPC 接口、健康检查、渠道入站、简易 webchat
- `handlers/events.go`：SSE 事件输出

### 2.2 service 分组

- `service/brain.go`：云端模型配置、provider 健康、默认/回退模型
- `service/brain_local.go`：本地大脑生命周期与参数更新
- `service/cerebellum.go`：小脑生命周期、下载、帮助、参数更新
- `service/dashboard.go`：系统状态快照
- `service/config.go`：运行时配置修改与持久化辅助
- `service/task.go`：Task API 请求到调度参数的转换

### 2.3 API 层角色

`api` 层尽量保持“薄”：

- 做参数校验、JSON 绑定、HTTP 状态码转换
- 调用 `runtime.Runtime` 暴露的方法
- 对配置编辑场景调用 `api/service` 中的纯业务辅助函数

## 3. `runtime` 层

`runtime.Runtime` 是整个项目的核心服务容器，聚合：

- 配置与配置存储
- Environment / Logger / Audit
- SessionStore / MemoryProvider
- BackendRegistry / ToolRegistry / PluginRegistry
- Deliverer / EventHub / ChannelManager
- TaskScheduler / HeartbeatRunner / FollowupQueue / ActiveRunRegistry
- SubagentRegistry / ProcessRegistry / ToolLoopRegistry
- BrainLocal / Cerebellum

运行时层的关键文件：

- `runtime_builder.go`：装配所有子系统并启动后台 runner
- `runtime_run.go`：单次 Agent 执行的总入口
- `pipeline_validate.go` ~ `pipeline_execute.go`：六阶段 pipeline
- `runtime_core.go`：运行时配置热更新、环境重载、服务 getter
- `runtime_chat.go`：WebChat / RPC 场景的聊天入口
- `runtime_gateway.go`：Gateway 配置快照与持久化桥接

## 4. `internal` 领域包

### 4.1 运行模型与配置

- `internal/config`：配置结构、默认值、JSON merge、路径解析、身份构建
- `internal/core`：共享类型与核心协议，尽量保持低依赖
- `internal/rtypes`：运行时接口抽象，削弱包间耦合

### 4.2 模型后端

- `internal/backend`：统一模型后端抽象与 provider/backend 解析
- `internal/localmodel`：本地 GGUF 模型生命周期管理
- `internal/cerebellum`：工具调用安全审查和本地帮助模式

### 4.3 交互与投递

- `internal/channel`：外部渠道适配器管理、配置解析、后台 runner 重启
- `internal/delivery`：reply dispatcher、delivery queue、router deliverer、transcript mirror
- `internal/gateway`：HTTP 服务辅助、地址解析、SSE/EventHub、鉴权辅助

### 4.4 Agent 能力层

- `internal/tool`：内置工具定义、注册表、审批、循环检测、进程管理
- `internal/skill`：技能扫描、快照、命令分发、安装和运行时环境覆盖
- `internal/memory`：工作区记忆索引、lexical/vector/qmd 混合召回
- `internal/plugin`：插件启停策略、环境变量覆盖

### 4.5 会话与异步编排

- `internal/session`：会话解析、key 规则、transcript、reset、fork、可见性策略
- `internal/task`：定时任务、活跃运行、跟进队列、子代理注册
- `internal/heartbeat`：定期唤醒与磁盘预算清理

### 4.6 基础设施

- `internal/infra`：日志、审计、动态代理 HTTP 客户端、环境解析、提示上下文文件
- `internal/event`：事件和审计记录的通用接口与实现桥接
- `internal/sandbox`：工具沙箱辅助能力

## 5. 前端与嵌入资源

- `web`：Next.js 15 前端工程
- `api/static/dist`：构建后写入的嵌入式静态资源目录
- `api/static_embed.go`：Go embed 静态资源入口
- `www`：仓库中已经存在的站点资源 / 品牌资源

## 6. 典型阅读路径

如果你要理解：

- **程序如何启动**：先看 `cmd/kocort/main.go` → `runtime/runtime_builder.go` → `api/server.go`
- **一次消息如何执行**：看 `runtime/runtime_run.go` 与 `runtime/pipeline_*.go`
- **配置如何生效**：看 `internal/config/loader.go`、`store.go`、`identity.go`
- **接口如何落到运行时**：看 `api/routes.go` → `api/handlers/*.go` → `runtime/*.go`
- **任务/子代理如何运转**：看 `internal/task/task_scheduler.go`、`spawn_service.go`、`runtime/runtime_subagent.go`

## 7. 模块边界判断建议

给仓库继续加功能时，建议沿用当前边界：

- 纯 HTTP 适配逻辑放 `api/handlers`
- 配置编辑和状态拼装放 `api/service`
- 需要跨子系统协同的能力放 `runtime`
- 可独立测试的领域逻辑放 `internal/*`
- 共享数据结构优先沉到 `internal/core`
