package app

import (
	"context"
	"strings"
	"testing"
)

func TestBotSubmissionHistoryPrivacyAndPagination(t *testing.T) {
	f := newBotCommerceFixture(t, 0)
	owner := integer(f.user["id"])
	other := commerceTestUser(t, f.a)
	ctx := context.Background()
	view := func() (string, string) {
		t.Helper()
		message, buttons, err := f.a.botCommerceView(f.state("submissions"))
		if err != nil {
			t.Fatal(err)
		}
		return message, encode(buttons)
	}
	add := func(user int64, title, url, status, note string) {
		t.Helper()
		id, err := catalogInsert(f.a.DB, "submissions", Row{
			"user_id": user, "title": title, "url": url, "url_hash": hash(url),
			"type": "group", "description": "", "status": status, "review_note": note, "created_at": now(),
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = f.a.DB.Exec("DELETE FROM ts_submissions WHERE id=?", id) })
	}

	f.open("home")
	_, buttons, err := f.a.botCommerceView(f.state("home"))
	if err != nil || !strings.Contains(encode(buttons), "我的提交记录") {
		t.Fatal("submission history entry is missing from the account menu", err)
	}
	f.click("submissions1")
	message, keyboard := view()
	if !strings.Contains(message, "还没有提交记录") || !strings.Contains(message, "/add 链接") || !strings.Contains(keyboard, "返回钱包") || strings.Contains(keyboard, "下一页") {
		t.Fatal("empty submission history is missing guidance or back navigation")
	}
	for _, title := range []string{"oldest-only", "record-two", "record-three", "record-four"} {
		add(owner, title, "https://t.me/history_"+randomHex(6), "pending", "")
	}
	code := "SecretFixture_" + randomHex(7)
	invite := "https://t.me/+" + code
	add(owner, "Private <group> "+invite, invite, "rejected", "<invalid> https://t.me/joinchat/"+code)
	add(owner, "approved-fixture", "https://t.me/history_"+randomHex(6), "approved", "")
	add(other, "another-users-record", "https://t.me/history_"+randomHex(6), "pending", "")

	f.click("submissions1")
	message, keyboard = view()
	for _, expected := range []string{"待审核", "已通过", "已拒绝", "拒绝原因", "&lt;group&gt;", "&lt;invalid&gt;", "第 1 / 2 页 · 共 6 条"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("first page missing %q", expected)
		}
	}
	for _, prohibited := range []string{code, "another-users-record", "oldest-only"} {
		if strings.Contains(message, prohibited) {
			t.Fatalf("first page unexpectedly contains %q", prohibited)
		}
	}
	if !strings.Contains(keyboard, "下一页") || strings.Contains(keyboard, "上一页") {
		t.Fatal("incorrect controls on first page")
	}
	foreign := f.callback("submissions2")
	foreign["from"] = Row{"id": f.tgID + 1}
	if f.a.botCommerceCallback(ctx, foreign, f.botID) == nil {
		t.Fatal("another Telegram user opened the submission history")
	}

	f.click("submissions2")
	message, keyboard = view()
	if !strings.Contains(message, "oldest-only") || !strings.Contains(message, "第 2 / 2 页") || !strings.Contains(keyboard, "上一页") || strings.Contains(keyboard, "下一页") {
		t.Fatal("incorrect contents or controls on second page")
	}
	f.click("home")
	f.state("home")
}
