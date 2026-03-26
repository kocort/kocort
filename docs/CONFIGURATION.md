# 配置体系

Kocort 的配置系统目标很明确：支持“默认值 + 本地 JSON 文件 + 运行时 API 热修改”的统一配置体验。

## 1. 配置文件组成

默认有三类 JSON 文件：

```text
<config-dir>/kocort.json
<config-dir>/models.json
<config-dir>/channels.json
```

职责划分：

- `kocort.json`：主配置，包含工具、插件、agents、session、tasks、gateway、memory、brainLocal、cerebellum 等
- `models.json`：provider 与 model 列表
- `channels.json`：渠道配置

运行时持久化时也按这个分区分别回写。

## 2. configDir 与 stateDir

### 2.1 configDir 默认规则

CLI 模式下，配置目录优先级为：

1. `-config-dir`
2. `KOCORT_CONFIG_DIR`
3. 当前工作目录下的 `.kocort`
4. 用户目录下的 `.kocort`

桌面模式下默认优先使用用户目录下的 `.kocort`。

### 2.2 stateDir 默认规则

状态目录优先级为：

1. `-state-dir`
2. `KOCORT_STATE_DIR`
3. `AppConfig.stateDir`
4. 回退到 `configDir`

状态目录主要存：

- `sessions.json`
- transcript JSONL
- audit
- tasks
- browser artifacts
- agent / workspace 子目录

## 3. 加载与 merge 逻辑

配置加载大致分四步：

1. 读取内置默认 JSON
2. 加载磁盘上的主配置
3. 加载 `models.json`
4. 加载 `channels.json`
5. deep merge 成一个 `AppConfig`

这意味着：

- 未写入磁盘的字段仍能继承默认值
- API 修改配置后可以只回写相关 section
- 运行时始终基于完整 `AppConfig` 工作

## 4. 路径解析规则

配置中的大多数路径都允许写相对路径，并相对于 `configDir` 解析；只有少数需要保持绝对路径的字段例外。

常见相对路径字段包括：

- `stateDir`
- `brainLocal.modelsDir`
- `cerebellum.modelsDir`
- 日志文件路径
- 数据源路径
- workspace / agentDir 等 agent 相关路径

## 5. 最重要的配置块

### 5.1 `models`

`models.providers` 定义 provider，provider 的 `api` 决定后端类型：

- `openai-completions`
- `anthropic-messages`
- `command`
- `cli`

每个 provider 下再声明可选模型列表，用于默认选择、fallback 和 UI 展示。

### 5.2 `agents`

这是最关键的行为层配置，负责定义：

- 默认 agent 与默认模型
- workspace / agentDir
- tool allow / deny / profile
- memory search 策略
- subagent 限制
- heartbeat、compaction、context pruning
- persona 与 identity 信息

运行时真正执行时用的是 `BuildConfiguredAgentIdentity()` 产出的聚合结果，而不是原始 JSON 直接读取。

### 5.3 `session`

控制会话行为：

- main key / DM scope
- reset trigger 与 freshness policy
- agent-to-agent 可见性
- session tools visibility
- send policy
- maintenance / disk budget

### 5.4 `tools`

控制工具系统：

- `exec` 等工具的基础行为
- elevated gate
- sandbox 配置
- loop detection
- browser 工具驱动设置

### 5.5 `plugins`

控制插件的 allow / deny、单插件启停和环境变量注入。

### 5.6 `channels`

按 channel ID 定义：

- 是否启用
- 默认 agent / defaultTo / defaultAccount
- allowFrom
- 文本分块限制
- adapter 私有配置

### 5.7 `gateway`

控制：

- bind / port
- 认证模式
- WebChat 是否启用
- 对外 HTTP 网关行为

### 5.8 `tasks`

控制任务系统是否启用、tick 周期与并发上限。

### 5.9 `memory`

控制工作区记忆后端、召回模式、QMD 配置、引用策略等。

### 5.10 `environment` 与 `network`

- `environment`：声明环境变量映射、是否 strict、是否 masked
- `network`：系统代理、自定义代理 URL、语言

运行时会把代理配置注入共享的动态 HTTP client，因此 API 热更新后新请求会自动使用新代理。

## 6. brainMode、brainLocal 与 cerebellum

这是项目里最容易混淆的部分。

### 6.1 `brainMode=cloud`

- 主推理由 provider backend 执行
- `cerebellum` 可以启用
- `brainLocal` 只是配置存在，不承担主流程

### 6.2 `brainMode=local`

- 主流程切到本地 GGUF 模型
- `cerebellum` 自动失效
- pipeline 中 model selection 固定为 `local/local`

### 6.3 `cerebellum`

它不是第二个主推理模型，而是：

- 工具调用前的语义审查器
- 或在帮助/配置场景下的本地帮助器

## 7. 配置的 API 热更新

大部分设置不是“重启后生效”，而是通过：

1. `api/service.ModifyAndPersist`
2. `runtime.ApplyConfig`
3. `runtime.PersistConfig`

立刻生效。

热更新会同步刷新：

- 日志等级
- 环境变量解析器
- 动态代理
- backend registry
- channel manager
- memory manager
- plugins

## 8. 最小可运行示例

至少需要一个可用 provider：

```json
{
  "models": {
    "providers": {
      "openai": {
        "api": "openai-completions",
        "baseUrl": "https://api.openai.com/v1",
        "apiKey": "${OPENAI_API_KEY}",
        "models": [{ "id": "gpt-4o-mini", "name": "gpt-4o-mini" }]
      }
    }
  }
}
```

如果要纯本地运行，则需要把 `brainMode` 切成 `local`，并配置 `brainLocal.modelId` 与模型目录。

## 9. `local-config` 的定位

仓库里的 `local-config` 既是样例，也是开发默认入口，适合：

- 本地启动 gateway
- 联调前端与 WebChat
- 调试 skills / tasks / sandbox
- 测试 API 持久化行为

常用命令：

```bash
KOCORT_HOME="$(pwd)/.kocort-local" \
GOCACHE="$(pwd)/.gocache" \
go run ./cmd/kocort -config-dir ./local-config -gateway
```

## 10. 配置设计上的建议

后续继续演进时，建议保持当前设计：

- “行为配置”尽量集中在 `agents`
- “接入配置”拆到 `models` 与 `channels`
- “可热更新配置”优先走 API + store 保存
- 不把运行时瞬时状态塞回配置文件
