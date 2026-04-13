package app

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

const langCookieName = "cbr_lang"

const (
	langZH = "zh-CN"
	langEN = "en"
)

var i18nCatalog = map[string]map[string]string{
	langZH: {
		"app.admin_console":             "管理后台",
		"nav.history":                   "历史记录",
		"nav.devices":                   "设备管理",
		"nav.tokens":                    "接入码",
		"nav.settings":                  "设置",
		"action.logout":                 "退出",
		"lang.zh":                       "中文",
		"lang.en":                       "English",
		"lang.select":                   "切换语言",
		"page.login":                    "管理员登录",
		"page.history":                  "剪切板历史",
		"page.history.subtitle":         "查看文本与图片记录，按设备、内容类型和文本关键词快速筛选。",
		"page.devices":                  "设备管理",
		"page.devices.subtitle":         "管理设备身份、在线状态、实时分发权限、实时接收权限与访问令牌。",
		"page.tokens":                   "接入码管理",
		"page.tokens.subtitle":          "生成新的接入码，并查看现有接入码的状态、使用情况和有效期。",
		"page.settings":                 "设置",
		"page.settings.subtitle":        "调整服务端实时分发、数据保留、图片上传限制和管理员密码。",
		"login.subtitle":                "服务端管理后台登录",
		"login.username":                "用户名",
		"login.password":                "密码",
		"login.submit":                  "登录",
		"history.all_devices":           "全部设备",
		"history.all_kinds":             "全部类型",
		"history.kind.text":             "文本",
		"history.kind.image":            "图片",
		"history.search_placeholder":    "搜索文本内容",
		"history.filter":                "筛选",
		"table.id":                      "编号",
		"table.device":                  "设备",
		"table.kind":                    "类型",
		"table.upload":                  "上传方式",
		"table.preview":                 "预览",
		"table.created":                 "创建时间",
		"table.os":                      "系统",
		"table.platform":                "平台",
		"table.version":                 "版本",
		"table.flags":                   "状态",
		"table.last_seen":               "最后在线",
		"table.actions":                 "操作",
		"table.prefix":                  "前缀",
		"table.usage":                   "使用次数",
		"table.status":                  "状态",
		"table.expires":                 "过期时间",
		"table.action":                  "操作",
		"history.chars":                 "字符",
		"history.empty":                 "还没有剪切板记录。",
		"devices.nickname":              "昵称",
		"devices.unnamed":               "未命名",
		"devices.no_devices":            "还没有注册设备。",
		"devices.realtime_help":         "实时分发和实时接收只影响 WebSocket 实时推送，不会阻止设备上传内容；如果要拒绝该设备后续请求，请使用“切换禁用”。",
		"devices.toggle_send":           "切换实时分发",
		"devices.toggle_receive":        "切换实时接收",
		"devices.toggle_disable":        "切换禁用设备",
		"devices.revoke_token":          "吊销令牌",
		"devices.online":                "在线",
		"devices.send":                  "允许实时分发",
		"devices.receive":               "允许实时接收",
		"devices.disabled":              "禁用",
		"common.yes":                    "是",
		"common.no":                     "否",
		"tokens.create":                 "创建接入码",
		"tokens.max_uses":               "最大使用次数",
		"tokens.expires_hours":          "多少小时后过期",
		"tokens.no_expiry":              "永不过期",
		"tokens.generate":               "生成接入码",
		"tokens.new_code":               "新接入码",
		"tokens.existing":               "现有接入码",
		"tokens.active":                 "可用",
		"tokens.exhausted":              "已用完",
		"tokens.expired":                "已过期",
		"tokens.revoked":                "已撤销",
		"tokens.revoke":                 "撤销",
		"tokens.empty":                  "还没有接入码。",
		"settings.runtime":              "运行时设置",
		"settings.enable_realtime":      "开启实时分发",
		"settings.enable_realtime_help": "开启后，某设备实时上传的新内容会立即推送给其他在线且允许接收的设备；关闭后仍会入库，但不再实时推送。",
		"settings.retention_days":       "保留天数（0 表示永久）",
		"settings.image_max_bytes":      "图片大小上限（字节）",
		"settings.save":                 "保存设置",
		"settings.security":             "安全",
		"settings.current_admin":        "当前管理员",
		"settings.password_help":        "管理员密码保存在数据库中。首次启动时会自动生成 10 位随机密码并输出到启动日志，后续可在这里修改。",
		"settings.current_password":     "当前密码",
		"settings.new_password":         "新密码",
		"settings.confirm_password":     "确认新密码",
		"settings.change_password":      "修改密码",
		"settings.storage":              "存储",
		"settings.db_driver":            "数据库驱动",
		"settings.blob_dir":             "Blob 存储目录",
		"msg.invalid_form":              "表单无效",
		"msg.login_failed":              "登录失败",
		"msg.logged_out":                "已退出登录",
		"msg.device_updated":            "设备已更新",
		"msg.update_failed":             "更新失败",
		"msg.device_token_revoked":      "设备令牌已吊销",
		"msg.revoke_failed":             "撤销失败",
		"msg.token_created":             "接入码已创建",
		"msg.token_revoked":             "接入码已撤销",
		"msg.token_not_active":          "接入码当前不可撤销",
		"msg.save_failed":               "保存失败",
		"msg.settings_saved":            "设置已保存",
		"msg.password_changed":          "管理员密码已更新",
		"msg.password_current_invalid":  "当前密码不正确",
		"msg.password_confirm_mismatch": "两次输入的新密码不一致",
		"msg.password_too_short":        "新密码长度至少需要 8 位",
	},
	langEN: {
		"app.admin_console":             "Admin Console",
		"nav.history":                   "History",
		"nav.devices":                   "Devices",
		"nav.tokens":                    "Tokens",
		"nav.settings":                  "Settings",
		"action.logout":                 "Logout",
		"lang.zh":                       "中文",
		"lang.en":                       "English",
		"lang.select":                   "Switch language",
		"page.login":                    "Admin Login",
		"page.history":                  "Clipboard History",
		"page.history.subtitle":         "Review text and image records with quick filters for device, kind, and content.",
		"page.devices":                  "Devices",
		"page.devices.subtitle":         "Manage device identity, online state, realtime fanout, realtime receive, and access tokens.",
		"page.tokens":                   "Enrollment Tokens",
		"page.tokens.subtitle":          "Create new enrollment codes and review token status, usage, and expiry at a glance.",
		"page.settings":                 "Settings",
		"page.settings.subtitle":        "Adjust realtime delivery, retention rules, image upload limits, and the admin password.",
		"login.subtitle":                "Server admin console login",
		"login.username":                "Username",
		"login.password":                "Password",
		"login.submit":                  "Login",
		"history.all_devices":           "All devices",
		"history.all_kinds":             "All kinds",
		"history.kind.text":             "Text",
		"history.kind.image":            "Image",
		"history.search_placeholder":    "Search text content",
		"history.filter":                "Filter",
		"table.id":                      "ID",
		"table.device":                  "Device",
		"table.kind":                    "Kind",
		"table.upload":                  "Upload",
		"table.preview":                 "Preview",
		"table.created":                 "Created",
		"table.os":                      "OS",
		"table.platform":                "Platform",
		"table.version":                 "Version",
		"table.flags":                   "Flags",
		"table.last_seen":               "Last Seen",
		"table.actions":                 "Actions",
		"table.prefix":                  "Prefix",
		"table.usage":                   "Usage",
		"table.status":                  "Status",
		"table.expires":                 "Expires",
		"table.action":                  "Action",
		"history.chars":                 "chars",
		"history.empty":                 "No clipboard items yet.",
		"devices.nickname":              "Nickname",
		"devices.unnamed":               "Unnamed",
		"devices.no_devices":            "No devices registered.",
		"devices.realtime_help":         "Realtime fanout and realtime receive only control WebSocket delivery. They do not block uploads; use disable if you want to reject future device requests.",
		"devices.toggle_send":           "Toggle Realtime Fanout",
		"devices.toggle_receive":        "Toggle Realtime Receive",
		"devices.toggle_disable":        "Toggle Device Disable",
		"devices.revoke_token":          "Revoke Token",
		"devices.online":                "online",
		"devices.send":                  "allow fanout",
		"devices.receive":               "allow receive",
		"devices.disabled":              "disabled",
		"common.yes":                    "yes",
		"common.no":                     "no",
		"tokens.create":                 "Create Enrollment Token",
		"tokens.max_uses":               "Max uses",
		"tokens.expires_hours":          "Expires in hours",
		"tokens.no_expiry":              "No expiry",
		"tokens.generate":               "Generate Token",
		"tokens.new_code":               "New code",
		"tokens.existing":               "Existing Tokens",
		"tokens.active":                 "active",
		"tokens.exhausted":              "exhausted",
		"tokens.expired":                "expired",
		"tokens.revoked":                "revoked",
		"tokens.revoke":                 "Revoke",
		"tokens.empty":                  "No enrollment tokens yet.",
		"settings.runtime":              "Runtime Settings",
		"settings.enable_realtime":      "Enable realtime fanout",
		"settings.enable_realtime_help": "When enabled, newly uploaded realtime items are pushed to other online devices that allow receiving. When disabled, items are still stored but not pushed in realtime.",
		"settings.retention_days":       "Retention days (0 = forever)",
		"settings.image_max_bytes":      "Image max bytes",
		"settings.save":                 "Save Settings",
		"settings.security":             "Security",
		"settings.current_admin":        "Current admin",
		"settings.password_help":        "The admin password is stored in the database. On first startup the server generates a random 10-character password and prints it to the startup log. You can change it here later.",
		"settings.current_password":     "Current password",
		"settings.new_password":         "New password",
		"settings.confirm_password":     "Confirm new password",
		"settings.change_password":      "Change Password",
		"settings.storage":              "Storage",
		"settings.db_driver":            "Database driver",
		"settings.blob_dir":             "Blob directory",
		"msg.invalid_form":              "Invalid form",
		"msg.login_failed":              "Login failed",
		"msg.logged_out":                "Logged out",
		"msg.device_updated":            "Device updated",
		"msg.update_failed":             "Update failed",
		"msg.device_token_revoked":      "Device token revoked",
		"msg.revoke_failed":             "Revoke failed",
		"msg.token_created":             "Token created",
		"msg.token_revoked":             "Token revoked",
		"msg.token_not_active":          "Token is no longer active",
		"msg.save_failed":               "Save failed",
		"msg.settings_saved":            "Settings saved",
		"msg.password_changed":          "Admin password updated",
		"msg.password_current_invalid":  "Current password is incorrect",
		"msg.password_confirm_mismatch": "The new passwords do not match",
		"msg.password_too_short":        "The new password must be at least 8 characters long",
	},
}

func normalizeLang(raw string) string {
	value := strings.TrimSpace(strings.ToLower(raw))
	switch {
	case strings.HasPrefix(value, "en"):
		return langEN
	case strings.HasPrefix(value, "zh"):
		return langZH
	default:
		return ""
	}
}

func detectLangFromHeader(header string) string {
	for _, part := range strings.Split(header, ",") {
		if lang := normalizeLang(part); lang != "" {
			return lang
		}
	}
	return langZH
}

func (a *App) resolveLang(w http.ResponseWriter, r *http.Request) string {
	if lang := normalizeLang(r.URL.Query().Get("lang")); lang != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     langCookieName,
			Value:    lang,
			Path:     "/",
			HttpOnly: false,
			SameSite: http.SameSiteLaxMode,
			Expires:  time.Now().Add(365 * 24 * time.Hour),
		})
		return lang
	}
	if cookie, err := r.Cookie(langCookieName); err == nil {
		if lang := normalizeLang(cookie.Value); lang != "" {
			return lang
		}
	}
	return detectLangFromHeader(r.Header.Get("Accept-Language"))
}

func translate(lang, key string) string {
	if catalog, ok := i18nCatalog[lang]; ok {
		if message, ok := catalog[key]; ok {
			return message
		}
	}
	if fallback, ok := i18nCatalog[langEN][key]; ok {
		return fallback
	}
	return key
}

func (a *App) translator(lang string) func(string) string {
	return func(key string) string {
		return translate(lang, key)
	}
}

func translateMessage(t func(string) string, raw string) string {
	if raw == "" {
		return ""
	}
	key := "msg." + raw
	message := t(key)
	if message == key {
		return raw
	}
	return message
}

func languageURL(r *http.Request, lang string) string {
	cloned := *r.URL
	query := cloned.Query()
	query.Set("lang", lang)
	cloned.RawQuery = query.Encode()
	return cloned.String()
}

func redirectWithMessage(w http.ResponseWriter, r *http.Request, path, messageKey string, extra map[string]string) {
	values := url.Values{}
	if messageKey != "" {
		values.Set("msg", messageKey)
	}
	for key, value := range extra {
		values.Set(key, value)
	}
	target := path
	if encoded := values.Encode(); encoded != "" {
		target += "?" + encoded
	}
	http.Redirect(w, r, target, http.StatusFound)
}
