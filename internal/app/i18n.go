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
		"table.app":              "应用名称",
		"tokens.window":          "临时接入窗口",
		"tokens.open":            "开启 / 重新开启 5 分钟",
		"tokens.close":           "立即关闭",
		"tokens.opened":          "接入已开启",
		"tokens.closed":          "接入已关闭",
		"tokens.code":            "当前接入码",
		"tokens.remaining":       "剩余秒数",
		"tokens.help":            "开启期间可无限次注册。重新开启会替换接入码并重新计时；服务端重启后默认关闭。",
		"settings.file_max_help": "正数限制单文件字节数，0 不限大小，-1 不接受文件上传。",
		"msg.enrollment_opened":  "接入已开启，5 分钟后自动关闭",
		"msg.enrollment_closed":  "接入已关闭",

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
		"page.history.subtitle":         "查看文本与文件记录，按设备、内容类型和文本关键词快速筛选。",
		"page.devices":                  "设备管理",
		"page.devices.subtitle":         "管理设备身份、在线状态、实时分发权限、实时接收权限与删除操作。",
		"page.tokens":                   "接入码管理",
		"page.tokens.subtitle":          "随时开启或关闭接入；每次开启持续 5 分钟，期间接入码可无限次使用。",
		"page.settings":                 "设置",
		"page.settings.subtitle":        "调整数据保留、文件上传限制和管理员密码。",
		"login.subtitle":                "服务端管理后台登录",
		"login.username":                "用户名",
		"login.password":                "密码",
		"login.submit":                  "登录",
		"history.all_devices":           "全部设备",
		"history.all_kinds":             "全部类型",
		"history.kind.text":             "文本",
		"history.kind.file":             "文件",
		"history.search_placeholder":    "搜索文本内容",
		"history.filter":                "筛选",
		"history.download":              "下载文件",
		"history.deleted_device":        "已删除设备",
		"history.pagination":            "分页",
		"history.page_info":             "第 %d / %d 页",
		"history.total_items":           "共 %d 条记录",
		"history.prev_page":             "上一页",
		"history.next_page":             "下一页",
		"table.id":                      "编号",
		"table.device":                  "设备",
		"table.kind":                    "类型",
		"table.upload":                  "上传方式",
		"table.preview":                 "预览",
		"table.created":                 "创建时间",
		"table.version":                 "版本",
		"table.flags":                   "状态",
		"table.last_seen":               "最后在线",
		"table.actions":                 "操作",
		"table.status":                  "状态",
		"table.action":                  "操作",
		"history.chars":                 "字符",
		"history.empty":                 "还没有剪切板记录。",
		"devices.nickname":              "昵称",
		"devices.unnamed":               "未命名",
		"devices.no_devices":            "还没有注册设备。",
		"devices.realtime_help":         "关闭发送或接收只限制分发与同步，设备仍可连接和上传。删除设备会撤销认证并断开连接，保留设备记录和历史来源关联。",
		"devices.toggle_send":           "切换实时分发",
		"devices.toggle_receive":        "切换实时接收",
		"devices.delete_device":         "删除设备",
		"devices.online":                "在线",
		"devices.send":                  "允许实时分发",
		"devices.receive":               "允许实时接收",
		"common.yes":                    "是",
		"common.no":                     "否",
		"settings.runtime":              "运行时设置",
		"settings.retention_days":       "保留天数（0 表示永久）",
		"settings.file_max_bytes":       "文件大小上限（字节）",
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
		"msg.device_deleted":            "设备已删除",
		"msg.revoke_failed":             "撤销失败",
		"msg.save_failed":               "保存失败",
		"msg.settings_saved":            "设置已保存",
		"msg.password_changed":          "管理员密码已更新",
		"msg.password_current_invalid":  "当前密码不正确",
		"msg.password_confirm_mismatch": "两次输入的新密码不一致",
		"msg.password_too_short":        "新密码长度至少需要 8 位",
	},
	langEN: {
		"table.app":              "Application",
		"tokens.window":          "Temporary Enrollment",
		"tokens.open":            "Open / Restart Five Minutes",
		"tokens.close":           "Close Now",
		"tokens.opened":          "Enrollment open",
		"tokens.closed":          "Enrollment closed",
		"tokens.code":            "Current code",
		"tokens.remaining":       "Seconds remaining",
		"tokens.help":            "Unlimited registrations while open. Reopening replaces the code and restarts the countdown. Enrollment is closed after a server restart.",
		"settings.file_max_help": "Positive: maximum bytes per file; 0: unlimited; -1: reject file uploads.",
		"msg.enrollment_opened":  "Enrollment opened for five minutes",
		"msg.enrollment_closed":  "Enrollment closed",

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
		"page.history.subtitle":         "Review text and file records with quick filters for device, kind, and content.",
		"page.devices":                  "Devices",
		"page.devices.subtitle":         "Manage device identity, online state, realtime fanout, realtime receive, and device deletion.",
		"page.tokens":                   "Enrollment Tokens",
		"page.tokens.subtitle":          "Open or close enrollment at any time. Each window lasts five minutes with unlimited registrations.",
		"page.settings":                 "Settings",
		"page.settings.subtitle":        "Adjust retention rules, file upload limits, and the admin password.",
		"login.subtitle":                "Server admin console login",
		"login.username":                "Username",
		"login.password":                "Password",
		"login.submit":                  "Login",
		"history.all_devices":           "All devices",
		"history.all_kinds":             "All kinds",
		"history.kind.text":             "Text",
		"history.kind.file":             "File",
		"history.search_placeholder":    "Search text content",
		"history.filter":                "Filter",
		"history.download":              "Download file",
		"history.deleted_device":        "Deleted device",
		"history.pagination":            "Pagination",
		"history.page_info":             "Page %d / %d",
		"history.total_items":           "%d total items",
		"history.prev_page":             "Previous",
		"history.next_page":             "Next",
		"table.id":                      "ID",
		"table.device":                  "Device",
		"table.kind":                    "Kind",
		"table.upload":                  "Upload",
		"table.preview":                 "Preview",
		"table.created":                 "Created",
		"table.version":                 "Version",
		"table.flags":                   "Flags",
		"table.last_seen":               "Last Seen",
		"table.actions":                 "Actions",
		"table.status":                  "Status",
		"table.action":                  "Action",
		"history.chars":                 "chars",
		"history.empty":                 "No clipboard items yet.",
		"devices.nickname":              "Nickname",
		"devices.unnamed":               "Unnamed",
		"devices.no_devices":            "No devices registered.",
		"devices.realtime_help":         "Turning off send or receive limits delivery and synchronization; connections and uploads remain allowed. Deleting a device revokes access and disconnects it while preserving its record and history links.",
		"devices.toggle_send":           "Toggle Realtime Fanout",
		"devices.toggle_receive":        "Toggle Realtime Receive",
		"devices.delete_device":         "Delete Device",
		"devices.online":                "online",
		"devices.send":                  "allow fanout",
		"devices.receive":               "allow receive",
		"common.yes":                    "yes",
		"common.no":                     "no",
		"settings.runtime":              "Runtime Settings",
		"settings.retention_days":       "Retention days (0 = forever)",
		"settings.file_max_bytes":       "File max bytes",
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
		"msg.device_deleted":            "Device deleted",
		"msg.revoke_failed":             "Revoke failed",
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
