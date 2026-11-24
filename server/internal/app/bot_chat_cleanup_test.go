package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestBotCleanupInputPolicy(t *testing.T) {
	for _, command := range []string{"start", "help", "account", "wallet", "recharge", "ads", "cancel", "add", "search"} {
		if !botDisposableCommand(command, "") {
			t.Fatal("temporary command was not eligible", command)
		}
	}
	for _, pair := range [][2]string{{"add", "https://t.me/example"}, {"search", "服务器"}, {"start", "auth_example"}, {"start", "join_example"}, {"login", ""}, {"discover", ""}, {"", "ordinary message"}} {
		if botDisposableCommand(pair[0], pair[1]) {
			t.Fatal("durable message was marked disposable", pair[0])
		}
	}
	for _, kind := range []string{"group", "supergroup", "channel"} {
		ctx := botPrivateTurn(context.Background(), 123, Row{"chat": Row{"id": -456, "type": kind}, "message_id": 1}, Row{"id": 456}, false)
		if _, ok := botCleanInput(ctx).Value(botChatTurnKey{}).(botChatTurn); ok {
			t.Fatal("group message entered private cleanup")
		}
	}
	ctx := botPrivateTurn(context.Background(), 123, Row{"chat": Row{"id": 456, "type": "private"}, "message_id": 1}, Row{"id": 457}, false)
	if _, ok := ctx.Value(botChatTurnKey{}).(botChatTurn); ok {
		t.Fatal("another person's message entered cleanup")
	}
	if botCredentialID(Row{"token": "123:fixture"}) != 123 || botCredentialID(Row{"token": "invalid"}) != 0 {
		t.Fatal("environment credential bot identity was not resolved safely")
	}
}

func botCleanupIDs(f *botCommerceFixture) []int64 {
	ids := []int64{}
	for _, call := range f.calls {
		if str(call["method"]) == "deleteMessage" {
			p := object(call["params"])
			if integer(p["chat_id"]) != f.tgID {
				f.t.Fatal("cleanup escaped the private conversation")
			}
			ids = append(ids, integer(p["message_id"]))
		}
	}
	return ids
}

func TestBotChatCleanupCommandsSurviveRestart(t *testing.T) {
	f := newBotCommerceFixture(t, 0)
	bot, ctx := Row{"id": f.botID}, context.Background()
	if err := f.a.handleUpdate(ctx, Row{"message": f.message(1, "/account")}, bot); err != nil {
		t.Fatal(err)
	}
	first := integer(f.session()["message_id"])
	if err := f.a.handleUpdate(ctx, Row{"message": f.message(2, "/help")}, bot); err != nil {
		t.Fatal(err)
	}
	current, err := one(f.a.DB, "SELECT message_id FROM ts_bot_chat_views WHERE bot_id=? AND chat_id=? AND view_key='panel'", f.botID, f.tgID)
	if err != nil || integer(current["message_id"]) == first {
		t.Fatal("command did not publish a new panel", err)
	}
	if len(botCleanupIDs(f)) != 0 {
		t.Fatal("cleanup ran before the turn had completed")
	}
	restarted, err := New(f.a.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.DB.Close()
	restarted.TelegramCall = f.transport
	restarted.cleanupBotMessages(ctx, f.botID, f.tgID, 20)
	deleted := botCleanupIDs(f)
	for _, id := range []int64{1, 2, first} {
		if !slices.Contains(deleted, id) {
			t.Fatal("restart lost a scheduled private deletion", id, deleted)
		}
	}
	if len(deleted) != 3 || slices.Contains(deleted, integer(current["message_id"])) {
		t.Fatal("cleanup removed the current screen or unrelated history")
	}
	// Replaying cleanup is idempotent and never scans old Telegram history.
	restarted.cleanupBotMessages(ctx, f.botID, f.tgID, 20)
	if len(botCleanupIDs(f)) != 3 {
		t.Fatal("completed deletions were retried")
	}
}

func TestBotChatCleanupPreservesInputsOnDeliveryFailure(t *testing.T) {
	f := newBotCommerceFixture(t, 0)
	f.open("home")
	first := integer(f.session()["message_id"])
	f.failMethod, f.failCount = "sendMessage", 1
	if err := f.a.handleUpdate(context.Background(), Row{"message": f.message(1, "/help")}, Row{"id": f.botID}); err == nil {
		t.Fatal("delivery failure was not reported")
	}
	f.a.cleanupBotMessages(context.Background(), f.botID, f.tgID, 20)
	if len(botCleanupIDs(f)) != 0 {
		t.Fatal("failed delivery deleted the input or last usable screen")
	}
	current, err := one(f.a.DB, "SELECT message_id FROM ts_bot_chat_views WHERE bot_id=? AND chat_id=? AND view_key='panel'", f.botID, f.tgID)
	if err != nil || integer(current["message_id"]) != first {
		t.Fatal("failed delivery discarded previous navigation", err)
	}
	if err := f.a.handleUpdate(context.Background(), Row{"message": f.message(1, "/help")}, Row{"id": f.botID}); err != nil {
		t.Fatal(err)
	}
	f.a.cleanupBotMessages(context.Background(), f.botID, f.tgID, 20)
	if ids := botCleanupIDs(f); len(ids) != 2 || !slices.Contains(ids, first) || !slices.Contains(ids, 1) {
		t.Fatal("successful retry did not clean up the completed turn", ids)
	}
}

func TestBotChatCleanupRetainsOrderReceipts(t *testing.T) {
	f := newBotCommerceFixture(t, 20_000_000)
	f.draft()
	f.click("buy")
	receipt := f.state("ad_detail")
	receiptID := integer(receipt["message_id"])
	orderID := integer(decode(str(receipt["payload"]))["ad_id"])
	f.a.cleanupBotMessages(context.Background(), f.botID, f.tgID, 50)
	if slices.Contains(botCleanupIDs(f), receiptID) {
		t.Fatal("purchase receipt was deleted")
	}
	before := len(f.calls)
	f.click("home")
	home := f.state("home")
	if integer(home["message_id"]) == receiptID {
		t.Fatal("returning home overwrote the purchase receipt")
	}
	for _, call := range f.calls[before:] {
		if str(call["method"]) == "editMessageText" && integer(object(call["params"])["message_id"]) == receiptID {
			t.Fatal("receipt content was replaced with navigation")
		}
	}
	f.click("adid" + str(orderID))
	if integer(f.state("ad_detail")["message_id"]) != receiptID {
		t.Fatal("reopening the same order created duplicate receipt messages")
	}
	f.a.cleanupBotMessages(context.Background(), f.botID, f.tgID, 50)
	if slices.Contains(botCleanupIDs(f), receiptID) || !slices.Contains(botCleanupIDs(f), integer(home["message_id"])) {
		t.Fatal("receipt was not retained while intermediate navigation was removed")
	}
	f.money("10.000000", 1)
}

func TestBotChatCleanupRetainsSearch(t *testing.T) {
	f := newBotCommerceFixture(t, 0)
	ctx := context.Background()
	f.open("home")
	current := integer(f.session()["message_id"])
	// A search result remains outside the disposable panel registry.
	result, err := f.a.Telegram(ctx, "sendMessage", Row{"chat_id": f.tgID, "text": "搜索结果 fixture"})
	if err != nil {
		t.Fatal(err)
	}
	f.a.retireBotPanel(ctx, f.botID, f.tgID, integer(result["message_id"]))
	f.a.cleanupBotMessages(ctx, f.botID, f.tgID, 20)
	if ids := botCleanupIDs(f); len(ids) != 1 || ids[0] != current {
		t.Fatal("search navigation cleanup removed the result", ids)
	}
	f.open("home")
	f.a.cleanupBotMessages(ctx, f.botID, f.tgID, 20)
	if slices.Contains(botCleanupIDs(f), integer(result["message_id"])) {
		t.Fatal("opening a new menu deleted a retained search")
	}
}

func TestBotChatCleanupRetryAndPermanentFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		retry   bool
	}{
		{"network", errors.New("fixture network failure"), true},
		{"rate_limit", &TelegramError{Code: 429, RetryAfter: 90, Description: "fixture"}, true},
		{"already_deleted", &TelegramError{Code: 400, Description: "message to delete not found"}, false},
		{"permission", &TelegramError{Code: 403, Description: "Forbidden"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBotCommerceFixture(t, 0)
			f.open("home")
			oldID := integer(f.session()["message_id"])
			f.open("home")
			transport := f.a.TelegramCall
			f.a.TelegramCall = func(ctx context.Context, method string, p Row) (Row, error) {
				if method == "deleteMessage" {
					return nil, tc.failure
				}
				return transport(ctx, method, p)
			}
			f.a.cleanupBotMessages(context.Background(), f.botID, f.tgID, 20)
			pending, err := one(f.a.DB, "SELECT * FROM ts_bot_message_cleanup WHERE bot_id=? AND chat_id=? AND message_id=?", f.botID, f.tgID, oldID)
			if err != nil || (pending != nil) != tc.retry {
				t.Fatal("wrong cleanup retry decision", err)
			}
			if tc.retry {
				if integer(pending["attempts"]) != 1 || expired(pending["next_attempt_at"]) {
					t.Fatal("cleanup retry was not deferred")
				}
				if tc.name == "rate_limit" && str(pending["next_attempt_at"]) < future(85*time.Second) {
					t.Fatal("Telegram retry_after was ignored")
				}
				if _, err = f.a.DB.Exec("UPDATE ts_bot_message_cleanup SET next_attempt_at=? WHERE bot_id=? AND chat_id=?", future(-time.Second), f.botID, f.tgID); err != nil {
					t.Fatal(err)
				}
				f.a.TelegramCall = transport
				f.a.cleanupBotMessages(context.Background(), f.botID, f.tgID, 20)
				if !slices.Contains(botCleanupIDs(f), oldID) {
					t.Fatal("deferred cleanup never recovered")
				}
			}
		})
	}
}

func TestBotChatCleanupProtectsActiveAndExpiredMessages(t *testing.T) {
	f := newBotCommerceFixture(t, 0)
	f.open("home")
	id := integer(f.session()["message_id"])
	tx, err := f.a.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []struct {
		bot, chat, message int64
		sent               string
	}{
		{f.botID, f.tgID, id, now()}, // Defensive check for a mistakenly queued current view.
		{f.botID, -f.tgID, 123, now()},
		{f.botID, f.tgID, 124, future(-48 * time.Hour)},
	} {
		if err = queueBotCleanup(tx, candidate.bot, candidate.chat, candidate.message, candidate.sent); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.a.cleanupBotMessages(context.Background(), f.botID, f.tgID, 20)
	if len(botCleanupIDs(f)) != 0 {
		t.Fatal("cleanup deleted a current, group, or over-age message")
	}
	if n, err := cCount(f.a.DB, "SELECT COUNT(*) FROM ts_bot_message_cleanup WHERE bot_id=? AND chat_id=?", f.botID, f.tgID); err != nil || n != 0 {
		t.Fatal("guarded queue entries were not retired", err)
	}
}

func TestBotChatCleanupHelpStopsHiddenFormInput(t *testing.T) {
	f := newBotCommerceFixture(t, 0)
	f.open("ads")
	f.click("slot" + str(f.slotID))
	f.click("write")
	f.state("ad_title")
	if err := f.a.handleUpdate(context.Background(), Row{"message": f.message(20, "/help")}, Row{"id": f.botID}); err != nil {
		t.Fatal(err)
	}
	f.state("home")
	handled, err := f.a.botCommerceText(context.Background(), f.message(21, "服务器"), f.botID)
	if err != nil || handled {
		t.Fatal("deleted form prompt continued consuming ordinary chat", err)
	}
}

func TestBotChatCleanupOnlyConsumesValidFormInput(t *testing.T) {
	f := newBotCommerceFixture(t, 0)
	f.open("ads")
	f.click("slot" + str(f.slotID))
	f.click("write")
	f.text(10, "有效的广告标题")
	s := f.state("ad_url")
	handled, err := f.a.botCommerceText(context.Background(), f.message(11, "不是网址"), f.botID)
	if !handled || err == nil {
		t.Fatal("invalid input was not rejected")
	}
	f.a.cleanupBotMessages(context.Background(), f.botID, f.tgID, 20)
	deleted := botCleanupIDs(f)
	if !slices.Contains(deleted, 10) || slices.Contains(deleted, 11) || slices.Contains(deleted, integer(s["message_id"])) {
		t.Fatal("cleanup did not preserve rejected input and its current prompt", deleted)
	}
}
