package main

import "fmt"

// Lang represents a supported UI language.
type Lang string

const (
	LangEN Lang = "en"
	LangZH Lang = "zh"
)

// currentLang is the detected UI language, set once at startup.
var currentLang = detectSystemLang()

// T returns the localized string for the given key.
func T(key string) string {
	if m, ok := translations[currentLang]; ok {
		if s, ok := m[key]; ok {
			return s
		}
	}
	// Fallback to English.
	if s, ok := translations[LangEN][key]; ok {
		return s
	}
	return key
}

// Tf returns the localized string for the given key, formatted with args.
func Tf(key string, args ...any) string {
	return fmt.Sprintf(T(key), args...)
}

// Translation keys — keep alphabetical within each group.
var translations = map[Lang]map[string]string{
	LangEN: {
		// Status line (shown as a disabled menu item)
		"status.starting":       "◑ Service Starting…",
		"status.running":        "● Service Running",
		"status.stopped":        "○ Service Stopped",
		"status.restarting":     "◑ Service Restarting…",
		"status.timeout":        "○ Service Start Timeout",
		"status.restart_failed": "○ Restart Failed",

		// Menu items
		"menu.open_dashboard": "Open Dashboard",
		"menu.restart":        "Restart Server",
		"menu.quit":           "Quit",

		// Tooltips (shown on hover over the tray icon)
		"tooltip.starting": "Kocort AI Agent — Starting",
		"tooltip.running":  "Kocort AI Agent — %s",
		"tooltip.error":    "Kocort AI Agent — Error",

		// Hints (secondary text / menu-item tooltip)
		"hint.status":  "Kocort backend status",
		"hint.open":    "Open Dashboard",
		"hint.restart": "Restart Server",
		"hint.quit":    "Quit",
	},
	LangZH: {
		"status.starting":       "◑ 服务启动中…",
		"status.running":        "● 服务运行中",
		"status.stopped":        "○ 服务已停止",
		"status.restarting":     "◑ 服务重启中…",
		"status.timeout":        "○ 服务启动超时",
		"status.restart_failed": "○ 重启失败",

		"menu.open_dashboard": "打开管理端",
		"menu.restart":        "重启服务",
		"menu.quit":           "退出",

		"tooltip.starting": "Kocort AI Agent — 服务启动中",
		"tooltip.running":  "Kocort AI Agent — %s",
		"tooltip.error":    "Kocort AI Agent — 启动异常",

		"hint.status":  "Kocort 后端状态",
		"hint.open":    "打开管理端",
		"hint.restart": "重启服务",
		"hint.quit":    "退出",
	},
}
