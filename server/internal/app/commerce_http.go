package app

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

type commerceHandler func(http.ResponseWriter, *http.Request, *Identity, Row) (any, error)

func (a *App) commerceUser(pattern string, write bool, fn commerceHandler) {
	a.Handle(pattern, "user", "", write, func(w http.ResponseWriter, r *http.Request, identity *Identity) {
		input := Row{}
		if write {
			var err error
			input, err = Body(r)
			if err != nil {
				Fail(w, 422, "请求格式不正确。")
				return
			}
		}
		data, err := fn(w, r, identity, input)
		if row, ok := data.(Row); err == nil && ok && row["method"] != nil && row["id"] != nil {
			err = a.annotateRechargeProof(row)
		}
		commerceReply(w, data, err)
	})
}

func (a *App) commerceAdmin(pattern, module, action string, write bool, fn commerceHandler) {
	a.Handle(pattern, "admin", module+"."+action, write, func(w http.ResponseWriter, r *http.Request, identity *Identity) {
		input := Row{}
		if write {
			var err error
			input, err = Body(r)
			if err != nil {
				Fail(w, 422, "请求格式不正确。")
				return
			}
			if err = identity.Access.CheckWrite(module, input); err != nil {
				Fail(w, 403, "无权修改所提交的字段。")
				return
			}
		}
		data, err := fn(w, r, identity, input)
		if err == nil {
			if row, ok := data.(map[string]any); ok {
				if action != "delete" && action != "resolve" {
					data = identity.Access.Project(module, row)
					if module == "recharges" {
						projected := data.(Row)
						if _, ok := projected["proof"]; ok {
							err = a.annotateRechargeProof(projected)
						}
					}
				}
			}
		}
		commerceReply(w, data, err)
	})
}

func (a *App) RegisterCommerce() {
	a.registerCommerceProof()
	a.commerceUser("GET /user/api/commerce/summary", false, func(_ http.ResponseWriter, r *http.Request, i *Identity, _ Row) (any, error) {
		config, err := commerceConfig(a.DB)
		if err != nil {
			return nil, err
		}
		balance, err := a.WalletBalance(i.ID)
		if err != nil {
			return nil, err
		}
		today, err := cCount(a.DB, "SELECT COALESCE(SUM(amount),0) FROM ts_reward_claims WHERE user_id=? AND day=?", i.ID, now()[:10])
		if err != nil {
			return nil, err
		}
		total, err := cCount(a.DB, "SELECT COALESCE(SUM(amount),0) FROM ts_reward_claims WHERE user_id=?", i.ID)
		if err != nil {
			return nil, err
		}
		active, err := cCount(a.DB, "SELECT COUNT(*) FROM ts_ad_orders WHERE user_id=? AND status='active' AND ends_at>?", i.ID, now())
		if err != nil {
			return nil, err
		}
		public := Row{}
		for _, key := range []string{"trc20_enabled", "offline_enabled", "offline_instructions", "reward_enabled", "reward_amount", "reward_daily_cap", "reward_user_cap"} {
			public[key] = config[key]
		}
		return Row{"balance": balance, "currency": "USDT", "reward_today": FormatMoney(today), "reward_total": FormatMoney(total), "active_ads": active, "config": public}, nil
	})
	a.commerceUser("GET /user/api/commerce/slots", false, func(_ http.ResponseWriter, _ *http.Request, _ *Identity, _ Row) (any, error) {
		rows, err := query(a.DB, "SELECT id,name,kind,description,price,duration_days,capacity,enabled FROM ts_ad_slots ORDER BY id")
		if err != nil {
			return nil, err
		}
		items := make([]Row, 0, len(rows))
		for _, row := range rows {
			items = append(items, moneyRow(row))
		}
		return Row{"kinds": adKinds, "items": items}, nil
	})
	a.commerceUser("GET /user/api/commerce/records/{section}", false, func(_ http.ResponseWriter, r *http.Request, i *Identity, _ Row) (any, error) {
		table := map[string]string{"ledger": "wallet_entries", "recharges": "recharges", "ads": "ad_orders"}[r.PathValue("section")]
		if table == "" {
			return nil, cErr(404, "接口不存在。")
		}
		total, err := cCount(a.DB, "SELECT COUNT(*) FROM ts_"+table+" WHERE user_id=?", i.ID)
		if err != nil {
			return nil, err
		}
		rows, err := query(a.DB, "SELECT * FROM ts_"+table+" WHERE user_id=? ORDER BY id DESC LIMIT 20 OFFSET ?", i.ID, (commercePage(r)-1)*20)
		if err != nil {
			return nil, err
		}
		items := make([]Row, 0, len(rows))
		for _, row := range rows {
			if table == "recharges" {
				if err = a.annotateRechargeProof(row); err != nil {
					return nil, err
				}
			}
			items = append(items, moneyRow(row))
		}
		return Row{"total": total, "items": items}, nil
	})
	a.commerceUser("POST /user/api/commerce/recharges", true, func(_ http.ResponseWriter, _ *http.Request, i *Identity, b Row) (any, error) {
		if err := a.commerceLimit(fmt.Sprintf("recharge-create:%d", i.ID), 6, time.Hour); err != nil {
			return nil, err
		}
		return a.CreateRecharge(i.ID, b)
	})
	a.commerceUser("POST /user/api/commerce/recharges/{id}/proof", true, func(_ http.ResponseWriter, r *http.Request, i *Identity, b Row) (any, error) {
		return a.rechargeProof(i.ID, commerceID(r), str(b["proof"]))
	})
	a.commerceUser("POST /user/api/commerce/recharges/{id}/check", true, func(_ http.ResponseWriter, r *http.Request, i *Identity, b Row) (any, error) {
		if err := a.commerceLimit(fmt.Sprintf("recharge-check:%d", i.ID), 6, time.Minute); err != nil {
			return nil, err
		}
		return a.CheckRecharge(r.Context(), commerceID(r), i.ID, str(b["txid"]))
	})
	a.commerceUser("POST /user/api/commerce/ads", true, func(_ http.ResponseWriter, _ *http.Request, i *Identity, b Row) (any, error) {
		if err := a.commerceLimit(fmt.Sprintf("ad-buy:%d", i.ID), 20, time.Minute); err != nil {
			return nil, err
		}
		return a.BuyAd(i.ID, b)
	})
	a.commerceUser("POST /user/api/commerce/search", true, func(_ http.ResponseWriter, _ *http.Request, i *Identity, b Row) (any, error) {
		if err := a.commerceLimit(fmt.Sprintf("user-search:%d", i.ID), 60, time.Minute); err != nil {
			return nil, err
		}
		result, err := a.Search(b, "web")
		if err != nil {
			return nil, err
		}
		var reward Row
		if integer(result["page"]) <= 1 {
			count := int64(0)
			switch items := result["items"].(type) {
			case []Row:
				count = int64(len(items))
			case []any:
				count = int64(len(items))
			}
			reward, err = a.Reward(i.ID, str(b["q"]), count, "web")
			if err != nil {
				return nil, err
			}
		}
		result["reward"] = reward
		result["ads"], err = a.LiveAds(str(b["q"]))
		return result, err
	})
	a.commerceAdmin("GET /admin/api/commerce/settings", "commerce_settings", "read", false, func(_ http.ResponseWriter, _ *http.Request, _ *Identity, _ Row) (any, error) {
		return commerceConfig(a.DB)
	})
	a.commerceAdmin("POST /admin/api/commerce/settings", "commerce_settings", "update", true, func(_ http.ResponseWriter, _ *http.Request, i *Identity, b Row) (any, error) {
		return a.saveCommerceConfig(b, i.ID)
	})
	// The connection response is operational feedback, not a configuration record.
	a.Handle("POST /admin/api/commerce/connection", "admin", "commerce_settings.update", true, func(w http.ResponseWriter, r *http.Request, i *Identity) {
		b, err := Body(r)
		if err != nil {
			Fail(w, 422, "请求格式不正确。")
			return
		}
		if err = i.Access.CheckWrite("commerce_settings", b); err != nil {
			Fail(w, 403, "无权修改所提交的字段。")
			return
		}
		if err = a.commerceLimit(fmt.Sprintf("commerce-connection:%d", i.ID), 6, time.Minute); err != nil {
			commerceReply(w, nil, err)
			return
		}
		config, err := commerceConfig(a.DB)
		if err == nil && str(config["receive_address"]) == "" {
			err = cErr(422, "请先配置收款地址。")
		}
		if err == nil {
			var client *tronscanClient
			client, err = a.newTronscan()
			if err == nil {
				_, err = client.transfers(r.Context(), str(config["receive_address"]), time.Now().Add(-time.Minute), 0)
			}
		}
		commerceReply(w, Row{"connected": true, "message": "Tronscan 查询成功（未进行转账）"}, err)
	})
	a.Handle("GET /admin/api/commerce/records/{section}", "admin", "", false, func(w http.ResponseWriter, r *http.Request, i *Identity) {
		result, err := a.commerceRecords(r, i)
		commerceReply(w, result, err)
	})
	a.commerceAdmin("POST /admin/api/commerce/slots", "ad_slots", "create", true, func(_ http.ResponseWriter, _ *http.Request, i *Identity, b Row) (any, error) {
		return a.saveAdSlot(0, b, i.ID)
	})
	a.commerceAdmin("POST /admin/api/commerce/slots/{id}", "ad_slots", "update", true, func(_ http.ResponseWriter, r *http.Request, i *Identity, b Row) (any, error) {
		return a.saveAdSlot(commerceID(r), b, i.ID)
	})
	a.commerceAdmin("DELETE /admin/api/commerce/slots/{id}", "ad_slots", "delete", true, func(_ http.ResponseWriter, r *http.Request, i *Identity, _ Row) (any, error) {
		return a.deleteAdSlot(commerceID(r), i.ID)
	})
	a.commerceAdmin("POST /admin/api/commerce/recharges/{id}/review", "recharges", "review", true, func(_ http.ResponseWriter, r *http.Request, i *Identity, b Row) (any, error) {
		return a.reviewRecharge(commerceID(r), str(b["decision"]), str(b["review_note"]), i.ID)
	})
	a.commerceAdmin("POST /admin/api/commerce/recharges/{id}/check", "recharges", "check", true, func(_ http.ResponseWriter, r *http.Request, i *Identity, b Row) (any, error) {
		if err := a.commerceLimit(fmt.Sprintf("admin-payment-check:%d", i.ID), 10, time.Minute); err != nil {
			return nil, err
		}
		return a.CheckRecharge(r.Context(), commerceID(r), 0, str(b["txid"]))
	})
	a.commerceAdmin("POST /admin/api/commerce/ads/{id}/review", "ad_orders", "review", true, func(_ http.ResponseWriter, r *http.Request, i *Identity, b Row) (any, error) {
		return a.reviewAd(r.Context(), commerceID(r), str(b["decision"]), str(b["review_note"]), i.ID)
	})
	a.commerceAdmin("POST /admin/api/commerce/ads/{id}/resolve", "ad_orders", "resolve", true, func(_ http.ResponseWriter, r *http.Request, i *Identity, b Row) (any, error) {
		return a.resolveAd(r.Context(), commerceID(r), integer(b["message_id"]), i.ID)
	})
}

func (a *App) commerceRecords(r *http.Request, identity *Identity) (Row, error) {
	module := r.PathValue("section")
	if module != "wallets" && module != "wallet_entries" && module != "recharges" && module != "ad_slots" && module != "ad_orders" {
		return nil, cErr(404, "接口不存在。")
	}
	if !identity.Access.Has(module + ".read") {
		return nil, cErr(403, "没有访问此模块的权限。")
	}
	available := map[string][]string{
		"wallets": {"id", "user_id"}, "wallet_entries": {"id", "user_id", "kind"},
		"recharges": {"id", "user_id", "status"}, "ad_slots": {"id", "kind"}, "ad_orders": {"id", "user_id", "status", "kind"},
	}
	where := " WHERE 1=1"
	args := []any{}
	for _, field := range []string{"id", "user_id", "status", "kind"} {
		value := strings.TrimSpace(r.URL.Query().Get(field))
		if value == "" {
			continue
		}
		known := false
		for _, f := range available[module] {
			if f == field {
				known = true
			}
		}
		if !known {
			return nil, cErr(422, "此列表不支持该筛选字段。")
		}
		if field != "id" {
			projected := identity.Access.Project(module, Row{field: value})
			if _, ok := projected[field]; !ok {
				return nil, cErr(403, "无权使用此筛选字段。")
			}
		}
		where += " AND `" + field + "`=?"
		args = append(args, value)
	}
	total, err := cCount(a.DB, "SELECT COUNT(*) FROM ts_"+module+where, args...)
	if err != nil {
		return nil, err
	}
	rows, err := query(a.DB, "SELECT * FROM ts_"+module+where+" ORDER BY id DESC LIMIT 20 OFFSET ?", append(args, (commercePage(r)-1)*20)...)
	if err != nil {
		return nil, err
	}
	items := make([]Row, 0, len(rows))
	for _, row := range rows {
		if module == "ad_orders" {
			job, e := cOne(a.DB, "SELECT state,message_id,error FROM ts_ad_deliveries WHERE order_id=?", row["id"])
			if e != nil {
				return nil, e
			}
			row["message_id"] = job["message_id"]
			row["delivery_state"] = str(job["state"])
			row["delivery_error"] = str(job["error"])
		}
		projected := identity.Access.Project(module, moneyRow(row))
		if module == "recharges" {
			if _, ok := projected["proof"]; ok {
				if err = a.annotateRechargeProof(projected); err != nil {
					return nil, err
				}
			}
		}
		items = append(items, projected)
	}
	return Row{"total": total, "items": items}, nil
}
