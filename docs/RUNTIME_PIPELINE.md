# Kocort 运行时执行管线

本文专门讲 `runtime` 层如何装配、如何执行一次 agent run，以及任务 / 会话 / 投递如何和主链路配合。

## 1. RuntimeBuilder：启动期装配

Runtime 在 `NewRuntimeFromConfig()` 中通过 `RuntimeBuilder.Build()` 组装。Builder 的职责不是简单 new struct，而是把一整套运行时服务接起来：

- 配置和路径解析
- 日志、审计、环境变量、动态代理 HTTP client
- 会话存储与 identity resolver
- 渠道管理、事件总线、统一 deliverer
- 工具注册表、插件、审批器
- 记忆、任务、心跳、子代理、进程注册表
- `brainLocal` 与 `cerebellum`

## 2. Runtime.Run 的六阶段

`runtime/runtime_run.go` 将单次执行拆成六个阶段：

```text
validate
   → resolve
   → gateQueue
   → loadContext
   → buildRunContext
   → execute
```

这种拆法的价值：

- 阶段职责更清晰
- 每一段更易测试
- API、任务、心跳、子代理都可以共用同一执行主线

## 3. Stage 1：validate

这一阶段负责把请求规范化，典型工作包括：

- 补齐 `AgentID` / `RunID`
- 处理默认 channel / target / timeout
- 校验是否具备运行所需字段

目标是把后续阶段都建立在“结构完整”的请求之上。

## 4. Stage 2：resolve

resolve 阶段主要完成三件事：

1. 解析 agent identity
2. 解析或创建 session
3. 处理 reset / compaction / shortcut 类逻辑

identity 的来源是配置层拼装后的 `core.AgentIdentity`，已经融合：

- agent defaults
- per-agent 覆盖
- 请求级 provider / model 覆盖
- workspace / sandbox / memory / tool policy / subagent policy

## 5. Stage 3：gateQueue

这一阶段处理并发冲突：

- 一个 session 已经有 active run 时，决定本次请求是丢弃、排队还是继续
- 建立 run context、取消函数、活跃运行登记
- 在退出时安排 follow-up queue drain

因此它是“串行化同一 session 执行”的关键闸门。

## 6. Stage 4：loadContext

`pipeline_context.go` 中这一阶段会装载完整运行上下文：

- 解析 `WorkspaceDir` 和 `AgentDir`
- 解析 sandbox dirs
- 读取 transcript 并做 sanitize
- 提取 internal prompt events
- 构建 workspace skill snapshot
- 读取 prompt context files
- 准备 memory backend 并执行 recall
- 解析最终模型选择

这里还有两个关键分支：

- `brainMode=local` 时，直接把 selection 固定为 `local/local`
- 云端模式下会校验 provider/model 是否还有效，必要时清除失效的 stored override 后重试

## 7. Stage 5：buildRunContext

这一阶段把“可运行”上下文装配出来：

- 构造 `DeliveryTarget`
- 为当前 run 创建 `ReplyDispatcher`
- 按 identity policy 过滤默认工具
- 解析并注入 plugin tools
- 构建 `rtypes.AgentRunContext`
- 注入技能提供的环境变量覆盖

这一步之后，模型后端拿到的是一个完整的运行时视图，而不是一堆零散参数。

## 8. Stage 6：execute

执行阶段有两个大分支。

### 8.1 技能命令短路

如果用户输入匹配技能命令，且命令被声明为 tool dispatch：

- 直接执行对应工具
- 发送最终回复
- 写入运行产物
- 结束 run

### 8.2 模型调用循环

如果不是技能短路，就进入模型调用主循环：

- 执行预先 memory flush 检查
- 调用 `backend.RunWithModelFallback`
- 在 provider / fallback model 间切换
- 等待 dispatcher drain
- 持久化 session state、transcript、任务状态
- 释放子代理公告

## 9. 错误与重试策略

执行阶段内置了几类重要恢复策略：

- **transient HTTP error**：重建 dispatcher 后重试一次
- **context overflow**：尝试 compaction 后重试一次
- **session reset 类错误**：必要时走 session reset 逻辑
- **partial success**：即使最终出错，只要已经向用户流出了可见 payload，就返回部分成功结果

这让长会话和不稳定 provider 场景下的体验更稳。

## 10. 回复、投递与事件流

### 10.1 ReplyDispatcher

Dispatcher 是模型运行阶段和外部投递之间的缓冲层，负责：

- 流式 token / block / final payload 输出
- 与 deliverer 协作做 channel 投递
- 收尾时等待所有可见消息发送完成

### 10.2 RouterDeliverer

`internal/delivery.RouterDeliverer` 会根据 channel 决定：

- `cli` / 空 channel：回退到 stdout deliverer
- `webchat`：写入 event hub，并镜像 transcript
- 外部渠道：进入 queue、hook、chunking、adapter send 流程

它同时负责 delivery queue 的 queued / sending / failed 状态更新。

### 10.3 EventHub

EventHub 为 WebChat 和调试流提供 SSE 事件来源，输出包括：

- assistant text delta
- reasoning delta
- tool call / tool result
- lifecycle event
- delivery event

## 11. 会话持久化

成功执行后，Runtime 会：

1. 基于执行结果构造新的 `SessionEntry`
2. 应用 backend/session state 变化
3. `Upsert` 到 session store
4. 生成 transcript messages 并追加到 JSONL

这使得后续 WebChat 历史、任务恢复、会话工具都能看到统一状态。

## 12. 任务、心跳、子代理如何复用 Pipeline

### 12.1 任务

`TaskScheduler` 调度到点后，最终仍然走 `Runtime.Run()` 或系统事件入口，因此任务不是“另一套执行器”。

### 12.2 心跳

`HeartbeatRunner` 定时唤醒 agent，调用 `RunHeartbeatTurn()`，内部同样依赖 Runtime 既有能力。

### 12.3 子代理

`sessions_spawn` 和 subagent 逻辑会创建新的 child session / task record，但其具体执行仍是统一 Run Pipeline。

## 13. 为什么这条管线重要

理解 `kocort` 最关键的一点就是：

- HTTP 只是入口
- 渠道只是入口
- 任务和心跳也是入口

真正的系统行为都沉到了 `runtime` 的统一执行管线中。这是项目当前最稳定、最值得继续保护的架构中心。
