package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"
)

func (a *App) CreateRecharge(user int64, input Row) (Row, error) {
	key, err := commerceRequestKey(input["request_key"])
	if err != nil {
		return nil, err
	}
	method := str(input["method"])
	if method != "trc20" && method != "offline" {
		return nil, cErr(422, "请选择充值方式。")
	}
	base, err := ParseMoney(input["amount"], false)
	if err != nil {
		return nil, err
	}
	if base < 1_000_000 || base > 100_000_000_000 || method == "trc20" && base%1_000_000 != 0 {
		return nil, cErr(422, "链上充值请输入 1–100000 的整数金额；线下充值最低 1 USDT。")
	}
	return cTx(a, func(tx *sql.Tx) (Row, error) {
		if _, err := cOne(tx, "SELECT id FROM ts_commerce_config WHERE id=1 FOR UPDATE"); err != nil {
			return nil, err
		}
		if err := activeCommerceIdentity(tx, user, input); err != nil {
			return nil, err
		}
		old, err := cOne(tx, "SELECT * FROM ts_recharges WHERE user_id=? AND request_key=?", user, key)
		if err != nil {
			return nil, err
		}
		if old != nil {
			previous := integer(old["amount"])
			if method == "trc20" {
				previous = previous / 1_000_000 * 1_000_000
			}
			if str(old["method"]) != method || previous != base {
				return nil, cErr(409, "请求编号已用于不同的充值，请刷新后重试。")
			}
			return moneyRow(old), nil
		}
		config, err := commerceConfig(tx)
		if err != nil {
			return nil, err
		}
		if !boolean(config[map[string]string{"trc20": "trc20_enabled", "offline": "offline_enabled"}[method]]) {
			return nil, cErr(409, "此充值方式暂未启用。")
		}
		count, err := cCount(tx, "SELECT COUNT(*) FROM ts_recharges WHERE user_id=? AND status IN ('pending','review') AND expires_at>?", user, now())
		if err != nil {
			return nil, err
		}
		if count >= 3 {
			return nil, cErr(409, "请先完成已有充值订单，最多同时保留三笔。")
		}
		amount := base
		var address any
		expiry := time.Now().Add(24 * time.Hour)
		if method == "trc20" {
			address = str(config["receive_address"])
			expiry = time.Now().Add(30 * time.Minute)
			allocated := false
			for i := 0; i < 100; i++ {
				tail, e := rand.Int(rand.Reader, big.NewInt(999999))
				if e != nil {
					return nil, e
				}
				amount = base + tail.Int64() + 1
				n, e := cCount(tx, "SELECT COUNT(*) FROM ts_recharges WHERE receive_address=? AND amount=?", address, amount)
				if e != nil {
					return nil, e
				}
				if n == 0 {
					allocated = true
					break
				}
			}
			if !allocated {
				return nil, cErr(409, "当前金额的付款订单较多，请更换充值金额。")
			}
		}
		res, err := tx.Exec("INSERT INTO ts_recharges(user_id,request_key,method,amount,receive_address,status,created_at,expires_at) VALUES(?,?,?,?,?,'pending',?,?)", user, key, method, amount, address, now(), cDate(expiry))
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		r, err := cOne(tx, "SELECT * FROM ts_recharges WHERE id=?", id)
		return moneyRow(r), err
	})
}

func rechargeOwned(db Queryer, id, user int64, lock bool) (Row, error) {
	statement := "SELECT * FROM ts_recharges WHERE id=?"
	args := []any{id}
	if user > 0 {
		statement += " AND user_id=?"
		args = append(args, user)
	}
	if lock {
		statement += " FOR UPDATE"
	}
	row, err := cOne(db, statement, args...)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, cErr(404, "充值订单不存在。")
	}
	return row, nil
}

func (a *App) rechargeProof(user, id int64, proof string) (Row, error) {
	proof = strings.TrimSpace(proof)
	if n := utf8.RuneCountInString(proof); n < 5 || n > 500 {
		return nil, cErr(422, "请填写 5–500 字转账凭证说明，包括付款人和转账编号。")
	}
	return cTx(a, func(tx *sql.Tx) (Row, error) {
		row, err := rechargeOwned(tx, id, user, true)
		if err != nil {
			return nil, err
		}
		if err = activeCommerceUser(tx, user); err != nil {
			return nil, err
		}
		if err = validateRechargeProofOrder(row); err != nil {
			return nil, err
		}
		if _, err = tx.Exec("UPDATE ts_recharges SET proof=?,status='review' WHERE id=?", proof, id); err != nil {
			return nil, err
		}
		row, err = rechargeOwned(tx, id, user, false)
		return moneyRow(row), err
	})
}

func (a *App) reviewRecharge(id int64, decision, note string, actor int64) (Row, error) {
	if (decision != "approve" && decision != "reject") || strings.TrimSpace(note) == "" {
		return nil, cErr(422, "请选择审核结果并填写核账说明。")
	}
	return cTx(a, func(tx *sql.Tx) (Row, error) {
		row, err := rechargeOwned(tx, id, 0, true)
		if err != nil {
			return nil, err
		}
		if str(row["method"]) != "offline" || str(row["status"]) != "review" {
			return nil, cErr(409, "仅可审核已提交凭证的线下充值。")
		}
		status := "rejected"
		var paidAt any
		if decision == "approve" {
			if _, err = walletPost(tx, integer(row["user_id"]), integer(row["amount"]), "recharge", cBusinessKey("recharge", id), fmt.Sprintf("线下充值 #%d", id)); err != nil {
				return nil, err
			}
			status = "paid"
			paidAt = now()
		}
		if _, err = tx.Exec("UPDATE ts_recharges SET status=?,review_note=?,reviewer_id=?,paid_at=? WHERE id=?", status, cText(note, 300), actor, paidAt, id); err != nil {
			return nil, err
		}
		if err = cAudit(tx, actor, "审核线下充值", fmt.Sprintf("订单 #%d，%s", id, decision)); err != nil {
			return nil, err
		}
		row, err = rechargeOwned(tx, id, 0, false)
		return moneyRow(row), err
	})
}

func (a *App) CheckRecharge(ctx context.Context, id, user int64, txid string) (Row, error) {
	if user > 0 {
		owner, err := one(a.DB, "SELECT status FROM ts_users WHERE id=?", user)
		if err != nil {
			return nil, err
		}
		if !boolean(owner["status"]) {
			return nil, cErr(403, "当前账号已停用。")
		}
	}
	order, err := rechargeOwned(a.DB, id, user, false)
	if err != nil {
		return nil, err
	}
	if str(order["status"]) == "paid" {
		return moneyRow(order), nil
	}
	if str(order["method"]) != "trc20" {
		return nil, cErr(422, "线下充值请提交凭证后等待审核。")
	}
	if s := str(order["status"]); s != "pending" && s != "expired" {
		return nil, cErr(409, "此订单不能自动入账。")
	}
	client, err := a.newTronscan()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	txid = strings.ToLower(strings.TrimSpace(txid))
	if txid == "" {
		deadline := time.Now().Add(7 * time.Second)
		for start := 0; start < 1000; start += 50 {
			items, e := client.transfers(ctx, str(order["receive_address"]), cTime(order["created_at"]), start)
			if e != nil {
				return nil, e
			}
			for _, item := range items {
				if str(item["to_address"]) == str(order["receive_address"]) && str(item["quant"]) == str(order["amount"]) && str(item["contract_address"]) == usdtTRC20 && integer(item["block_ts"]) >= cTime(order["created_at"]).UnixMilli() {
					txid = str(item["transaction_id"])
					break
				}
			}
			if txid != "" || len(items) < 50 || time.Now().After(deadline) {
				break
			}
		}
		if txid == "" {
			return nil, cErr(409, "尚未找到匹配的转账，请按订单完整金额付款后再核查。")
		}
	}
	proof, err := client.transaction(ctx, txid)
	if err != nil {
		return nil, err
	}
	return a.settleRecharge(id, txid, proof)
}

func (a *App) settleRecharge(id int64, txid string, proof Row) (Row, error) {
	return cTx(a, func(tx *sql.Tx) (Row, error) {
		row, err := rechargeOwned(tx, id, 0, true)
		if err != nil {
			return nil, err
		}
		if str(row["status"]) == "paid" {
			return moneyRow(row), nil
		}
		if str(row["method"]) != "trc20" || (str(row["status"]) != "pending" && str(row["status"]) != "expired") {
			return nil, cErr(409, "此订单不能自动入账。")
		}
		if err = verifyTRC20(proof, row, txid); err != nil {
			return nil, err
		}
		n, err := cCount(tx, "SELECT COUNT(*) FROM ts_recharges WHERE txid=?", txid)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, cErr(409, "该交易已入账，请勿重复提交。")
		}
		if _, err = tx.Exec("UPDATE ts_recharges SET status='paid',txid=?,paid_at=? WHERE id=?", txid, now(), id); err != nil {
			return nil, err
		}
		if _, err = walletPost(tx, integer(row["user_id"]), integer(row["amount"]), "recharge", cBusinessKey("recharge", id), fmt.Sprintf("USDT-TRC20 充值 #%d", id)); err != nil {
			return nil, err
		}
		row, err = rechargeOwned(tx, id, 0, false)
		return moneyRow(row), err
	})
}
