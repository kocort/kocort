# Linux Desktop Build

`desktop/linux/` 提供 Kocort 在 Ubuntu 桌面环境下的托盘版说明。桌面入口来自 `./cmd/kocort-desktop`，启动后会先拉起本地后端，确认页面就绪后自动打开管理端。

## What It Does

- 托盘图标驻留在 Ubuntu 顶栏状态区
- 菜单项：`打开管理端` / `重启服务` / `退出`
- 默认地址：`http://127.0.0.1:18789`
- 后端真正可访问后，才自动打开浏览器页面

## Ubuntu Scope

当前 Linux 桌面支持目标为 Ubuntu Desktop（GNOME）。托盘依赖 StatusNotifierItem / AppIndicator 机制；Ubuntu 22.04+ 默认桌面通常已具备对应支持。

如果你的系统裁剪过 GNOME 组件，确保已启用 AppIndicator / KStatusNotifierItem 支持。

## Recommended Build

优先使用项目根目录脚本：

```bash
./scripts/build-desktop.sh --linux
```

输出目录示例：`dist/linux_amd64/`。

## Manual Build

```bash
go build -trimpath -ldflags "-s -w" -o dist/linux_amd64/kocort-desktop ./cmd/kocort-desktop
```

## Install on Ubuntu

用户级安装示例：

```bash
install -Dm755 dist/linux_amd64/kocort-desktop ~/.local/bin/kocort-desktop
install -Dm644 desktop/icons/icon.png ~/.local/share/icons/hicolor/512x512/apps/kocort.png
install -Dm644 desktop/linux/kocort.desktop ~/.local/share/applications/kocort.desktop
```

如需立即刷新桌面入口缓存，可执行：

```bash
update-desktop-database ~/.local/share/applications 2>/dev/null || true
gtk-update-icon-cache ~/.local/share/icons/hicolor 2>/dev/null || true
```

## Notes

- `kocort.desktop` 现在默认执行 `kocort-desktop`
- 如果你把二进制安装到其他位置，请同步修改 `Exec` 或把目录加入 `PATH`
- 首次启动若浏览器未弹出，可从托盘菜单点击 `打开管理端`