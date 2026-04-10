<p align="center">
  <img src="web/public/logo.svg" width="120" alt="Kocort Logo" />
</p>

<h1 align="center">Kocort</h1>

<p align="center">
  <b>Desktop AI Agent Assistant — Dual-Brain Architecture · Zero CLI · Cross-Platform</b>
</p>

<p align="center">
  <a href="https://github.com/kocort/kocort/releases"><img src="https://img.shields.io/github/v/release/kocort/kocort" alt="Latest Release" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/kocort/kocort" alt="License" /></a>
  <img src="https://img.shields.io/badge/Go-1.23-00ADD8?logo=go" alt="Go 1.23" />
  <img src="https://img.shields.io/badge/Platforms-macOS%20%7C%20Windows%20%7C%20Linux-222222" alt="Platforms" />
</p>

<p align="center">
  <a href="#quick-start">Quick Start</a> ·
  <a href="#core-features">Features</a> ·
  <a href="#architecture-overview">Architecture</a> ·
  <a href="#api-reference">API</a> ·
  <a href="docs/README.md">Docs</a> ·
  <a href="README-CN.md">中文文档</a>
</p>

---

## What is Kocort?

Kocort is a **desktop-grade AI Agent application**, similar to [OpenClaw](https://github.com/openclaw/openclaw), designed to give everyone—even those unfamiliar with the command line—a personal AI assistant with zero barrier to entry.

It is a **fully self-contained AI assistant**—with built-in session management, tool orchestration, skill system, sub-agent scheduling, and multi-layered security. Just download and run, no complex CLI operations needed.

### Key Advantages

| | Feature | Description |
|---|---|---|
| 🧠 | **Dual-Brain Architecture** | Cloud brain (powerful reasoning) + local cerebellum (offline safety review); sensitive data never leaves your device |
| 🖥️ | **Zero CLI** | All operations via GUI, ready to use out of the box |
| 📦 | **Single-Binary Deployment** | Compiles to a single executable with embedded web frontend |
| 🔒 | **Proactive Security** | Static rule interception + cerebellum semantic review, dual protection |
| 🏠 | **Fully Local** | Built-in local model support, no cloud API required |
| 🌍 | **Cross-Platform** | Native support for Windows / macOS / Linux, single codebase multi-platform builds |
| 📊 | **Visual Management** | Graphical management panel for skills and tasks—install / configure / schedule all through the UI |

---

## Core Features

### 🧠 Dual-Brain Architecture

Kocort's pioneering security architecture—Brain handles reasoning, Cerebellum handles review:

```
                 ┌─────────────────┐
                 │   User Request   │
                 └────────┬────────┘
                          ▼
              ┌───────────────────────┐
              │      🧠 Brain          │
              │  Reasoning & Decisions │
              │  Cloud or Local LLM    │
              └───────────┬───────────┘
                          │ generates tool_call
                          ▼
              ┌───────────────────────┐
              │  🛡️ Cerebellum        │
              │  Safety Review & Gate  │
              │  Local Model · Offline  │
              │                        │
              │  approve → ✅ Execute   │
              │  flag    → ⚠️ Log+Exec  │
              │  reject  → ❌ Block    │
              └───────────┬───────────┘
                          │ ✅
                          ▼
              ┌───────────────────────┐
              │    Tool Execution      │
              └───────────────────────┘
```

- **Brain**: Cloud/remote LLM handles reasoning and decision-making (supports OpenAI, Anthropic, etc.)
- **Cerebellum**: Local 1.7B quantized model, runs fully offline
  - Performs semantic safety review on every `tool_call` (intent consistency, data exfiltration, injection attacks, etc.)
  - Review results: `approve` / `flag` / `reject`, with risk level and reasoning
  - Intelligently skips low-risk read-only operations for performance
  - Graceful degradation: auto-allows when cerebellum is unavailable, never blocks normal flow
- **Local Brain**: Local models can also fully replace cloud backends to run the Agent

### 📡 Multi-Channel Integration

Out-of-the-box messaging channel adapters:

- Feishu (Lark) · Telegram · Discord · Slack · WhatsApp · Zalo
- Generic Webhook (custom integration)
- WebChat (built-in web chat interface)

### 🛠️ Tool System

30+ built-in tools with a comprehensive governance framework:

**File Operations**:
- `read` / `write` / `edit` / `apply_patch` — File read/write and editing
- `grep` / `find` / `ls` — File search and directory browsing

**Command Execution**:
- `exec` — Shell command execution (PTY support)
- `process` — Background process management

**Web & Browser**:
- `web_search` — Web search
- `web_fetch` — URL content fetching and extraction
- `browser` — Browser automation (navigation / screenshots / PDF / console)

**Memory System**:
- `memory_search` / `memory_get` — Persistent workspace memory search and retrieval

**Sessions & Sub-Agents**:
- `session_status` — Session status and model switching
- `sessions_list` / `sessions_history` — Session listing and history
- `sessions_send` / `sessions_spawn` / `sessions_yield` — Cross-session messaging and sub-agents
- `subagents` — Sub-agent orchestration (list / kill / info / steer / send)
- `agents_list` — Available agent listing

**Messaging & Media**:
- `send` — Proactively send messages, files, or images
- `image` — Image analysis
- `image_generate` — Image generation

**Automation**:
- `cron` — Scheduled tasks (one-shot / interval / cron expressions)
- `canvas` — Canvas display and interaction

**Security Governance**:
- Tool Policy — allow/deny/profile/group policies
- Sandbox — Sandbox isolation (directory whitelist + read-only protection)
- Elevated Gate — Tiered interception for high-risk operations (low/medium/high/critical)
- Tool Approval — Dynamic approval for sensitive tools
- Loop Detection — Automatic circuit-breaking for repetitive/ping-pong calls

### 🔄 Pipeline Execution Engine

Each Agent run goes through 6 independently testable stages:

```
validate → resolve → gateQueue → loadContext → buildRunCtx → execute
```

1. **validate** — Readiness check, input validation
2. **resolve** — Identity / session / command resolution
3. **gateQueue** — Active-run mutual exclusion, queue/discard decisions
4. **loadContext** — Workspace / transcript / skills / memory loading
5. **buildRunCtx** — Tool filtering, RunContext assembly
6. **execute** — Skill dispatch or model call loop + retry

### 📋 More Features

- **Skill System** — Discovery / snapshot / command dispatch / implicit injection / install / remote skills
- **Memory System** — Workspace file lexical recall + hybrid recall
- **Sub-Agent Orchestration** — Spawn / registry / completion announce / send / steer
- **Task Scheduling** — One-shot / interval / cron scheduling modes
- **Heartbeat Mechanism** — Periodic wake-up, conditional evaluation
- **Recoverable Delivery** — WAL queue / replay / hooks / chunking / transcript mirror
- **Audit Dashboard** — Real-time runtime status, audit log queries

---

## Architecture Overview

```
┌─────────────────────────────────────────────┐
│            cmd/kocort/main.go               │  Entry Layer
├─────────────────────────────────────────────┤
│              api/ (Gin)                     │  HTTP API Layer
├─────────────────────────────────────────────┤
│            runtime/ (Runtime)               │  Orchestration Layer — System Core
├─────────────────────────────────────────────┤
│              internal/                      │  Domain Layer
│  ┌────────┬────────┬──────────┬──────────┐  │
│  │  core  │ config │ backend  │ channel  │  │
│  │  tool  │  task  │ session  │  infra   │  │
│  │delivery│heartbeat│  skill  │ sandbox  │  │
│  │cerebellum│localmodel│memory│  event   │  │
│  └────────┴────────┴──────────┴──────────┘  │
├─────────────────────────────────────────────┤
│              utils/                         │  Utilities
└─────────────────────────────────────────────┘
```

**Dependency Direction**:

```
cmd/kocort → api → runtime → internal/config → internal/core
                                    ↓
                          internal/{domain packages}
```

**Key Constraints**:
- `internal/core` has zero internal dependencies (only depends on stdlib)
- Domain packages do not depend on each other; shared types go through `core`
- Progressive migration: type extraction complete, implementation files migrated on demand

---

## Security Model

Kocort implements defense-in-depth with multiple layers:

| Layer | Mechanism | Description |
|-------|-----------|-------------|
| L1 | **Tool Policy** | allow/deny/profile/group, subagent depth limits |
| L2 | **Elevated Gate** | Tiered interception for high-risk ops (low → critical), requires user confirmation |
| L3 | **Sandbox Isolation** | File operations restricted to authorized directories, system-critical paths read-only |
| L4 | **Tool Approval** | Sensitive tools require dynamic approval flow |
| L5 | **Loop Detection** | Automatic circuit-breaking for repetitive / ping-pong calls, global circuit breaker |
| L6 | **Cerebellum Semantic Review** | Local model performs intent consistency, injection attack, data exfiltration analysis on tool_calls |
| L7 | **Session Isolation** | Inter-session access control, 4-level visibility policies (self/tree/agent/all) |

---

## Quick Start

### Prerequisites

- Go 1.23+
- Node.js 20+ and npm (required to build the embedded `web/` frontend)
- (Optional) llama.cpp shared libraries for local model inference (downloaded automatically or set `KOCORT_LLAMA_LIB_DIR`)

### Build

```bash
# Install frontend dependencies once
cd web && npm install && cd ..

# One-step packaging:
# 1. build web/ as static export
# 2. sync assets into Go embed dir
# 3. build the Go binary for the current platform
./scripts/build.sh
```

Embedded frontend assets are staged in `api/static/dist` during the build and served directly by the Go API layer from the final binary.

The default output path is `dist/<goos>_<goarch>/kocort`.

### Configuration

Kocort supports three-file separated configuration:

```
local-config/
├── kocort.json    # Main configuration
├── models.json      # Model/Provider configuration
└── channels.json    # Channel configuration
```

**Minimal Configuration** (`models.json`):

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

### Run

```bash
# Example on Apple Silicon macOS
./dist/darwin_arm64/kocort
```

Running `cmd/kocort` with no flags starts the HTTP server by default. The default address is `http://127.0.0.1:18789`.

Default config directory resolution for CLI mode:

1. `KOCORT_CONFIG_DIR`
2. `./.kocort` if it already exists
3. `~/.kocort` if it already exists
4. otherwise `./.kocort`

### Desktop Builds

Desktop packaging uses a separate script:

```bash
# macOS menubar app
./scripts/build-desktop.sh --macos

# Windows tray app
./scripts/build-desktop.sh --windows
```

See `desktop/macos/README.md` and `desktop/windows/README.md` for signing, DMG, notarization, tray icon, and resource details.

---

## Docs at a Glance

The root docs set has been rebuilt around the current codebase implementation:

| Document | Focus |
|----------|-------|
| [docs/README.md](docs/README.md) | Reading order and documentation scope |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Layering, runtime-centered architecture, startup modes |
| [docs/RUNTIME_PIPELINE.md](docs/RUNTIME_PIPELINE.md) | RuntimeBuilder, 6-stage pipeline, delivery and persistence |
| [docs/MODULES.md](docs/MODULES.md) | Package map across `api`, `runtime`, `internal`, `web` |
| [docs/CONFIGURATION.md](docs/CONFIGURATION.md) | Config loading, path resolution, hot reload, brain modes |
| [docs/API_REFERENCE.md](docs/API_REFERENCE.md) | HTTP / RPC / channel webhook route map |
| [docs/CLIENTS_AND_DEPLOYMENT.md](docs/CLIENTS_AND_DEPLOYMENT.md) | Web UI, desktop shell, embedded assets, build flow |
| [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) | Local development, build, testing, extension boundaries |
| [docs/ACP_BRIDGE.md](docs/ACP_BRIDGE.md) | ACP stdio bridge, protocol runtime client, and session mapping |

---

## API Reference

### Chat Interaction

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/workspace/chat/bootstrap` | Chat initialization |
| POST | `/api/workspace/chat/send` | Send message |
| POST | `/api/workspace/chat/cancel` | Cancel run |
| GET | `/api/workspace/chat/history` | Chat history |
| GET | `/api/workspace/chat/events` | SSE event stream |

### Engine Management

| Method | Path | Description |
|--------|------|-------------|
| GET/POST | `/api/engine/brain/*` | Brain configuration / model management |
| POST | `/api/engine/brain/cerebellum/start\|stop\|restart` | Cerebellum lifecycle |
| POST | `/api/engine/brain/local/start\|stop\|restart` | Local brain lifecycle |
| POST | `/api/engine/brain/mode` | Brain mode switching |

### System Management

| Method | Path | Description |
|--------|------|-------------|
| GET | `/healthz` | Health check |
| GET | `/api/system/dashboard` | Dashboard |
| POST | `/api/system/audit/list` | Audit logs |

### Channels & RPC

| Method | Path | Description |
|--------|------|-------------|
| POST | `/channels/:channelID` | Channel inbound webhook |
| POST | `/rpc/chat.send` | RPC chat send |
| GET | `/rpc/chat.events` | RPC SSE event stream |

> For the complete API documentation, see [docs/API_REFERENCE.md](docs/API_REFERENCE.md)

---

## Project Structure

```
kocort/
├── cmd/kocort/           # CLI + HTTP server entry point
├── cmd/kocort-desktop/   # Desktop tray/menubar entry point
├── api/                  # HTTP API (Gin)
├── runtime/              # Runtime orchestration core
├── internal/
│   ├── core/             # Shared types (zero dependencies)
│   ├── config/           # Config structs and loading
│   ├── backend/          # LLM Backend abstraction
│   ├── channel/          # Channel integration adapters
│   ├── cerebellum/       # Cerebellum safety review
│   ├── localmodel/       # Local model lifecycle
│   ├── tool/             # Tool registration and execution
│   ├── session/          # Session management
│   ├── task/             # Task scheduling
│   ├── delivery/         # Message delivery
│   ├── heartbeat/        # Heartbeat scheduling
│   ├── skill/            # Skill system
│   ├── infra/            # Infrastructure (logging/audit)
│   └── sandbox/          # Sandbox execution
├── web/                  # Frontend (Next.js)
├── desktop/              # macOS/Windows desktop packaging assets
├── api/static/dist/      # Embedded frontend assets after web build
├── utils/                # Utility functions
├── defaults/             # Default configuration examples
├── local-config/         # Local runtime configuration
├── docs/                 # Documentation
└── scripts/              # Build scripts
```

---

## Tech Stack

| Component | Technology |
|-----------|------------|
| **Language** | Go 1.23 |
| **HTTP Framework** | Gin |
| **LLM SDK** | go-openai, anthropic-sdk-go |
| **Local Inference** | llama.cpp (purego dynamic loading), GGUF format |
| **WebSocket** | gorilla/websocket |
| **Frontend** | Next.js |
| **Task Scheduling** | robfig/cron |
| **File Watching** | fsnotify |

---

## Documentation Index

| Document | Description |
|----------|-------------|
| [README.md](docs/README.md) | Documentation entry and reading order |
| [ARCHITECTURE.md](docs/ARCHITECTURE.md) | Project architecture and subsystem overview |
| [RUNTIME_PIPELINE.md](docs/RUNTIME_PIPELINE.md) | RuntimeBuilder and 6-stage execution pipeline |
| [MODULES.md](docs/MODULES.md) | Module map across `api`, `runtime`, `internal`, and `web` |
| [CONFIGURATION.md](docs/CONFIGURATION.md) | Config loading, structure, hot reload, and local-config usage |
| [API_REFERENCE.md](docs/API_REFERENCE.md) | HTTP and RPC endpoint reference |
| [CLIENTS_AND_DEPLOYMENT.md](docs/CLIENTS_AND_DEPLOYMENT.md) | Web UI, desktop shell, and deployment flow |
| [DEVELOPMENT.md](docs/DEVELOPMENT.md) | Local development and extension guide |
| [ACP_BRIDGE.md](docs/ACP_BRIDGE.md) | ACP bridge and runtime architecture |

---

## Comparison with OpenClaw

| Feature | OpenClaw | Kocort |
|---------|----------|--------|
| Interaction | CLI | GUI (zero CLI) |
| Security Model | Static rules | Dual-brain architecture (static rules + semantic review) |
| Local Models | Not supported | Built-in llama.cpp inference |
| Deployment | CLI tool | Single binary + web UI |
| Offline Mode | Not supported | Fully supported |
| Channel Integration | Terminal | Feishu/Telegram/Discord/Slack/WhatsApp, etc. |
| Cross-Platform | macOS / Linux | ✅ Windows / macOS / Linux native support |
| Skill/Task Management | CLI manual config | ✅ Visual management panel (install/configure/schedule) |

---

## License

[MIT](LICENSE)

---

<p align="center">
  <sub>Built with 🧠 dual-brain architecture</sub>
</p>
