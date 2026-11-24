package app

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"time"
)

func (a *App) activateAd(job, order Row, message int64) error {
	_, err := cTx(a, func(tx *sql.Tx) (Row, error) {
		current, e := cOne(tx, "SELECT status FROM ts_ad_orders WHERE id=? FOR UPDATE", order["id"])
		if e != nil {
			return nil, e
		}
		if str(current["status"]) != "dispatching" {
			return nil, cErr(409, "广告订单状态已变化。")
		}
		_, e = tx.Exec("UPDATE ts_ad_deliveries SET state='sent',message_id=?,error='',updated_at=? WHERE id=?", message, now(), job["id"])
		if e != nil {
			return nil, e
		}
		_, e = tx.Exec("UPDATE ts_ad_orders SET status='active',starts_at=?,ends_at=? WHERE id=?", now(), cDate(time.Now().Add(time.Duration(integer(order["duration_days"]))*24*time.Hour)), order["id"])
		return Row{}, e
	})
	return err
}

func (a *App) markAdIssue(job Row, message int64, note string) error {
	_, err := cTx(a, func(tx *sql.Tx) (Row, error) {
		var messageValue any
		if message > 0 {
			messageValue = message
		}
		_, e := tx.Exec("UPDATE ts_ad_deliveries SET state='needs_review',message_id=?,error=?,updated_at=? WHERE id=?", messageValue, note, now(), job["id"])
		if e != nil {
			return nil, e
		}
		_, e = tx.Exec("UPDATE ts_ad_orders SET status='delivery_issue' WHERE id=? AND status='dispatching'", job["order_id"])
		return Row{}, e
	})
	return err
}

// Telegram does not offer an idempotency key for sending. Ambiguous sends require review.
func (a *App) deliverAd(ctx context.Context, id int64) error {
	job, err := cTx(a, func(tx *sql.Tx) (Row, error) {
		job, e := cOne(tx, "SELECT * FROM ts_ad_deliveries WHERE id=? FOR UPDATE", id)
		if e != nil {
			return nil, e
		}
		if job == nil || str(job["state"]) != "queued" {
			return nil, nil
		}
		order, e := cOne(tx, "SELECT status FROM ts_ad_orders WHERE id=? FOR UPDATE", job["order_id"])
		if e != nil {
			return nil, e
		}
		if str(order["status"]) != "dispatching" {
			return nil, nil
		}
		_, e = tx.Exec("UPDATE ts_ad_deliveries SET state='sending',updated_at=? WHERE id=?", now(), id)
		return job, e
	})
	if err != nil || job == nil {
		return err
	}
	order, err := cOne(a.DB, "SELECT * FROM ts_ad_orders WHERE id=?", job["order_id"])
	messageID := int64(0)
	send := func() error {
		if err != nil {
			return err
		}
		me, e := a.Telegram(ctx, "getMe", Row{})
		if e != nil {
			return e
		}
		if integer(me["id"]) != integer(job["bot_id"]) {
			return cErr(409, "机器人身份已更换。")
		}
		sent, e := a.Telegram(ctx, "sendMessage", Row{"chat_id": order["target_chat"], "text": adHTML(order) + "\n\n" + html.EscapeString(str(order["content"])), "parse_mode": "HTML", "link_preview_options": Row{"is_disabled": true}})
		if e != nil {
			return e
		}
		messageID = integer(sent["message_id"])
		if messageID < 1 {
			return cErr(503, "发送响应没有消息编号。")
		}
		if _, e = a.DB.Exec("UPDATE ts_ad_deliveries SET message_id=?,updated_at=? WHERE id=?", messageID, now(), id); e != nil {
			return e
		}
		if str(order["kind"]) == "group_pin" {
			if _, e = a.Telegram(ctx, "pinChatMessage", Row{"chat_id": order["target_chat"], "message_id": messageID, "disable_notification": true}); e != nil {
				return e
			}
		}
		return a.activateAd(job, order, messageID)
	}
	if err = send(); err != nil {
		note := "发送结果未确认，禁止自动重发；请检查聊天记录后处理。"
		if messageID > 0 {
			note = "消息已发送，置顶或入库未确认；请核实后处理。"
		}
		return a.markAdIssue(job, messageID, note)
	}
	return nil
}

func (a *App) resolveAd(ctx context.Context, id, message, actor int64) (Row, error) {
	if message < 1 {
		return nil, cErr(422, "请填写实际投放消息 ID。")
	}
	order, err := cOne(a.DB, "SELECT * FROM ts_ad_orders WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	job, err := cOne(a.DB, "SELECT * FROM ts_ad_deliveries WHERE order_id=?", id)
	if err != nil {
		return nil, err
	}
	if order == nil || job == nil || str(order["status"]) != "delivery_issue" {
		return nil, cErr(409, "没有待处理的投放异常。")
	}
	me, err := a.Telegram(ctx, "getMe", Row{})
	if err != nil {
		return nil, cErr(503, "暂时无法确认机器人身份。")
	}
	if integer(me["id"]) != integer(job["bot_id"]) {
		return nil, cErr(409, "请切回原机器人后处理此消息。")
	}
	_, err = cTx(a, func(tx *sql.Tx) (Row, error) {
		current, e := cOne(tx, "SELECT status FROM ts_ad_orders WHERE id=? FOR UPDATE", id)
		if e != nil {
			return nil, e
		}
		if str(current["status"]) != "delivery_issue" {
			return nil, cErr(409, "订单状态已变化，请刷新。")
		}
		if _, e = tx.Exec("UPDATE ts_ad_orders SET status='dispatching' WHERE id=?", id); e != nil {
			return nil, e
		}
		_, e = tx.Exec("UPDATE ts_ad_deliveries SET state='sending',message_id=?,updated_at=? WHERE id=?", message, now(), job["id"])
		return Row{}, e
	})
	if err != nil {
		return nil, err
	}
	if str(order["kind"]) == "group_pin" {
		_, err = a.Telegram(ctx, "pinChatMessage", Row{"chat_id": order["target_chat"], "message_id": message, "disable_notification": true})
	}
	if err == nil {
		err = a.activateAd(job, order, message)
	}
	if err != nil {
		_ = a.markAdIssue(job, message, "未能确认投放，请检查目标消息与权限。")
		return nil, cErr(409, "无法确认投放，请检查消息 ID 与机器人权限。")
	}
	_, err = cTx(a, func(tx *sql.Tx) (Row, error) {
		return Row{}, cAudit(tx, actor, "人工确认广告投放", fmt.Sprintf("订单 #%d，消息 #%d", id, message))
	})
	return Row{"resolved": true}, err
}

func (a *App) maintainAds(ctx context.Context) error {
	jobs, err := query(a.DB, "SELECT * FROM ts_ad_deliveries WHERE state='sending' AND updated_at<?", cDate(time.Now().Add(-5*time.Minute)))
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if _, err = cTx(a, func(tx *sql.Tx) (Row, error) {
			res, e := tx.Exec("UPDATE ts_ad_deliveries SET state='needs_review',error='进程中断，投放结果需人工核对。',updated_at=? WHERE id=? AND state='sending' AND updated_at<?", now(), job["id"], cDate(time.Now().Add(-5*time.Minute)))
			if e != nil {
				return nil, e
			}
			n, _ := res.RowsAffected()
			if n > 0 {
				_, e = tx.Exec("UPDATE ts_ad_orders SET status='delivery_issue' WHERE id=? AND status='dispatching'", job["order_id"])
			}
			return Row{}, e
		}); err != nil {
			return err
		}
	}
	if _, err = a.DB.Exec("UPDATE ts_ad_orders SET status='completed' WHERE status='active' AND ends_at<=?", now()); err != nil {
		return err
	}
	jobs, err = query(a.DB, "SELECT d.*,o.target_chat FROM ts_ad_deliveries d JOIN ts_ad_orders o ON o.id=d.order_id WHERE o.kind='group_pin' AND o.status IN ('completed','refunded') AND d.state IN ('sent','needs_review') AND d.message_id IS NOT NULL ORDER BY d.id LIMIT 20")
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		me, e := a.Telegram(ctx, "getMe", Row{})
		if e != nil || integer(me["id"]) != integer(job["bot_id"]) {
			continue
		}
		_, e = a.Telegram(ctx, "unpinChatMessage", Row{"chat_id": job["target_chat"], "message_id": job["message_id"]})
		if e == nil {
			_, err = a.DB.Exec("UPDATE ts_ad_deliveries SET state='finished',error='',updated_at=? WHERE id=?", now(), job["id"])
		} else {
			_, err = a.DB.Exec("UPDATE ts_ad_deliveries SET error='结束置顶失败，将自动重试；请核对机器人权限。',updated_at=? WHERE id=?", now(), job["id"])
		}
		if err != nil {
			return err
		}
	}
	return nil
}
