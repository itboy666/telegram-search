package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBotMySQLSearchOwnershipAndRevision(t *testing.T) {
	if os.Getenv("STARINDEX_BOT_TEST") != "1" {
		t.Skip("set STARINDEX_BOT_TEST=1")
	}
	root, _ := filepath.Abs("../../..")
	a, e := New(root)
	if e != nil {
		t.Fatal(e)
	}
	defer a.DB.Close()
	tag := "gobot_" + randomHex(5)
	tg := time.Now().UnixMilli()
	chat := tg
	session := randomHex(12)
	ctx := context.Background()
	calls := []Row{}
	a.TelegramCall = func(_ context.Context, method string, p Row) (Row, error) {
		calls = append(calls, Row{"method": method, "params": p})
		return Row{"message_id": 99}, nil
	}
	uid, e := catalogInsert(a.DB, "users", Row{"username": tag, "display_name": tag, "telegram_id": tg, "status": 1, "created_at": now()})
	if e != nil {
		t.Fatal(e)
	}
	ids := []int64{}
	defer func() {
		snapshot, err := one(a.DB, "SELECT search_token FROM ts_bot_sessions WHERE id=?", session)
		if err != nil {
			t.Error("read test snapshot for cleanup", err)
		} else if token := str(snapshot["search_token"]); token != "" {
			if _, err = a.DB.Exec("DELETE FROM ts_search_snapshots WHERE token_hash=?", hash(token)); err != nil {
				t.Error("snapshot cleanup", err)
			}
		}
		a.DB.Exec("DELETE FROM ts_bot_sessions WHERE id=?", session)
		a.DB.Exec("DELETE FROM ts_bot_verifications WHERE user_id=?", tg)
		a.DB.Exec("DELETE FROM ts_telegram_profiles WHERE user_id=?", uid)
		a.DB.Exec("DELETE FROM ts_users WHERE id=?", uid)
		a.DB.Exec("DELETE FROM ts_rate_limits WHERE bucket LIKE ?", "%"+str(tg)+"%")
		a.DB.Exec("DELETE FROM ts_search_logs WHERE query=?", tag)
		for _, id := range ids {
			a.DB.Exec("DELETE FROM ts_resources WHERE id=?", id)
		}
	}()
	for i := 0; i < 3; i++ {
		link := "https://t.me/" + tag + str(i)
		id, e := catalogInsert(a.DB, "resources", Row{"title": tag + str(i), "description": "", "url": link, "url_hash": hash(link), "type": "channel", "status": "published", "created_at": now(), "updated_at": now()})
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, id)
	}
	_, e = a.DB.Exec("INSERT INTO ts_bot_sessions(id,telegram_user_id,chat_id,message_id,query,type,page,revision,page_size,filters_expanded,expires_at) VALUES(?,?,?,?,?,'',1,1,2,0,?)", session, tg, chat, 99, tag, future(time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	creds, e := a.botCredentials()
	if e != nil {
		t.Fatal(e)
	}
	botID := integer(object(creds["bot"])["id"])
	policy := a.securityPolicy(botID, chat)
	_, e = a.DB.Exec("INSERT INTO ts_bot_verifications(bot_id,chat_id,user_id,policy_hash,state,answer_hashes,payload,pending_query,reply_context,verified_until,expires_at) VALUES(?,?,?,?,'passed','[]','{}','','{}',?,?)", botID, chat, tg, policy["hash"], time.Now().Unix()+3600, future(time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	cb := Row{"id": "callback_fixture", "data": "s:" + session + ":1:p2", "from": Row{"id": tg + 1}, "message": Row{"chat": Row{"id": chat, "type": "private"}, "message_id": 99}}
	if e = a.botCallback(ctx, cb, botID); e != nil {
		t.Fatal(e)
	}
	last := object(calls[len(calls)-1]["params"])
	if !boolean(last["show_alert"]) || !strings.Contains(str(last["text"]), "不能操作别人的") {
		t.Fatal("ownership alert missing")
	}
	if len(calls) != 1 {
		t.Fatal("unauthorized action edited message")
	}
	cb["from"] = Row{"id": tg}
	if e = a.botCallback(ctx, cb, botID); e != nil {
		t.Fatal(e)
	}
	s, _ := one(a.DB, "SELECT * FROM ts_bot_sessions WHERE id=?", session)
	if integer(s["page"]) != 2 || integer(s["revision"]) != 2 {
		t.Fatal("page or revision not advanced")
	}
	n := len(calls)
	if e = a.botCallback(ctx, cb, botID); e != nil {
		t.Fatal(e)
	}
	if len(calls) != n+1 || !strings.Contains(str(object(calls[len(calls)-1]["params"])["text"]), "已经更新") {
		t.Fatal("stale callback accepted")
	}
	cb["data"] = "s:" + session + ":2:f1"
	if e = a.botCallback(ctx, cb, botID); e != nil {
		t.Fatal(e)
	}
	s, _ = one(a.DB, "SELECT * FROM ts_bot_sessions WHERE id=?", session)
	if integer(s["page"]) != 2 || integer(s["filters_expanded"]) != 1 {
		t.Fatal("filter expansion lost page")
	}
}
