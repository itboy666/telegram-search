package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTelegramSubmitPublicAndPrivateLinks(t *testing.T) {
	f := newBotCommerceFixture(t, 0)
	user := integer(f.user["id"])
	username := "fixture_" + randomHex(8)
	code := "Invite_" + randomHex(10)
	publicLink, inviteLink := "https://t.me/"+username, "https://t.me/+"+code
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = f.a.DB.Exec("DELETE FROM ts_resources WHERE owner_id=? AND url_hash=?", user, hash(inviteLink))
		_, _ = f.a.DB.Exec("DELETE FROM ts_submissions WHERE user_id=?", user)
		_, _ = f.a.DB.Exec("DELETE FROM ts_link_locks WHERE url_hash IN (?,?)", hash(inviteLink), hash(publicLink))
		_, _ = f.a.DB.Exec("DELETE FROM ts_rate_limits WHERE bucket=?", "submission:"+str(user))
	})
	fetches := 0
	f.a.HTTP.Transport = commerceFixtureTransport(func(r *http.Request) (*http.Response, error) {
		fetches++
		if r.Method != "GET" || r.URL.Host != "t.me" {
			t.Fatal("unexpected preview request")
		}
		var body string
		switch r.URL.String() {
		case publicLink:
			body = `<div class="tgme_page_title">公开开发群</div><div class="tgme_page_extra">30 members</div>`
		case inviteLink:
			body = `<title>Telegram</title>`
		default:
			t.Fatal("unexpected preview URL")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	send := func(id int64, command string) Row {
		t.Helper()
		if err := f.a.handleUpdate(ctx, Row{"message": f.message(id, command)}, Row{"id": f.botID}); err != nil {
			t.Fatal(err)
		}
		return object(f.calls[len(f.calls)-1]["params"])
	}
	if receipt := str(send(21, "/add\n@"+strings.ToUpper(username))["text"]); !strings.Contains(receipt, "公开开发群") || !strings.Contains(receipt, "我的提交记录") {
		t.Fatal("public submission receipt missing title or next step", receipt)
	}
	if receipt := str(send(22, "/add "+publicLink)["text"]); !strings.Contains(receipt, "请勿重复提交") {
		t.Fatal("alias failed to deduplicate", receipt)
	}
	params := send(23, "/add https://t.me/joinchat/"+code)
	if receipt := str(params["text"]); !strings.Contains(receipt, "Telegram 私人邀请") || strings.Contains(receipt, code) || params["reply_parameters"] != nil {
		t.Fatal("private receipt missing or exposed invite")
	}
	if receipt := str(send(24, "/add tg://join?invite="+code)["text"]); !strings.Contains(receipt, "请勿重复提交") || fetches != 2 {
		t.Fatal("private aliases failed to deduplicate before contacting Telegram", receipt, fetches)
	}
	pending, err := one(f.a.DB, "SELECT id,status FROM ts_submissions WHERE user_id=? AND url=?", user, inviteLink)
	if err != nil || str(pending["status"]) != "pending" {
		t.Fatal("private invite bypassed review", err)
	}
	if _, err = f.a.catalogReview(Row{"status": "approved"}, integer(pending["id"]), 0); err != nil {
		t.Fatal("administrator could not approve the private invite", err)
	}
	record, err := one(f.a.DB, "SELECT url,status FROM ts_resources WHERE owner_id=? AND url=?", user, inviteLink)
	if err != nil || str(record["status"]) != "published" {
		t.Fatal("approval did not publish the canonical invite URL", err)
	}
}
