# 前端、桌面端与发布方式

Kocort 的用户界面并不只有一个入口，当前至少有三种消费方式：浏览器、嵌入式单页、桌面壳。

## 1. Web 前端

`web/` 是独立的 Next.js 15 工程，主要用于：

- Brain 配置与模型管理
- 聊天主界面
- tasks / system / integrations 等管理页

从依赖和目录看，前端使用了：

- React 19
- Next.js 15
- Ant Design / `@ant-design/x`
- 一套自定义 `components/*` + `lib/api/*` 的 API 封装

## 2. 嵌入式前端

发布时并不是单独部署 `web` 服务，而是：

1. 在 `web` 目录构建前端产物
2. 同步到 `api/static/dist`
3. 由 Go 二进制 embed 打包
4. 通过 Gateway 直接提供页面与 API

这就是 README 中强调的单二进制部署能力。

## 3. 简易 WebChat

除了完整前端，`api/handlers/rpc.go` 还内置了一个轻量 HTML WebChat，用于：

- API 调试
- SSE 事件观察
- 前端尚未构建时的快速验证

它不是正式 UI，但在排查 Runtime 问题时很有用。

## 4. Desktop 壳

`cmd/kocort-desktop` 会启动同一套 Runtime + API Server，再交给平台壳处理系统托盘或菜单栏。

这意味着桌面端特点是：

- 后端逻辑与 CLI/Gateway 完全复用
- 只是在启动入口、配置目录和 OS 集成上不同
- macOS / Windows / Linux 的包装资源集中在 `desktop/`

## 5. 渠道适配器也是“客户端”

严格说，`internal/channel` 下的各种 adapter 也可视为 Kocort 的另一类“客户端层”：

- 它们把外部消息平台接进 Runtime
- 也负责把最终回复发回各平台

因此 Kocort 对外不是单一 Web 应用，而是 Web UI + Desktop + Messaging Channels 的多入口系统。

## 6. 构建与发布路径

### 6.1 后端主二进制

推荐用仓库脚本：

```bash
./scripts/build.sh
```

脚本会：

- 构建 `web`
- 刷新 `api/static/dist`
- 编译 `cmd/kocort`

### 6.2 启用本地模型能力

默认构建已经包含 `llama.cpp` 相关支持：

```bash
./scripts/build.sh
```

### 6.3 桌面交付

桌面模式仍以 `cmd/kocort-desktop` 为核心，平台包装资源位于：

- `desktop/macos`
- `desktop/windows`
- `desktop/linux`

## 7. 开发期常见运行方式

### 7.1 只跑后端

```bash
go run ./cmd/kocort -config-dir ./local-config -gateway
```

### 7.2 只跑前端

```bash
cd web
npm install
npm run dev
```

### 7.3 联调模式

一边启动 Go gateway，一边启动 `web` 开发服务器，用前端直接调用本地 API。

## 8. 部署设计的优点

当前发布方式的好处是：

- 对普通用户只需要一个主程序
- 对开发者仍保留独立前端工程的迭代效率
- 对桌面端和 Web 端保持同一后端逻辑
- 对多渠道接入保持统一事件与会话模型
