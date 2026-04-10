# Kocort 开发指南

本文面向准备修改后端、前端或运行时逻辑的开发者。

## 1. 开发环境

建议至少具备：

- Go 1.23+
- Node.js（用于 `web`）
- `npm`
- 如果要启用本地模型：可用的 C/C++ 编译器

## 2. 最常用的本地启动方式

### 2.1 Gateway 开发模式

```bash
go run ./cmd/kocort -config-dir ./local-config -gateway
```

如果想把缓存和状态隔离到仓库内：

```bash
KOCORT_HOME="$(pwd)/.kocort-local" \
GOCACHE="$(pwd)/.gocache" \
go run ./cmd/kocort -config-dir ./local-config -gateway
```

### 2.2 单次消息执行

```bash
go run ./cmd/kocort -config-dir ./local-config -message "hello" -agent main
```

### 2.3 前端开发

```bash
cd web
npm install
npm run dev
```

## 3. 构建

### 3.1 标准构建

```bash
./scripts/build.sh
```

这个脚本会先构建 `web`，再刷新嵌入式静态资源，最后编译 `cmd/kocort`。

### 3.2 默认本地模型构建

```bash
./scripts/build.sh
```

### 3.3 其他常用选项

- `./scripts/build.sh --test`
- `./scripts/build.sh --vet`
- `./scripts/build.sh --clean`
- `./scripts/build.sh --cross`

## 4. 测试

### 4.1 全量 Go 测试

```bash
go test ./...
```

### 4.2 llamadl / cerebellum 相关测试

脚本里已有针对测试流程：

```bash
./scripts/build.sh --test
```

集成测试（需要真实的 llama.cpp 库和模型）使用 `integration` build tag：

```bash
# 先设置库路径
export KOCORT_LLAMA_LIB_DIR=/path/to/llama-libs

# 运行集成测试
go test -tags integration -v ./internal/localmodel/...
```

### 4.3 仓库自带快速运行示例

`runtest.sh` 当前保存了一条本地网关启动命令，可作为临时调试参考。

## 5. 推荐的代码阅读顺序

### 5.1 后端主链路

1. `cmd/kocort/main.go`
2. `runtime/runtime_builder.go`
3. `api/routes.go`
4. `runtime/runtime_run.go`
5. `runtime/pipeline_*.go`

### 5.2 配置与状态

1. `internal/config/types.go`
2. `internal/config/loader.go`
3. `internal/config/identity.go`
4. `internal/session/session_store.go`

### 5.3 前端 API 对接

1. `web/lib/api/*`
2. `web/components/*`
3. 对应的 `api/handlers/*` 与 `api/service/*`

## 6. 修改代码时的边界建议

### 6.1 改 API 时

- handler 只做请求适配
- 复杂状态拼装优先放 `api/service`
- 跨子系统行为放 `runtime`

### 6.2 改运行时时

- 新的跨模块流程优先接入 pipeline 或 runtime service
- 不要把 HTTP 细节带入 `runtime`
- 共享协议优先沉到 `internal/core` 或 `internal/rtypes`

### 6.3 改工具 / 技能 / 任务时

- 工具实现放 `internal/tool`
- 技能扫描和 dispatch 放 `internal/skill`
- 调度与状态机放 `internal/task`

## 7. 配置调试建议

调试配置问题时优先确认：

1. `configDir` 实际指向哪里
2. `stateDir` 是否和预期一致
3. 相对路径是否被解析到了正确目录
4. provider / model 是否真实存在于 merged config 中
5. API 保存后是否触发了 `runtime.ApplyConfig`

## 8. 前后端联调建议

- 对 UI 结构问题，优先跑 `web` dev server
- 对消息流、SSE、任务或会话问题，优先用 Go gateway + 简易 WebChat 验证
- 对嵌入资源问题，用 `./scripts/build.sh` 检查 `api/static/dist` 是否被刷新

## 9. 后续文档维护原则

如果代码继续演化，建议文档也遵循这三条：

- 文档写“当前已实现行为”，不要混入长期计划
- 配置、架构、接口分开写，避免一个文件包打天下
- 变更 Runtime 主链路时，优先同步更新 `RUNTIME_PIPELINE.md`
