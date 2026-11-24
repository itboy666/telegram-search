package app

import (
	"context"
	"fmt"
	"slices"
)

func botCommerceStatus(status string) string {
	if label := map[string]string{"pending": "待处理", "review": "凭证待审核", "paid": "已到账", "rejected": "已拒绝", "expired": "已到期", "active": "投放中", "dispatching": "投放处理中", "delivery_issue": "投放待核对", "refunded": "已退款"}[status]; label != "" {
		return label
	}
	return status
}

func (a *App) botCommerceButton(s Row, text, action, icon string) Row {
	return a.iconButton(icon, Row{"text": text, "callback_data": fmt.Sprintf("c:%s:%d:%s", str(s["id"]), integer(s["revision"]), action)})
}

func (a *App) renderBotCommerce(ctx context.Context, s Row, edit bool) error {
	text, buttons, err := a.botCommerceView(s)
	if err != nil {
		return err
	}
	p := Row{"chat_id": s["chat_id"], "text": text, "parse_mode": "HTML", "link_preview_options": Row{"is_disabled": true}, "reply_markup": Row{"inline_keyboard": buttons}}
	key, editID := "panel", int64(0)
	payload := decode(str(s["payload"]))
	switch str(s["state"]) {
	case "recharge_detail":
		key = "recharge:" + str(integer(payload["recharge_id"]))
	case "ad_detail":
		key = "ad:" + str(integer(payload["ad_id"]))
	}
	if edit {
		editID = integer(s["message_id"])
	}
	sent, err := a.deliverBotView(ctx, integer(s["bot_id"]), integer(s["chat_id"]), key, editID, p)
	if err != nil {
		return err
	}
	if id := integer(sent["message_id"]); id > 0 {
		_, err = a.DB.Exec("UPDATE ts_bot_commerce_sessions SET message_id=? WHERE id=? AND revision=?", id, s["id"], s["revision"])
		s["message_id"] = id
	}
	return err
}

func (a *App) botCommerceView(s Row) (string, [][]Row, error) {
	telegramID := integer(s["telegram_user_id"])
	if telegramID <= 0 || integer(s["chat_id"]) != telegramID {
		return "", nil, cErr(403, "请在自己的私聊中操作钱包。")
	}
	p := decode(str(s["payload"]))
	user, state := integer(s["user_id"]), str(s["state"])
	ad, recharge := object(p["ad"]), object(p["recharge"])
	button := func(text, action, icon string) Row { return a.botCommerceButton(s, text, action, icon) }
	title := func(icon, text string) string { return a.iconHTML(icon) + " <b>" + text + "</b>\n\n" }
	rows := [][]Row{}
	text := ""
	balance, err := a.WalletBalance(user)
	if err != nil {
		return "", nil, err
	}
	switch state {
	case "home":
		text = title("user", "我的钱包") + "账户 ID：<code>" + str(telegramID) + "</code>\n可用余额：<b>" + balance + " USDT</b>\n\n充值、广告购买和账单查询，均可在这里完成。"
		rows = append(rows,
			[]Row{button("充值余额", "recharge", "plus"), button("购买广告", "slots", "star")},
			[]Row{button("充值订单", "recharges1", "clock"), button("我的广告", "orders1", "star")},
			[]Row{button("资金明细", "ledger1", "clock"), button("刷新余额", "refresh", "refresh")})
		if len(ad) > 0 {
			rows = append(rows, []Row{button("继续广告草稿", "resume", "edit"), button("清除草稿", "cancel", "close")})
		}
		rows = append(rows, a.homeRow())
	case "recharge_methods":
		cfg, e := a.CommerceConfig()
		if e != nil {
			return "", nil, e
		}
		text = title("plus", "充值余额 · 选择方式") + "当前余额：" + balance + " USDT\n\n选择方式后输入金额，确认后生成付款订单。"
		if boolean(cfg["trc20_enabled"]) {
			rows = append(rows, []Row{button("USDT · TRC20", "trc", "plus")})
		}
		if boolean(cfg["offline_enabled"]) {
			rows = append(rows, []Row{button("线下转账", "offline", "plus")})
		}
		if len(rows) == 0 {
			text += "\n\n暂未开放充值方式，请稍后再来。"
		}
	case "recharge_amount":
		method := "线下转账"
		hint := "金额范围 1–100000 USDT，最多六位小数。"
		if str(recharge["method"]) == "trc20" {
			method, hint = "USDT · TRC20", "请输入 1–100000 的整数，例如：10。\n系统会为订单分配带六位小数的准确付款金额。"
		}
		text = title("plus", "充值余额 · 1/2") + "方式：" + method + "\n\n请发送充值金额。\n" + hint
	case "recharge_confirm":
		text = title("plus", "充值余额 · 2/2") + "方式：" + map[string]string{"trc20": "USDT · TRC20", "offline": "线下转账"}[str(recharge["method"])] + "\n申请金额：<b>" + escape(str(recharge["amount"])) + " USDT</b>"
		if str(recharge["method"]) == "trc20" {
			text += "\n\n确认后会生成专属小数金额和收款地址。\n请按下一页的完整金额付款，实际到账金额计入余额。"
		} else {
			text += "\n\n确认后显示收款说明。转账后直接发送文字凭证或截图，后台核账后入账。"
		}
		text += "\n\n本步骤只创建订单，不会自动转账。"
		rows = append(rows, []Row{button("确认生成付款订单", "create", "check")})
	case "recharge_detail":
		order, e := rechargeOwned(a.DB, integer(p["recharge_id"]), user, false)
		if e != nil {
			return "", nil, e
		}
		status, method := str(order["status"]), str(order["method"])
		text = title("plus", "充值订单 #"+str(order["id"])) + "状态：<b>" + botCommerceStatus(status) + "</b>\n金额：<code>" + FormatMoney(integer(order["amount"])) + "</code> USDT\n有效期至：" + str(order["expires_at"]) + "（北京时间）"
		if method == "trc20" {
			if status == "pending" && !expired(order["expires_at"]) {
				text += "\n\n网络：<b>TRON / TRC20</b>\n收款地址：\n<code>" + escape(str(order["receive_address"])) + "</code>\n\n请按上面的完整六位小数金额付款，勿取整。\n转账完成后点击「核查到账」，也可提交交易哈希。"
			} else if status == "expired" || status == "pending" && expired(order["expires_at"]) {
				text += "\n\n付款窗口已结束，请勿继续转账。\n若在有效期内已付款，可继续核查到账。"
			}
			if slices.Contains([]string{"pending", "expired"}, status) {
				rows = append(rows, []Row{button("核查到账", "check", "refresh"), button("提交交易哈希", "txid", "edit")})
			}
		} else if slices.Contains([]string{"pending", "review"}, status) {
			cfg, e := a.CommerceConfig()
			if e != nil {
				return "", nil, e
			}
			if !expired(order["expires_at"]) {
				text += "\n\n<b>收款说明</b>\n" + escape(clip(str(cfg["offline_instructions"]), 900)) + "\n\n付款后点击下方按钮，发送凭证说明或转账截图。"
				rows = append(rows, []Row{button("提交 / 补充凭证", "proof", "edit")})
			} else {
				text += "\n\n凭证提交窗口已结束；已提交的凭证仍可由管理员审核。"
			}
			if str(order["proof"]) != "" {
				text += "\n\n已提交：" + escape(clip(str(order["proof"]), 200))
			}
		}
		if status == "paid" {
			text += "\n\n已记入余额。当前可用：<b>" + balance + " USDT</b>"
		}
		if str(order["review_note"]) != "" {
			text += "\n\n审核说明：" + escape(str(order["review_note"]))
		}
		rows = append(rows, []Row{button("刷新订单", "refresh", "refresh"), button("全部充值订单", "recharges1", "clock")})
		if len(ad) > 0 {
			rows = append(rows, []Row{button("继续购买广告", "resume", "star")})
		}
	case "recharge_proof":
		text = title("edit", "提交转账凭证") + "充值订单：#" + str(p["recharge_id"]) + "\n\n直接发送 5–500 字说明（付款人、时间、转账编号），或发送转账截图。\n支持 JPEG、PNG、WebP 图片，最大 5 MB；截图可附上说明。\n\n提交后等待后台核账，无需打开网站。"
	case "recharge_txid":
		text = title("edit", "提交交易哈希") + "充值订单：#" + str(p["recharge_id"]) + "\n\n请发送 TRON 链上的 64 位交易哈希（TXID）。\n系统会核对币种、地址、金额、付款时间和确认状态。\n\n不要发送钱包私钥或助记词。"
	case "slots":
		var e error
		text, rows, e = a.botCommerceList(s, "slots")
		if e != nil {
			return "", nil, e
		}
	case "slot":
		slot, e := a.botCommerceSlot(integer(ad["slot_id"]))
		if e != nil {
			return "", nil, e
		}
		text = title("star", "广告位详情") + "<b>" + escape(str(ad["quoted_name"])) + "</b>\n类型：" + escape(adKinds[str(ad["quoted_kind"])]) + "\n价格：<b>" + escape(str(ad["quoted_price"])) + " USDT / " + str(ad["quoted_days"]) + " 天</b>\n当前余额：" + balance + " USDT\n\n" + escape(str(slot["description"])) + "\n\n购买后扣除余额，审核通过后开始计时；审核拒绝退回余额。\n库存与报价将在确认购买时再次校验。"
		if str(ad["quoted_target"]) != "" {
			text += "\n投放目标：<code>" + escape(str(ad["quoted_target"])) + "</code>"
		}
		rows = append(rows, []Row{button("填写广告内容", "write", "edit")})
	case "ad_title", "ad_url", "ad_content", "ad_keyword":
		prompts := map[string]string{
			"ad_title":   "1/4 · 广告标题\n\n请发送 1–60 字单行标题。",
			"ad_url":     "2/4 · 跳转链接\n\n请发送完整的 HTTPS 链接，例如 https://t.me/your_channel。",
			"ad_content": "3/4 · 广告正文\n\n请发送 1–500 字正文。只展示标题时可点击「跳过正文」。",
			"ad_keyword": "4/4 · 匹配词\n\n请发送一个精确匹配的关键词或品牌词，最多 80 字。",
		}
		text = title("edit", "填写广告") + escape(str(ad["quoted_name"])) + "\n\n" + prompts[state]
		field := map[string]string{"ad_title": "title", "ad_url": "url", "ad_content": "content", "ad_keyword": "keyword"}[state]
		if str(ad[field]) != "" {
			text += "\n\n当前内容：" + escape(clip(str(ad[field]), 160)) + "\n可重新发送进行修改。"
		}
		if state == "ad_content" {
			rows = append(rows, []Row{button("跳过正文", "skip", "right")})
		}
		text += "\n\n草稿保留 30 分钟；/wallet 继续，/cancel 退出。"
	case "ad_confirm":
		text = title("star", "广告预览 · 确认购买") + "广告位：" + escape(str(ad["quoted_name"])) + "\n类型：" + escape(adKinds[str(ad["quoted_kind"])]) + "\n投放时长：" + str(ad["quoted_days"]) + " 天\n本次扣款：<b>" + escape(str(ad["quoted_price"])) + " USDT</b>\n可用余额：" + balance + " USDT"
		if str(ad["quoted_target"]) != "" {
			text += "\n投放目标：<code>" + escape(str(ad["quoted_target"])) + "</code>"
		}
		text += "\n\n<b>" + escape(str(ad["title"])) + "</b>\n" + escape(str(ad["url"]))
		if str(ad["content"]) != "" {
			text += "\n\n" + escape(str(ad["content"]))
		}
		if str(ad["keyword"]) != "" {
			text += "\n\n匹配词：" + escape(str(ad["keyword"]))
		}
		text += "\n\n点击确认将从余额扣款并提交审核。\n审核通过后计时；拒绝则退回余额。"
		rows = append(rows, []Row{button("确认购买并扣款", "buy", "check")}, []Row{button("刷新报价", "requote", "refresh"), button("先充值", "recharge", "plus")})
	case "ad_detail":
		order, e := a.botOwnedAd(integer(p["ad_id"]), user)
		if e != nil {
			return "", nil, e
		}
		status := botCommerceStatus(str(order["status"]))
		if str(order["status"]) == "pending" {
			status = "已扣款 · 等待审核"
		} else if str(order["status"]) == "rejected" {
			status = "已拒绝 · 余额已退回"
		}
		text = title("star", "广告订单 #"+str(order["id"])) + "广告位：" + escape(str(order["slot_name"])) + "\n状态：<b>" + status + "</b>\n实付：" + FormatMoney(integer(order["price"])) + " USDT\n投放时长：" + str(order["duration_days"]) + " 天\n\n<b>" + escape(str(order["title"])) + "</b>\n" + escape(str(order["url"]))
		if str(order["content"]) != "" {
			text += "\n\n" + escape(str(order["content"]))
		}
		if str(order["keyword"]) != "" {
			text += "\n\n匹配词：" + escape(str(order["keyword"]))
		}
		if str(order["starts_at"]) != "" {
			text += "\n开始：" + str(order["starts_at"]) + "\n结束：" + str(order["ends_at"]) + "（北京时间）"
		}
		if str(order["review_note"]) != "" {
			text += "\n\n审核说明：" + escape(str(order["review_note"]))
		}
		rows = append(rows, []Row{button("刷新状态", "refresh", "refresh"), button("全部广告订单", "orders1", "clock")})
	case "ledger", "recharges", "orders":
		var e error
		text, rows, e = a.botCommerceList(s, state)
		if e != nil {
			return "", nil, e
		}
	default:
		return "", nil, cErr(409, "页面已失效，请发送 /account 重新打开。")
	}
	if state != "home" {
		if slices.Contains([]string{"recharge_amount", "recharge_confirm", "recharge_proof", "recharge_txid", "slot", "ad_title", "ad_url", "ad_content", "ad_keyword", "ad_confirm"}, state) {
			rows = append(rows, []Row{button("上一步", "back", "left"), button("退出填写", "cancel", "close")})
		}
		rows = append(rows, []Row{button("返回钱包", "home", "user")})
	}
	return text, rows, nil
}

func (a *App) botCommerceList(s Row, section string) (string, [][]Row, error) {
	p := decode(str(s["payload"]))
	page := catalogLimit(p["page"], 1, 1, 10000)
	table := map[string]string{"slots": "ad_slots", "ledger": "wallet_entries", "recharges": "recharges", "orders": "ad_orders"}[section]
	where, args := " WHERE user_id=?", []any{s["user_id"]}
	if section == "slots" {
		where, args = " WHERE enabled=1", nil
	}
	total, err := cCount(a.DB, "SELECT COUNT(*) FROM ts_"+table+where, args...)
	if err != nil {
		return "", nil, err
	}
	pages := (total + 4) / 5
	if pages < 1 {
		pages = 1
	}
	if page > pages {
		page = pages
	}
	items, err := query(a.DB, "SELECT * FROM ts_"+table+where+" ORDER BY id DESC LIMIT 5 OFFSET ?", append(args, (page-1)*5)...)
	if err != nil {
		return "", nil, err
	}
	name := map[string]string{"slots": "可购买的广告位", "ledger": "资金明细", "recharges": "我的充值订单", "orders": "我的广告订单"}[section]
	text := a.iconHTML("clock") + " <b>" + name + "</b>\n\n"
	rows := [][]Row{}
	if len(items) == 0 {
		text += "暂无记录。"
	}
	for _, r := range items {
		id := str(r["id"])
		switch section {
		case "slots":
			text += "#" + id + " <b>" + escape(str(r["name"])) + "</b>\n" + escape(adKinds[str(r["kind"])]) + " · " + FormatMoney(integer(r["price"])) + " USDT / " + str(r["duration_days"]) + " 天\n\n"
			rows = append(rows, []Row{a.botCommerceButton(s, "#"+id+" "+clip(str(r["name"]), 24), "slot"+id, "star")})
		case "ledger":
			amount := FormatMoney(integer(r["amount"]))
			if integer(r["amount"]) > 0 {
				amount = "+" + amount
			}
			text += "#" + id + " <b>" + amount + " USDT</b>\n" + escape(clip(str(r["note"]), 100)) + "\n余额 " + FormatMoney(integer(r["balance_after"])) + " · " + str(r["created_at"]) + "\n\n"
		case "recharges":
			text += "#" + id + " <b>" + FormatMoney(integer(r["amount"])) + " USDT</b> · " + botCommerceStatus(str(r["status"])) + "\n" + str(r["created_at"]) + "\n\n"
			rows = append(rows, []Row{a.botCommerceButton(s, "查看充值 #"+id, "rechargeid"+id, "clock")})
		case "orders":
			text += "#" + id + " <b>" + escape(clip(str(r["title"]), 60)) + "</b>\n" + FormatMoney(integer(r["price"])) + " USDT · " + botCommerceStatus(str(r["status"])) + "\n" + str(r["created_at"]) + "\n\n"
			rows = append(rows, []Row{a.botCommerceButton(s, "查看广告 #"+id, "adid"+id, "star")})
		}
	}
	text += fmt.Sprintf("\n第 %d / %d 页 · 共 %d 条", page, pages, total)
	pagination := []Row{}
	if page > 1 {
		pagination = append(pagination, a.botCommerceButton(s, "上一页", section+str(page-1), "left"))
	}
	if page < pages {
		pagination = append(pagination, a.botCommerceButton(s, "下一页", section+str(page+1), "right"))
	}
	if len(pagination) > 0 {
		rows = append(rows, pagination)
	}
	return text, rows, nil
}
