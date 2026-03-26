# Windows Desktop Build

`desktop/windows/` 提供 Kocort Windows 托盘版的构建说明和可选资源文件。桌面入口来自 `./cmd/kocort-desktop`，启动后会在系统托盘提供菜单并打开本地管理端。

## What It Does

- 托盘图标驻留在系统通知区域
- 菜单项：`打开管理端` / `重启服务` / `退出`
- 默认打开地址：`http://127.0.0.1:18789`
- 使用 `-H windowsgui` 隐藏控制台窗口

## Prerequisites

- Go 1.23+
- 可选：ImageMagick（用于从 PNG 生成 `.ico`）
- 可选：MinGW `windres` 或 [go-winres](https://github.com/tc-hib/go-winres)（用于嵌入 exe 图标和 manifest）

## Recommended Build

优先使用项目根目录脚本：

```bash
./scripts/build-desktop.sh --windows
```

如需构建 ARM64：

```bash
./scripts/build-desktop.sh --windows-arm
```

输出目录：`dist/windows_amd64/` 或 `dist/windows_arm64/`。

## Manual Build

```powershell
$env:GOOS = "windows"
$env:GOARCH = "amd64"
go build -trimpath -ldflags "-H windowsgui -s -w" -o dist/windows_amd64/kocort-desktop.exe ./cmd/kocort-desktop
```

## Tray Icon

`cmd/kocort-desktop/tray_windows.go` 直接通过 `//go:embed tray.png` 嵌入托盘图标。

如果你还想给最终 `.exe` 添加资源图标，构建脚本仍会优先准备 `cmd/kocort-desktop/tray.ico`：

1. 如果文件已存在，直接使用
2. 如果安装了 ImageMagick，则由 `desktop/icons/tray.png` 自动生成 `.ico`
3. 如果没有 ImageMagick，则直接复制 `tray.png` 作为回退方案

手动生成示例：

```powershell
magick desktop/icons/tray.png -define icon:auto-resize=64,48,32,16 cmd/kocort-desktop/tray.ico
```

## Optional EXE Icon and Manifest

仓库已提供：

- `desktop/windows/kocort.rc`
- `desktop/windows/kocort.manifest`

这两者用于给最终 `.exe` 添加资源图标、DPI 感知和兼容性清单，但不是托盘功能所必需。

### Option A: Use `windres`

```powershell
cd desktop/windows
windres -i kocort.rc -O coff -o ../../cmd/kocort-desktop/kocort.syso
cd ../..
go build -trimpath -ldflags "-H windowsgui -s -w" -o dist/windows_amd64/kocort-desktop.exe ./cmd/kocort-desktop
```

### Option B: Use `go-winres`

如果你更习惯 `go-winres`，可以生成等价的 `.syso` 文件后放到 `cmd/kocort-desktop/`，再执行正常 `go build`。

## Notes

- `./scripts/build-desktop.sh --windows` 会自动处理托盘图标准备
- 托盘图标与 EXE 资源图标是两套机制：前者由 `go:embed` 提供，后者由 `.syso` 提供
- Windows 桌面版本当前不依赖 Swift 外壳，直接运行 Go 桌面入口即可
