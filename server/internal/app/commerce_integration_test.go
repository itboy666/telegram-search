package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func commerceTestApp(t *testing.T) *App {
	t.Helper()
	if os.Getenv("STARINDEX_DB_TEST") != "1" {
		t.Skip("set STARINDEX_DB_TEST=1 to run isolated MySQL fixtures")
	}
	root := os.Getenv("STARINDEX_ROOT")
	if root == "" {
		_, file, _, _ := runtime.Caller(0)
		root = filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	}
	a, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.DB.Close() })
	return a
}

func commerceTestUser(t *testing.T, a *App) int64 {
	t.Helper()
	name := "gocommercetest_" + randomHex(8)
	res, err := a.DB.Exec("INSERT INTO ts_users(username,password_hash,display_name,status,created_at) VALUES(?,'','Go 财务测试',1,?)", name, now())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	t.Cleanup(func() {
		// Exact IDs only; no production records are changed.
		_, _ = a.DB.Exec("DELETE d FROM ts_ad_deliveries d JOIN ts_ad_orders o ON o.id=d.order_id WHERE o.user_id=?", id)
		for _, table := range []string{"wallet_entries", "wallets", "reward_claims", "ad_orders", "recharges", "sessions"} {
			_, _ = a.DB.Exec("DELETE FROM ts_"+table+" WHERE user_id=?", id)
		}
		_, _ = a.DB.Exec("DELETE FROM ts_users WHERE id=?", id)
	})
	return id
}

func commerceCredit(t *testing.T, a *App, user, amount int64) {
	t.Helper()
	_, err := cTx(a, func(tx *sql.Tx) (Row, error) {
		return walletPost(tx, user, amount, "test_credit", fmt.Sprintf("go-test:%d:%s", user, randomHex(4)), "仅测试夹具，测试后删除")
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCommerceDatabaseConcurrentWallet(t *testing.T) {
	a := commerceTestApp(t)
	user := commerceTestUser(t, a)
	commerceCredit(t, a, user, 100_000_000)
	var succeeded atomic.Int64
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := cTx(a, func(tx *sql.Tx) (Row, error) {
				return walletPost(tx, user, -20_000_000, "test_debit", fmt.Sprintf("go-test-debit:%d:%d", user, n), "并发测试")
			})
			if err == nil {
				succeeded.Add(1)
			}
		}(n)
	}
	wg.Wait()
	if succeeded.Load() != 5 {
		t.Fatalf("expected 5 successful debits, got %d", succeeded.Load())
	}
	balance, err := a.WalletBalance(user)
	if err != nil || balance != "0.000000" {
		t.Fatalf("wallet=%s err=%v", balance, err)
	}
	sum, err := cCount(a.DB, "SELECT COALESCE(SUM(amount),0) FROM ts_wallet_entries WHERE user_id=?", user)
	if err != nil || sum != 0 {
		t.Fatalf("ledger mismatch: %d, %v", sum, err)
	}
}

func TestCommerceDatabaseConcurrentRechargeReview(t *testing.T) {
	a := commerceTestApp(t)
	user := commerceTestUser(t, a)
	res, err := a.DB.Exec("INSERT INTO ts_recharges(user_id,request_key,method,amount,status,proof,created_at,expires_at) VALUES(?,?,'offline',10000000,'review','测试凭证',?,?)", user, randomHex(16), now(), cDate(time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	var succeeded atomic.Int64
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := a.reviewRecharge(id, "approve", "Go 自动测试", 0)
			if err == nil {
				succeeded.Add(1)
			}
		}()
	}
	wg.Wait()
	t.Cleanup(func() {
		_, _ = a.DB.Exec("DELETE FROM ts_audit_logs WHERE actor_id=0 AND action='审核线下充值' AND detail=?", fmt.Sprintf("订单 #%d，approve", id))
	})
	if succeeded.Load() != 1 {
		t.Fatalf("expected one settlement, got %d", succeeded.Load())
	}
	balance, _ := a.WalletBalance(user)
	if balance != "10.000000" {
		t.Fatalf("balance=%s", balance)
	}
	n, err := cCount(a.DB, "SELECT COUNT(*) FROM ts_wallet_entries WHERE business_key=?", cBusinessKey("recharge", id))
	if err != nil || n != 1 {
		t.Fatalf("ledger count=%d err=%v", n, err)
	}
}

func TestCommerceDatabaseConcurrentAdPurchase(t *testing.T) {
	a := commerceTestApp(t)
	user := commerceTestUser(t, a)
	commerceCredit(t, a, user, 100_000_000)
	res, err := a.DB.Exec("INSERT INTO ts_ad_slots(name,kind,description,price,duration_days,capacity,target_chat,enabled,created_at,updated_at) VALUES('Go 并发测试','keyword','',20000000,7,50,'',1,?,?)", now(), now())
	if err != nil {
		t.Fatal(err)
	}
	slot, _ := res.LastInsertId()
	t.Cleanup(func() {
		_, _ = a.DB.Exec("DELETE FROM ts_ad_orders WHERE slot_id=?", slot)
		_, _ = a.DB.Exec("DELETE FROM ts_ad_slots WHERE id=?", slot)
	})
	var success atomic.Int64
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := a.BuyAd(user, Row{"slot_id": slot, "request_key": randomHex(16), "title": "Go 测试广告", "url": "https://example.com", "keyword": "go-test"})
			if err == nil {
				success.Add(1)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 5 {
		t.Fatalf("expected 5 purchases, got %d", success.Load())
	}
	n, err := cCount(a.DB, "SELECT COUNT(*) FROM ts_ad_orders WHERE user_id=?", user)
	if err != nil || n != 5 {
		t.Fatalf("orphan order count=%d err=%v", n, err)
	}
	balance, _ := a.WalletBalance(user)
	if balance != "0.000000" {
		t.Fatalf("balance=%s", balance)
	}
	sum, _ := cCount(a.DB, "SELECT SUM(amount) FROM ts_wallet_entries WHERE user_id=?", user)
	if sum != 0 {
		t.Fatal("ledger/balance mismatch")
	}
}

func TestCommerceDatabaseTRC20SettlementIdempotency(t *testing.T) {
	a := commerceTestApp(t)
	user := commerceTestUser(t, a)
	proof, order, _ := commerceProofFixture()
	order["created_at"] = cDate(time.Now().Add(-time.Hour))
	order["expires_at"] = cDate(time.Now().Add(-30 * time.Minute))
	proof["timestamp"] = time.Now().Add(-45 * time.Minute).UnixMilli()
	txid := randomHex(32)
	proof["hash"] = txid
	// The fixture is an expired order whose transfer was sent inside its payment window.
	res, err := a.DB.Exec("INSERT INTO ts_recharges(user_id,request_key,method,amount,receive_address,status,created_at,expires_at) VALUES(?,?,'trc20',?,?, 'expired',?,?)", user, randomHex(16), order["amount"], order["receive_address"], order["created_at"], order["expires_at"])
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	first, err := a.settleRecharge(id, txid, proof)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.settleRecharge(id, txid, proof)
	if err != nil {
		t.Fatal(err)
	}
	if str(first["status"]) != "paid" || str(second["status"]) != "paid" {
		t.Fatal("settlement failed")
	}
	balance, _ := a.WalletBalance(user)
	if balance != "50.123456" {
		t.Fatalf("balance=%s", balance)
	}
	n, _ := cCount(a.DB, "SELECT COUNT(*) FROM ts_wallet_entries WHERE business_key=?", cBusinessKey("recharge", id))
	if n != 1 {
		t.Fatal("double credit")
	}
}

func TestCommerceDatabaseHTTPAuthorization(t *testing.T) {
	a := commerceTestApp(t)
	user := commerceTestUser(t, a)
	other := commerceTestUser(t, a)
	token, csrf := randomHex(32), randomHex(24)
	_, err := cTx(a, func(tx *sql.Tx) (Row, error) { return Row{}, insertSession(tx, "user", user, token, csrf) })
	if err != nil {
		t.Fatal(err)
	}
	order, err := a.DB.Exec("INSERT INTO ts_recharges(user_id,request_key,method,amount,status,created_at,expires_at) VALUES(?,?,'offline',1000000,'pending',?,?)", other, randomHex(16), now(), cDate(time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	orderID, _ := order.LastInsertId()
	a.RegisterCommerce()
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	request := func(method, path, cookie, csrfToken string, body Row) (int, Row) {
		var encoded []byte
		if body != nil {
			encoded, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "starindex_user", Value: cookie})
		}
		if csrfToken != "" {
			req.Header.Set("X-CSRF-Token", csrfToken)
		}
		resp, e := server.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		value := Row{}
		if e = json.NewDecoder(resp.Body).Decode(&value); e != nil {
			t.Fatal(e)
		}
		return resp.StatusCode, value
	}
	if status, _ := request("GET", "/user/api/commerce/summary", "", "", nil); status != 401 {
		t.Fatalf("unauthenticated=%d", status)
	}
	if status, result := request("GET", "/user/api/commerce/summary", token, "", nil); status != 200 || str(object(result["data"])["balance"]) != "0.000000" {
		t.Fatalf("summary=%d %v", status, result)
	}
	if status, _ := request("POST", "/user/api/commerce/ads", token, "", Row{}); status != 403 {
		t.Fatalf("missing csrf=%d", status)
	}
	if status, _ := request("POST", fmt.Sprintf("/user/api/commerce/recharges/%d/proof", orderID), token, csrf, Row{"proof": "合法长度的测试说明"}); status != 404 {
		t.Fatalf("other user order access=%d", status)
	}
	if status, _ := request("GET", "/user/api/commerce/records/unknown", token, "", nil); status != 404 {
		t.Fatalf("unknown module=%d", status)
	}
	if status, result := request("GET", "/user/api/commerce/records/recharges", token, "", nil); status != 200 || integer(object(result["data"])["total"]) != 0 {
		t.Fatalf("cross-user listing=%d %v", status, result)
	}
	if status, _ := request("GET", "/admin/api/commerce/settings", token, "", nil); status != 401 {
		t.Fatalf("user cookie impersonated admin=%d", status)
	}
}

func TestCommerceDatabaseConfigurationCompatibility(t *testing.T) {
	a := commerceTestApp(t)
	config, err := a.CommerceConfig()
	if err != nil {
		t.Fatal("cannot read migrated commerce configuration")
	}
	if boolean(config["trc20_enabled"]) && !ValidTRONAddress(str(config["receive_address"])) {
		t.Fatal("configured receiving address failed checksum")
	}
	if boolean(config["key_configured"]) {
		secret, err := a.commerceSecret()
		if err != nil || len(secret) < 16 {
			t.Fatal("cannot decrypt existing PHP AES-GCM commerce key")
		}
	}
	for _, key := range []string{"reward_amount", "reward_daily_cap", "reward_user_cap"} {
		if _, err = ParseMoney(config[key], false); err != nil {
			t.Fatalf("invalid configured monetary value %s", key)
		}
	}
	if _, exposed := config["api_key"]; exposed {
		t.Fatal("plaintext API key exposed in configuration")
	}
	if _, exposed := config["secret"]; exposed {
		t.Fatal("encrypted API key exposed in configuration")
	}
}
