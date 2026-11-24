package app

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

var securityDefaults = Row{"private_mode": "off", "group_mode": "off", "allow_group_override": 1, "trust_minutes": 30, "challenge_seconds": 180, "max_attempts": 3, "cooldown_seconds": 300, "search_limit": 30, "group_limit": 20}
var verificationModes = []string{"off", "emoji", "button", "text", "math", "sequence", "random"}

func (a *App) securitySettings() Row {
	v := catalogMerge(securityDefaults, decode(a.setting("bot_security", "{}")))
	return v
}
func (a *App) securityPolicy(botID, chatID int64) Row {
	p := a.securitySettings()
	p["mode"] = p["private_mode"]
	if chatID < 0 {
		p["mode"] = p["group_mode"]
		if boolean(p["allow_group_override"]) {
			g, _ := one(a.DB, "SELECT verify_mode FROM ts_bot_groups WHERE bot_id=? AND chat_id=?", botID, chatID)
			if g != nil && str(g["verify_mode"]) != "inherit" && slices.Contains(verificationModes, str(g["verify_mode"])) {
				p["mode"] = g["verify_mode"]
			}
		}
	}
	p["hash"] = hash(encode([]any{p["revision"], p["mode"], p["trust_minutes"]}))
	return p
}
func (a *App) saveSetting(name string, v any) error {
	s := str(v)
	_, e := a.DB.Exec("INSERT INTO ts_settings(name,value) VALUES(?,?) ON DUPLICATE KEY UPDATE value=?", name, s, s)
	return e
}
func (a *App) RegisterBotAdmin() {
	a.Handle("GET /admin/api/bot", "admin", "bot.read", false, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		JSON(w, id.Access.Project("bot", a.botState()))
	})
	a.Handle("POST /admin/api/bot", "admin", "bot.update", true, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		b, e := Body(r)
		if e != nil {
			Error(w, e)
			return
		}
		if e = id.Access.CheckWrite("bot", b); e != nil {
			Error(w, e)
			return
		}
		if size, ok := b["page_size"]; ok {
			if n := integer(size); n < 1 || n > 30 {
				Fail(w, 422, "每页显示条数须为 1–30 的整数。")
				return
			}
		}
		if token, ok := b["token"]; ok {
			s := strings.TrimSpace(str(token))
			if !regexp.MustCompile(`^[1-9]\d{4,19}:[A-Za-z0-9_-]{20,128}$`).MatchString(s) {
				Fail(w, 422, "请输入完整的 Bot Token。")
				return
			}
			v, e := a.telegramRaw(r.Context(), s, "getMe", Row{})
			bot := object(v)
			if e != nil || !boolean(bot["is_bot"]) || str(bot["id"]) != strings.Split(s, ":")[0] || str(bot["username"]) == "" {
				Fail(w, 422, "机器人 Token 验证失败，请检查凭据与网络。")
				return
			}
			version := randomHex(16)
			cipher, e := seal(a.Root, ".bot-token.key", s, "telegram_bot_credentials:"+version)
			if e != nil {
				Error(w, e)
				return
			}
			if e = a.saveSetting("telegram_bot_credentials", encode(Row{"encrypted": cipher, "version": version, "bot": Row{"id": bot["id"], "username": bot["username"], "name": bot["first_name"]}, "updated_at": now()})); e != nil {
				Error(w, e)
				return
			}
		}
		if size, ok := b["page_size"]; ok {
			if e = a.saveSetting("bot_page_size", integer(size)); e != nil {
				Error(w, e)
				return
			}
		}
		a.Audit(id.ID, "更新机器人配置", "已验证并加密保存配置")
		JSON(w, id.Access.Project("bot", a.botState()))
	})
	a.Handle("GET /admin/api/bot-menu", "admin", "bot_menu.read", false, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		p := a.botMenuPayload()
		p["config"] = id.Access.Project("bot_menu", object(p["config"]))
		JSON(w, p)
	})
	a.Handle("POST /admin/api/bot-menu", "admin", "bot_menu.update", true, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		b, e := Body(r)
		if e != nil {
			Error(w, e)
			return
		}
		if e = id.Access.CheckWrite("bot_menu", b); e != nil {
			Error(w, e)
			return
		}
		v := catalogMerge(a.botMenu(), b)
		if e = a.validateMenu(v); e != nil {
			Error(w, e)
			return
		}
		tx, e := a.DB.Begin()
		if e != nil {
			Error(w, e)
			return
		}
		defer tx.Rollback()
		welcome := str(v["welcome"])
		delete(v, "welcome")
		for k, s := range map[string]string{"bot_welcome": welcome, "bot_menu_layout": encode(v)} {
			if _, e = tx.Exec("INSERT INTO ts_settings(name,value) VALUES(?,?) ON DUPLICATE KEY UPDATE value=?", k, s, s); e != nil {
				Error(w, e)
				return
			}
		}
		if e = catalogAudit(tx, id.ID, "更新机器人菜单", "欢迎语与菜单布局已保存"); e == nil {
			e = tx.Commit()
		}
		if e != nil {
			Error(w, e)
			return
		}
		p := a.botMenuPayload()
		p["config"] = id.Access.Project("bot_menu", object(p["config"]))
		JSON(w, p)
	})
	a.Handle("GET /admin/api/bot-security", "admin", "bot_security.read", false, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		JSON(w, id.Access.Project("bot_security", a.securitySettings()))
	})
	a.Handle("POST /admin/api/bot-security", "admin", "bot_security.update", true, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		b, e := Body(r)
		if e != nil {
			Error(w, e)
			return
		}
		if e = id.Access.CheckWrite("bot_security", b); e != nil {
			Error(w, e)
			return
		}
		bounds := map[string][2]int64{"allow_group_override": {0, 1}, "trust_minutes": {1, 1440}, "challenge_seconds": {30, 600}, "max_attempts": {1, 5}, "cooldown_seconds": {30, 3600}, "search_limit": {1, 60}, "group_limit": {1, 20}}
		for k, v := range b {
			if k == "private_mode" || k == "group_mode" {
				if !slices.Contains(verificationModes, str(v)) {
					Fail(w, 422, "请选择有效验证方式。")
					return
				}
			} else {
				limit, ok := bounds[k]
				n := integer(v)
				if !ok || n < limit[0] || n > limit[1] {
					Fail(w, 422, "安全设置超出允许范围。")
					return
				}
				b[k] = n
			}
		}
		p := catalogMerge(a.securitySettings(), b)
		p["revision"] = randomHex(8)
		if e = a.saveSetting("bot_security", encode(p)); e != nil {
			Error(w, e)
			return
		}
		a.Audit(id.ID, "更新机器人安全策略", "搜索验证与限流配置已保存")
		JSON(w, id.Access.Project("bot_security", p))
	})
	a.Handle("GET /admin/api/bot-groups", "admin", "bot_groups.read", false, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		where := " WHERE 1=1"
		args := []any{}
		if v := r.URL.Query().Get("id"); v != "" {
			where += " AND id=?"
			args = append(args, v)
		}
		if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
			filters := []string{}
			for _, field := range []string{"title", "chat_id"} {
				if catalogReadable(id.Access, "bot_groups", field) {
					filters = append(filters, field+" LIKE ? ESCAPE '!'")
					args = append(args, catalogLike(q))
				}
			}
			if len(filters) > 0 {
				where += " AND (" + strings.Join(filters, " OR ") + ")"
			}
		}
		count, e := one(a.DB, "SELECT COUNT(*) AS n FROM ts_bot_groups"+where, args...)
		if e != nil {
			Error(w, e)
			return
		}
		page := catalogLimit(r.URL.Query().Get("page"), 1, 1, 100000)
		items, e := query(a.DB, "SELECT * FROM ts_bot_groups"+where+" ORDER BY id DESC LIMIT 20 OFFSET ?", append(args, (page-1)*20)...)
		if e != nil {
			Error(w, e)
			return
		}
		for i, r := range items {
			items[i] = id.Access.Project("bot_groups", r)
		}
		JSON(w, Row{"items": items, "total": integer(count["n"]), "page": page, "page_size": 20})
	})
	a.Handle("POST /admin/api/bot-groups/{id}", "admin", "bot_groups.update", true, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		b, e := Body(r)
		if e != nil {
			Error(w, e)
			return
		}
		if e = id.Access.CheckWrite("bot_groups", b); e != nil {
			Error(w, e)
			return
		}
		values, e := validateGroupSettings(b)
		if e != nil {
			Error(w, e)
			return
		}
		group, e := one(a.DB, "SELECT * FROM ts_bot_groups WHERE id=?", integer(r.PathValue("id")))
		if e != nil {
			Error(w, e)
			return
		}
		if group == nil {
			Fail(w, 404, "群记录不存在。")
			return
		}
		if e = a.checkGroupCapabilities(r.Context(), group, values); e != nil {
			Error(w, e)
			return
		}
		v, e := a.saveGroupSettings(integer(r.PathValue("id")), b, id.ID, 0)
		if e != nil {
			Error(w, e)
			return
		}
		JSON(w, id.Access.Project("bot_groups", v))
	})
}

var menuActions = Row{"hot": "查看热搜", "discover": "发现资源", "rank": "群频道榜", "mine": "我的资料", "commerce": "营销中心与钱包", "help": "使用帮助", "submit": "提交收录说明", "search": "搜索指定关键词", "message": "回复自定义文字", "url": "打开 HTTPS 链接"}

func (a *App) botMenu() Row {
	v := decode(a.setting("bot_menu_layout", "{}"))
	if len(v) == 0 {
		rows := []any{}
		for _, names := range [][]string{{"hot", "discover"}, {"rank", "mine"}, {"help", "submit"}} {
			row := []any{}
			for _, key := range names {
				icon := key
				if key == "mine" {
					icon = "user"
				}
				row = append(row, Row{"id": key, "text": menuActions[key], "icon": icon, "action": key, "value": "", "style": "default", "enabled": true})
			}
			rows = append(rows, row)
		}
		v = Row{"welcome_icon": "search", "hint": "发送关键词，发现值得收藏的资源。", "rows": rows}
	}
	v["welcome"] = a.setting("bot_welcome", "你好，{{nickname}}！欢迎使用星索，发送关键词开始搜索。")
	return v
}
func (a *App) botMenuPayload() Row {
	return Row{"config": a.botMenu(), "icons": iconCatalog(a.Root), "actions": menuActions, "pack": "tgiosicons", "delivery": a.botState()["custom_emoji"], "template": a.templateMetadata()}
}

var templateLabels = Row{"nickname": "昵称", "username": "用户名", "user_id": "用户 ID", "first_name": "名字", "last_name": "姓氏", "language_code": "语言", "is_premium": "Premium", "datetime": "当前时间", "date": "日期", "time": "时间", "weekday": "星期", "timezone": "时区", "site_name": "站点名称"}
var tagPattern = regexp.MustCompile(`\{\{([^{}]*)\}\}`)

func validateTemplate(s string) error {
	for _, m := range tagPattern.FindAllStringSubmatch(s, -1) {
		if templateLabels[strings.TrimSpace(m[1])] == nil {
			return &DomainError{Status: 422, Message: "欢迎语包含未知标签：" + m[0]}
		}
	}
	rest := tagPattern.ReplaceAllString(s, "")
	if strings.Contains(rest, "{{") || strings.Contains(rest, "}}") {
		return &DomainError{Status: 422, Message: "欢迎语标签格式不完整。"}
	}
	return nil
}
func (a *App) templateValues(from Row) Row {
	t := time.Now().In(location)
	first, last := strings.TrimSpace(str(from["first_name"])), strings.TrimSpace(str(from["last_name"]))
	nick := strings.TrimSpace(first + " " + last)
	if nick == "" {
		nick = "Telegram 用户"
	}
	if first == "" {
		first = "Telegram 用户"
	}
	if last == "" {
		last = "未设置"
	}
	user := strings.TrimPrefix(str(from["username"]), "@")
	if user != "" {
		user = "@" + user
	} else {
		user = "未设置"
	}
	lang := str(from["language_code"])
	if lang == "" {
		lang = "未知"
	}
	premium := "否"
	if boolean(from["is_premium"]) {
		premium = "是"
	}
	return Row{"nickname": nick, "username": user, "user_id": str(from["id"]), "first_name": first, "last_name": last, "language_code": lang, "is_premium": premium, "datetime": t.Format("2006-01-02 15:04:05"), "date": t.Format("2006-01-02"), "time": t.Format("15:04:05"), "weekday": []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}[int(t.Weekday())], "timezone": "Asia/Shanghai", "site_name": a.Settings()["site_name"]}
}
func (a *App) templateMetadata() Row {
	tags := []Row{}
	for _, k := range []string{"nickname", "username", "user_id", "first_name", "last_name", "language_code", "is_premium", "datetime", "date", "time", "weekday", "timezone", "site_name"} {
		group := "用户信息"
		if slices.Contains([]string{"datetime", "date", "time", "weekday", "timezone", "site_name"}, k) {
			group = "时间与站点"
		}
		tags = append(tags, Row{"key": k, "token": "{{" + k + "}}", "label": templateLabels[k], "group": group, "description": templateLabels[k]})
	}
	return Row{"tags": tags, "limits": Row{"welcome": 1800, "hint": 240}, "timezone": "Asia/Shanghai", "server_time": time.Now().In(location).Format(time.RFC3339), "sample_values": a.templateValues(Row{"id": 123456789, "first_name": "小林", "username": "lin_demo", "language_code": "zh-hans", "is_premium": true})}
}
func renderTemplate(text string, values Row, limit int) string {
	return clip(tagPattern.ReplaceAllStringFunc(text, func(s string) string {
		m := tagPattern.FindStringSubmatch(s)
		if v, ok := values[strings.TrimSpace(m[1])]; ok {
			return str(v)
		}
		return s
	}), limit)
}
func (a *App) validateMenu(v Row) error {
	bad := func(s string) error { return &DomainError{Status: 422, Message: s} }
	for k, limit := range map[string]int{"welcome": 1800, "hint": 240} {
		text, ok := v[k].(string)
		if !ok || utf8.RuneCountInString(text) > limit || (k == "welcome" && strings.TrimSpace(text) == "") {
			return bad("欢迎语或提示语长度无效。")
		}
		if e := validateTemplate(text); e != nil {
			return e
		}
	}
	icons := iconCatalog(a.Root)
	if str(v["welcome_icon"]) != "" && icons[str(v["welcome_icon"])] == nil {
		return bad("请选择有效图标。")
	}
	rows, ok := v["rows"].([]any)
	if !ok || len(rows) > 10 {
		return bad("菜单最多 10 行。")
	}
	seen := map[string]bool{}
	for _, value := range rows {
		row, ok := value.([]any)
		if !ok || len(row) < 1 || len(row) > 5 {
			return bad("每行须有 1–5 个按钮。")
		}
		for _, v := range row {
			b := object(v)
			id := str(b["id"])
			if !regexp.MustCompile(`^[a-z0-9_]{1,20}$`).MatchString(id) || seen[id] || id == "home" {
				return bad("按钮标识无效或重复。")
			}
			seen[id] = true
			if len(seen) > 24 {
				return bad("菜单最多 24 个按钮。")
			}
			if b["enabled"] != true && b["enabled"] != false {
				return bad("按钮启用状态无效。")
			}
			if strings.TrimSpace(str(b["text"])) == "" || utf8.RuneCountInString(str(b["text"])) > 24 {
				return bad("按钮文字须为 1–24 字。")
			}
			if str(b["icon"]) != "" && icons[str(b["icon"])] == nil {
				return bad("按钮图标无效。")
			}
			if !slices.Contains([]string{"default", "primary", "success", "danger"}, str(b["style"])) {
				return bad("按钮颜色无效。")
			}
			action := str(b["action"])
			if menuActions[action] == nil {
				return bad("按钮动作无效。")
			}
			value := str(b["value"])
			if slices.Contains([]string{"url", "search", "message"}, action) && strings.TrimSpace(value) == "" {
				return bad("请填写按钮动作内容。")
			}
			limit := 1800
			if action == "search" {
				limit = 120
			}
			if action == "url" {
				limit = 500
				u, e := url.Parse(value)
				if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
					return bad("按钮链接须为 HTTPS 地址。")
				}
			}
			if utf8.RuneCountInString(value) > limit {
				return bad("按钮内容过长。")
			}
		}
	}
	return nil
}
func escape(s any) string { return html.EscapeString(str(s)) }
