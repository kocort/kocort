<p align="center">
  <img src="web/public/logo.svg" width="120" alt="Kocort Logo" />
</p>

<h1 align="center">Kocort</h1>

<p align="center">
  <b>桌面级 AI Agent 助手 — 双脑架构 · 零命令行 · 全平台支持</b>
</p>

<p align="center">
  <a href="https://github.com/kocort/kocort/releases"><img src="https://img.shields.io/github/v/release/kocort/kocort" alt="Latest Release" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/kocort/kocort" alt="License" /></a>
  <img src="https://img.shields.io/badge/Go-1.23-00ADD8?logo=go" alt="Go 1.23" />
  <img src="https://img.shields.io/badge/Platforms-macOS%20%7C%20Windows%20%7C%20Linux-222222" alt="Platforms" />
</p>

<p align="center">
  <a href="#快速开始">快速开始</a> ·
  <a href="#核心特性">核心特性</a> ·
  <a href="#架构概览">架构</a> ·
  <a href="#api-参考">API</a> ·
  <a href="docs/README.md">文档入口</a> ·
  <a href="README.md">English</a>
</p>

---

## 什么是 Kocort？

Kocort 是一个**桌面级 AI Agent 应用**，类似 [OpenClaw](https://github.com/openclaw/openclaw)，让不懂命令行的用户也能零门槛拥有自己的个人 AI 助理。

它是一个**开箱即用的完整 AI 助手**——内置会话管理、工具编排、技能系统、子代理调度和多层安全防护，下载即用，无需复杂命令行操作。

### 核心优势

| | 特性 | 描述 |
|---|---|---|
| 🧠 | **双脑架构** | 云端大脑（强力推理）+ 本地小脑（离线安全审查），敏感信息永不出设备 |
| 🖥️ | **零命令行** | 全部操作通过图形界面完成，开箱即用 |
| 📦 | **单文件部署** | 编译为单个可执行文件，内嵌 Web 前端 |
| 🔒 | **主动安全** | 静态规则拦截 + 小脑语义审查，双重防护 |
| 🏠 | **纯本地运行** | 内置支持本地模型，无需任何云端 API 即可使用 |
| 🌍 | **全平台支持** | 原生支持 Windows / macOS / Linux，一套代码多平台编译分发 |
| 📊 | **图形化管理** | 技能与任务的可视化管理面板，安装 / 配置 / 调度全程图形操作 |

---

## 核心特性

### 🧠 双脑架构（Dual-Brain）

Kocort 首创的安全架构——大脑负责推理，小脑负责审查：

```
                 ┌─────────────────┐
                 │     用户请求     │
                 └────────┬────────┘
                          ▼
              ┌───────────────────────┐
              │     🧠 大脑 (Brain)    │
              │    负责推理 & 决策      │
              │   云端或本地 LLM 驱动   │
              └───────────┬───────────┘
                          │ 产生 tool_call
                          ▼
              ┌───────────────────────┐
              │  🛡️ 小脑 (Cerebellum)  │
              │   负责安全审查 & 拦截   │
              │   本地模型 · 完全离线   │
              │                       │
              │  approve → ✅ 放行执行  │
              │  flag    → ⚠️ 标记+执行 │
              │  reject  → ❌ 拦截阻断  │
              └───────────┬───────────┘
                          │ ✅
                          ▼
              ┌───────────────────────┐
              │       工具执行         │
              └───────────────────────┘
```

- **大脑（Brain）**：云端/远程 LLM 负责推理决策（支持 OpenAI、Anthropic 等）
- **小脑（Cerebellum）**：本地 1.7B 量化模型，完全离线运行
  - 对每一条 `tool_call` 做语义安全审查（意图一致性、数据外泄、注入攻击等）
  - 审查结果：`approve` / `flag` / `reject`，附带风险等级和原因
  - 智能跳过低风险只读操作，不影响性能
  - 优雅降级：小脑不可用时自动放行，不阻塞正常流程
- **本地大脑（Brain Local）**：本地模型也可完全替代云端后端运行 Agent

### 📡 多渠道集成

开箱即用的消息渠道适配器：

- 飞书（Feishu） · Telegram · Discord · Slack · WhatsApp · Zalo
- Generic Webhook（自定义接入）
- WebChat（内置 Web 聊天界面）

### 🛠️ 工具系统

30+ 内置工具，完整的管控体系：

**文件操作**：
- `read` / `write` / `edit` / `apply_patch` — 文件读写与编辑
- `grep` / `find` / `ls` — 文件搜索与目录浏览

**命令执行**：
- `exec` — Shell 命令执行（支持 PTY）
- `process` — 后台进程管理

**Web & 浏览器**：
- `web_search` — 网页搜索
- `web_fetch` — URL 内容获取与提取
- `browser` — 浏览器自动化（导航 / 截图 / PDF / 控制台）

**记忆系统**：
- `memory_search` / `memory_get` — 持久化工作区记忆搜索与获取

**会话 & 子代理**：
- `session_status` — 会话状态查看与模型切换
- `sessions_list` / `sessions_history` — 会话列表与历史
- `sessions_send` / `sessions_spawn` / `sessions_yield` — 跨会话通信与子代理
- `subagents` — 子代理编排（list / kill / info / steer / send）
- `agents_list` — 可用 Agent 列表

**消息 & 媒体**：
- `send` — 主动发送消息、文件或图片
- `image` — 图像分析
- `image_generate` — 图像生成

**自动化**：
- `cron` — 定时任务调度（one-shot / interval / cron 表达式）
- `canvas` — Canvas 展示与交互

**安全管控**：
- Tool Policy — allow/deny/profile/group 策略
- Sandbox — 沙箱隔离（目录白名单 + 只读保护）
- Elevated Gate — 高危操作分级拦截（low/medium/high/critical）
- Tool Approval — 敏感工具动态审批
- 循环检测 — 重复/乒乓调用自动熔断

### 🔄 管线式执行引擎

每次 Agent 运行经过 6 个独立可测试阶段：

```
validate → resolve → gateQueue → loadContext → buildRunCtx → execute
```

1. **validate** — 就绪检查、输入校验
2. **resolve** — 身份 / 会话 / 命令解析
3. **gateQueue** — active-run 互斥、队列/丢弃决策
4. **loadContext** — workspace / transcript / skills / memory 加载
5. **buildRunCtx** — 工具过滤、RunContext 组装
6. **execute** — 技能分发或模型调用循环 + 重试

### 📋 更多特性

- **技能系统** — 发现 / 快照 / 命令分发 / 隐式注入 / 安装 / 远程技能
- **记忆系统** — workspace 文件 lexical recall + hybrid recall
- **子代理编排** — spawn / registry / completion announce / send / steer
- **任务调度** — one-shot / interval / cron 三种调度模式
- **心跳机制** — 定期唤醒、条件评估
- **可恢复投递** — WAL 队列 / replay / hooks / chunking / transcript mirror
- **审计仪表盘** — 实时运行状态、审计日志查询

---

## 架构概览

```
┌─────────────────────────────────────────────┐
│            cmd/kocort/main.go               │  入口层
├─────────────────────────────────────────────┤
│              api/ (Gin)                     │  HTTP API 层
├─────────────────────────────────────────────┤
│            runtime/ (Runtime)               │  编排层 — 系统中枢
├─────────────────────────────────────────────┤
│              internal/                      │  领域层
│  ┌────────┬────────┬──────────┬──────────┐  │
│  │  core  │ config │ backend  │ channel  │  │
│  │  tool  │  task  │ session  │  infra   │  │
│  │delivery│heartbeat│  skill  │ sandbox  │  │
│  │cerebellum│localmodel│memory│  event   │  │
│  └────────┴────────┴──────────┴──────────┘  │
├─────────────────────────────────────────────┤
│              utils/                         │  工具函数
└─────────────────────────────────────────────┘
```

**依赖方向**：

```
cmd/kocort → api → runtime → internal/config → internal/core
                                    ↓
                          internal/{domain packages}
```

**关键约束**：
- `internal/core` 零内部依赖（只依赖标准库）
- domain 包之间不互相依赖，通过 `core` 共享类型
- 渐进式迁移：类型提取已完成，实现文件按需迁入

---

## 安全模型

Kocort 实现了多层纵深防御：

| 层级 | 机制 | 描述 |
|------|------|------|
| L1 | **工具策略** | allow/deny/profile/group，子代理深度限制 |
| L2 | **提权门控** | 高危操作分级拦截（low → critical），需用户确认 |
| L3 | **沙箱隔离** | 文件操作限制在授权目录，系统关键路径只读 |
| L4 | **工具审批** | 敏感工具需通过动态审批流程 |
| L5 | **循环检测** | 重复 / 乒乓调用自动熔断，全局熔断器保护 |
| L6 | **小脑语义审查** | 本地模型对 tool_call 做意图一致性、注入攻击、数据外泄等语义分析 |
| L7 | **会话隔离** | 会话间访问控制，4 级可见性策略（self/tree/agent/all） |

---

## 快速开始

### 环境要求

- Go 1.23+
- Node.js 20+ 与 npm（用于构建内嵌 `web/` 前端）
- （可选）llama.cpp 共享库用于本地模型推理（首次使用时自动下载，或设置 `KOCORT_LLAMA_LIB_DIR`）

### 编译

```bash
# 先安装一次前端依赖
cd web && npm install && cd ..

# 一键打包：
# 1. 编译 web/ 为静态站点
# 2. 同步到 Go embed 目录
# 3. 为当前平台生成 Go 二进制
./scripts/build.sh
```

前端构建产物会在打包阶段同步到 `api/static/dist`，最终由 Go API 层直接从可执行文件内提供静态资源服务。

默认输出路径为 `dist/<goos>_<goarch>/kocort`。

### 配置

Kocort 支持三文件分离配置：

```
local-config/
├── kocort.json    # 主配置
├── models.json      # 模型/Provider 配置
└── channels.json    # 渠道配置
```

**最小配置**（`models.json`）：

```json
{
  "models": {
    "providers": {
      "openai": {
        "api": "openai-completions",
        "baseUrl": "https://api.openai.com/v1",
        "apiKey": "${OPENAI_API_KEY}",
        "models": [
          {
            "id": "gpt-4o-mini",
            "name": "gpt-4o-mini"
          }
        ]
      }
    }
  }
}
```

### 运行

```bash
# Apple Silicon macOS 示例
./dist/darwin_arm64/kocort
```

`cmd/kocort` 在无参数时会默认启动 HTTP 服务，默认访问地址为 `http://127.0.0.1:18789`。

CLI 模式下的默认配置目录解析顺序：

1. `KOCORT_CONFIG_DIR`
2. 如果当前目录已存在 `./.kocort`
3. 如果用户目录已存在 `~/.kocort`
4. 否则默认使用 `./.kocort`

### 桌面版构建

桌面打包使用独立脚本：

```bash
# macOS 菜单栏应用
./scripts/build-desktop.sh --macos

# Windows 托盘应用
./scripts/build-desktop.sh --windows
```

签名、DMG、公证、托盘图标与资源嵌入等细节请参考 `desktop/macos/README.md` 与 `desktop/windows/README.md`。

---

## 文档速览

根目录文档体系已经按当前实现重写，建议从这里进入：

| 文档 | 内容重点 |
|------|----------|
| [docs/README.md](docs/README.md) | 阅读顺序与文档范围 |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | 分层、启动模式、Runtime 中枢架构 |
| [docs/RUNTIME_PIPELINE.md](docs/RUNTIME_PIPELINE.md) | RuntimeBuilder、6 阶段执行管线、投递与持久化 |
| [docs/MODULES.md](docs/MODULES.md) | `api`、`runtime`、`internal`、`web` 模块地图 |
| [docs/CONFIGURATION.md](docs/CONFIGURATION.md) | 配置加载、路径解析、热更新、brain 模式 |
| [docs/API_REFERENCE.md](docs/API_REFERENCE.md) | HTTP / RPC / channel webhook 路由地图 |
| [docs/CLIENTS_AND_DEPLOYMENT.md](docs/CLIENTS_AND_DEPLOYMENT.md) | Web 前端、桌面壳、嵌入式资源和构建发布 |
| [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) | 本地开发、构建测试、扩展边界 |

---

## API 参考

### 聊天交互

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/workspace/chat/bootstrap` | 聊天初始化 |
| POST | `/api/workspace/chat/send` | 发送消息 |
| POST | `/api/workspace/chat/cancel` | 取消运行 |
| GET | `/api/workspace/chat/history` | 聊天历史 |
| GET | `/api/workspace/chat/events` | SSE 事件流 |

### 引擎管理

| 方法 | 路径 | 说明 |
|------|------|------|
| GET/POST | `/api/engine/brain/*` | 大脑配置 / 模型管理 |
| POST | `/api/engine/brain/cerebellum/start\|stop\|restart` | 小脑生命周期 |
| POST | `/api/engine/brain/local/start\|stop\|restart` | 本地大脑生命周期 |
| POST | `/api/engine/brain/mode` | 大脑模式切换 |

### 系统管理

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/healthz` | 健康检查 |
| GET | `/api/system/dashboard` | 仪表盘 |
| POST | `/api/system/audit/list` | 审计日志 |

### 渠道 & RPC

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/channels/:channelID` | 渠道 Inbound Webhook |
| POST | `/rpc/chat.send` | RPC 聊天发送 |
| GET | `/rpc/chat.events` | RPC SSE 事件流 |

> 完整 API 文档请参阅 [docs/API_REFERENCE.md](docs/API_REFERENCE.md)

---

## 项目结构

```
kocort/
├── cmd/kocort/           # CLI + HTTP 服务入口
├── cmd/kocort-desktop/   # 桌面托盘 / 菜单栏入口
├── api/                  # HTTP API (Gin)
├── runtime/              # Runtime 编排核心
├── internal/
│   ├── core/             # 共享类型（零依赖）
│   ├── config/           # 配置结构体与加载
│   ├── backend/          # LLM Backend 抽象
│   ├── channel/          # 渠道集成适配器
│   ├── cerebellum/       # 小脑安全审查
│   ├── localmodel/       # 本地模型生命周期
│   ├── tool/             # 工具注册与执行
│   ├── session/          # 会话管理
│   ├── task/             # 任务调度
│   ├── delivery/         # 消息投递
│   ├── heartbeat/        # 心跳调度
│   ├── skill/            # 技能系统
│   ├── infra/            # 基础设施（日志/审计）
│   └── sandbox/          # 沙箱执行
├── web/                  # 前端 (Next.js)
├── desktop/              # macOS / Windows 桌面打包资源
├── api/static/dist/      # 前端构建后嵌入的静态资源
├── utils/                # 工具函数
├── defaults/             # 默认配置示例
├── local-config/         # 本地运行配置
├── docs/                 # 文档
└── scripts/              # 构建脚本
```

---

## 技术栈

| 组件 | 技术 |
|------|------|
| **语言** | Go 1.23 |
| **HTTP 框架** | Gin |
| **LLM SDK** | go-openai, anthropic-sdk-go |
| **本地推理** | llama.cpp (purego 动态加载), GGUF 格式 |
| **WebSocket** | gorilla/websocket |
| **前端** | Next.js |
| **任务调度** | robfig/cron |
| **文件监控** | fsnotify |

---

## 文档索引

| 文档 | 说明 |
|------|------|
| [README.md](docs/README.md) | 文档入口与推荐阅读顺序 |
| [ARCHITECTURE.md](docs/ARCHITECTURE.md) | 项目架构与主要子系统说明 |
| [RUNTIME_PIPELINE.md](docs/RUNTIME_PIPELINE.md) | RuntimeBuilder 与 6 阶段执行管线 |
| [MODULES.md](docs/MODULES.md) | `api`、`runtime`、`internal`、`web` 模块职责地图 |
| [CONFIGURATION.md](docs/CONFIGURATION.md) | 配置结构、加载规则、热更新与 local-config 用法 |
| [API_REFERENCE.md](docs/API_REFERENCE.md) | HTTP / RPC 接口参考 |
| [CLIENTS_AND_DEPLOYMENT.md](docs/CLIENTS_AND_DEPLOYMENT.md) | Web 前端、桌面壳与发布方式 |
| [DEVELOPMENT.md](docs/DEVELOPMENT.md) | 本地开发与扩展指南 |

---

## 与 OpenClaw 的对比

| 特性 | OpenClaw | Kocort |
|------|----------|--------|
| 交互方式 | CLI | 图形界面（零命令行） |
| 安全模型 | 静态规则 | 双脑架构（静态规则 + 语义审查） |
| 本地模型 | 不支持 | 内置 llama.cpp 推理 |
| 部署方式 | CLI 工具 | 单文件 + Web 界面 |
| 离线运行 | 不支持 | 完全支持 |
| 渠道集成 | 终端 | 飞书/Telegram/Discord/Slack/WhatsApp 等 |
| 全平台支持 | macOS / Linux | ✅ Windows / macOS / Linux 全平台原生支持 |
| 技能/任务管理 | CLI 手动配置 | ✅ 图形化管理面板（安装/配置/调度） |

---

## 许可证

[MIT](LICENSE)

---

<p align="center">
  <sub>Built with 🧠 dual-brain architecture</sub>
</p>
