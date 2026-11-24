package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

func (a *App) handleUpdate(ctx context.Context, u, bot Row) error {
	botID := integer(bot["id"])
	if event, ok := u["my_chat_member"]; ok {
		return a.Membership(ctx, object(event), botID)
	}
	if event, ok := u["chat_member"]; ok {
		return a.GroupMemberEvent(ctx, catalogMerge(object(event), Row{"_update_id": u["update_id"]}), botID)
	}
	if callback, ok := u["callback_query"]; ok {
		cb := object(callback)
		ctx = botPrivateTurn(ctx, botID, object(cb["message"]), object(cb["from"]), true)
		e := a.botCallback(ctx, cb, botID)
		var de *DomainError
		var ce *commerceError
		if errors.As(e, &de) || errors.As(e, &ce) {
			return a.answerCallback(ctx, cb, clip(e.Error(), 200), true)
		}
		return e
	}
	m := object(u["message"])
	if len(m) == 0 {
		m = object(u["edited_message"])
	}
	if len(m) == 0 {
		return nil
	}
	handled, e := a.GroupServiceMessage(ctx, catalogMerge(m, Row{"_update_id": u["update_id"]}), botID)
	if e != nil || handled {
		return e
	}
	handled, e = a.Moderate(ctx, m, botID)
	if e != nil {
		return e
	}
	if handled || u["edited_message"] != nil {
		return nil
	}
	chat := object(m["chat"])
	chatID := integer(chat["id"])
	from := object(m["from"])
	text := strings.TrimSpace(str(m["text"]))
	if !slices.Contains([]string{"private", "group", "supergroup"}, str(chat["type"])) || boolean(m["is_automatic_forward"]) || boolean(from["is_bot"]) {
		return nil
	}
	if m["sender_chat"] != nil || integer(from["id"]) <= 0 {
		return nil
	}
	if chatID < 0 {
		allowed, e := a.GroupSearchAllowed(botID, chatID, integer(from["id"]))
		if e != nil || !allowed {
			return e
		}
	}
	command, arg := "", text
	if strings.HasPrefix(text, "/") {
		parts := strings.SplitN(text, " ", 2)
		head := strings.SplitN(strings.TrimPrefix(parts[0], "/"), "@", 2)
		if len(head) > 1 && !strings.EqualFold(head[1], str(bot["username"])) {
			return nil
		}
		command = strings.ToLower(head[0])
		arg = ""
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}
		if command == "submit" {
			command = "add"
		}
	}
	if !botCommandAllowed(command, str(chat["type"])) {
		return nil
	}
	if str(chat["type"]) == "private" && (chatID <= 0 || chatID != integer(from["id"])) {
		return nil
	}
	ctx = botPrivateTurn(ctx, botID, m, from, false)
	if botDisposableCommand(command, arg) {
		ctx = botCleanInput(ctx)
	}
	if e = a.RateLimit("bot:"+str(from["id"]), 30, 60); e != nil {
		return a.sendText(ctx, chatID, e.Error(), replyContext(m))
	}
	run := func() error {
		if command == "start" && strings.HasPrefix(arg, "auth_") && chatID > 0 {
			return a.promptTelegramAuth(ctx, chatID, strings.TrimPrefix(arg, "auth_"), from)
		}
		if command == "start" && strings.HasPrefix(arg, "join_") && chatID > 0 {
			return a.JoinVerificationStart(ctx, m, botID, strings.TrimPrefix(arg, "join_"))
		}
		if chatID < 0 && botGroupAdminCommand(command) {
			return a.GroupCommand(ctx, m, botID, command, arg)
		}
		if handled, e := a.JoinVerificationText(ctx, m, botID); e != nil || handled {
			return e
		}
		if handled, e := a.CaptchaText(ctx, m, botID); e != nil || handled {
			return e
		}
		if command == "" && chatID > 0 {
			if handled, e := a.botCommerceText(ctx, m, botID); e != nil || handled {
				return e
			}
		}
		if text == "" {
			return nil
		}
		if chatID > 0 && slices.Contains([]string{"start", "search", "discover", "add", "help"}, command) {
			if e := a.pauseBotCommerce(ctx, botID, chatID); e != nil {
				return e
			}
		}
		switch command {
		case "start":
			if chatID < 0 {
				return a.sendText(ctx, chatID, botGroupHelp, replyContext(m))
			}
			return a.botStart(ctx, chatID, from)
		case "help":
			if chatID < 0 {
				return a.sendText(ctx, chatID, botGroupHelp, replyContext(m))
			}
			return a.botPanelText(ctx, chatID, botPrivateHelp, nil)
		case "account", "wallet", "ads", "recharge", "cancel", "login":
			if chatID < 0 {
				return nil
			}
			user, e := a.telegramUser(from)
			if e != nil {
				return e
			}
			if command == "login" {
				return a.botWebsiteLogin(ctx, chatID, user)
			}
			view := map[string]string{"account": "home", "wallet": "resume", "ads": "ads", "recharge": "recharge", "cancel": "cancel"}[command]
			return a.openBotCommerce(ctx, chatID, user, botID, view)
		case "add":
			if chatID < 0 {
				return a.sendText(ctx, chatID, "请私聊机器人发送 /add 链接 提交收录。", replyContext(m))
			}
			if arg == "" {
				return a.botPanelText(ctx, chatID, "发送 /add https://t.me/username\n系统会自动获取标题并提交后台审核，无需输入其他内容。", nil)
			}
			user, e := a.telegramUser(from)
			if e != nil {
				return e
			}
			row, e := a.submitTelegram(ctx, integer(user["id"]), strings.TrimSpace(strings.SplitN(arg, "|", 2)[0]))
			if e != nil {
				return e
			}
			return a.botRecordText(ctx, botID, chatID, "已自动获取标题："+str(row["title"])+"\n\n收录申请 #"+str(row["id"])+" 已提交，请等待审核。", replyContext(m))
		case "search":
			if arg == "" {
				if chatID > 0 {
					return a.botPanelText(ctx, chatID, "请使用 /search 关键词 开始搜索。", nil)
				}
				return a.sendText(ctx, chatID, "请使用 /search 关键词 开始搜索。", replyContext(m))
			}
			return a.startSearch(ctx, m, arg, "")
		case "discover":
			return a.startSearch(ctx, m, "", "")
		case "":
			return a.startSearch(ctx, m, text, "")
		default:
			return nil
		}
	}
	if e = run(); e != nil {
		var de *DomainError
		var ce *commerceError
		if errors.As(e, &de) || errors.As(e, &ce) {
			return a.sendText(ctx, chatID, e.Error(), replyContext(m))
		}
		return e
	}
	return nil
}

const groupSearchVerificationRequired = "请先完成本次入群验证，再发送关键词搜索。"

// Composite search views are session filters, not editable resource types.
const (
	botSearchCommunities = "community"
	botSearchPosts       = "post"
)

func botSearchTypeLabel(typ string) string {
	switch typ {
	case botSearchCommunities:
		return "群组与频道"
	case botSearchPosts:
		return "帖子"
	case "":
		return "全部类型"
	default:
		return str(ResourceTypes[typ])
	}
}

func (a *App) startSearch(ctx context.Context, m Row, keyword, typ string) error {
	keyword = strings.TrimSpace(keyword)
	// Only new bot searches adopt this default. Existing empty-type sessions
	// retain their original all-resource snapshots, as do website/API queries.
	if typ == "" {
		typ = botSearchCommunities
	}
	if len([]rune(keyword)) > 120 {
		return &DomainError{Status: 422, Message: "搜索关键词最多 120 字。"}
	}
	c, e := a.botCredentials()
	if e != nil {
		return e
	}
	botID := integer(object(c["bot"])["id"])
	from := object(m["from"])
	chat := integer(object(m["chat"])["id"])
	if chat < 0 {
		allowed, e := a.GroupSearchAllowed(botID, chat, integer(from["id"]))
		if e != nil {
			return e
		}
		if !allowed {
			return catalogErr(403, groupSearchVerificationRequired)
		}
	}
	if gated, e := a.CaptchaGate(ctx, m, botID, keyword); e != nil || gated {
		return e
	}
	user, e := a.telegramUser(from)
	if e != nil {
		return e
	}
	size := a.botPageSize()
	s := Row{"id": randomHex(12), "telegram_user_id": from["id"], "chat_id": chat, "query": keyword, "type": typ, "page": int64(1), "revision": int64(1), "page_size": size, "filters_expanded": 0, "expires_at": future(2 * time.Hour)}
	res, e := a.Search(Row{"q": keyword, "type": typ, "page": 1, "page_size": size}, "bot")
	if e != nil {
		return e
	}
	s["search_token"] = res["search_token"]
	_, e = a.DB.Exec("INSERT INTO ts_bot_sessions(id,telegram_user_id,chat_id,query,type,page,revision,page_size,filters_expanded,search_token,expires_at) VALUES(?,?,?,?,?,1,1,?,0,?,?)", s["id"], from["id"], chat, keyword, typ, size, s["search_token"], s["expires_at"])
	if e != nil {
		return e
	}
	payload := a.renderSearch(s, res)
	payload["chat_id"] = chat
	for k, v := range replyContext(m) {
		payload[k] = v
	}
	sent, e := a.sendReply(ctx, "sendMessage", payload)
	if e != nil {
		return e
	}
	_, e = a.DB.Exec("UPDATE ts_bot_sessions SET message_id=? WHERE id=?", sent["message_id"], s["id"])
	if e != nil {
		return e
	}
	a.retireBotPanel(ctx, botID, chat, integer(sent["message_id"]))
	_, _ = a.Reward(integer(user["id"]), keyword, integer(res["total"]), "bot")
	return nil
}
func rowsOf(v any) []Row {
	if rows, ok := v.([]Row); ok {
		return rows
	}
	out := []Row{}
	for _, r := range array(v) {
		out = append(out, object(r))
	}
	return out
}
func compactTitle(s string, width int) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) || r == 0x200B || r == 0x200E || r == 0x200F || r == 0xFEFF {
			return -1
		}
		return r
	}, s)
	var out strings.Builder
	cols, units := 0, 0
	for _, r := range s {
		w, u := 1, 1
		if r >= 0x2E80 {
			w = 2
		}
		if unicode.IsMark(r) || r == 0x200D || r == 0x200C {
			w = 0
		}
		if r > 0xFFFF {
			u = 2
		}
		if cols+w > width-1 || units+u > 63 {
			return strings.TrimSpace(out.String()) + "…"
		}
		out.WriteRune(r)
		cols += w
		units += u
	}
	if out.Len() == 0 {
		return "未命名资源"
	}
	return out.String()
}
func (a *App) renderSearch(s, result Row) Row {
	typ := str(s["type"])
	label := botSearchTypeLabel(typ)
	heading := "发现资源"
	if str(s["query"]) != "" {
		heading = "搜索：" + str(s["query"])
	}
	text := a.iconHTML("search") + " <b>" + escape(heading) + "</b>\n" + label + "\n\n"
	ads, _ := a.LiveAdsSeed(str(s["query"]), str(s["id"]))
	for _, kind := range []string{"search_top", "keyword", "brand"} {
		if ad := object(ads[kind]); len(ad) > 0 {
			text += adHTML(ad) + "\n"
		}
	}
	if len(ads) > 0 {
		text += "\n"
	}
	items := rowsOf(result["items"])
	for i, r := range items {
		members := ""
		if n := integer(r["members"]); n > 0 {
			members = "  " + str(n)
			if n >= 1000 {
				members = "  " + strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", float64(n)/1000), "0"), ".") + " K"
			}
		}
		text += "\u200e" + str(i+1) + ". " + a.iconHTML(str(r["type"])) + " \u2068<a href=\"" + escape(r["url"]) + "\">" + escape(compactTitle(str(r["title"]), 74-len(members))) + "</a>\u2069\u200e" + members + "\n"
	}
	if len(items) == 0 {
		if typ == botSearchCommunities || typ == "group" || typ == "channel" {
			text += "暂无匹配的群组或频道，可点击「帖子」继续查找，或尝试其他关键词。\n"
		} else {
			text += "暂时没有匹配的资源，试试其他关键词。\n"
		}
	}
	pages := integer(result["total_pages"])
	if pages < 1 {
		pages = 1
	}
	text += "\n共 " + str(result["total"]) + " 条结果，当前第 " + str(s["page"]) + " 页，共 " + str(pages) + " 页"
	if result["window_total"] != nil && integer(result["window_total"]) < integer(result["total"]) {
		text += "\n本次随机展示 " + str(result["window_total"]) + " 条，可重新搜索换一组。"
	}
	if integer(s["chat_id"]) < 0 {
		text += "\n仅搜索者本人可操作下方按钮"
	}
	if pages > 100 {
		text += "\n可浏览前 100 页，更多结果请细化关键词。"
	}
	data := func(action string) string { return "s:" + str(s["id"]) + ":" + str(s["revision"]) + ":" + action }
	typeButton := func(t string) Row {
		label, icon := botSearchTypeLabel(t), t
		switch t {
		case botSearchCommunities:
			label, icon = "群频道", "group"
		case botSearchPosts:
			icon = "article"
		case "":
			label = "全部"
		}
		b := Row{"text": label, "callback_data": data("t" + t)}
		if typ == t {
			b["style"] = "primary"
		}
		return a.iconButton(icon, b)
	}
	expanded := boolean(s["filters_expanded"])
	expandText, action := "更多筛选", "f1"
	if expanded {
		expandText, action = "收起筛选", "f0"
	}
	buttons := [][]Row{{typeButton(botSearchCommunities), typeButton("group"), typeButton("channel"), typeButton(botSearchPosts), a.iconButton("plus", Row{"text": expandText, "callback_data": data(action)})}}
	if expanded {
		buttons = append(buttons, []Row{typeButton(""), typeButton("video"), typeButton("article"), typeButton("audio"), typeButton("file")}, []Row{typeButton("bot"), typeButton("shop"), typeButton("other"), typeButton("website")})
	}
	nav := []Row{}
	page := integer(s["page"])
	if page > 1 {
		nav = append(nav, a.iconButton("left", Row{"text": "上一页", "callback_data": data("p" + str(page-1))}))
	}
	home := "返回主菜单"
	if integer(s["chat_id"]) < 0 {
		home = "搜索帮助"
	}
	nav = append(nav, a.iconButton("menu", Row{"text": home, "callback_data": data("h")}))
	if page < pages && page < 100 {
		nav = append(nav, a.iconButton("right", Row{"text": "下一页", "callback_data": data("p" + str(page+1))}))
	}
	buttons = append(buttons, nav)
	if ad := object(ads["search_bottom"]); len(ad) > 0 {
		buttons = append(buttons, []Row{a.iconButton("link", Row{"text": "广告 · " + str(ad["title"]), "url": ad["url"]})})
	}
	return Row{"text": text, "parse_mode": "HTML", "link_preview_options": Row{"is_disabled": true}, "reply_markup": Row{"inline_keyboard": buttons}}
}
func (a *App) answerCallback(ctx context.Context, cb Row, text string, alert bool) error {
	_, e := a.Telegram(ctx, "answerCallbackQuery", Row{"callback_query_id": cb["id"], "text": text, "show_alert": alert, "cache_time": 0})
	var te *TelegramError
	if errors.As(e, &te) && (te.Code == 400 || te.Code == 403) {
		return nil
	}
	return e
}
func (a *App) botCallback(ctx context.Context, cb Row, botID int64) error {
	data := str(cb["data"])
	if strings.HasPrefix(data, "c:") {
		return a.botCommerceCallback(ctx, cb, botID)
	}
	if strings.HasPrefix(data, "auth:") {
		return a.confirmTelegramAuth(ctx, cb)
	}
	if strings.HasPrefix(data, "v:") {
		chat := integer(object(object(cb["message"])["chat"])["id"])
		if chat < 0 {
			allowed, e := a.GroupSearchAllowed(botID, chat, integer(object(cb["from"])["id"]))
			if e != nil {
				return e
			}
			if !allowed {
				return a.answerCallback(ctx, cb, groupSearchVerificationRequired, true)
			}
		}
		return a.CaptchaCallback(ctx, cb, botID)
	}
	if strings.HasPrefix(data, "j:") {
		return a.JoinVerificationCallback(ctx, cb, botID)
	}
	if strings.HasPrefix(data, "m:") {
		return a.menuCallback(ctx, cb)
	}
	p := regexp.MustCompile(`^s:([a-f0-9]{24}):(\d+):(p\d+|t[a-z]*|f[01]|l\d+|h)$`).FindStringSubmatch(data)
	if p == nil {
		return a.answerCallback(ctx, cb, "按钮无效，请重新搜索。", true)
	}
	tx, e := a.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	s, e := one(tx, "SELECT * FROM ts_bot_sessions WHERE id=? FOR UPDATE", p[1])
	if e != nil {
		return e
	}
	if s == nil || expired(s["expires_at"]) {
		return a.answerCallback(ctx, cb, "搜索已过期，请重新发送关键词。", true)
	}
	from, m := object(cb["from"]), object(cb["message"])
	if integer(s["telegram_user_id"]) != integer(from["id"]) {
		return a.answerCallback(ctx, cb, "不能操作别人的搜索结果。请发送关键词，创建你自己的搜索。", true)
	}
	if integer(s["chat_id"]) != integer(object(m["chat"])["id"]) || integer(s["message_id"]) != integer(m["message_id"]) {
		return a.answerCallback(ctx, cb, "这个按钮不属于当前消息。", true)
	}
	if integer(s["chat_id"]) < 0 {
		allowed, e := a.GroupSearchAllowed(botID, integer(s["chat_id"]), integer(from["id"]))
		if e != nil {
			return e
		}
		if !allowed {
			return a.answerCallback(ctx, cb, groupSearchVerificationRequired, true)
		}
	}
	if integer(s["revision"]) != integer(p[2]) {
		return a.answerCallback(ctx, cb, "页面已经更新，请使用最新的按钮。", false)
	}
	if e = a.RateLimit("bot-callback:"+str(from["id"]), 40, 60); e != nil {
		return a.answerCallback(ctx, cb, e.Error(), true)
	}
	if _, e = a.telegramUser(from); e != nil {
		return a.answerCallback(ctx, cb, e.Error(), true)
	}
	if p[3] == "h" {
		_ = tx.Rollback()
		if integer(s["chat_id"]) < 0 {
			return a.answerCallback(ctx, cb, "仅搜索者本人可翻页和筛选，发送新关键词可创建自己的搜索。", true)
		}
		_ = a.answerCallback(ctx, cb, "", false)
		return a.botStart(ctx, integer(s["chat_id"]), from)
	}
	if !a.CaptchaTrusted(botID, integer(s["chat_id"]), integer(from["id"])) {
		return a.answerCallback(ctx, cb, "需要重新验证，请发送关键词完成验证。", true)
	}
	if p[3][0] == 'l' {
		return a.answerCallback(ctx, cb, "推荐功能已取消，请重新搜索。", true)
	}
	// Holding this short session row lock serializes edits; stale revisions cannot overwrite newer pages.
	next := catalogMerge(s, Row{})
	action := p[3]
	switch action[0] {
	case 'p':
		next["page"] = catalogLimit(action[1:], 1, 1, 100)
	case 'f':
		next["filters_expanded"] = integer(action[1:])
	case 't':
		t := action[1:]
		if t != "" && t != botSearchCommunities && t != botSearchPosts && ResourceTypes[t] == nil {
			return a.answerCallback(ctx, cb, "分类无效。", true)
		}
		next["type"] = t
		next["page"] = int64(1)
		next["search_token"] = ""
	}
	search := Row{"q": s["query"], "type": next["type"], "page": next["page"], "page_size": s["page_size"], "search_token": next["search_token"]}
	res, e := a.Search(search, "bot-page")
	if e != nil {
		return e
	}
	pages := integer(res["total_pages"])
	if pages < 1 {
		pages = 1
	}
	if integer(next["page"]) > pages {
		next["page"] = pages
		search["search_token"], search["page"] = res["search_token"], pages
		res, e = a.Search(search, "bot-page")
		if e != nil {
			return e
		}
	}
	next["search_token"] = res["search_token"]
	next["revision"] = integer(s["revision"]) + 1
	_ = a.answerCallback(ctx, cb, "", false)
	payload := a.renderSearch(next, res)
	payload["chat_id"] = s["chat_id"]
	payload["message_id"] = s["message_id"]
	_, e = a.Telegram(ctx, "editMessageText", payload)
	var te *TelegramError
	if e != nil && !(errors.As(e, &te) && strings.Contains(te.Description, "message is not modified")) {
		return e
	}
	_, e = tx.Exec("UPDATE ts_bot_sessions SET type=?,page=?,filters_expanded=?,revision=?,search_token=? WHERE id=? AND revision=?", next["type"], next["page"], next["filters_expanded"], next["revision"], next["search_token"], s["id"], s["revision"])
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (a *App) homeRow() []Row {
	return []Row{a.iconButton("menu", Row{"text": "返回主菜单", "callback_data": "m:" + hash(encode(a.botMenu()))[:10] + ":home"})}
}
func (a *App) botStart(ctx context.Context, chat int64, from Row) error {
	if chat <= 0 || chat != integer(from["id"]) || boolean(from["is_bot"]) {
		return catalogErr(403, "请在自己的私聊中打开主菜单。")
	}
	menu := a.botMenu()
	version := hash(encode(menu))[:10]
	rows := [][]Row{}
	for _, raw := range array(menu["rows"]) {
		buttons := []Row{}
		for _, v := range array(raw) {
			b := object(v)
			if !boolean(b["enabled"]) {
				continue
			}
			out := Row{"text": b["text"]}
			if str(b["action"]) == "url" {
				out["url"] = b["value"]
			} else {
				out["callback_data"] = "m:" + version + ":" + str(b["id"])
			}
			if str(b["style"]) != "default" {
				out["style"] = b["style"]
			}
			buttons = append(buttons, a.iconButton(str(b["icon"]), out))
		}
		if len(buttons) > 0 {
			rows = append(rows, buttons)
		}
	}
	values := a.templateValues(from)
	text := a.iconHTML(str(menu["welcome_icon"])) + " " + escape(renderTemplate(str(menu["welcome"]), values, 1800))
	if str(menu["hint"]) != "" {
		text += "\n\n" + escape(renderTemplate(str(menu["hint"]), values, 240))
	}
	return a.botPanel(ctx, chat, Row{"text": text, "parse_mode": "HTML", "link_preview_options": Row{"is_disabled": true}, "reply_markup": Row{"inline_keyboard": rows}})
}
func (a *App) menuCallback(ctx context.Context, cb Row) error {
	from, m := object(cb["from"]), object(cb["message"])
	chat := integer(object(m["chat"])["id"])
	if chat <= 0 || chat != integer(from["id"]) || boolean(from["is_bot"]) {
		return a.answerCallback(ctx, cb, "请在私聊中发送 /start 打开自己的菜单。", true)
	}
	menu := a.botMenu()
	parts := strings.Split(str(cb["data"]), ":")
	if len(parts) != 3 || parts[1] != hash(encode(menu))[:10] {
		return a.answerCallback(ctx, cb, "菜单已更新，请发送 /start。", true)
	}
	if e := a.RateLimit("bot-callback:"+str(from["id"]), 40, 60); e != nil {
		return a.answerCallback(ctx, cb, e.Error(), true)
	}
	if parts[2] == "home" {
		c, e := a.botCredentials()
		if e != nil {
			return e
		}
		if e = a.pauseBotCommerce(ctx, integer(object(c["bot"])["id"]), chat); e != nil {
			return e
		}
		_ = a.answerCallback(ctx, cb, "", false)
		return a.botStart(ctx, chat, from)
	}
	button := Row{}
	for _, row := range array(menu["rows"]) {
		for _, v := range array(row) {
			b := object(v)
			if str(b["id"]) == parts[2] && boolean(b["enabled"]) {
				button = b
			}
		}
	}
	if len(button) == 0 {
		return a.answerCallback(ctx, cb, "此按钮已停用。", true)
	}
	user, e := a.telegramUser(from)
	if e != nil {
		return a.answerCallback(ctx, cb, e.Error(), true)
	}
	_ = a.answerCallback(ctx, cb, "", false)
	action := str(button["action"])
	if action != "mine" && action != "commerce" {
		c, e := a.botCredentials()
		if e != nil {
			return e
		}
		if e = a.pauseBotCommerce(ctx, integer(object(c["bot"])["id"]), chat); e != nil {
			return e
		}
	}
	switch action {
	case "mine", "commerce":
		return a.botCommerce(ctx, chat, user)
	case "discover", "search":
		keyword := ""
		if action == "search" {
			keyword = str(button["value"])
		}
		return a.startSearch(ctx, Row{"chat": m["chat"], "from": from}, keyword, "")
	}
	body := botPrivateHelp
	switch action {
	case "submit":
		body = "发送 /add https://t.me/username\n系统自动获取标题并提交审核。"
	case "message":
		body = str(button["value"])
	case "hot":
		items, e := query(a.DB, "SELECT query,COUNT(*) n FROM ts_search_logs WHERE created_at>=? AND query<>'' GROUP BY query ORDER BY n DESC LIMIT 10", future(-7*24*time.Hour))
		if e != nil {
			return e
		}
		body = "最近 7 天热搜\n"
		for _, r := range items {
			body += "\n" + str(r["query"]) + " · " + str(r["n"]) + " 次"
		}
		if len(items) == 0 {
			body = "最近 7 天还没有搜索记录。"
		}
	case "rank":
		items, e := query(a.DB, "SELECT title,url,members FROM ts_resources WHERE status='published' AND type IN('group','channel') AND members>0 ORDER BY members DESC,id DESC LIMIT 10")
		if e != nil {
			return e
		}
		body = "按已录入成员数排序（非实时）\n"
		for _, r := range items {
			body += "\n" + str(r["title"]) + " · " + str(r["members"]) + "\n" + str(r["url"])
		}
		if len(items) == 0 {
			body = "暂时没有可生成榜单的数据。"
		}
	}
	return a.botPanelText(ctx, chat, clip(body, 3800), Row{"reply_markup": Row{"inline_keyboard": [][]Row{a.homeRow()}}})
}
func (a *App) botWebsiteLogin(ctx context.Context, chat int64, user Row) error {
	if chat <= 0 || chat != integer(user["telegram_id"]) || !boolean(user["status"]) {
		return cErr(403, "请使用已绑定的 Telegram 账号打开网站登录。")
	}
	cfg, e := a.CommerceConfig()
	if e != nil {
		return e
	}
	balance, e := a.WalletBalance(integer(user["id"]))
	if e != nil {
		return e
	}
	tx, e := a.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = activeCommerceIdentity(tx, integer(user["id"]), Row{"expected_telegram_id": chat}); e != nil {
		return e
	}
	code := strings.ToUpper(randomHex(10))
	if _, e = tx.Exec("DELETE FROM ts_bot_login_codes WHERE user_id=? AND used_at IS NULL", user["id"]); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO ts_bot_login_codes(user_id,code_hash,expires_at) VALUES(?,?,?)", user["id"], hash(code), future(5*time.Minute)); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	text := a.iconHTML("user") + " <b>网站登录（可选）</b>\n\n账户 ID：<code>" + str(integer(user["telegram_id"])) + "</code>\n余额：" + balance + " USDT\n\n网站登录码：<code>" + code + "</code>\n有效期 5 分钟，仅能使用一次，请勿转发。\n充值、购买广告和查询账单均可在 /wallet 中完成。"
	base := str(cfg["public_url"]) + "/user/telegram-login"
	rows := [][]Row{{a.iconButton("user", Row{"text": "打开网站登录", "url": base})}, a.homeRow()}
	_, e = a.Telegram(ctx, "sendMessage", Row{"chat_id": chat, "text": text, "parse_mode": "HTML", "reply_markup": Row{"inline_keyboard": rows}})
	return e
}
