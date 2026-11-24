package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type commerceError struct {
	status  int
	message string
}

func (e *commerceError) Error() string      { return e.message }
func cErr(status int, message string) error { return &commerceError{status, message} }

func commerceReply(w http.ResponseWriter, value any, err error) {
	if err == nil {
		JSON(w, value)
		return
	}
	var ce *commerceError
	if errors.As(err, &ce) {
		Fail(w, ce.status, ce.message)
	} else {
		var domain *DomainError
		if errors.As(err, &domain) {
			Error(w, err)
			return
		}
		// SQL, connection strings and provider responses are never exposed.
		Fail(w, 503, "服务暂时不可用，请稍后重试。")
	}
}

func cOne(db Queryer, statement string, args ...any) (Row, error) {
	r, err := one(db, statement, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

func cCount(db Queryer, statement string, args ...any) (int64, error) {
	var n int64
	err := db.QueryRow(statement, args...).Scan(&n)
	return n, err
}

func cTx(a *App, fn func(*sql.Tx) (Row, error)) (Row, error) {
	var last error
	for attempt := 0; attempt < 4; attempt++ {
		tx, err := a.DB.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			return nil, err
		}
		result, err := fn(tx)
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if err == nil {
			return result, nil
		}
		last = err
		if !strings.Contains(err.Error(), "1213") && !strings.Contains(err.Error(), "1205") {
			return nil, err
		}
		time.Sleep(time.Duration(10*(attempt+1)) * time.Millisecond)
	}
	return nil, last
}

func cText(v any, max int) string {
	s := []rune(strings.TrimSpace(str(v)))
	if len(s) > max {
		s = s[:max]
	}
	return string(s)
}

func cTime(v any) time.Time {
	if t, ok := v.(time.Time); ok {
		return t
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339Nano, "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, str(v), loc); err == nil {
			return t
		}
	}
	return time.Time{}
}

func cDate(t time.Time) string {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	return t.In(loc).Format("2006-01-02 15:04:05")
}

func cAudit(tx *sql.Tx, actor int64, action, detail string) error {
	_, err := tx.Exec("INSERT INTO ts_audit_logs(actor_id,action,detail,created_at) VALUES(?,?,?,?)", actor, action, cText(detail, 500), now())
	return err
}

var commerceDefaults = Row{
	"trc20_enabled": false, "receive_address": "", "offline_enabled": false,
	"offline_instructions": "", "reward_enabled": false, "reward_amount": "0.010000",
	"reward_daily_cap": "10.000000", "reward_user_cap": "1.000000",
	"public_url": "http://127.0.0.1:8088",
}

func commerceConfig(db Queryer) (Row, error) {
	r, err := cOne(db, "SELECT config,secret FROM ts_commerce_config WHERE id=1")
	if err != nil {
		return nil, err
	}
	out := Row{}
	for k, v := range commerceDefaults {
		out[k] = v
	}
	if r != nil {
		values := Row{}
		if err := json.Unmarshal([]byte(str(r["config"])), &values); err != nil {
			return nil, err
		}
		for k := range commerceDefaults {
			if v, ok := values[k]; ok {
				out[k] = v
			}
		}
	}
	out["key_configured"] = r != nil && str(r["secret"]) != ""
	return out, nil
}

// CommerceConfig exposes public operational values; the API key is never returned.
func (a *App) CommerceConfig() (Row, error) { return commerceConfig(a.DB) }

func (a *App) saveCommerceConfig(input Row, actor int64) (Row, error) {
	return cTx(a, func(tx *sql.Tx) (Row, error) {
		stored, err := cOne(tx, "SELECT config,secret FROM ts_commerce_config WHERE id=1 FOR UPDATE")
		if err != nil {
			return nil, err
		}
		if stored == nil {
			return nil, cErr(503, "支付配置尚未初始化。")
		}
		values, err := commerceConfig(tx)
		if err != nil {
			return nil, err
		}
		for k := range commerceDefaults {
			if v, ok := input[k]; ok {
				values[k] = v
			}
		}
		for _, k := range []string{"trc20_enabled", "offline_enabled", "reward_enabled"} {
			values[k] = boolean(values[k])
		}
		for _, k := range []string{"reward_amount", "reward_daily_cap", "reward_user_cap"} {
			amount, e := ParseMoney(values[k], false)
			if e != nil {
				return nil, e
			}
			values[k] = FormatMoney(amount)
		}
		address := strings.TrimSpace(str(values["receive_address"]))
		if address != "" && !ValidTRONAddress(address) {
			return nil, cErr(422, "TRC20 收款地址校验失败。")
		}
		values["receive_address"] = address
		instructions := strings.TrimSpace(str(values["offline_instructions"]))
		if utf8.RuneCountInString(instructions) > 2000 {
			return nil, cErr(422, "收款说明最多 2000 字。")
		}
		values["offline_instructions"] = instructions
		publicURL := strings.TrimRight(strings.TrimSpace(str(values["public_url"])), "/")
		u, e := url.Parse(publicURL)
		if e != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
			return nil, cErr(422, "请输入网站根地址，例如 https://example.com。")
		}
		values["public_url"] = publicURL
		secret := str(stored["secret"])
		if key := strings.TrimSpace(str(input["api_key"])); key != "" {
			if !regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`).MatchString(key) {
				return nil, cErr(422, "Tronscan API Key 格式不正确。")
			}
			secret, err = encryptSecret(a.Root, ".commerce.key", key)
			if err != nil {
				return nil, cErr(503, "支付密钥加密失败。")
			}
		}
		if boolean(values["trc20_enabled"]) && (address == "" || secret == "") {
			return nil, cErr(422, "启用链上充值前，请填写收款地址及 API Key。")
		}
		if boolean(values["offline_enabled"]) && instructions == "" {
			return nil, cErr(422, "请先填写线下转账收款说明。")
		}
		delete(values, "key_configured")
		encoded, err := json.Marshal(values)
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec("UPDATE ts_commerce_config SET config=?,secret=?,updated_at=? WHERE id=1", string(encoded), secret, now())
		if err != nil {
			return nil, err
		}
		if err = cAudit(tx, actor, "更新支付与奖励设置", "密钥仅加密保存；配置已更新"); err != nil {
			return nil, err
		}
		values["key_configured"] = secret != ""
		return values, nil
	})
}

func (a *App) commerceSecret() (string, error) {
	r, err := cOne(a.DB, "SELECT secret FROM ts_commerce_config WHERE id=1")
	if err != nil {
		return "", err
	}
	if r == nil || str(r["secret"]) == "" {
		return "", cErr(503, "尚未配置 Tronscan API Key。")
	}
	secret, err := decryptSecret(a.Root, ".commerce.key", str(r["secret"]))
	if err != nil {
		return "", cErr(503, "支付密钥无法解密，请恢复密钥文件或重新保存。")
	}
	return secret, nil
}

func activeCommerceUser(tx *sql.Tx, user int64) error {
	r, err := cOne(tx, "SELECT id,status FROM ts_users WHERE id=? FOR UPDATE", user)
	if err != nil {
		return err
	}
	if r == nil || !boolean(r["status"]) {
		return cErr(403, "当前账号已停用。")
	}
	return nil
}

func activeCommerceIdentity(tx *sql.Tx, user int64, input Row) error {
	if err := activeCommerceUser(tx, user); err != nil {
		return err
	}
	if expected, ok := input["expected_telegram_id"]; ok {
		r, err := one(tx, "SELECT telegram_id FROM ts_users WHERE id=?", user)
		if err != nil {
			return err
		}
		if integer(expected) <= 0 || integer(r["telegram_id"]) != integer(expected) {
			return cErr(403, "Telegram 绑定已变更，请重新打开钱包。")
		}
	}
	return nil
}

// walletPost may only be called within the transaction owning the business order.
func walletPost(tx *sql.Tx, user, amount int64, kind, key, note string) (Row, error) {
	u, err := cOne(tx, "SELECT id FROM ts_users WHERE id=? FOR UPDATE", user)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, cErr(404, "用户不存在。")
	}
	if _, err = tx.Exec("INSERT IGNORE INTO ts_wallets(user_id,balance,updated_at) VALUES(?,0,?)", user, now()); err != nil {
		return nil, err
	}
	wallet, err := cOne(tx, "SELECT id,balance FROM ts_wallets WHERE user_id=? FOR UPDATE", user)
	if err != nil {
		return nil, err
	}
	old, err := cOne(tx, "SELECT * FROM ts_wallet_entries WHERE business_key=?", key)
	if err != nil {
		return nil, err
	}
	if old != nil {
		if integer(old["user_id"]) != user || integer(old["amount"]) != amount || str(old["kind"]) != kind {
			return nil, cErr(409, "资金业务编号冲突，请联系管理员。")
		}
		return old, nil
	}
	balance, err := safeBalance(integer(wallet["balance"]), amount)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec("UPDATE ts_wallets SET balance=?,updated_at=? WHERE id=?", balance, now(), wallet["id"]); err != nil {
		return nil, err
	}
	res, err := tx.Exec("INSERT INTO ts_wallet_entries(user_id,amount,balance_after,kind,business_key,note,created_at) VALUES(?,?,?,?,?,?,?)", user, amount, balance, kind, key, cText(note, 300), now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return cOne(tx, "SELECT * FROM ts_wallet_entries WHERE id=?", id)
}

func (a *App) WalletBalance(user int64) (string, error) {
	row, err := cOne(a.DB, "SELECT balance FROM ts_wallets WHERE user_id=?", user)
	if err != nil {
		return "", err
	}
	return FormatMoney(integer(row["balance"])), nil
}

func commerceRequestKey(input any) (string, error) {
	key, ok := input.(string)
	if !ok || !regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`).MatchString(key) {
		return "", cErr(422, "请求编号无效，请刷新页面。")
	}
	return key, nil
}

// Counters are stored in MySQL so limits remain consistent across processes.
func (a *App) commerceLimit(bucket string, limit int64, period time.Duration) error {
	_, err := cTx(a, func(tx *sql.Tx) (Row, error) {
		if _, e := tx.Exec("INSERT IGNORE INTO ts_rate_limits(bucket,hits,expires_at) VALUES(?,0,?)", bucket, cDate(time.Now().Add(period))); e != nil {
			return nil, e
		}
		row, e := cOne(tx, "SELECT hits,expires_at FROM ts_rate_limits WHERE bucket=? FOR UPDATE", bucket)
		if e != nil {
			return nil, e
		}
		hits := integer(row["hits"])
		expires := str(row["expires_at"])
		if !cTime(expires).After(time.Now()) {
			hits = 0
			expires = cDate(time.Now().Add(period))
		}
		if hits >= limit {
			return nil, cErr(429, "操作过于频繁，请稍后重试。")
		}
		_, e = tx.Exec("UPDATE ts_rate_limits SET hits=?,expires_at=? WHERE bucket=?", hits+1, expires, bucket)
		return Row{}, e
	})
	return err
}

func commercePage(r *http.Request) int64 {
	p := integer(r.URL.Query().Get("page"))
	if p < 1 {
		p = 1
	}
	if p > 100000 {
		p = 100000
	}
	return p
}

func commerceID(r *http.Request) int64 { return integer(r.PathValue("id")) }

func cBusinessKey(prefix string, id int64) string { return fmt.Sprintf("%s:%d", prefix, id) }
