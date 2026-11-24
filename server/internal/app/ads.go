package app

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var adKinds = map[string]string{
	"keyword": "关键词排名", "brand": "品牌展示", "search_top": "搜索顶部轮播",
	"search_bottom": "搜索底部按钮", "group_pin": "群组置顶", "channel": "频道广告",
}

func (a *App) saveAdSlot(id int64, input Row, actor int64) (Row, error) {
	return cTx(a, func(tx *sql.Tx) (Row, error) {
		old, err := cOne(tx, "SELECT * FROM ts_ad_slots WHERE id=? FOR UPDATE", id)
		if err != nil {
			return nil, err
		}
		if id > 0 && old == nil {
			return nil, cErr(404, "广告位不存在。")
		}
		values := Row{"name": "", "kind": "keyword", "description": "", "price": int64(10000000), "duration_days": int64(7), "capacity": int64(1), "target_chat": "", "enabled": false}
		for k := range values {
			if v, ok := old[k]; ok {
				values[k] = v
			}
			if v, ok := input[k]; ok && k != "price" {
				values[k] = v
			}
		}
		if v, ok := input["price"]; ok {
			amount, e := ParseMoney(v, false)
			if e != nil {
				return nil, e
			}
			values["price"] = amount
		}
		name := strings.TrimSpace(str(values["name"]))
		kind := str(values["kind"])
		if name == "" || utf8.RuneCountInString(name) > 80 || adKinds[kind] == "" {
			return nil, cErr(422, "请填写广告位名称并选择有效类型。")
		}
		if utf8.RuneCountInString(str(values["description"])) > 500 {
			return nil, cErr(422, "广告位说明最多 500 字。")
		}
		days := integer(values["duration_days"])
		capacity := integer(values["capacity"])
		if days < 1 || days > 365 || capacity < 1 || capacity > 50 {
			return nil, cErr(422, "投放天数须为 1–365，容量须为 1–50。")
		}
		if kind == "group_pin" && capacity != 1 {
			return nil, cErr(422, "群置顶为独占投放，容量请设置为 1。")
		}
		target := strings.TrimSpace(str(values["target_chat"]))
		enabled := boolean(values["enabled"])
		if len(target) > 40 {
			return nil, cErr(422, "目标群或频道格式不正确。")
		}
		if (kind == "group_pin" || kind == "channel") && enabled && !regexp.MustCompile(`^(-[1-9][0-9]{4,19}|@[a-zA-Z0-9_]{5,32})$`).MatchString(target) {
			return nil, cErr(422, "群组或频道广告位需填写群 ID 或 @频道名。")
		}
		if old != nil && (str(old["kind"]) != kind || str(old["target_chat"]) != target) {
			n, e := cCount(tx, "SELECT COUNT(*) FROM ts_ad_orders WHERE slot_id=? AND status IN ('pending','dispatching','active','delivery_issue')", id)
			if e != nil {
				return nil, e
			}
			if n > 0 {
				return nil, cErr(409, "广告位仍有订单，不能修改类型或目标聊天。请新建广告位。")
			}
		}
		args := []any{name, kind, str(values["description"]), integer(values["price"]), days, capacity, target, enabled, now()}
		if id > 0 {
			args = append(args, id)
			_, err = tx.Exec("UPDATE ts_ad_slots SET name=?,kind=?,description=?,price=?,duration_days=?,capacity=?,target_chat=?,enabled=?,updated_at=? WHERE id=?", args...)
		} else {
			args = append(args, now())
			var res sql.Result
			res, err = tx.Exec("INSERT INTO ts_ad_slots(name,kind,description,price,duration_days,capacity,target_chat,enabled,updated_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)", args...)
			if err == nil {
				id, _ = res.LastInsertId()
			}
		}
		if err != nil {
			return nil, err
		}
		if err = cAudit(tx, actor, "保存广告位", fmt.Sprintf("广告位 #%d", id)); err != nil {
			return nil, err
		}
		r, err := cOne(tx, "SELECT * FROM ts_ad_slots WHERE id=?", id)
		return moneyRow(r), err
	})
}

func (a *App) deleteAdSlot(id, actor int64) (Row, error) {
	return cTx(a, func(tx *sql.Tx) (Row, error) {
		r, err := cOne(tx, "SELECT id FROM ts_ad_slots WHERE id=? FOR UPDATE", id)
		if err != nil {
			return nil, err
		}
		if r == nil {
			return nil, cErr(404, "广告位不存在。")
		}
		n, err := cCount(tx, "SELECT COUNT(*) FROM ts_ad_orders WHERE slot_id=?", id)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, cErr(409, "广告位已有订单，请停用以保留历史。")
		}
		if _, err = tx.Exec("DELETE FROM ts_ad_slots WHERE id=?", id); err != nil {
			return nil, err
		}
		if err = cAudit(tx, actor, "删除空广告位", fmt.Sprintf("广告位 #%d", id)); err != nil {
			return nil, err
		}
		return Row{"deleted": true}, nil
	})
}

func validateAd(input Row) (title, link, keyword, content string, err error) {
	title = strings.TrimSpace(str(input["title"]))
	link = strings.TrimSpace(str(input["url"]))
	keyword = strings.ToLower(strings.TrimSpace(str(input["keyword"])))
	content = strings.TrimSpace(str(input["content"]))
	control := func(s string) bool {
		for _, r := range s {
			if unicode.IsControl(r) {
				return true
			}
		}
		return false
	}
	if title == "" || utf8.RuneCountInString(title) > 60 || control(title) {
		err = cErr(422, "广告标题须为 1–60 字单行文本。")
		return
	}
	u, e := url.Parse(link)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || len(link) > 500 || strings.ContainsAny(link, " \t\r\n") || control(link) {
		err = cErr(422, "请输入完整的 HTTPS 广告链接。")
		return
	}
	if utf8.RuneCountInString(content) > 500 || utf8.RuneCountInString(keyword) > 80 {
		err = cErr(422, "广告正文最多 500 字，关键词最多 80 字。")
	}
	return
}

func (a *App) BuyAd(user int64, input Row) (Row, error) {
	key, err := commerceRequestKey(input["request_key"])
	if err != nil {
		return nil, err
	}
	title, link, keyword, content, err := validateAd(input)
	if err != nil {
		return nil, err
	}
	return cTx(a, func(tx *sql.Tx) (Row, error) {
		slot, err := cOne(tx, "SELECT * FROM ts_ad_slots WHERE id=? FOR UPDATE", integer(input["slot_id"]))
		if err != nil {
			return nil, err
		}
		if err = activeCommerceIdentity(tx, user, input); err != nil {
			return nil, err
		}
		old, err := cOne(tx, "SELECT * FROM ts_ad_orders WHERE user_id=? AND request_key=?", user, key)
		if err != nil {
			return nil, err
		}
		if old != nil {
			if integer(old["slot_id"]) != integer(input["slot_id"]) || str(old["title"]) != title || str(old["url"]) != link || str(old["keyword"]) != keyword || str(old["content"]) != content {
				return nil, cErr(409, "请求编号已用于不同广告，请刷新后重试。")
			}
			return moneyRow(old), nil
		}
		if slot == nil || !boolean(slot["enabled"]) {
			return nil, cErr(409, "广告位暂未开放购买。")
		}
		if value, ok := input["quoted_price"]; ok {
			quoted, e := ParseMoney(value, false)
			if e != nil {
				return nil, e
			}
			if quoted != integer(slot["price"]) {
				return nil, cErr(409, "广告位价格已调整，请刷新后重新确认。")
			}
		}
		if value, ok := input["quoted_days"]; ok && integer(value) != integer(slot["duration_days"]) {
			return nil, cErr(409, "投放时长已调整，请刷新后重新确认。")
		}
		for _, field := range []string{"kind", "target", "name"} {
			slotField := field
			if field == "target" {
				slotField = "target_chat"
			}
			if value, ok := input["quoted_"+field]; ok && str(value) != str(slot[slotField]) {
				return nil, cErr(409, "广告位名称、类型或投放位置已调整，请刷新报价后重新确认。")
			}
		}
		kind := str(slot["kind"])
		scoped := kind == "keyword" || kind == "brand"
		if scoped && keyword == "" {
			return nil, cErr(422, "请填写需要精确匹配的关键词或品牌词。")
		}
		statement := "SELECT COUNT(*) FROM ts_ad_orders WHERE slot_id=? AND (status IN ('pending','dispatching','delivery_issue') OR (status='active' AND ends_at>?))"
		args := []any{slot["id"], now()}
		if scoped {
			statement += " AND keyword=?"
			args = append(args, keyword)
		}
		n, err := cCount(tx, statement, args...)
		if err != nil {
			return nil, err
		}
		if n >= integer(slot["capacity"]) {
			return nil, cErr(409, "此广告位的名额已满，请稍后购买。")
		}
		res, err := tx.Exec("INSERT INTO ts_ad_orders(user_id,slot_id,request_key,slot_name,kind,price,duration_days,target_chat,title,url,content,keyword,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'pending',?)", user, slot["id"], key, slot["name"], kind, slot["price"], slot["duration_days"], slot["target_chat"], title, link, content, keyword, now())
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		if _, err = walletPost(tx, user, -integer(slot["price"]), "ad_purchase", cBusinessKey("ad-buy", id), fmt.Sprintf("购买 %s #%d", slot["name"], id)); err != nil {
			return nil, err
		}
		r, err := cOne(tx, "SELECT * FROM ts_ad_orders WHERE id=?", id)
		return moneyRow(r), err
	})
}

func (a *App) reviewAd(ctx context.Context, id int64, decision, note string, actor int64) (Row, error) {
	if (decision != "approve" && decision != "reject" && decision != "refund") || strings.TrimSpace(note) == "" {
		return nil, cErr(422, "请选择审核结果并填写说明。")
	}
	order, err := cOne(a.DB, "SELECT * FROM ts_ad_orders WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	if order == nil {
		return nil, cErr(404, "广告订单不存在。")
	}
	external := str(order["kind"]) == "group_pin" || str(order["kind"]) == "channel"
	var botID int64
	if decision == "approve" && external {
		me, e := a.Telegram(ctx, "getMe", Row{})
		if e != nil {
			return nil, cErr(503, "暂时无法确认机器人身份。")
		}
		botID = integer(me["id"])
		chat, e := a.Telegram(ctx, "getChat", Row{"chat_id": order["target_chat"]})
		if e != nil {
			return nil, cErr(409, "无法访问投放目标，请检查机器人权限。")
		}
		member, e := a.Telegram(ctx, "getChatMember", Row{"chat_id": order["target_chat"], "user_id": botID})
		if e != nil {
			return nil, cErr(409, "无法确认机器人权限。")
		}
		allowed := str(order["kind"]) == "channel" && str(chat["type"]) == "channel" && boolean(member["can_post_messages"]) ||
			str(order["kind"]) == "group_pin" && (str(chat["type"]) == "group" || str(chat["type"]) == "supergroup") && boolean(member["can_pin_messages"])
		if !allowed {
			return nil, cErr(409, "机器人缺少投放目标的发帖或置顶权限，请先配置。")
		}
	}
	return cTx(a, func(tx *sql.Tx) (Row, error) {
		row, e := cOne(tx, "SELECT * FROM ts_ad_orders WHERE id=? FOR UPDATE", id)
		if e != nil {
			return nil, e
		}
		if row == nil {
			return nil, cErr(404, "广告订单不存在。")
		}
		if decision != "refund" && str(row["status"]) != "pending" {
			return nil, cErr(409, "该订单已审核，请刷新列表。")
		}
		if decision == "refund" && str(row["status"]) != "pending" && str(row["status"]) != "active" && str(row["status"]) != "delivery_issue" {
			return nil, cErr(409, "当前状态不支持退款，请先核实投放结果。")
		}
		status := "active"
		starts := row["starts_at"]
		ends := row["ends_at"]
		if decision == "approve" {
			if external {
				status = "dispatching"
				if _, e = tx.Exec("INSERT INTO ts_ad_deliveries(order_id,bot_id,state,updated_at) VALUES(?,?,'queued',?)", id, botID, now()); e != nil {
					return nil, e
				}
			} else {
				starts = now()
				ends = cDate(time.Now().Add(time.Duration(integer(row["duration_days"])) * 24 * time.Hour))
			}
		} else {
			status = "refunded"
			if decision == "reject" {
				status = "rejected"
			}
			if _, e = walletPost(tx, integer(row["user_id"]), integer(row["price"]), "ad_refund", cBusinessKey("ad-refund", id), fmt.Sprintf("广告订单 #%d 退款", id)); e != nil {
				return nil, e
			}
		}
		if _, e = tx.Exec("UPDATE ts_ad_orders SET status=?,review_note=?,reviewer_id=?,starts_at=?,ends_at=? WHERE id=?", status, cText(note, 300), actor, starts, ends, id); e != nil {
			return nil, e
		}
		if e = cAudit(tx, actor, "审核广告订单", fmt.Sprintf("订单 #%d，%s；%s", id, decision, cText(note, 150))); e != nil {
			return nil, e
		}
		r, e := cOne(tx, "SELECT * FROM ts_ad_orders WHERE id=?", id)
		return moneyRow(r), e
	})
}

func (a *App) LiveAds(keyword string) (Row, error) { return a.LiveAdsSeed(keyword, randomHex(8)) }

func (a *App) LiveAdsSeed(keyword, seed string) (Row, error) {
	rows, err := query(a.DB, "SELECT id,title,url,content,kind FROM ts_ad_orders WHERE status='active' AND starts_at<=? AND ends_at>? AND kind IN ('keyword','brand','search_top','search_bottom') AND (kind IN ('search_top','search_bottom') OR keyword=?) ORDER BY id LIMIT 200", now(), now(), strings.ToLower(strings.TrimSpace(keyword)))
	if err != nil {
		return nil, err
	}
	groups := map[string][]Row{}
	for _, row := range rows {
		k := str(row["kind"])
		groups[k] = append(groups[k], row)
	}
	out := Row{}
	for _, kind := range []string{"keyword", "brand", "search_top", "search_bottom"} {
		items := groups[kind]
		if len(items) == 0 {
			continue
		}
		n, _ := strconv.ParseUint(hash(seed + ":" + kind)[:7], 16, 32)
		out[kind] = items[int(n)%len(items)]
	}
	return out, nil
}

func adHTML(ad Row) string {
	return `广告 · <a href="` + html.EscapeString(str(ad["url"])) + `">` + html.EscapeString(str(ad["title"])) + `</a>`
}
