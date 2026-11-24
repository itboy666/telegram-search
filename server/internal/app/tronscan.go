package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const usdtTRC20 = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"

func ValidTRONAddress(address string) bool {
	if !regexp.MustCompile(`^T[1-9A-HJ-NP-Za-km-z]{33}$`).MatchString(address) {
		return false
	}
	alphabet := "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	n := new(big.Int)
	for _, char := range address {
		index := strings.IndexRune(alphabet, char)
		if index < 0 {
			return false
		}
		n.Mul(n, big.NewInt(58))
		n.Add(n, big.NewInt(int64(index)))
	}
	raw := n.Bytes()
	if len(raw) != 25 || raw[0] != 0x41 {
		return false
	}
	first := sha256.Sum256(raw[:21])
	second := sha256.Sum256(first[:])
	return subtle.ConstantTimeCompare(raw[21:], second[:4]) == 1
}

type tronscanClient struct {
	key  string
	http *http.Client
	base string
}

func (a *App) newTronscan() (*tronscanClient, error) {
	key, err := a.commerceSecret()
	if err != nil {
		return nil, err
	}
	client := &http.Client{}
	if a.HTTP != nil {
		copy := *a.HTTP
		client = &copy
	}
	client.Timeout = 5 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &tronscanClient{key: key, base: "https://apilist.tronscanapi.com/api/", http: client}, nil
}

func (c *tronscanClient) request(ctx context.Context, path string, params url.Values) (Row, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("TRON-PRO-API-KEY", c.key)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, cErr(503, "暂时无法连接 Tronscan，订单仍保留，请稍后核查。")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		if res.StatusCode == 429 {
			return nil, cErr(503, "Tronscan 请求频繁，请稍后重试。")
		}
		return nil, cErr(503, "暂时无法连接 Tronscan，订单仍保留，请稍后核查。")
	}
	decoder := json.NewDecoder(io.LimitReader(res.Body, 4<<20))
	decoder.UseNumber()
	data := Row{}
	err = decoder.Decode(&data)
	if err != nil || data == nil || data["Error"] != nil || data["error"] != nil {
		return nil, cErr(503, "Tronscan 返回异常，未入账，请稍后重试。")
	}
	return data, nil
}

func (c *tronscanClient) transfers(ctx context.Context, address string, since time.Time, start int) ([]Row, error) {
	data, err := c.request(ctx, "token_trc20/transfers", url.Values{
		"relatedAddress": {address}, "contract_address": {usdtTRC20},
		"start_timestamp": {strconv.FormatInt(since.UnixMilli(), 10)},
		"start":           {strconv.Itoa(start)}, "limit": {"50"}, "sort": {"-timestamp"},
	})
	if err != nil {
		return nil, err
	}
	items, ok := data["token_transfers"].([]any)
	if !ok {
		return nil, cErr(503, "Tronscan 未返回有效转账列表，未入账。")
	}
	rows := make([]Row, 0, len(items))
	for _, item := range items {
		if r, ok := item.(map[string]any); ok {
			rows = append(rows, r)
		}
	}
	return rows, nil
}

func (c *tronscanClient) transaction(ctx context.Context, txid string) (Row, error) {
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(txid) {
		return nil, cErr(422, "交易哈希须为 64 位十六进制字符。")
	}
	return c.request(ctx, "transaction-info", url.Values{"hash": {txid}})
}

// verifyTRC20 is fail-closed: every provider proof must match the immutable order.
func verifyTRC20(tx, order Row, txid string) error {
	confirmed, okConfirmed := tx["confirmed"].(bool)
	revert, okRevert := tx["revert"].(bool)
	if str(tx["hash"]) != txid || str(tx["contractRet"]) != "SUCCESS" ||
		!okConfirmed || !confirmed || !okRevert || revert || integer(tx["confirmations"]) < 19 {
		return cErr(409, "交易尚未成功确认，请稍后核查。")
	}
	when := integer(tx["timestamp"])
	created := cTime(order["created_at"])
	expires := cTime(order["expires_at"])
	if created.IsZero() || expires.IsZero() || when < created.UnixMilli() || when > expires.UnixMilli() {
		return cErr(409, "转账时间不在此订单的付款时段内，请联系管理员核对。")
	}
	matches := 0
	items, _ := tx["trc20TransferInfo"].([]any)
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if str(item["contract_address"]) == usdtTRC20 && str(item["to_address"]) == str(order["receive_address"]) &&
			str(item["type"]) == "Transfer" && str(item["tokenType"]) == "trc20" &&
			integer(item["decimals"]) == 6 && str(item["amount_str"]) == strconv.FormatInt(integer(order["amount"]), 10) {
			matches++
		}
	}
	if matches != 1 {
		return cErr(409, "币种、收款地址或金额与此订单不符，未入账。")
	}
	return nil
}
