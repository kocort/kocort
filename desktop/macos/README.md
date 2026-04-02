# Kocort macOS App

`desktop/macos/` 包含 Kocort 的原生 macOS 菜单栏应用。它由一个 Swift AppKit 外壳和一个 Go 后端进程组成，用于在 macOS 上提供原生 menubar 体验。

## Architecture

```text
Kocort.app/
  Contents/
    MacOS/
      KocortApp          ← Swift 外壳（菜单栏图标 + 进程管理）
    Resources/
      kocort             ← Go 桌面入口二进制（由 ./cmd/kocort-desktop 构建）
      AppIcon.icns
      tray.png
      kocort.json        ← 可选默认配置
      models.json        ← 可选默认模型配置
      channels.json      ← 可选默认渠道配置
    Info.plist
```

Swift 外壳负责：

- 创建原生 macOS 菜单栏图标
- 提供菜单项：`打开管理端` / `重启服务` / `查看日志` / `关于 Kocort` / `退出`
- 启动、停止、重启 Go 后端进程
- 检测本地服务真正就绪后自动打开管理端页面
- 将服务日志写入 `~/.kocort/kocort-server.log`
- 在首次启动时向 `~/.kocort/` 写入最小配置文件

Go 进程以网关模式运行，默认服务地址为 `http://127.0.0.1:18789`。

## Prerequisites

- macOS 13+
- Go 1.23+
- Xcode Command Line Tools（`swift`、`codesign`、`xcrun`）
- 可选：`create-dmg`（用于生成 DMG）

```bash
xcode-select --install
brew install create-dmg
```

## Build

推荐直接使用项目根目录构建脚本：

```bash
# 构建当前机器架构的 .app
./scripts/build-desktop.sh --macos

# 构建 universal .app（arm64 + amd64）
./scripts/build-desktop.sh --macos-universal
```

构建产物位于 `dist/Kocort.app`。

## Signing and Distribution

### Sign `.app`

```bash
KOCORT_CODESIGN_IDENTITY="Developer ID Application: Your Name (TEAMID)" \
./scripts/build-desktop.sh --macos-sign
```

### Build DMG

```bash
KOCORT_CODESIGN_IDENTITY="Developer ID Application: Your Name (TEAMID)" \
./scripts/build-desktop.sh --macos-dmg
```

### Notarize DMG

```bash
KOCORT_CODESIGN_IDENTITY="Developer ID Application: Your Name (TEAMID)" \
KOCORT_APPLE_ID="dev@example.com" \
KOCORT_TEAM_ID="ABCDE12345" \
KOCORT_NOTARY_PASSWORD="xxxx-xxxx-xxxx-xxxx" \
./scripts/build-desktop.sh --macos-notarize
```

### App Store / Sandbox

```bash
KOCORT_APP_STORE=1 \
KOCORT_CODESIGN_IDENTITY="Apple Distribution: Your Name (TEAMID)" \
./scripts/build-desktop.sh --macos-sign
```

设置 `KOCORT_APP_STORE=1` 后会改用 `KocortApp.entitlements`；默认使用 `KocortApp-direct.entitlements`。

## Development

如果你只想调试 Swift 外壳：

```bash
cd desktop/macos/KocortApp
swift build
swift run
```

`swift run` 启动时会按以下顺序查找 `kocort` 二进制：

1. `.app` Bundle 的 `Resources/`
2. `.app` 同级目录
3. 同级的 `dist/macos_*` 目录
4. 系统 `PATH`

如果只是验证完整桌面包，优先使用根目录脚本构建再直接打开 `dist/Kocort.app`。

## Runtime Files

- 配置目录：`~/.kocort/`
- 日志文件：`~/.kocort/kocort-server.log`
- 默认服务地址：`http://127.0.0.1:18789`

## Entitlements

- `KocortApp.entitlements`：App Store / 沙盒分发
- `KocortApp-direct.entitlements`：直接分发（Hardened Runtime）
