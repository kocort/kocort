# Kocort Web UI

`web/` 是 Kocort 的 Next.js 前端工程，提供聊天、系统状态、工具、任务、渠道和配置等管理界面。

## Overview

- 技术栈：Next.js 15 + React 19 + TypeScript + Ant Design X
- 构建模式：静态导出（`next.config.ts` 中 `output: 'export'`）
- 默认 API 地址：`http://127.0.0.1:18789`
- 产物目录：`web/out/`
- 主工程构建时会把静态产物同步到 `api/static/dist/`，由 Go 网关直接嵌入并提供服务

## Prerequisites

- Node.js 20+
- npm
- 已启动的 Kocort 网关（默认监听 `127.0.0.1:18789`）

## Local Development

```bash
cd web
npm install
npm run dev
```

默认开发地址为 `http://127.0.0.1:3000`。

注意：前端页面会请求本地 Kocort API，因此开发前通常还需要在项目根目录启动后端网关。

## Production Build

```bash
cd web
npm run build
```

执行完成后，静态站点会输出到 `web/out/`。

## Embed Into Go Binary

项目根目录下的构建脚本会自动完成以下步骤：

1. 执行 `web/` 的静态构建
2. 将 `web/out/` 同步到 `api/static/dist/`
3. 构建最终的 Go 可执行文件

```bash
./scripts/build.sh
```

如果你只想刷新内嵌前端资源，也可以先在 `web/` 下执行 `npm run build`，再运行主构建脚本。

## Useful Commands

```bash
npm run dev    # 本地开发
npm run build  # 生成静态导出
npm run start  # 启动 Next 生产模式（仅用于单独调试）
npm run lint   # 运行 ESLint
```
