package app

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const moneyMax int64 = 1_000_000_000_000

var moneyPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,6})(?:\.([0-9]{1,6}))?$`)

// ParseMoney accepts decimal text, never binary floating point.
func ParseMoney(value any, allowZero bool) (int64, error) {
	var text string
	switch v := value.(type) {
	case string:
		text = strings.TrimSpace(v)
	case json.Number:
		text = v.String()
	case int:
		text = strconv.Itoa(v)
	case int64:
		text = strconv.FormatInt(v, 10)
	default:
		return 0, cErr(422, "金额必须为数字文本，最多六位小数。")
	}
	m := moneyPattern.FindStringSubmatch(text)
	if m == nil {
		return 0, cErr(422, "金额格式不正确，最多六位小数。")
	}
	whole, _ := strconv.ParseInt(m[1], 10, 64)
	fraction, _ := strconv.ParseInt(m[2]+strings.Repeat("0", 6-len(m[2])), 10, 64)
	amount := whole*1_000_000 + fraction
	if amount > moneyMax || amount == 0 && !allowZero {
		return 0, cErr(422, "金额须大于零且不超过 1,000,000 USDT。")
	}
	return amount, nil
}

func FormatMoney(amount int64) string {
	// Avoid overflowing at MinInt64 when formatting existing ledger data.
	var magnitude uint64
	sign := ""
	if amount < 0 {
		sign = "-"
		magnitude = uint64(-(amount + 1)) + 1
	} else {
		magnitude = uint64(amount)
	}
	return fmt.Sprintf("%s%d.%06d", sign, magnitude/1_000_000, magnitude%1_000_000)
}

func moneyRow(row Row) Row {
	out := Row{}
	for k, v := range row {
		out[k] = v
	}
	if _, ok := out["enabled"]; ok {
		out["enabled"] = boolean(out["enabled"])
	}
	for _, k := range []string{"amount", "balance", "balance_after", "price"} {
		if v, ok := out[k]; ok {
			out[k] = FormatMoney(integer(v))
		}
	}
	return out
}

func safeBalance(balance, amount int64) (int64, error) {
	if amount == 0 || amount > moneyMax || amount < -moneyMax || amount > 0 && balance > math.MaxInt64-amount {
		return 0, cErr(422, "资金金额超出允许范围。")
	}
	if amount < 0 && balance < -amount {
		return 0, cErr(409, "余额不足，请先充值。")
	}
	return balance + amount, nil
}
