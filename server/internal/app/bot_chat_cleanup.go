package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

type botChatTurnKey struct{}
type botChatTurn struct {
	botID, chatID, editID int64
	input                 Row
	cleanInput            bool
}

func botPrivateTurn(ctx context.Context, botID int64, m, from Row, callback bool) context.Context {
	chat := object(m["chat"])
	id := integer(chat["id"])
	if botID <= 0 || id <= 0 || id != integer(from["id"]) || boolean(from["is_bot"]) || str(chat["type"]) != "private" {
		return ctx
	}
	turn := botChatTurn{botID: botID, chatID: id}
	if callback {
		turn.editID = integer(m["message_id"])
	} else {
		turn.input = m
	}
	return context.WithValue(ctx, botChatTurnKey{}, turn)
}

func botCleanInput(ctx context.Context) context.Context {
	if turn, ok := ctx.Value(botChatTurnKey{}).(botChatTurn); ok {
		turn.cleanInput = true
		return context.WithValue(ctx, botChatTurnKey{}, turn)
	}
	return ctx
}

func botDisposableCommand(command, arg string) bool {
	if command == "start" {
		return arg == ""
	}
	if command == "add" || command == "search" {
		return arg == ""
	}
	return slices.Contains([]string{"help", "account", "wallet", "recharge", "ads", "cancel"}, command)
}

func botCredentialID(c Row) int64 {
	if id := integer(object(c["bot"])["id"]); id > 0 {
		return id
	}
	// Environment-only credentials do not yet have a stored getMe profile.
	parts := strings.SplitN(str(c["token"]), ":", 2)
	if len(parts) == 2 {
		return integer(parts[0])
	}
	return 0
}

// Cleanup must use the bot that created the tracked message, including after a
// token switch. Never send an old bot's deletion queue using new credentials.
func (a *App) telegramForBot(ctx context.Context, botID int64, method string, params Row) (Row, error) {
	if a.TelegramCall != nil {
		return a.TelegramCall(ctx, method, params)
	}
	c, err := a.botCredentials()
	if err != nil {
		return nil, err
	}
	if botID <= 0 || botCredentialID(c) != botID {
		return nil, catalogErr(409, "机器人配置已变更，请重新打开菜单。")
	}
	result, err := a.telegramRaw(ctx, str(c["token"]), method, params)
	if err != nil {
		return nil, err
	}
	if row, ok := result.(map[string]any); ok {
		return row, nil
	}
	return Row{"ok": result}, nil
}

func (a *App) botPanel(ctx context.Context, chat int64, params Row) error {
	turn, ok := ctx.Value(botChatTurnKey{}).(botChatTurn)
	if !ok {
		c, err := a.botCredentials()
		if err != nil {
			return err
		}
		turn = botChatTurn{botID: botCredentialID(c), chatID: chat}
	}
	if chat <= 0 || turn.chatID != chat {
		return catalogErr(403, "请在自己的私聊中打开菜单。")
	}
	_, err := a.deliverBotView(ctx, turn.botID, chat, "panel", turn.editID, params)
	return err
}

func (a *App) botPanelText(ctx context.Context, chat int64, text string, extra Row) error {
	params := Row{"text": text, "link_preview_options": Row{"is_disabled": true}}
	for key, value := range extra {
		params[key] = value
	}
	// The input command will be removed after successful delivery. Temporary
	// screens should not retain a quote pointing at that deleted command.
	delete(params, "reply_parameters")
	return a.botPanel(ctx, chat, params)
}

func (a *App) botRecordText(ctx context.Context, botID, chat int64, text string, extra Row) error {
	params := catalogMerge(extra, Row{"chat_id": chat, "text": text, "link_preview_options": Row{"is_disabled": true}})
	sent, err := a.sendReply(ctx, "sendMessage", params)
	if err == nil {
		a.retireBotPanel(ctx, botID, chat, integer(sent["message_id"]))
	}
	return err
}

func (a *App) withBotViewLock(ctx context.Context, botID, chat int64, fn func() error) error {
	conn, err := a.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	lock := fmt.Sprintf("bot-view:%d:%d", botID, chat)
	var acquired int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", lock).Scan(&acquired); err != nil {
		return err
	}
	if acquired != 1 {
		return cErr(409, "上一页正在更新，请稍后重试。")
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(release, "SELECT RELEASE_LOCK(?)", lock)
	}()
	return fn()
}

// A panel is disposable navigation. Other keys identify durable order receipts.
// A receipt can be refreshed, but is never overwritten with a different view.
// Only recorded bot messages and explicitly consumed private inputs are queued.
func (a *App) deliverBotView(ctx context.Context, botID, chat int64, key string, editID int64, params Row) (Row, error) {
	if botID <= 0 || chat <= 0 || key == "" {
		return nil, cErr(403, "请在自己的私聊中操作。")
	}
	var sent Row
	err := a.withBotViewLock(ctx, botID, chat, func() error {
		panel, err := one(a.DB, "SELECT * FROM ts_bot_chat_views WHERE bot_id=? AND chat_id=? AND view_key='panel'", botID, chat)
		if err != nil {
			return err
		}
		target := Row{}
		if key != "panel" {
			target, err = one(a.DB, "SELECT * FROM ts_bot_chat_views WHERE bot_id=? AND chat_id=? AND view_key=?", botID, chat, key)
			if err != nil {
				return err
			}
		}
		if target == nil || integer(target["message_id"]) == 0 {
			if editID > 0 && editID == integer(panel["message_id"]) {
				target = panel
			}
		}
		p := catalogMerge(params, Row{"chat_id": chat})
		delete(p, "message_id")
		if id := integer(target["message_id"]); id > 0 {
			p["message_id"] = id
			sent, err = a.telegramForBot(ctx, botID, "editMessageText", p)
			var te *TelegramError
			if errors.As(err, &te) && te.Code == 400 {
				description := strings.ToLower(te.Description)
				if strings.Contains(description, "message is not modified") {
					sent, err = Row{"message_id": id}, nil
				} else if strings.Contains(description, "message to edit not found") || strings.Contains(description, "message can't be edited") {
					delete(p, "message_id")
					sent, err = a.telegramForBot(ctx, botID, "sendMessage", p)
				}
			}
		} else {
			sent, err = a.telegramForBot(ctx, botID, "sendMessage", p)
		}
		if err != nil {
			return err // Keep the old screen and the user's input on delivery failure.
		}
		id := integer(sent["message_id"])
		if id <= 0 {
			return errors.New("Telegram 未返回新的消息标识。")
		}
		tx, err := a.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		sentAt := now()
		if id == integer(target["message_id"]) {
			sentAt = str(target["sent_at"])
		}
		if _, err = tx.Exec("INSERT INTO ts_bot_chat_views(bot_id,chat_id,view_key,message_id,sent_at,updated_at) VALUES(?,?,?,?,?,?) ON DUPLICATE KEY UPDATE message_id=VALUES(message_id),sent_at=VALUES(sent_at),updated_at=VALUES(updated_at)", botID, chat, key, id, sentAt, now()); err != nil {
			return err
		}
		if oldID := integer(panel["message_id"]); oldID > 0 && oldID != id {
			if err = queueBotCleanup(tx, botID, chat, oldID, str(panel["sent_at"])); err != nil {
				return err
			}
		}
		if key != "panel" {
			if _, err = tx.Exec("DELETE FROM ts_bot_chat_views WHERE bot_id=? AND chat_id=? AND view_key='panel'", botID, chat); err != nil {
				return err
			}
		}
		if turn, ok := ctx.Value(botChatTurnKey{}).(botChatTurn); ok && turn.cleanInput && turn.botID == botID && turn.chatID == chat {
			if inputID := integer(turn.input["message_id"]); inputID > 0 && inputID != id {
				inputAt := now()
				if date := integer(turn.input["date"]); date > 0 {
					inputAt = time.Unix(date, 0).In(location).Format("2006-01-02 15:04:05")
				}
				if err = queueBotCleanup(tx, botID, chat, inputID, inputAt); err != nil {
					return err
				}
			}
		}
		return tx.Commit()
	})
	return sent, err
}

func queueBotCleanup(tx *sql.Tx, botID, chat, messageID int64, sentAt string) error {
	date, err := time.ParseInLocation("2006-01-02 15:04:05", sentAt, location)
	// Telegram refuses deletions after 48h. Leave a margin and keep old history.
	if err != nil || botID <= 0 || chat <= 0 || messageID <= 0 || time.Since(date) >= 47*time.Hour {
		return nil
	}
	_, err = tx.Exec("INSERT IGNORE INTO ts_bot_message_cleanup(bot_id,chat_id,message_id,next_attempt_at,expires_at) VALUES(?,?,?,?,?)", botID, chat, messageID, now(), date.Add(47*time.Hour).Format("2006-01-02 15:04:05"))
	return err
}

// Search results, submission receipts and authentication messages are retained.
// After a new result exists, only the preceding temporary navigation is retired.
func (a *App) retireBotPanel(ctx context.Context, botID, chat, newerMessageID int64) {
	if botID <= 0 || chat <= 0 || newerMessageID <= 0 {
		return
	}
	_ = a.withBotViewLock(ctx, botID, chat, func() error {
		tx, err := a.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		panel, err := one(tx, "SELECT * FROM ts_bot_chat_views WHERE bot_id=? AND chat_id=? AND view_key='panel' FOR UPDATE", botID, chat)
		if err != nil || panel == nil || integer(panel["message_id"]) >= newerMessageID {
			return err
		}
		if err = queueBotCleanup(tx, botID, chat, integer(panel["message_id"]), str(panel["sent_at"])); err != nil {
			return err
		}
		if _, err = tx.Exec("DELETE FROM ts_bot_chat_views WHERE bot_id=? AND chat_id=? AND view_key='panel'", botID, chat); err != nil {
			return err
		}
		return tx.Commit()
	})
}

// Cleanup never returns a business error: a failed deletion must not replay a
// payment or prevent the next bot update. Persistent retries survive restarts.
func (a *App) cleanupBotMessages(ctx context.Context, botID, chat int64, limit int) {
	if botID <= 0 || a.DB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, _ = a.DB.ExecContext(ctx, "DELETE FROM ts_bot_message_cleanup WHERE bot_id=? AND expires_at<=? LIMIT 1000", botID, now())
	// These are tracking rows only; pruning never deletes an actual receipt.
	_, _ = a.DB.ExecContext(ctx, "DELETE FROM ts_bot_chat_views WHERE bot_id=? AND updated_at<? LIMIT 1000", botID, future(-72*time.Hour))
	where, args := "bot_id=? AND chat_id>0 AND next_attempt_at<=? AND expires_at>?", []any{botID, now(), now()}
	if chat > 0 {
		where += " AND chat_id=?"
		args = append(args, chat)
	}
	args = append(args, limit)
	items, err := query(a.DB, "SELECT * FROM ts_bot_message_cleanup WHERE "+where+" ORDER BY next_attempt_at LIMIT ?", args...)
	if err != nil {
		return
	}
	for _, item := range items {
		if ctx.Err() != nil {
			break
		}
		chatID, messageID := integer(item["chat_id"]), integer(item["message_id"])
		// Do not delete any view still registered as current or as an order.
		active, err := one(a.DB, "SELECT message_id FROM ts_bot_chat_views WHERE bot_id=? AND chat_id=? AND message_id=? LIMIT 1", botID, chatID, messageID)
		if err != nil {
			break
		}
		if active == nil {
			result, e := a.telegramForBot(ctx, botID, "deleteMessage", Row{"chat_id": chatID, "message_id": messageID})
			err = e
			if err == nil && !boolean(result["ok"]) {
				err = errors.New("Telegram deletion unconfirmed")
			}
		}
		var te *TelegramError
		permanent := errors.As(err, &te) && (te.Code == 400 || te.Code == 403 || te.Code == 401 || te.Code == 404)
		if err == nil || permanent || integer(item["attempts"]) >= 5 {
			_, _ = a.DB.Exec("DELETE FROM ts_bot_message_cleanup WHERE bot_id=? AND chat_id=? AND message_id=?", botID, chatID, messageID)
			continue
		}
		delay := 30 * time.Second * time.Duration(integer(item["attempts"])+1)
		if te != nil && time.Duration(te.RetryAfter)*time.Second > delay {
			delay = time.Duration(te.RetryAfter) * time.Second
		}
		_, _ = a.DB.Exec("UPDATE ts_bot_message_cleanup SET attempts=attempts+1,next_attempt_at=? WHERE bot_id=? AND chat_id=? AND message_id=?", future(delay), botID, chatID, messageID)
		if te != nil && te.Code == 429 {
			_, _ = a.DB.Exec("UPDATE ts_bot_message_cleanup SET next_attempt_at=GREATEST(next_attempt_at,?) WHERE bot_id=?", future(delay), botID)
			break
		}
	}
}
