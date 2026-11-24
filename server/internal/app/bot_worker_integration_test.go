package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func botWorkerTestApp(t *testing.T) *App {
	t.Helper()
	if os.Getenv("STARINDEX_BOT_TEST") != "1" {
		t.Skip("set STARINDEX_BOT_TEST=1 for isolated bot update fixtures")
	}
	root, _ := filepath.Abs("../../..")
	a, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.DB.Close() })
	return a
}

func botWorkerTestUpdate(t *testing.T, a *App, botID int64) Row {
	t.Helper()
	updateID := time.Now().UnixMicro() + int64(securityRandom(1_000_000))
	t.Cleanup(func() { _, _ = a.DB.Exec("DELETE FROM ts_bot_updates WHERE bot_id=? AND update_id=?", botID, updateID) })
	return Row{"update_id": updateID, "callback_query": Row{"id": "worker-fixture", "data": "unknown"}}
}

func TestBotWorkerMySQLTerminalStatesAndRetry(t *testing.T) {
	a := botWorkerTestApp(t)
	ctx := context.Background()
	bot := Row{"id": int64(9_100_000_000_000) + int64(securityRandom(1_000_000_000))}
	calls := 0
	var transportError error
	a.TelegramCall = func(_ context.Context, _ string, _ Row) (Row, error) {
		calls++
		return Row{"ok": true}, transportError
	}
	for _, state := range []string{"done", "ignored"} {
		u := botWorkerTestUpdate(t, a, integer(bot["id"]))
		_, err := a.DB.Exec("INSERT INTO ts_bot_updates(bot_id,update_id,state,created_at,completed_at) VALUES(?,?,?,?,?)", bot["id"], u["update_id"], state, now(), now())
		if err != nil {
			t.Fatal(err)
		}
		before, _ := one(a.DB, "SELECT * FROM ts_bot_updates WHERE bot_id=? AND update_id=?", bot["id"], u["update_id"])
		if err = a.processBotUpdate(ctx, u, bot); err != nil {
			t.Fatal(err)
		}
		after, _ := one(a.DB, "SELECT * FROM ts_bot_updates WHERE bot_id=? AND update_id=?", bot["id"], u["update_id"])
		if calls != 0 || str(after["state"]) != state || str(after["completed_at"]) != str(before["completed_at"]) {
			t.Fatalf("terminal %s update was replayed or mutated", state)
		}
	}

	u := botWorkerTestUpdate(t, a, integer(bot["id"]))
	transportError = errors.New("network unavailable")
	if err := a.processBotUpdate(ctx, u, bot); !errors.Is(err, transportError) {
		t.Fatal("temporary failure must remain retryable", err)
	}
	r, _ := one(a.DB, "SELECT * FROM ts_bot_updates WHERE bot_id=? AND update_id=?", bot["id"], u["update_id"])
	if str(r["state"]) != "processing" || r["completed_at"] != nil || calls != 1 {
		t.Fatal("failed update must not be completed")
	}
	transportError = nil
	if err := a.processBotUpdate(ctx, u, bot); err != nil {
		t.Fatal(err)
	}
	r, _ = one(a.DB, "SELECT * FROM ts_bot_updates WHERE bot_id=? AND update_id=?", bot["id"], u["update_id"])
	if str(r["state"]) != "done" || r["completed_at"] == nil || calls != 2 {
		t.Fatal("successful retry was not completed")
	}
	if err := a.processBotUpdate(ctx, u, bot); err != nil || calls != 2 {
		t.Fatal("completed retry was replayed", err)
	}
}

func TestBotWorkerMySQLCallbackDomainRefusalCompletes(t *testing.T) {
	a := botWorkerTestApp(t)
	ctx := context.Background()
	cred, err := a.botCredentials()
	if err != nil {
		t.Fatal(err)
	}
	bot := object(cred["bot"])
	botID := integer(bot["id"])
	tg := int64(9_200_000_000_000) + int64(securityRandom(1_000_000_000))
	uid, err := catalogInsert(a.DB, "users", Row{"username": "go_bot_refusal_" + randomHex(8), "display_name": "Bot refusal fixture", "telegram_id": tg, "status": 0, "created_at": now()})
	if err != nil {
		t.Fatal(err)
	}
	challenge, answer := randomHex(16), randomHex(6)
	t.Cleanup(func() {
		_, _ = a.DB.Exec("DELETE FROM ts_bot_verifications WHERE bot_id=? AND challenge_id=?", botID, challenge)
		_, _ = a.DB.Exec("DELETE FROM ts_telegram_profiles WHERE user_id=?", uid)
		_, _ = a.DB.Exec("DELETE FROM ts_users WHERE id=?", uid)
		for _, key := range []string{"guard-answer:" + str(botID) + ":" + str(tg) + ":" + str(tg), "guard-search:" + str(botID) + ":" + str(tg)} {
			_, _ = a.DB.Exec("DELETE FROM ts_rate_limits WHERE bucket=?", key)
		}
	})
	policy := a.securityPolicy(botID, tg)
	original := Row{"chat": Row{"id": tg, "type": "private"}, "from": Row{"id": tg}, "message_id": 122}
	_, err = a.DB.Exec("INSERT INTO ts_bot_verifications(bot_id,chat_id,user_id,challenge_id,method,policy_hash,state,answer_hashes,payload,pending_query,reply_context,message_id,thread_id,issued_at,challenge_until,expires_at) VALUES(?,?,?,?,'button',?,'pending',?,?,'Go',?,123,0,?,?,?)",
		botID, tg, tg, challenge, policy["hash"], encode([]string{hash(answer)}), encode(Row{"choices": Row{answer: "确认"}}), encode(Row{"original_message": original}), time.Now().Unix(), time.Now().Unix()+180, future(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	u := botWorkerTestUpdate(t, a, botID)
	u["callback_query"] = Row{"id": "refusal-fixture", "data": "v:" + challenge + ":" + answer, "from": Row{"id": tg}, "message": Row{"chat": Row{"id": tg}, "message_id": 123}}
	calls := []Row{}
	a.TelegramCall = func(_ context.Context, method string, p Row) (Row, error) {
		calls = append(calls, Row{"method": method, "params": p})
		return Row{"ok": true, "message_id": 123}, nil
	}
	if err = a.processBotUpdate(ctx, u, bot); err != nil {
		t.Fatal("business refusal must complete the update", err)
	}
	row, _ := one(a.DB, "SELECT state,completed_at FROM ts_bot_updates WHERE bot_id=? AND update_id=?", botID, u["update_id"])
	if str(row["state"]) != "done" || row["completed_at"] == nil || len(calls) < 1 {
		t.Fatal("refused callback was not completed")
	}
	last := calls[len(calls)-1]
	params := object(last["params"])
	if str(last["method"]) != "answerCallbackQuery" || str(params["callback_query_id"]) != "refusal-fixture" || !boolean(params["show_alert"]) || !strings.Contains(str(params["text"]), "账号已停用") {
		t.Fatal("disabled account did not receive a business refusal popup")
	}
	n := len(calls)
	if err = a.processBotUpdate(ctx, u, bot); err != nil || len(calls) != n {
		t.Fatal("refused callback was retried", err)
	}
}
