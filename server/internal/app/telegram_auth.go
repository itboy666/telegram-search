package app

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

func (a *App) RegisterTelegramLogin() {
	a.Handle("POST /user/api/telegram/challenges", "", "", true, a.createChallenge)
	a.Handle("GET /user/api/telegram/challenges/{id}", "", "", false, a.pollChallenge)
	a.Handle("POST /user/api/telegram/challenges/{id}/consume", "", "", true, a.consumeChallenge)
	a.Handle("GET /user/api/telegram", "user", "", false, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		p, _ := one(a.DB, "SELECT username,display_name FROM ts_telegram_profiles WHERE user_id=? AND telegram_id=?", id.ID, id.User["telegram_id"])
		JSON(w, Row{"bound": integer(id.User["telegram_id"]) > 0, "telegram_id": id.User["telegram_id"], "username": p["username"], "display_name": id.User["display_name"], "can_unbind": str(id.User["password_hash"]) != "", "has_password": str(id.User["password_hash"]) != ""})
	})
	a.Handle("POST /user/api/telegram/unbind", "user", "", true, a.unbindTelegram)
}
func (a *App) PublicTelegram() Row {
	c, e := a.botCredentials()
	if e != nil {
		return Row{"available": false, "username": ""}
	}
	bot := object(c["bot"])
	return Row{"available": str(c["token"]) != "" && str(bot["username"]) != "", "username": str(bot["username"])}
}
func (a *App) createChallenge(w http.ResponseWriter, r *http.Request, _ *Identity) {
	if e := a.RateLimit("tg-challenge:"+clientIP(r), 15, 300); e != nil {
		Error(w, e)
		return
	}
	b, e := Body(r)
	if e != nil {
		Error(w, e)
		return
	}
	purpose := str(b["purpose"])
	if purpose != "login" && purpose != "bind" {
		Fail(w, 422, "登录用途无效。")
		return
	}
	var uid, sid any
	if purpose == "bind" {
		id, e := a.Identify(r, "user", true)
		if e != nil {
			Error(w, e)
			return
		}
		if integer(id.User["telegram_id"]) > 0 {
			Fail(w, 409, "账号已经绑定 Telegram，请先解绑。")
			return
		}
		uid, sid = id.ID, id.Session["id"]
	}
	c, e := a.botCredentials()
	if e != nil {
		Error(w, e)
		return
	}
	bot := object(c["bot"])
	if str(bot["username"]) == "" || str(c["token"]) == "" {
		Fail(w, 503, "机器人尚未配置，请使用账号密码登录。")
		return
	}
	id, browser, botToken := randomHex(16), randomHex(32), randomHex(16)
	expiry := future(5 * time.Minute)
	_, e = a.DB.Exec("INSERT INTO ts_telegram_auth_challenges(id,browser_hash,bot_hash,purpose,user_id,session_id,bot_id,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?)", id, hash(browser), hash(botToken), purpose, uid, sid, bot["id"], now(), expiry)
	if e != nil {
		Error(w, e)
		return
	}
	JSON(w, Row{"id": id, "token": browser, "url": "https://t.me/" + str(bot["username"]) + "?start=auth_" + botToken, "expires_at": expiry})
}
func challengeBrowser(row Row, token string) bool {
	return row != nil && len(token) == 64 && subtle.ConstantTimeCompare([]byte(str(row["browser_hash"])), []byte(hash(token))) == 1
}
func (a *App) pollChallenge(w http.ResponseWriter, r *http.Request, _ *Identity) {
	if e := a.RateLimit("tg-poll:"+clientIP(r), 150, 60); e != nil {
		Error(w, e)
		return
	}
	row, e := one(a.DB, "SELECT * FROM ts_telegram_auth_challenges WHERE id=?", r.PathValue("id"))
	if e != nil {
		Error(w, e)
		return
	}
	if !challengeBrowser(row, r.Header.Get("X-Telegram-Challenge-Token")) {
		Fail(w, 404, "登录请求不存在。")
		return
	}
	status := str(row["status"])
	if expired(row["expires_at"]) || row["consumed_at"] != nil {
		status = "expired"
	}
	JSON(w, Row{"status": status, "expires_at": row["expires_at"]})
}
func (a *App) consumeChallenge(w http.ResponseWriter, r *http.Request, _ *Identity) {
	if e := a.RateLimit("tg-consume:"+clientIP(r), 15, 300); e != nil {
		Error(w, e)
		return
	}
	b, e := Body(r)
	if e != nil {
		Error(w, e)
		return
	}
	token, csrf := randomHex(32), randomHex(24)
	result, e := cTx(a, func(tx *sql.Tx) (Row, error) {
		ch, e := one(tx, "SELECT * FROM ts_telegram_auth_challenges WHERE id=? FOR UPDATE", r.PathValue("id"))
		if e != nil {
			return nil, e
		}
		if !challengeBrowser(ch, str(b["token"])) || expired(ch["expires_at"]) || ch["consumed_at"] != nil || str(ch["status"]) != "confirmed" {
			return nil, catalogErr(401, "登录请求未确认或已过期，请重新发起。")
		}
		cred, e := a.botCredentials()
		if e != nil || integer(object(cred["bot"])["id"]) != integer(ch["bot_id"]) {
			return nil, catalogErr(401, "机器人配置已变化，请重新发起登录。")
		}
		profile := decode(str(ch["telegram_profile"]))
		tgid := integer(ch["telegram_id"])
		if tgid < 1 || integer(profile["id"]) != tgid || boolean(profile["is_bot"]) {
			return nil, catalogErr(401, "Telegram 身份验证无效，请重新发起。")
		}
		var u Row
		switch str(ch["purpose"]) {
		case "bind":
			id, e := a.Identify(r, "user", true)
			if e != nil {
				return nil, e
			}
			if id.ID != integer(ch["user_id"]) || integer(id.Session["id"]) != integer(ch["session_id"]) {
				return nil, catalogErr(403, "请使用发起绑定的原账号和浏览器。")
			}
			u, e = one(tx, "SELECT * FROM ts_users WHERE id=? FOR UPDATE", id.ID)
			if e != nil {
				return nil, e
			}
			if u == nil || !boolean(u["status"]) {
				return nil, catalogErr(403, "当前账号已停用。")
			}
			// Session revocation and logout may have committed while waiting for the user lock.
			if e = recheckUserSession(tx, id, r); e != nil {
				return nil, e
			}
			if integer(u["telegram_id"]) != 0 {
				return nil, catalogErr(409, "账号绑定状态已变化。")
			}
			existing, e := one(tx, "SELECT id FROM ts_users WHERE telegram_id=? FOR UPDATE", tgid)
			if e != nil {
				return nil, e
			}
			if existing != nil {
				return nil, catalogErr(409, "此 Telegram 已有独立账号。请使用 Telegram 登录该账号；为保护余额和历史记录，系统不会自动合并账号。")
			}
			res, e := tx.Exec("UPDATE ts_users SET telegram_id=? WHERE id=? AND telegram_id IS NULL", tgid, id.ID)
			if e != nil {
				var duplicate *mysql.MySQLError
				if errors.As(e, &duplicate) && duplicate.Number == 1062 {
					return nil, catalogErr(409, "此 Telegram 已绑定其他账号，请刷新后重试。")
				}
				return nil, e
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				return nil, catalogErr(409, "账号绑定状态已变化。")
			}
			u["telegram_id"] = tgid
			if e = cAudit(tx, 0, "用户绑定 Telegram", "用户 ID "+str(u["id"])); e != nil {
				return nil, e
			}
		case "login":
			u, e = telegramUserTx(tx, profile)
			if e != nil {
				return nil, e
			}
			if e = insertSession(tx, "user", integer(u["id"]), token, csrf); e != nil {
				return nil, e
			}
			if _, e = tx.Exec("UPDATE ts_users SET last_login_at=? WHERE id=?", now(), u["id"]); e != nil {
				return nil, e
			}
		default:
			return nil, catalogErr(401, "登录用途无效，请重新发起。")
		}
		if e = saveTelegramProfile(tx, integer(u["id"]), profile); e != nil {
			return nil, e
		}
		if _, e = tx.Exec("UPDATE ts_telegram_auth_challenges SET consumed_at=?,status='consumed' WHERE id=?", now(), ch["id"]); e != nil {
			return nil, e
		}
		return Row{"user": u, "purpose": ch["purpose"]}, nil
	})
	if e != nil {
		Error(w, e)
		return
	}
	u := object(result["user"])
	if str(result["purpose"]) == "login" {
		a.loginResult(w, r, "user", u, token, csrf)
	} else {
		JSON(w, Row{"user": publicUser(u), "bound": true})
	}
}
func telegramUserTx(tx *sql.Tx, from Row) (Row, error) {
	id := integer(from["id"])
	if id <= 0 || boolean(from["is_bot"]) {
		return nil, &DomainError{Status: 403, Message: "Telegram 用户身份无效。"}
	}
	u, e := one(tx, "SELECT * FROM ts_users WHERE telegram_id=? FOR UPDATE", id)
	if e != nil {
		return nil, e
	}
	if u == nil {
		name := clip(strings.TrimSpace(str(from["first_name"])+" "+str(from["last_name"])), 100)
		if name == "" {
			name = "Telegram 用户"
		}
		base, username := "tg_"+str(id), "tg_"+str(id)
		for attempt := 0; attempt < 5; attempt++ {
			existing, err := one(tx, "SELECT id FROM ts_users WHERE username=? FOR UPDATE", username)
			if err != nil {
				return nil, err
			}
			if existing == nil {
				_, e = tx.Exec("INSERT INTO ts_users(username,display_name,telegram_id,created_at) VALUES(?,?,?,?)", username, name, id, now())
				if e == nil {
					u, e = one(tx, "SELECT * FROM ts_users WHERE telegram_id=? FOR UPDATE", id)
					if e != nil {
						return nil, e
					}
					break
				}
				var duplicate *mysql.MySQLError
				if !errors.As(e, &duplicate) || duplicate.Number != 1062 {
					return nil, e
				}
				// READ COMMITTED sees the winner of a simultaneous first Telegram login.
				u, e = one(tx, "SELECT * FROM ts_users WHERE telegram_id=? FOR UPDATE", id)
				if e != nil {
					return nil, e
				}
				if u != nil {
					break
				}
			}
			if attempt == 4 {
				return nil, &DomainError{Status: 503, Message: "暂时无法创建账号，请稍后重试。"}
			}
			// An unbound account keeps its identity, balance and history. Never relink it implicitly.
			username = base + "_" + randomHex(4)
		}
	}
	if !boolean(u["status"]) {
		return nil, &DomainError{Status: 403, Message: "当前账号已停用，请联系管理员。"}
	}
	return u, nil
}
func saveTelegramProfile(tx *sql.Tx, id int64, p Row) error {
	_, e := tx.Exec("INSERT INTO ts_telegram_profiles(user_id,telegram_id,username,display_name,updated_at) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE username=?,display_name=?,updated_at=?", id, p["id"], clip(str(p["username"]), 80), clip(str(p["first_name"])+" "+str(p["last_name"]), 100), now(), clip(str(p["username"]), 80), clip(str(p["first_name"])+" "+str(p["last_name"]), 100), now())
	return e
}
func (a *App) telegramUser(from Row) (Row, error) {
	return cTx(a, func(tx *sql.Tx) (Row, error) {
		u, e := telegramUserTx(tx, from)
		if e != nil {
			return nil, e
		}
		if e = saveTelegramProfile(tx, integer(u["id"]), from); e != nil {
			return nil, e
		}
		return u, nil
	})
}
func (a *App) unbindTelegram(w http.ResponseWriter, r *http.Request, id *Identity) {
	if e := a.RateLimit("unbind:"+str(id.ID), 5, 300); e != nil {
		Error(w, e)
		return
	}
	b, e := Body(r)
	if e != nil {
		Error(w, e)
		return
	}
	if str(id.User["password_hash"]) == "" || !passwordVerify(str(id.User["password_hash"]), str(b["password"])) {
		Fail(w, 422, "请输入正确的网站登录密码；尚无密码时请先设置。")
		return
	}
	_, e = cTx(a, func(tx *sql.Tx) (Row, error) {
		u, e := one(tx, "SELECT * FROM ts_users WHERE id=? FOR UPDATE", id.ID)
		if e != nil {
			return nil, e
		}
		if u == nil || !boolean(u["status"]) || str(u["password_hash"]) != str(id.User["password_hash"]) {
			return nil, catalogErr(409, "账号状态已变化。")
		}
		if e = recheckUserSession(tx, id, r); e != nil {
			return nil, e
		}
		for _, stmt := range []string{"UPDATE ts_users SET telegram_id=NULL WHERE id=?", "DELETE FROM ts_telegram_profiles WHERE user_id=?", "DELETE FROM ts_bot_login_codes WHERE user_id=?", "DELETE FROM ts_telegram_auth_challenges WHERE user_id=?"} {
			if _, e = tx.Exec(stmt, id.ID); e != nil {
				return nil, e
			}
		}
		if _, e = tx.Exec("DELETE FROM ts_sessions WHERE user_id=? AND id<>?", id.ID, id.Session["id"]); e != nil {
			return nil, e
		}
		return Row{}, cAudit(tx, 0, "用户解绑 Telegram", "用户 ID "+str(id.ID))
	})
	if e != nil {
		Error(w, e)
		return
	}
	JSON(w, Row{"unbound": true})
}
func (a *App) promptTelegramAuth(ctx context.Context, chat int64, token string, from Row) error {
	ch, e := one(a.DB, "SELECT * FROM ts_telegram_auth_challenges WHERE bot_hash=?", hash(token))
	if e != nil {
		return e
	}
	if ch == nil || expired(ch["expires_at"]) || str(ch["status"]) != "pending" {
		return a.sendText(ctx, chat, "这次登录请求已过期，请回到网站重新发起。", nil)
	}
	c, e := a.botCredentials()
	if e != nil {
		return e
	}
	if integer(object(c["bot"])["id"]) != integer(ch["bot_id"]) {
		return errors.New("机器人配置已变更")
	}
	uid := integer(from["id"])
	res, e := a.DB.Exec("UPDATE ts_telegram_auth_challenges SET telegram_id=?,telegram_profile=? WHERE id=? AND status='pending' AND (telegram_id IS NULL OR telegram_id=?)", uid, encode(from), ch["id"], uid)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 && integer(ch["telegram_id"]) != uid {
		return a.sendText(ctx, chat, "该请求已由另一个 Telegram 账号打开，请重新发起。", nil)
	}
	text := "确认使用当前 Telegram 账号登录星索网站？\n仅确认你本人刚刚在浏览器发起的请求。"
	if str(ch["purpose"]) == "bind" {
		u, _ := one(a.DB, "SELECT username FROM ts_users WHERE id=?", ch["user_id"])
		text = "确认将当前 Telegram 绑定到网站账号「" + str(u["username"]) + "」？\n绑定后，此 Telegram 可以登录该网站账号。"
	}
	_, e = a.Telegram(ctx, "sendMessage", Row{"chat_id": chat, "text": text, "reply_markup": Row{"inline_keyboard": [][]Row{{{"text": "确认授权", "callback_data": "auth:" + str(ch["id"]) + ":yes", "style": "primary"}, {"text": "取消", "callback_data": "auth:" + str(ch["id"]) + ":no"}}}}})
	return e
}
func (a *App) confirmTelegramAuth(ctx context.Context, cb Row) error {
	parts := strings.Split(str(cb["data"]), ":")
	if len(parts) != 3 {
		return nil
	}
	from := object(cb["from"])
	m := object(cb["message"])
	chat := integer(object(m["chat"])["id"])
	answer := "请求已过期，请重新发起。"
	if chat == integer(from["id"]) && chat > 0 {
		state := "confirmed"
		if parts[2] != "yes" {
			state = "cancelled"
		}
		c, e := a.botCredentials()
		if e != nil {
			return e
		}
		res, e := a.DB.Exec("UPDATE ts_telegram_auth_challenges SET status=?,confirmed_at=? WHERE id=? AND telegram_id=? AND bot_id=? AND status='pending' AND expires_at>?", state, now(), parts[1], from["id"], object(c["bot"])["id"], now())
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			if state == "confirmed" {
				answer = "已确认，请回到原浏览器继续。"
			} else {
				answer = "已取消授权。"
			}
		}
	}
	_, e := a.Telegram(ctx, "answerCallbackQuery", Row{"callback_query_id": cb["id"], "text": answer, "show_alert": true})
	return e
}
