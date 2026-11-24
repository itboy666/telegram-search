package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type commerceFixtureTransport func(*http.Request) (*http.Response, error)

func (fn commerceFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type botCommerceFixture struct {
	t           *testing.T
	a           *App
	user        Row
	botID, tgID int64
	slotID      int64
	mu          sync.Mutex
	calls       []Row
	nextMessage int64
	failMethod  string
	failCount   int
}

func newBotCommerceFixture(t *testing.T, credit int64) *botCommerceFixture {
	t.Helper()
	a := commerceTestApp(t)
	userID := commerceTestUser(t, a)
	tg := int64(9_500_000_000_000) + int64(securityRandom(1_000_000_000))
	f := &botCommerceFixture{t: t, a: a, tgID: tg, botID: tg + 1_000_000_000, nextMessage: 1000}
	if _, err := a.DB.Exec("UPDATE ts_users SET telegram_id=? WHERE id=?", tg, userID); err != nil {
		t.Fatal(err)
	}
	var err error
	f.user, err = one(a.DB, "SELECT * FROM ts_users WHERE id=?", userID)
	if err != nil {
		t.Fatal(err)
	}
	f.slotID, err = catalogInsert(a.DB, "ad_slots", Row{
		"name": "Bot 独立广告夹具 " + randomHex(6), "kind": "keyword", "description": "只用于模拟传输测试",
		"price": int64(10_000_000), "duration_days": 7, "capacity": 50,
		"target_chat": "", "enabled": 1, "created_at": now(), "updated_at": now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, table := range []string{"bot_chat_views", "bot_message_cleanup"} {
			_, _ = a.DB.Exec("DELETE FROM ts_"+table+" WHERE bot_id=? AND chat_id=?", f.botID, tg)
		}
		for _, table := range []string{"bot_commerce_sessions", "telegram_profiles", "bot_login_codes"} {
			_, _ = a.DB.Exec("DELETE FROM ts_"+table+" WHERE user_id=?", userID)
		}
		_, _ = a.DB.Exec("DELETE FROM ts_ad_orders WHERE slot_id=?", f.slotID)
		_, _ = a.DB.Exec("DELETE FROM ts_ad_slots WHERE id=?", f.slotID)
		for _, key := range []string{
			"bot:" + str(tg), "bot-callback:" + str(tg),
			"ad-buy:" + str(userID), "recharge-create:" + str(userID), "recharge-check:" + str(userID),
		} {
			_, _ = a.DB.Exec("DELETE FROM ts_rate_limits WHERE bucket=?", key)
		}
	})
	a.TelegramCall = f.transport
	if credit > 0 {
		commerceCredit(t, a, userID, credit)
	}
	return f
}

func (f *botCommerceFixture) transport(_ context.Context, method string, params Row) (Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, Row{"method": method, "params": decode(encode(params))})
	if method == f.failMethod && f.failCount > 0 {
		f.failCount--
		return nil, errors.New("fixture transport unavailable")
	}
	if method == "sendMessage" || method == "sendPhoto" {
		f.nextMessage++
		return Row{"message_id": f.nextMessage}, nil
	}
	return Row{"ok": true, "message_id": params["message_id"]}, nil
}

func (f *botCommerceFixture) from() Row {
	return Row{"id": f.tgID, "first_name": "Bot commerce fixture", "is_bot": false}
}

func (f *botCommerceFixture) message(id int64, text string) Row {
	return Row{"message_id": id, "text": text, "from": f.from(), "chat": Row{"id": f.tgID, "type": "private"}}
}

func (f *botCommerceFixture) session() Row {
	f.t.Helper()
	s, err := one(f.a.DB, "SELECT * FROM ts_bot_commerce_sessions WHERE bot_id=? AND telegram_user_id=?", f.botID, f.tgID)
	if err != nil || s == nil {
		f.t.Fatal("missing commerce session", err)
	}
	return s
}

func (f *botCommerceFixture) state(want string) Row {
	f.t.Helper()
	s := f.session()
	if str(s["state"]) != want {
		f.t.Fatalf("state=%s want=%s", s["state"], want)
	}
	return s
}

func (f *botCommerceFixture) open(view string) {
	f.t.Helper()
	if err := f.a.openBotCommerce(context.Background(), f.tgID, f.user, f.botID, view); err != nil {
		f.t.Fatal(err)
	}
}

func (f *botCommerceFixture) callback(action string) Row {
	f.t.Helper()
	s := f.session()
	return Row{"id": "fixture_" + randomHex(5), "from": f.from(),
		"data":    fmt.Sprintf("c:%s:%d:%s", str(s["id"]), integer(s["revision"]), action),
		"message": Row{"message_id": s["message_id"], "chat": Row{"id": f.tgID, "type": "private"}}}
}

func (f *botCommerceFixture) click(action string) {
	f.t.Helper()
	if err := f.a.botCommerceCallback(context.Background(), f.callback(action), f.botID); err != nil {
		f.t.Fatal(err)
	}
}

func (f *botCommerceFixture) text(id int64, text string) {
	f.t.Helper()
	handled, err := f.a.botCommerceText(context.Background(), f.message(id, text), f.botID)
	if err != nil || !handled {
		f.t.Fatalf("input not consumed: handled=%t err=%v", handled, err)
	}
}

func (f *botCommerceFixture) draft() {
	f.t.Helper()
	f.open("ads")
	f.click("slot" + str(f.slotID))
	f.click("write")
	f.text(10, "独立测试广告")
	f.text(11, "https://example.com/bot-commerce-fixture")
	f.text(12, "广告正文只进入待审订单")
	f.text(13, "bot-fixture-"+str(f.tgID))
	f.state("ad_confirm")
}

func (f *botCommerceFixture) money(wantBalance string, wantOrders int64) {
	f.t.Helper()
	balance, err := f.a.WalletBalance(integer(f.user["id"]))
	if err != nil || balance != wantBalance {
		f.t.Fatalf("balance=%s want=%s err=%v", balance, wantBalance, err)
	}
	n, err := cCount(f.a.DB, "SELECT COUNT(*) FROM ts_ad_orders WHERE user_id=?", f.user["id"])
	if err != nil || n != wantOrders {
		f.t.Fatalf("orders=%d want=%d err=%v", n, wantOrders, err)
	}
	entries, err := cCount(f.a.DB, "SELECT COUNT(*) FROM ts_wallet_entries WHERE user_id=? AND kind='ad_purchase'", f.user["id"])
	if err != nil || entries != wantOrders {
		f.t.Fatalf("purchase ledger entries=%d want=%d err=%v", entries, wantOrders, err)
	}
}

func TestBotCommerceTelegramIdentityAndRemovedLoginEntry(t *testing.T) {
	f := newBotCommerceFixture(t, 20_000_000)
	f.open("home")
	text, buttons, err := f.a.botCommerceView(f.state("home"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "账户 ID：<code>"+str(f.tgID)+"</code>") || strings.Contains(text, "<code>"+str(f.user["id"])+"</code>") {
		t.Fatal("wallet must identify the Telegram user, not the internal database key")
	}
	if strings.Contains(text, "网站") || strings.Contains(encode(buttons), "网站登录") || strings.Contains(encode(buttons), ":website") {
		t.Fatal("wallet retained the website login entry")
	}
	before := len(f.calls)
	f.click("website") // Compatibility with a button in a previously sent wallet.
	f.state("home")
	if len(f.calls) != before+2 || str(f.calls[before]["method"]) != "answerCallbackQuery" || str(f.calls[before+1]["method"]) != "editMessageText" {
		t.Fatal("old website button must refresh the wallet in place")
	}
	if n, err := cCount(f.a.DB, "SELECT COUNT(*) FROM ts_bot_login_codes WHERE user_id=?", f.user["id"]); err != nil || n != 0 {
		t.Fatal("old wallet button issued a website credential", err)
	}
	if strings.Contains(encode(object(f.calls[len(f.calls)-1]["params"])["reply_markup"]), ":website") {
		t.Fatal("refreshed old wallet still exposes the removed button")
	}
	// An explicit private /login still supports the independent website login.
	if err := f.a.handleUpdate(context.Background(), Row{"message": f.message(2, "/login")}, Row{"id": f.botID}); err != nil {
		t.Fatal(err)
	}
	if n, err := cCount(f.a.DB, "SELECT COUNT(*) FROM ts_bot_login_codes WHERE user_id=?", f.user["id"]); err != nil || n != 1 {
		t.Fatal("explicit private website login no longer works", err)
	}
	last := object(f.calls[len(f.calls)-1]["params"])
	if integer(last["chat_id"]) != f.tgID || !strings.Contains(str(last["text"]), "账户 ID：<code>"+str(f.tgID)+"</code>") {
		t.Fatal("website login message used the wrong user identity")
	}
	f.money("20.000000", 0)
}

func TestBotCommerceRouteDraftAndCancel(t *testing.T) {
	f := newBotCommerceFixture(t, 20_000_000)
	ctx := context.Background()
	bot := Row{"id": f.botID, "username": "FixtureBot"}
	if err := f.a.handleUpdate(ctx, Row{"message": f.message(1, "/ads")}, bot); err != nil {
		t.Fatal(err)
	}
	f.state("slots")
	f.click("slot" + str(f.slotID))
	f.click("write")
	title := "route-fixture-" + randomHex(8)
	if err := f.a.handleUpdate(ctx, Row{"message": f.message(2, title)}, bot); err != nil {
		t.Fatal(err)
	}
	s := f.state("ad_url")
	if str(object(decode(str(s["payload"]))["ad"])["title"]) != title {
		t.Fatal("route failed to save title")
	}
	if n, err := cCount(f.a.DB, "SELECT COUNT(*) FROM ts_search_logs WHERE query=?", title); err != nil || n != 0 {
		t.Fatal("advertisement input leaked into search", err)
	}
	if n, err := cCount(f.a.DB, "SELECT COUNT(*) FROM ts_bot_login_codes WHERE user_id=?", f.user["id"]); err != nil || n != 0 {
		t.Fatal("wallet navigation unexpectedly issued a website credential", err)
	}
	old := f.callback("back")
	if err := f.a.handleUpdate(ctx, Row{"message": f.message(3, "/cancel")}, bot); err != nil {
		t.Fatal(err)
	}
	s = f.state("home")
	if len(decode(str(s["payload"]))) != 0 {
		t.Fatal("cancel retained unfinished draft")
	}
	if err := f.a.botCommerceCallback(ctx, old, f.botID); err == nil {
		t.Fatal("old draft button remained usable after cancel")
	}
	f.money("20.000000", 0)
}

func TestBotCommerceConcurrentConfirmationAndReplay(t *testing.T) {
	f := newBotCommerceFixture(t, 30_000_000)
	f.draft()
	cb := f.callback("buy")
	var wg sync.WaitGroup
	start := make(chan struct{})
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// Competing transitions may reject as busy or stale; neither may charge.
			_ = f.a.botCommerceCallback(context.Background(), cb, f.botID)
		}()
	}
	close(start)
	wg.Wait()
	f.state("ad_detail")
	f.money("20.000000", 1)
	if err := f.a.botCommerceCallback(context.Background(), cb, f.botID); err == nil {
		t.Fatal("completed purchase accepted its old confirmation button")
	}
	f.money("20.000000", 1)
}

func TestBotCommerceOwnershipAndExpiredConfirmation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*botCommerceFixture, Row) int64
	}{
		{"another_sender", func(f *botCommerceFixture, cb Row) int64 {
			cb["from"] = Row{"id": f.tgID + 1}
			return f.botID
		}},
		{"another_bot", func(f *botCommerceFixture, _ Row) int64 { return f.botID + 1 }},
		{"another_message", func(f *botCommerceFixture, cb Row) int64 {
			object(cb["message"])["message_id"] = integer(object(cb["message"])["message_id"]) + 1
			return f.botID
		}},
		{"group_chat", func(f *botCommerceFixture, cb Row) int64 {
			object(cb["message"])["chat"] = Row{"id": -f.tgID, "type": "supergroup"}
			return f.botID
		}},
		{"expired", func(f *botCommerceFixture, _ Row) int64 {
			_, err := f.a.DB.Exec("UPDATE ts_bot_commerce_sessions SET expires_at=? WHERE id=?", future(-time.Minute), f.session()["id"])
			if err != nil {
				f.t.Fatal(err)
			}
			return f.botID
		}},
		{"disabled_user", func(f *botCommerceFixture, _ Row) int64 {
			_, err := f.a.DB.Exec("UPDATE ts_users SET status=0 WHERE id=?", f.user["id"])
			if err != nil {
				f.t.Fatal(err)
			}
			return f.botID
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newBotCommerceFixture(t, 20_000_000)
			f.draft()
			cb := f.callback("buy")
			botID := tc.mutate(f, cb)
			if err := f.a.botCommerceCallback(context.Background(), cb, botID); err == nil {
				t.Fatal("invalid financial confirmation was accepted")
			}
			f.money("20.000000", 0)
		})
	}
}

func TestBotCommerceQuoteChangesRequireNewConfirmation(t *testing.T) {
	for _, tc := range []struct {
		field string
		value any
	}{
		{"price", int64(12_000_000)}, {"duration_days", 14},
		{"kind", "search_top"}, {"target_chat", "@fixture_ad_destination"}, {"name", "已修改的广告位名称"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			f := newBotCommerceFixture(t, 30_000_000)
			f.draft()
			if _, err := f.a.DB.Exec("UPDATE ts_ad_slots SET "+tc.field+"=? WHERE id=?", tc.value, f.slotID); err != nil {
				t.Fatal(err)
			}
			f.click("buy")
			f.state("ad_confirm")
			f.money("30.000000", 0)
			f.click("requote")
			f.state("ad_confirm")
			f.click("buy")
			f.state("ad_detail")
			balance := "20.000000"
			if tc.field == "price" {
				balance = "18.000000"
			}
			f.money(balance, 1)
		})
	}
}

func TestBotCommerceInsufficientBalanceAndCapacity(t *testing.T) {
	t.Run("balance_then_resume", func(t *testing.T) {
		f := newBotCommerceFixture(t, 0)
		f.draft()
		f.click("buy")
		f.state("ad_confirm")
		f.money("0.000000", 0)
		commerceCredit(t, f.a, integer(f.user["id"]), 10_000_000)
		f.click("buy")
		f.state("ad_detail")
		f.money("0.000000", 1)
	})
	t.Run("capacity_rechecked", func(t *testing.T) {
		f := newBotCommerceFixture(t, 20_000_000)
		f.draft()
		if _, err := f.a.DB.Exec("UPDATE ts_ad_slots SET capacity=0 WHERE id=?", f.slotID); err != nil {
			t.Fatal(err)
		}
		f.click("buy")
		f.state("ad_confirm")
		f.money("20.000000", 0)
	})
}

func TestBotCommerceCommittedPurchaseSurvivesTransportFailureAndRestart(t *testing.T) {
	f := newBotCommerceFixture(t, 20_000_000)
	f.draft()
	cb := f.callback("buy")
	f.mu.Lock()
	f.failMethod, f.failCount = "editMessageText", 1
	f.mu.Unlock()
	if err := f.a.botCommerceCallback(context.Background(), cb, f.botID); err == nil {
		t.Fatal("fixture failed to interrupt result delivery")
	}
	f.state("ad_detail")
	f.money("10.000000", 1)
	// A new App has no in-memory conversation data, as after a process restart.
	restarted, err := New(f.a.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.DB.Close()
	restarted.TelegramCall = f.transport
	if err = restarted.openBotCommerce(context.Background(), f.tgID, f.user, f.botID, "resume"); err != nil {
		t.Fatal(err)
	}
	f.state("ad_detail")
	if err = restarted.botCommerceCallback(context.Background(), cb, f.botID); err == nil {
		t.Fatal("old purchase button was revived by restart")
	}
	f.money("10.000000", 1)
}

func TestBotCommerceConsumedTextReplayNeverBecomesSearch(t *testing.T) {
	f := newBotCommerceFixture(t, 0)
	f.open("ads")
	f.click("slot" + str(f.slotID))
	f.click("write")
	f.text(10, "重复文本测试")
	f.text(11, "https://example.com/fixture-replay")
	f.text(12, "完整正文")
	f.mu.Lock()
	f.failMethod, f.failCount = "sendMessage", 1
	f.mu.Unlock()
	m := f.message(13, "private-ad-keyword-"+randomHex(5))
	if handled, err := f.a.botCommerceText(context.Background(), m, f.botID); !handled || err == nil {
		t.Fatal("fixture did not interrupt delivery after saving input", err)
	}
	f.state("ad_confirm")
	if handled, err := f.a.botCommerceText(context.Background(), m, f.botID); !handled || err != nil {
		t.Fatal("replayed financial text would fall through into search", handled, err)
	}
	f.state("ad_confirm")
	f.money("0.000000", 0)
}

func TestBotCommerceOfflineProofAndOrderPrivacy(t *testing.T) {
	f := newBotCommerceFixture(t, 0)
	other := commerceTestUser(t, f.a)
	makeOrder := func(user int64) int64 {
		t.Helper()
		id, err := catalogInsert(f.a.DB, "recharges", Row{
			"user_id": user, "request_key": randomHex(16), "method": "offline", "amount": 5_000_000,
			"status": "pending", "created_at": now(), "expires_at": future(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	own, foreign := makeOrder(integer(f.user["id"])), makeOrder(other)
	f.open("home")
	f.click("rechargeid" + str(foreign))
	f.state("home")
	f.click("rechargeid" + str(own))
	f.state("recharge_detail")
	f.click("proof")
	f.text(10, "测试付款人：Fixture；转账编号：offline_fixture_12345")
	f.state("recharge_detail")
	order, err := rechargeOwned(f.a.DB, own, integer(f.user["id"]), false)
	if err != nil || str(order["status"]) != "review" || !strings.Contains(str(order["proof"]), "offline_fixture_12345") {
		t.Fatal("bot proof did not enter the shared review workflow", err)
	}
	untouched, err := rechargeOwned(f.a.DB, foreign, other, false)
	if err != nil || str(untouched["status"]) != "pending" || str(untouched["proof"]) != "" {
		t.Fatal("another user's recharge was changed", err)
	}
	balance, err := f.a.WalletBalance(integer(f.user["id"]))
	if err != nil || balance != "0.000000" {
		t.Fatal("unreviewed proof credited a wallet", err)
	}
	receiptID := integer(f.session()["message_id"])
	f.click("home")
	f.a.cleanupBotMessages(context.Background(), f.botID, f.tgID, 30)
	if deleted := botCleanupIDs(f); slices.Contains(deleted, 10) || slices.Contains(deleted, receiptID) {
		t.Fatal("payment evidence or recharge receipt was removed", deleted)
	}
}

func TestBotCommerceExpiredInputAndRecovery(t *testing.T) {
	f := newBotCommerceFixture(t, 0)
	f.open("ads")
	f.click("slot" + str(f.slotID))
	f.click("write")
	old := f.session()["id"]
	if _, err := f.a.DB.Exec("UPDATE ts_bot_commerce_sessions SET expires_at=? WHERE id=?", future(-time.Minute), old); err != nil {
		t.Fatal(err)
	}
	if handled, err := f.a.botCommerceText(context.Background(), f.message(10, "超时后不应搜索此文案"), f.botID); !handled || err == nil {
		t.Fatal("expired checkout input was not explicitly refused", handled, err)
	}
	f.open("resume")
	s := f.state("home")
	if str(s["id"]) == str(old) || len(decode(str(s["payload"]))) != 0 {
		t.Fatal("expired unfinished conversation was revived")
	}
}

func TestBotCommerceRechargeCreationAndRecovery(t *testing.T) {
	f := newBotCommerceFixture(t, 0)
	// Only run the database suite while the operational workers are stopped.
	// Restore the exact configuration after exercising both payment methods.
	config, err := one(f.a.DB, "SELECT config FROM ts_commerce_config WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.a.DB.Exec("UPDATE ts_commerce_config SET config=? WHERE id=1", config["config"]); err != nil {
			t.Error("restore commerce configuration", err)
		}
	})
	if _, err = f.a.DB.Exec("UPDATE ts_commerce_config SET config=JSON_SET(config,'$.offline_enabled',true,'$.trc20_enabled',true,'$.receive_address',?) WHERE id=1", "TJRabPrwbZy45sbavfcjinPJC18kjpRTv8"); err != nil {
		t.Fatal(err)
	}
	for n, method := range []string{"offline", "trc"} {
		f.open("recharge")
		f.click(method)
		f.state("recharge_amount")
		f.text(int64(100+n), "10")
		f.state("recharge_confirm")
		cb := f.callback("create")
		if err = f.a.botCommerceCallback(context.Background(), cb, f.botID); err != nil {
			t.Fatal(err)
		}
		s := f.state("recharge_detail")
		order, err := rechargeOwned(f.a.DB, integer(decode(str(s["payload"]))["recharge_id"]), integer(f.user["id"]), false)
		if err != nil {
			t.Fatal(err)
		}
		if str(order["status"]) != "pending" || str(order["request_key"]) == "" {
			t.Fatal("unexpected recharge state")
		}
		if method == "trc" && (integer(order["amount"]) <= 10_000_000 || integer(order["amount"]) >= 11_000_000) {
			t.Fatal("TRC20 payable amount was not allocated accurately")
		}
		if err = f.a.botCommerceCallback(context.Background(), cb, f.botID); err == nil {
			t.Fatal("old recharge confirmation accepted")
		}
		count, err := cCount(f.a.DB, "SELECT COUNT(*) FROM ts_recharges WHERE user_id=?", f.user["id"])
		if err != nil || count != int64(n+1) {
			t.Fatal("duplicate recharge created", count, err)
		}
	}
	balance, err := f.a.WalletBalance(integer(f.user["id"]))
	if err != nil || balance != "0.000000" {
		t.Fatal("creating recharge credited a wallet", err)
	}
	f.click("txid")
	if handled, err := f.a.botCommerceText(context.Background(), f.message(200, "not-a-chain-hash"), f.botID); !handled || err == nil {
		t.Fatal("invalid TXID accepted")
	}
	f.open("cancel")
	f.state("home")
}

func TestBotCommerceEscapingCallbackSizeAndPause(t *testing.T) {
	f := newBotCommerceFixture(t, 0)
	f.open("ads")
	f.click("slot" + str(f.slotID))
	f.click("write")
	oldReply := f.message(5, "reply to a previous step")
	oldReply["reply_to_message"] = Row{"message_id": 999}
	if handled, err := f.a.botCommerceText(context.Background(), oldReply, f.botID); !handled || err == nil {
		t.Fatal("reply to an old step was accepted")
	}
	f.state("ad_title")
	f.text(10, `<script>test</script> & title`)
	f.text(11, "https://example.com/?a=1&b=2")
	f.text(12, `<b>untrusted text</b>`)
	f.text(13, "<&>")
	s := f.state("ad_confirm")
	text, rows, err := f.a.botCommerceView(s)
	if err != nil || strings.Contains(text, "<script>") || !strings.Contains(text, "&lt;b&gt;") {
		t.Fatal("unescaped user markup in financial preview", err)
	}
	for _, row := range rows {
		for _, button := range row {
			if data := str(button["callback_data"]); len(data) > 64 || !strings.HasPrefix(data, "c:") {
				t.Fatal("invalid callback payload length/namespace")
			}
		}
	}
	if err = f.a.pauseBotCommerce(context.Background(), f.botID, f.tgID); err != nil {
		t.Fatal(err)
	}
	f.state("home")
	handled, err := f.a.botCommerceText(context.Background(), f.message(20, "服务器"), f.botID)
	if err != nil || handled {
		t.Fatal("leaving checkout still consumed ordinary search", err)
	}
}

func TestBotCommerceTRC20CheckUsesVerifiedProof(t *testing.T) {
	f := newBotCommerceFixture(t, 0)
	original, err := one(f.a.DB, "SELECT secret FROM ts_commerce_config WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	f.a.Root = t.TempDir()
	encrypted, err := encryptSecret(f.a.Root, ".commerce.key", "fixture-tronscan-key")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.a.DB.Exec("UPDATE ts_commerce_config SET secret=? WHERE id=1", original["secret"]); err != nil {
			t.Error("restore payment secret", err)
		}
	})
	if _, err = f.a.DB.Exec("UPDATE ts_commerce_config SET secret=? WHERE id=1", encrypted); err != nil {
		t.Fatal(err)
	}
	proof, order, _ := commerceProofFixture()
	txid := randomHex(32)
	proof["hash"] = txid
	order["amount"] = 50_000_000 + int64(securityRandom(999999))
	object(array(proof["trc20TransferInfo"])[0])["amount_str"] = str(order["amount"])
	id, err := catalogInsert(f.a.DB, "recharges", catalogMerge(order, Row{"user_id": f.user["id"], "request_key": randomHex(16), "method": "trc20", "status": "pending"}))
	if err != nil {
		t.Fatal(err)
	}
	accept, requests := false, 0
	f.a.HTTP = &http.Client{Transport: commerceFixtureTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.Host != "apilist.tronscanapi.com" || req.Header.Get("TRON-PRO-API-KEY") != "fixture-tronscan-key" {
			t.Fatal("unexpected external request")
		}
		result := catalogMerge(proof, Row{})
		if !accept {
			result["confirmed"] = false
		}
		if strings.Contains(req.URL.Path, "/transfers") {
			result = Row{"token_transfers": []any{Row{"transaction_id": txid, "to_address": order["receive_address"], "quant": str(order["amount"]), "contract_address": usdtTRC20, "block_ts": proof["timestamp"]}}}
		}
		encoded, _ := json.Marshal(result)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(encoded))), Header: http.Header{}}, nil
	})}
	f.open("home")
	f.click("rechargeid" + str(id))
	f.click("check")
	stored, _ := rechargeOwned(f.a.DB, id, integer(f.user["id"]), false)
	balance, _ := f.a.WalletBalance(integer(f.user["id"]))
	if str(stored["status"]) != "pending" || balance != "0.000000" {
		t.Fatal("unconfirmed chain proof credited the balance")
	}
	accept = true
	f.click("txid")
	f.text(100, txid)
	f.state("recharge_detail")
	stored, _ = rechargeOwned(f.a.DB, id, integer(f.user["id"]), false)
	balance, _ = f.a.WalletBalance(integer(f.user["id"]))
	if str(stored["status"]) != "paid" || balance != FormatMoney(integer(order["amount"])) || requests != 3 {
		t.Fatal("verified chain proof did not settle exactly once", requests)
	}
	f.a.cleanupBotMessages(context.Background(), f.botID, f.tgID, 30)
	if slices.Contains(botCleanupIDs(f), 100) {
		t.Fatal("user-supplied transaction hash was deleted")
	}
	f.click("refresh")
	if _, err = f.a.CheckRecharge(context.Background(), id, integer(f.user["id"]), txid); err != nil || requests != 3 {
		t.Fatal("settled order triggered another chain request", err)
	}
}
