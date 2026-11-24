package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

var botCommerceCallbackPattern = regexp.MustCompile(`^c:([a-f0-9]{24}):([1-9][0-9]*):([a-z]+[0-9]*)$`)
var botCommerceTXID = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
var botCommerceTransitions = make(chan struct{}, 4)

// A per-bot/user advisory lock serializes a short conversation transition across
// processes. Financial transactions keep their own locks and idempotency keys.
// No financial DB transaction is held open while Telegram is being contacted.
func (a *App) withBotCommerceLock(ctx context.Context, botID, telegramID int64, fn func() error) error {
	// Leave connections available for the financial transactions inside fn.
	// Saturation rejects promptly instead of letting lock holders exhaust the pool.
	select {
	case botCommerceTransitions <- struct{}{}:
		defer func() { <-botCommerceTransitions }()
	default:
		return cErr(429, "钱包操作较多，请稍后重试。")
	}
	conn, err := a.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	key := "bot-commerce:" + hash(fmt.Sprintf("%d:%d", botID, telegramID))[:40]
	var acquired int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", key).Scan(&acquired); err != nil {
		return err
	}
	if acquired != 1 {
		return cErr(409, "上一项操作正在处理，请稍后再试。")
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(release, "SELECT RELEASE_LOCK(?)", key)
	}()
	return fn()
}

func (a *App) botCommerce(ctx context.Context, chat int64, user Row) error {
	credentials, err := a.botCredentials()
	if err != nil {
		return err
	}
	return a.openBotCommerce(ctx, chat, user, integer(object(credentials["bot"])["id"]), "resume")
}

func (a *App) pauseBotCommerce(ctx context.Context, botID, chat int64) error {
	if chat <= 0 {
		return nil
	}
	return a.withBotCommerceLock(ctx, botID, chat, func() error {
		_, err := a.DB.Exec("UPDATE ts_bot_commerce_sessions SET state='home',revision=revision+1,updated_at=? WHERE bot_id=? AND telegram_user_id=? AND state<>'home'", now(), botID, chat)
		return err
	})
}

func (a *App) openBotCommerce(ctx context.Context, chat int64, user Row, botID int64, view string) error {
	if chat <= 0 || chat != integer(user["telegram_id"]) || !boolean(user["status"]) {
		return cErr(403, "请使用已绑定的 Telegram 账号，在私聊中打开钱包。")
	}
	return a.withBotCommerceLock(ctx, botID, chat, func() error {
		s, err := one(a.DB, "SELECT * FROM ts_bot_commerce_sessions WHERE bot_id=? AND telegram_user_id=?", botID, chat)
		if err != nil {
			return err
		}
		if s == nil || expired(s["expires_at"]) || integer(s["user_id"]) != integer(user["id"]) {
			s = Row{"id": randomHex(12), "bot_id": botID, "telegram_user_id": chat, "chat_id": chat, "user_id": user["id"], "state": "home", "payload": "{}", "revision": 0, "message_id": 0, "last_input_id": 0}
			_, err = a.DB.Exec("INSERT INTO ts_bot_commerce_sessions(id,bot_id,telegram_user_id,chat_id,user_id,state,payload,revision,expires_at,updated_at) VALUES(?,?,?,?,?,'home','{}',0,?,?) ON DUPLICATE KEY UPDATE id=VALUES(id),user_id=VALUES(user_id),state='home',payload='{}',revision=0,message_id=0,last_input_id=0,expires_at=VALUES(expires_at),updated_at=VALUES(updated_at)", s["id"], botID, chat, chat, user["id"], future(30*time.Minute), now())
			if err != nil {
				return err
			}
		}
		p := decode(str(s["payload"]))
		switch view {
		case "recharge":
			s["state"] = "recharge_methods"
		case "ads":
			s["state"], p["page"] = "slots", 1
		case "home":
			s["state"] = "home"
		case "cancel":
			s["state"], p = "home", Row{}
		}
		return a.saveRenderBotCommerce(ctx, s, p, false)
	})
}

func (a *App) saveRenderBotCommerce(ctx context.Context, s, p Row, edit bool) error {
	s["revision"] = integer(s["revision"]) + 1
	s["payload"] = encode(p)
	s["expires_at"] = future(30 * time.Minute)
	_, err := a.DB.Exec("UPDATE ts_bot_commerce_sessions SET state=?,payload=?,revision=?,last_input_id=?,expires_at=?,updated_at=? WHERE id=?", s["state"], s["payload"], s["revision"], s["last_input_id"], s["expires_at"], now(), s["id"])
	if err != nil {
		return err
	}
	return a.renderBotCommerce(ctx, s, edit)
}

func (a *App) botCommerceCallback(ctx context.Context, cb Row, botID int64) error {
	parts := botCommerceCallbackPattern.FindStringSubmatch(str(cb["data"]))
	from, m := object(cb["from"]), object(cb["message"])
	chat, tg := integer(object(m["chat"])["id"]), integer(from["id"])
	if parts == nil || chat <= 0 || chat != tg || boolean(from["is_bot"]) {
		return cErr(403, "请在自己的私聊中操作钱包。")
	}
	if err := a.RateLimit("bot-callback:"+str(tg), 40, 60); err != nil {
		return err
	}
	return a.withBotCommerceLock(ctx, botID, tg, func() error {
		s, err := one(a.DB, "SELECT * FROM ts_bot_commerce_sessions WHERE id=? AND bot_id=? AND telegram_user_id=? AND chat_id=?", parts[1], botID, tg, chat)
		if err != nil {
			return err
		}
		if s == nil || expired(s["expires_at"]) {
			return cErr(409, "操作已过期，请发送 /wallet 重新打开；已创建的订单仍可查询。")
		}
		if integer(s["message_id"]) != integer(m["message_id"]) || integer(s["revision"]) != integer(parts[2]) {
			return cErr(409, "页面已经更新，请使用最新按钮，或发送 /wallet 恢复。")
		}
		user, err := a.telegramUser(from)
		if err != nil {
			return err
		}
		if integer(user["id"]) != integer(s["user_id"]) {
			return cErr(403, "账号绑定已变更，请发送 /wallet 重新打开。")
		}
		_ = a.answerCallback(ctx, cb, "正在处理", false)
		p := decode(str(s["payload"]))
		err = a.botCommerceAction(ctx, s, p, parts[3])
		if err != nil {
			return a.botCommerceFeedback(ctx, chat, err)
		}
		return a.saveRenderBotCommerce(ctx, s, p, true)
	})
}

func (a *App) botCommerceFeedback(ctx context.Context, chat int64, err error) error {
	var ce *commerceError
	var de *DomainError
	text := "暂时未能完成操作。已创建的订单会保留，请发送 /wallet 查看状态后重试。"
	if errors.As(err, &ce) || errors.As(err, &de) {
		text = err.Error() + "\n\n可继续使用当前按钮，或发送 /wallet 恢复操作。"
	}
	return a.sendText(ctx, chat, text, nil)
}

func (a *App) botCommerceAction(ctx context.Context, s, p Row, action string) error {
	user, state := integer(s["user_id"]), str(s["state"])
	ad, recharge := object(p["ad"]), object(p["recharge"])
	switch action {
	case "home":
		s["state"] = "home"
	case "cancel":
		for k := range p {
			delete(p, k)
		}
		s["state"] = "home"
	case "back":
		back := map[string]string{"recharge_amount": "recharge_methods", "recharge_confirm": "recharge_amount", "recharge_proof": "recharge_detail", "recharge_txid": "recharge_detail", "slot": "slots", "ad_title": "slot", "ad_url": "ad_title", "ad_content": "ad_url", "ad_keyword": "ad_content", "ad_confirm": "ad_content"}
		if state == "ad_confirm" && (str(ad["quoted_kind"]) == "keyword" || str(ad["quoted_kind"]) == "brand") {
			s["state"] = "ad_keyword"
		} else if previous := back[state]; previous != "" {
			s["state"] = previous
		} else {
			s["state"] = "home"
		}
	case "refresh":
		// The view always reloads authoritative balances and order statuses.
	case "recharge":
		s["state"] = "recharge_methods"
	case "trc", "offline":
		if state != "recharge_methods" {
			return cErr(409, "请先选择充值方式。")
		}
		method := "trc20"
		if action == "offline" {
			method = "offline"
		}
		cfg, err := a.CommerceConfig()
		if err != nil {
			return err
		}
		if !boolean(cfg[method+"_enabled"]) {
			return cErr(409, "此充值方式暂未启用。")
		}
		p["recharge"] = Row{"method": method, "request_key": "bot_" + randomHex(16)}
		s["state"] = "recharge_amount"
	case "create":
		if state != "recharge_confirm" {
			return cErr(409, "请先填写充值金额。")
		}
		if err := a.commerceLimit(fmt.Sprintf("recharge-create:%d", user), 6, time.Hour); err != nil {
			return err
		}
		order, err := a.CreateRecharge(user, catalogMerge(recharge, Row{"expected_telegram_id": s["telegram_user_id"]}))
		if err != nil {
			return err
		}
		p["recharge_id"], s["state"] = order["id"], "recharge_detail"
	case "proof", "txid", "check":
		if state != "recharge_detail" {
			return cErr(409, "请先打开充值订单。")
		}
		order, err := rechargeOwned(a.DB, integer(p["recharge_id"]), user, false)
		if err != nil {
			return err
		}
		if action == "check" {
			if err = a.commerceLimit(fmt.Sprintf("recharge-check:%d", user), 6, time.Minute); err != nil {
				return err
			}
			_, err = a.CheckRecharge(ctx, integer(order["id"]), user, "")
			return err
		}
		if action == "proof" {
			if str(order["method"]) != "offline" || !slices.Contains([]string{"pending", "review"}, str(order["status"])) || expired(order["expires_at"]) {
				return cErr(409, "当前订单不能补交凭证，请刷新订单状态。")
			}
			s["state"] = "recharge_proof"
		} else {
			if str(order["method"]) != "trc20" || !slices.Contains([]string{"pending", "expired"}, str(order["status"])) {
				return cErr(409, "当前订单不能提交交易哈希。")
			}
			s["state"] = "recharge_txid"
		}
	case "slots":
		s["state"], p["page"] = "slots", 1
	case "write":
		if state != "slot" || len(ad) == 0 {
			return cErr(409, "请先选择广告位。")
		}
		s["state"] = "ad_title"
	case "resume":
		if len(ad) == 0 {
			return cErr(409, "暂无广告草稿，请先选择广告位。")
		}
		s["state"] = "ad_title"
		if _, _, _, _, err := validateAd(ad); err == nil && (str(ad["quoted_kind"]) != "keyword" && str(ad["quoted_kind"]) != "brand" || str(ad["keyword"]) != "") {
			s["state"] = "ad_confirm"
		}
	case "skip":
		if state != "ad_content" {
			return cErr(409, "当前步骤不能跳过。")
		}
		ad["content"] = ""
		p["ad"] = ad
		s["state"] = botAdAfterContent(ad)
	case "buy":
		if state != "ad_confirm" {
			return cErr(409, "请先预览并确认广告内容。")
		}
		if err := a.commerceLimit(fmt.Sprintf("ad-buy:%d", user), 20, time.Minute); err != nil {
			return err
		}
		order, err := a.BuyAd(user, catalogMerge(ad, Row{"expected_telegram_id": s["telegram_user_id"]}))
		if err != nil {
			return err
		}
		p["ad_id"], s["state"] = order["id"], "ad_detail"
		delete(p, "ad")
	case "requote":
		if state != "ad_confirm" || len(ad) == 0 {
			return cErr(409, "请先完成广告填写。")
		}
		slot, err := a.botCommerceSlot(integer(ad["slot_id"]))
		if err != nil {
			return err
		}
		botAdQuote(ad, slot)
		p["ad"] = ad
		if botAdAfterContent(ad) == "ad_keyword" && str(ad["keyword"]) == "" {
			s["state"] = "ad_keyword"
		}
	case "website":
		// Old wallet messages may still contain this removed button. Refresh
		// the wallet without creating a credential; /login remains explicit.
		s["state"] = "home"
	default:
		for _, section := range []string{"ledger", "recharges", "orders", "slots"} {
			if strings.HasPrefix(action, section) && integer(strings.TrimPrefix(action, section)) > 0 {
				page := integer(strings.TrimPrefix(action, section))
				if page < 1 || page > 10000 {
					return cErr(422, "页码无效。")
				}
				p["page"], s["state"] = page, section
				return nil
			}
		}
		if strings.HasPrefix(action, "slot") {
			slot, err := a.botCommerceSlot(integer(strings.TrimPrefix(action, "slot")))
			if err != nil {
				return err
			}
			ad = Row{"slot_id": slot["id"], "request_key": "bot_" + randomHex(16)}
			botAdQuote(ad, slot)
			p["ad"], s["state"] = ad, "slot"
			return nil
		}
		if strings.HasPrefix(action, "rechargeid") {
			order, err := rechargeOwned(a.DB, integer(strings.TrimPrefix(action, "rechargeid")), user, false)
			if err != nil {
				return err
			}
			p["recharge_id"], s["state"] = order["id"], "recharge_detail"
			return nil
		}
		if strings.HasPrefix(action, "adid") {
			order, err := a.botOwnedAd(integer(strings.TrimPrefix(action, "adid")), user)
			if err != nil {
				return err
			}
			p["ad_id"], s["state"] = order["id"], "ad_detail"
			return nil
		}
		return cErr(422, "按钮无效，请发送 /wallet。")
	}
	return nil
}

func botAdAfterContent(ad Row) string {
	if str(ad["quoted_kind"]) == "keyword" || str(ad["quoted_kind"]) == "brand" {
		return "ad_keyword"
	}
	return "ad_confirm"
}

func botAdQuote(ad, slot Row) {
	ad["quoted_price"], ad["quoted_days"] = FormatMoney(integer(slot["price"])), slot["duration_days"]
	ad["quoted_kind"], ad["quoted_target"], ad["quoted_name"] = slot["kind"], slot["target_chat"], slot["name"]
}

func (a *App) botCommerceSlot(id int64) (Row, error) {
	slot, err := one(a.DB, "SELECT * FROM ts_ad_slots WHERE id=? AND enabled=1", id)
	if err == nil && slot == nil {
		err = cErr(409, "广告位暂未开放购买，请重新选择。")
	}
	return slot, err
}

func (a *App) botOwnedAd(id, user int64) (Row, error) {
	order, err := one(a.DB, "SELECT * FROM ts_ad_orders WHERE id=? AND user_id=?", id, user)
	if err == nil && order == nil {
		err = cErr(404, "广告订单不存在。")
	}
	return order, err
}

// Only an active private conversation consumes text/media; commands and
// verification answers are routed before this method by handleUpdate.
func (a *App) botCommerceText(ctx context.Context, m Row, botID int64) (bool, error) {
	from := object(m["from"])
	chat, tg := integer(object(m["chat"])["id"]), integer(from["id"])
	if chat <= 0 || chat != tg {
		return false, nil
	}
	s, err := one(a.DB, "SELECT * FROM ts_bot_commerce_sessions WHERE bot_id=? AND telegram_user_id=?", botID, tg)
	if err != nil || s == nil {
		return false, err
	}
	if integer(m["message_id"]) > 0 && integer(m["message_id"]) <= integer(s["last_input_id"]) {
		// A delivery retry must never turn an already consumed amount, TXID or
		// advertisement into a public search query, even after leaving input state.
		return true, nil
	}
	waiting := []string{"recharge_amount", "recharge_proof", "recharge_txid", "ad_title", "ad_url", "ad_content", "ad_keyword"}
	if !slices.Contains(waiting, str(s["state"])) {
		return false, nil
	}
	err = a.withBotCommerceLock(ctx, botID, tg, func() error {
		s, err = one(a.DB, "SELECT * FROM ts_bot_commerce_sessions WHERE id=?", s["id"])
		if err != nil || s == nil {
			return err
		}
		if expired(s["expires_at"]) {
			return cErr(409, "填写已超时，请发送 /wallet 重新开始；这条消息没有用于搜索。")
		}
		if !slices.Contains(waiting, str(s["state"])) || integer(m["message_id"]) <= integer(s["last_input_id"]) {
			return nil
		}
		if reply := integer(object(m["reply_to_message"])["message_id"]); reply > 0 && reply != integer(s["message_id"]) {
			return cErr(409, "这条回复对应旧步骤，请根据最新提示填写，或发送 /wallet 恢复。")
		}
		user, err := a.telegramUser(from)
		if err != nil {
			return err
		}
		if integer(user["id"]) != integer(s["user_id"]) {
			return cErr(403, "账号绑定已变更，请发送 /wallet。")
		}
		p := decode(str(s["payload"]))
		inputState := str(s["state"])
		if err = a.botCommerceInput(ctx, s, p, m); err != nil {
			return err
		}
		s["last_input_id"] = m["message_id"]
		if inputState != "recharge_proof" && inputState != "recharge_txid" {
			ctx = botCleanInput(botPrivateTurn(ctx, botID, m, from, false))
		}
		return a.saveRenderBotCommerce(ctx, s, p, false)
	})
	return true, err
}

func (a *App) botCommerceInput(ctx context.Context, s, p, m Row) error {
	text, state := strings.TrimSpace(str(m["text"])), str(s["state"])
	ad := object(p["ad"])
	user := integer(s["user_id"])
	switch state {
	case "recharge_amount":
		amount, err := ParseMoney(text, false)
		if err != nil {
			return err
		}
		r := object(p["recharge"])
		if amount < 1_000_000 || amount > 100_000_000_000 || str(r["method"]) == "trc20" && amount%1_000_000 != 0 {
			return cErr(422, "请输入 1–100000 USDT；TRC20 充值请输入整数，例如 10。")
		}
		r["amount"] = FormatMoney(amount)
		p["recharge"], s["state"] = r, "recharge_confirm"
	case "recharge_proof":
		var err error
		if len(array(m["photo"])) > 0 || m["document"] != nil {
			_, err = a.rechargeTelegramProof(ctx, user, integer(p["recharge_id"]), integer(s["bot_id"]), m)
		} else {
			_, err = a.rechargeProof(user, integer(p["recharge_id"]), text)
		}
		if err != nil {
			return err
		}
		s["state"] = "recharge_detail"
	case "recharge_txid":
		if !botCommerceTXID.MatchString(text) {
			return cErr(422, "请输入 64 位交易哈希（TXID），不要发送钱包私钥或助记词。")
		}
		if err := a.commerceLimit(fmt.Sprintf("recharge-check:%d", user), 6, time.Minute); err != nil {
			return err
		}
		if _, err := a.CheckRecharge(ctx, integer(p["recharge_id"]), user, text); err != nil {
			return err
		}
		s["state"] = "recharge_detail"
	case "ad_title":
		_, _, _, _, err := validateAd(Row{"title": text, "url": "https://example.com"})
		if err != nil {
			return err
		}
		ad["title"], s["state"] = text, "ad_url"
	case "ad_url":
		_, _, _, _, err := validateAd(Row{"title": ad["title"], "url": text})
		if err != nil {
			return err
		}
		ad["url"], s["state"] = text, "ad_content"
	case "ad_content":
		if text == "" || len([]rune(text)) > 500 {
			return cErr(422, "请发送 1–500 字广告正文，或点击「跳过正文」。")
		}
		ad["content"], s["state"] = text, botAdAfterContent(ad)
	case "ad_keyword":
		if text == "" || len([]rune(text)) > 80 || strings.ContainsAny(text, "\r\n\t") {
			return cErr(422, "请发送 1–80 字单行关键词或品牌词。")
		}
		ad["keyword"], s["state"] = strings.ToLower(text), "ad_confirm"
	default:
		return cErr(409, "当前没有等待填写的内容，请发送 /wallet。")
	}
	if len(ad) > 0 {
		p["ad"] = ad
	}
	return nil
}
