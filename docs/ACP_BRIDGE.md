# ACP

`kocort` exposes ACP in two places:

- ACP server bridge:
  `cmd/kocort/main.go` + `internal/acpbridge/server.go`
- ACP protocol runtime client:
  `internal/backend/acp_runtime_adapter.go`

The server side accepts ACP over stdio and projects it onto the local API /
gateway layer. The runtime side starts an external ACP-capable agent process
and speaks ACP to it over stdio.

## Server Bridge

The ACP server entrypoint is:

```bash
kocort -acp
```

The bridge layer is split as follows:

- `internal/acpbridge/server.go`
  Owns ACP JSON-RPC handling, ACP session ids, prompt/session state, transcript
  replay, prompt flattening, event translation, and stdio transport.
- `api/service/acp_gateway.go`
  Acts as a gateway-shaped facade over `runtime`, `SessionStore`, and event
  streaming.

Bridge behavior:

- supports `initialize`
- supports `session/new`
- supports `session/load`
- supports `session/list`
- supports `session/prompt`
- supports `session/cancel`
- supports `session/set_mode`
- supports `session/set_config_option`
- maps ACP prompt blocks into chat requests
- replays stored user / assistant transcript text on session load
- translates runtime assistant / tool / lifecycle events into ACP
  `session/update` notifications

Session mapping rules:

- `_meta.sessionLabel` resolves an existing runtime session by label
- `_meta.sessionKey` binds directly to a runtime session key
- bridge defaults from CLI flags apply when explicit prompt metadata is absent
- `requireExisting` rejects missing sessions
- `resetSession` resets the target runtime session before first use

ACP server flags:

- `-acp-session`
- `-acp-session-label`
- `-acp-require-existing`
- `-acp-reset-session`
- `-acp-no-prefix-cwd`
- `-acp-provenance`

## ACP Runtime Client

ACP-backed model providers use:

- `internal/backend/acp_backend.go`
- `internal/backend/acp_runtime_adapter.go`
- `internal/acp/manager.go`

This runtime is a real ACP protocol client, not a local CLI shim. For each ACP
session it:

1. starts the configured external command
2. opens an ACP stdio connection
3. sends `initialize`
4. loads an existing ACP session when a persisted session id is available
5. falls back to `session/new` when resume is unavailable or fails
6. sends `session/prompt`, `session/set_mode`, `session/set_model`, raw
   `session/set_config_option`, and `session/cancel`
7. handles agent-to-client ACP methods such as `session/update`,
   `fs/read_text_file`, `fs/write_text_file`, and `session/request_permission`

Manager responsibilities in `internal/acp/manager.go`:

- cache ACP runtime handles by runtime session key
- persist ACP metadata into `SessionStore`
- re-attach to persistent ACP sessions after restart
- apply persisted runtime options
- expose ACP status / control operations to the engine API

## Engine and Runtime Integration

ACP session management APIs live in:

- `api/service/acp_admin.go`
- `api/handlers/engine.go`

Startup restore for persistent ACP sessions lives in:

- `runtime/runtime_acp_bootstrap.go`

ACP-backed child-agent lifecycle remains in:

- `runtime/runtime_acp_spawn.go`
- `internal/task/acp_child_lifecycle.go`

## Current Boundaries

Current ACP implementation boundaries:

- transcript replay is text-first; historic ACP tool-call replay is not restored
- ACP terminal client methods are not exposed by `kocort`
- ACP plan updates are not emitted because runtime does not produce plan state
- session usage and status are derived from local runtime/session state, not a
  remote gateway row

## Source Pointers

- `cmd/kocort/main.go`
- `internal/acpbridge/server.go`
- `api/service/acp_gateway.go`
- `api/service/acp_admin.go`
- `internal/backend/acp_backend.go`
- `internal/backend/acp_runtime_adapter.go`
- `internal/acp/manager.go`
- `runtime/runtime_acp_bootstrap.go`
