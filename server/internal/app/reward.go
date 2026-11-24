package app

import (
	"database/sql"
	"fmt"
	"strings"
)

// Reward must be invoked only for a first page with natural search results.
func (a *App) Reward(userID int64, keyword string, resultCount int64, source string) (Row, error) {
	if userID < 1 || strings.TrimSpace(keyword) == "" || resultCount < 1 || (source != "web" && source != "bot") {
		return nil, nil
	}
	config, err := commerceConfig(a.DB)
	if err != nil {
		return nil, err
	}
	if !boolean(config["reward_enabled"]) {
		return nil, nil
	}
	return cTx(a, func(tx *sql.Tx) (Row, error) {
		if _, err := cOne(tx, "SELECT id FROM ts_commerce_config WHERE id=1 FOR UPDATE"); err != nil {
			return nil, err
		}
		config, err := commerceConfig(tx)
		if err != nil {
			return nil, err
		}
		if !boolean(config["reward_enabled"]) {
			return nil, nil
		}
		if err = activeCommerceUser(tx, userID); err != nil {
			return nil, err
		}
		day := now()[:10]
		n, err := cCount(tx, "SELECT COUNT(*) FROM ts_reward_claims WHERE user_id=? AND day=?", userID, day)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, nil
		}
		amount, err := ParseMoney(config["reward_amount"], false)
		if err != nil {
			return nil, err
		}
		dailyCap, err := ParseMoney(config["reward_daily_cap"], false)
		if err != nil {
			return nil, err
		}
		userCap, err := ParseMoney(config["reward_user_cap"], false)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec("INSERT IGNORE INTO ts_reward_days(day,amount) VALUES(?,0)", day); err != nil {
			return nil, err
		}
		spent, err := cOne(tx, "SELECT amount FROM ts_reward_days WHERE day=? FOR UPDATE", day)
		if err != nil {
			return nil, err
		}
		lifetime, err := cCount(tx, "SELECT COALESCE(SUM(amount),0) FROM ts_reward_claims WHERE user_id=?", userID)
		if err != nil {
			return nil, err
		}
		if integer(spent["amount"])+amount > dailyCap || lifetime+amount > userCap {
			return nil, nil
		}
		if _, err = tx.Exec("INSERT INTO ts_reward_claims(user_id,day,amount,source,created_at) VALUES(?,?,?,?,?)", userID, day, amount, source, now()); err != nil {
			return nil, err
		}
		if _, err = tx.Exec("UPDATE ts_reward_days SET amount=amount+? WHERE day=?", amount, day); err != nil {
			return nil, err
		}
		row, err := walletPost(tx, userID, amount, "search_reward", fmt.Sprintf("reward:%d:%s", userID, day), "每日首次有效搜索奖励")
		return moneyRow(row), err
	})
}
