package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type TelegramError struct {
	Code        int
	Description string
	RetryAfter  int
}

func (e *TelegramError) Error() string {
	return fmt.Sprintf("Telegram 请求失败（%d）：%s", e.Code, e.Description)
}
func (a *App) botCredentials() (Row, error) {
	r, e := one(a.DB, "SELECT value FROM ts_settings WHERE name='telegram_bot_credentials'")
	if e != nil {
		return nil, e
	}
	if r == nil {
		token := a.Env["telegram.token"]
		return Row{"token": token, "version": hash(token), "source": "environment", "bot": Row{}}, nil
	}
	c := decode(str(r["value"]))
	token, e := unseal(a.Root, ".bot-token.key", str(c["encrypted"]), "telegram_bot_credentials:"+str(c["version"]))
	if e != nil {
		return nil, &DomainError{Status: 503, Message: "机器人凭据无法读取，请恢复 .bot-token.key 或重新保存 Token。"}
	}
	return Row{"token": token, "version": c["version"], "source": "database", "bot": object(c["bot"]), "updated_at": c["updated_at"]}, nil
}
func (a *App) telegramRaw(ctx context.Context, token, method string, params Row) (any, error) {
	if token == "" {
		return nil, &DomainError{Status: 503, Message: "机器人尚未配置。"}
	}
	var body io.Reader
	ct := "application/json"
	if photo, ok := params["photo"].([]byte); ok {
		buf := &bytes.Buffer{}
		writer := multipart.NewWriter(buf)
		for k, v := range params {
			if k == "photo" {
				part, e := writer.CreateFormFile("photo", "verification.png")
				if e != nil {
					return nil, e
				}
				if _, e = part.Write(photo); e != nil {
					return nil, e
				}
			} else {
				value := str(v)
				switch v.(type) {
				case map[string]any, []any, []Row, [][]Row:
					value = encode(v)
				}
				if e := writer.WriteField(k, value); e != nil {
					return nil, e
				}
			}
		}
		if e := writer.Close(); e != nil {
			return nil, e
		}
		body = buf
		ct = writer.FormDataContentType()
	} else {
		b, e := json.Marshal(params)
		if e != nil {
			return nil, e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, "POST", "https://api.telegram.org/bot"+token+"/"+method, body)
	if e != nil {
		return nil, errors.New("Telegram 请求无法创建。")
	}
	req.Header.Set("Content-Type", ct)
	res, e := a.HTTP.Do(req)
	if e != nil {
		return nil, errors.New("暂时无法连接 Telegram，请检查网络。")
	}
	defer res.Body.Close()
	d := json.NewDecoder(io.LimitReader(res.Body, 10<<20))
	d.UseNumber()
	v := Row{}
	if e = d.Decode(&v); e != nil {
		return nil, errors.New("Telegram 响应无效。")
	}
	if !boolean(v["ok"]) {
		return nil, &TelegramError{Code: int(integer(v["error_code"])), Description: clip(str(v["description"]), 250), RetryAfter: int(integer(object(v["parameters"])["retry_after"]))}
	}
	return v["result"], nil
}
func (a *App) Telegram(ctx context.Context, method string, params Row) (Row, error) {
	if a.TelegramCall != nil {
		return a.TelegramCall(ctx, method, params)
	}
	c, e := a.botCredentials()
	if e != nil {
		return nil, e
	}
	v, e := a.telegramRaw(ctx, str(c["token"]), method, params)
	if e != nil {
		return nil, e
	}
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	return Row{"ok": v}, nil
}
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		if n < 1 {
			return ""
		}
		return string(r[:n-1]) + "…"
	}
	return s
}
func (a *App) sendText(ctx context.Context, chat int64, text string, extra Row) error {
	p := Row{"chat_id": chat, "text": text, "link_preview_options": Row{"is_disabled": true}}
	for k, v := range extra {
		p[k] = v
	}
	_, e := a.sendReply(ctx, "sendMessage", p)
	return e
}
func (a *App) sendReply(ctx context.Context, method string, p Row) (Row, error) {
	result, e := a.Telegram(ctx, method, p)
	var te *TelegramError
	if e != nil && p["reply_parameters"] != nil && errors.As(e, &te) && te.Code == 400 && (strings.Contains(strings.ToLower(te.Description), "message to be replied") || strings.Contains(strings.ToLower(te.Description), "reply message not found")) {
		copy := Row{}
		for k, v := range p {
			if k != "reply_parameters" {
				copy[k] = v
			}
		}
		return a.Telegram(ctx, method, copy)
	}
	return result, e
}
func replyContext(m Row) Row {
	out := Row{}
	if integer(m["message_id"]) > 0 {
		out["reply_parameters"] = Row{"message_id": m["message_id"], "allow_sending_without_reply": true}
	}
	if integer(m["message_thread_id"]) > 0 {
		out["message_thread_id"] = m["message_thread_id"]
	}
	return out
}
func (a *App) workerState(name string) Row {
	r, _ := one(a.DB, "SELECT payload FROM ts_go_worker_state WHERE name=?", name)
	return decode(str(r["payload"]))
}
func (a *App) setWorkerState(name string, values Row) {
	old := a.workerState(name)
	for k, v := range values {
		old[k] = v
	}
	s := encode(old)
	_, _ = a.DB.Exec("INSERT INTO ts_go_worker_state(name,payload,updated_at) VALUES(?,?,?) ON DUPLICATE KEY UPDATE payload=?,updated_at=?", name, s, now(), s, now())
}
func (a *App) botState() Row {
	c, e := a.botCredentials()
	if e != nil {
		return Row{"configured": true, "running": false, "state": "error", "last_error": e.Error(), "token": Row{"configured": true, "source": "database"}}
	}
	state := a.workerState("bot")
	bot := object(c["bot"])
	if integer(state["id"]) != integer(bot["id"]) || str(state["credential_version"]) != str(c["version"]) {
		state = Row{}
	}
	for k, v := range bot {
		state[k] = v
	}
	state["configured"] = str(c["token"]) != ""
	state["running"] = str(state["state"]) == "running" && integer(state["heartbeat"]) > time.Now().Unix()-70
	state["token"] = Row{"configured": state["configured"], "source": c["source"], "updated_at": c["updated_at"]}
	state["mode"] = "long_polling"
	state["page_size"] = a.botPageSize()
	commandScopes := Row{}
	commands := []string{}
	for _, menu := range botCommandMenus() {
		commandScopes[menu.Scope] = menu.Commands
		if menu.Scope == "all_private_chats" {
			for _, command := range menu.Commands {
				commands = append(commands, "/"+command.Command)
			}
		}
	}
	state["commands"], state["command_scopes"] = commands, commandScopes
	return state
}
func waitContext(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
func (a *App) RunBot(ctx context.Context) error {
	if e := os.MkdirAll(filepath.Join(a.Root, "runtime"), 0700); e != nil {
		return e
	}
	f, e := os.OpenFile(filepath.Join(a.Root, "runtime/bot.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return errors.New("已有 Telegram worker 运行。")
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	groupCtx, stopGroups := context.WithCancel(ctx)
	groupDone := make(chan struct{})
	go func() {
		defer close(groupDone)
		a.RunGroupManagement(groupCtx)
	}()
	defer func() {
		stopGroups()
		<-groupDone
	}()
	version, blocked := "", ""
	commandsSynced := false
	var commandsRetryAt time.Time
	var offset int64
	var bot Row
	defer a.setWorkerState("bot", Row{"state": "stopped", "heartbeat": time.Now().Unix()})
	for ctx.Err() == nil {
		cred, e := a.botCredentials()
		if e != nil {
			a.setWorkerState("bot", Row{"state": "error", "last_error": e.Error()})
			if !waitContext(ctx, 5*time.Second) {
				break
			}
			continue
		}
		if str(cred["version"]) == blocked {
			if !waitContext(ctx, time.Second) {
				break
			}
			continue
		}
		if version != str(cred["version"]) {
			token := str(cred["token"])
			if !regexp.MustCompile(`^\d+:[A-Za-z0-9_-]+$`).MatchString(token) {
				a.setWorkerState("bot", Row{"state": "waiting", "last_error": "请在后台保存 Bot Token。"})
				blocked = str(cred["version"])
				continue
			}
			bot, e = a.Telegram(ctx, "getMe", Row{})
			if e == nil {
				var hook Row
				hook, e = a.Telegram(ctx, "getWebhookInfo", Row{})
				if e == nil && str(hook["url"]) != "" {
					_, e = a.Telegram(ctx, "deleteWebhook", Row{"drop_pending_updates": false})
				}
			}
			if e != nil {
				a.setWorkerState("bot", Row{"state": "error", "last_error": "无法连接机器人，请检查 Token 与网络。"})
				if !waitContext(ctx, 5*time.Second) {
					break
				}
				continue
			}
			state := a.workerState("bot")
			offset = 0
			if integer(state["id"]) == integer(bot["id"]) {
				offset = integer(state["offset"])
			} else if b, err := os.ReadFile(filepath.Join(a.Root, "runtime/bot.json")); err == nil {
				old := decode(string(b))
				if integer(old["id"]) == integer(bot["id"]) {
					offset = integer(old["offset"])
				}
			}
			version = str(cred["version"])
			commandsSynced, commandsRetryAt = false, time.Time{}
			a.setWorkerState("bot", Row{"id": bot["id"], "username": bot["username"], "name": bot["first_name"], "credential_version": version, "checked_at": now(), "can_join_groups": bot["can_join_groups"], "can_read_all_group_messages": bot["can_read_all_group_messages"], "commands_state": "pending", "commands_error": nil, "commands_synced_at": nil, "commands_retry_at": nil})
		}
		if !commandsSynced && !time.Now().Before(commandsRetryAt) {
			syncCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			err := a.syncBotCommands(syncCtx)
			cancel()
			if err != nil {
				delay := 30 * time.Second
				var te *TelegramError
				if errors.As(err, &te) && time.Duration(te.RetryAfter)*time.Second > delay {
					delay = time.Duration(te.RetryAfter) * time.Second
				}
				commandsRetryAt = time.Now().Add(delay)
				a.setWorkerState("bot", Row{"commands_state": "retrying", "commands_error": err.Error(), "commands_retry_at": commandsRetryAt.In(location).Format("2006-01-02 15:04:05")})
				log.Print("Bot command menus could not be synchronized; retry scheduled")
			} else {
				commandsSynced = true
				a.setWorkerState("bot", Row{"commands_state": "synced", "commands_error": nil, "commands_synced_at": now(), "commands_retry_at": nil})
			}
		}
		a.cleanupBotMessages(ctx, integer(bot["id"]), 0, 8)
		a.setWorkerState("bot", Row{"state": "running", "heartbeat": time.Now().Unix(), "offset": offset, "last_error": nil, "pid": os.Getpid()})
		result, e := a.telegramRaw(ctx, str(cred["token"]), "getUpdates", Row{"offset": offset, "timeout": 20, "allowed_updates": []string{"message", "edited_message", "callback_query", "my_chat_member", "chat_member"}})
		if ctx.Err() != nil {
			break
		}
		if e != nil {
			var te *TelegramError
			delay := 3 * time.Second
			if errors.As(e, &te) {
				if te.Code == 401 || te.Code == 404 || te.Code == 409 {
					blocked = version
				}
				if te.RetryAfter > 0 {
					delay = time.Duration(te.RetryAfter) * time.Second
				}
			}
			a.setWorkerState("bot", Row{"state": "error", "last_error": "轮询失败，正在等待重试或新的机器人配置。"})
			if !waitContext(ctx, delay) {
				break
			}
			continue
		}
		current, err := a.botCredentials()
		if err != nil || str(current["version"]) != version {
			continue
		}
		for _, value := range array(result) {
			u := object(value)
			updateID := integer(u["update_id"])
			if err = a.processBotUpdate(ctx, u, bot); err != nil {
				log.Printf("Bot update %d failed (%T); retrying", updateID, err)
				waitContext(ctx, 2*time.Second)
				break
			}
			offset = updateID + 1
			a.setWorkerState("bot", Row{"offset": offset, "last_update_at": now(), "heartbeat": time.Now().Unix()})
			a.cleanupBotMessages(ctx, integer(bot["id"]), 0, 8)
		}
	}
	return nil
}
func (a *App) processBotUpdate(ctx context.Context, u, bot Row) error {
	updateID := integer(u["update_id"])
	done, err := one(a.DB, "SELECT state FROM ts_bot_updates WHERE bot_id=? AND update_id=?", bot["id"], updateID)
	if err != nil {
		return err
	}
	// Legacy PHP records ignored updates as terminal; replaying them can resend messages.
	if state := str(done["state"]); state == "done" || state == "ignored" {
		return nil
	}
	_, err = a.DB.Exec("INSERT INTO ts_bot_updates(bot_id,update_id,state,created_at) VALUES(?,?,'processing',?) ON DUPLICATE KEY UPDATE state='processing'", bot["id"], updateID, now())
	if err != nil {
		return err
	}
	if err = a.handleUpdate(ctx, u, bot); err != nil {
		var te *TelegramError
		if !errors.As(err, &te) || (te.Code != 400 && te.Code != 403) {
			return err
		}
	}
	_, err = a.DB.Exec("UPDATE ts_bot_updates SET state='done',completed_at=? WHERE bot_id=? AND update_id=?", now(), bot["id"], updateID)
	return err
}
func iconCatalog(root string) Row {
	b, e := os.ReadFile(filepath.Join(root, "resources/icons/telegram-ios/catalog.json"))
	if e != nil {
		return Row{}
	}
	return decode(string(b))
}
func (a *App) iconHTML(name string) string {
	v := object(iconCatalog(a.Root)[name])
	if str(v["id"]) == "" {
		return ""
	}
	return `<tg-emoji emoji-id="` + str(v["id"]) + `">` + str(v["emoji"]) + `</tg-emoji>`
}
func (a *App) iconButton(name string, b Row) Row {
	v := object(iconCatalog(a.Root)[name])
	if str(v["id"]) != "" {
		b["icon_custom_emoji_id"] = str(v["id"])
	}
	return b
}
func (a *App) setting(name, def string) string {
	r, e := one(a.DB, "SELECT value FROM ts_settings WHERE name=?", name)
	if e != nil || r == nil {
		return def
	}
	return str(r["value"])
}
func (a *App) botPageSize() int64 {
	v, _ := strconv.ParseInt(a.setting("bot_page_size", "12"), 10, 64)
	if v < 1 || v > 30 {
		return 12
	}
	return v
}
