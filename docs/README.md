# Kocort 文档总览

这套文档按当前仓库的真实实现重写，目标是把 `kocort` 说明清楚，而不是保留历史迁移计划或路线图。

## 推荐阅读顺序

1. [项目架构](./ARCHITECTURE.md)
2. [运行时装配与执行管线](./RUNTIME_PIPELINE.md)
3. [模块与职责地图](./MODULES.md)
4. [配置体系](./CONFIGURATION.md)
5. [HTTP / RPC 接口参考](./API_REFERENCE.md)
6. [前端、桌面端与发布方式](./CLIENTS_AND_DEPLOYMENT.md)
7. [开发指南](./DEVELOPMENT.md)

## 这套文档覆盖什么

- `cmd/kocort` 与 `cmd/kocort-desktop` 的启动模式
- `api` 的 HTTP、RPC、事件流和静态资源承载方式
- `runtime` 的 Builder、Run Pipeline、任务、心跳、投递与运行时服务
- `internal/*` 里关键领域包的职责边界
- `web` 前端、嵌入式静态资源与桌面包装方式
- `local-config` / `defaults` / 状态目录的配置与落盘规则

## 当前项目定位

Kocort 不是单一聊天程序，而是一个“可本地运行的 Agent 平台容器”，包括：

- 统一模型后端层：OpenAI 兼容、Anthropic、command、cli、本地 GGUF
- 统一运行时层：会话、记忆、技能、工具、子代理、任务、审计、事件
- 统一接入层：WebChat、系统管理面板、外部渠道 webhook、桌面壳
- 本地能力：`brainLocal` 纯本地推理与 `cerebellum` 工具调用审查

## 文档编写原则

- 以源码行为为准，不再保留“未来计划型”文档。
- 优先解释运行链路、模块边界和配置落点。
- 先讲系统分层，再讲运行时，再讲配置、接口和开发。

## 关键源码入口

- `cmd/kocort/main.go`
- `cmd/kocort-desktop/main.go`
- `api/routes.go`
- `runtime/runtime_builder.go`
- `runtime/runtime_run.go`
- `runtime/pipeline_*.go`
- `internal/config/types.go`
- `internal/acpbridge/server.go`

## ACP

- ACP bridge implementation: [ACP_BRIDGE.md](./ACP_BRIDGE.md)
- ACP stdio entrypoint: `kocort -acp`
