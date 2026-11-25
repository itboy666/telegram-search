package app

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReplyDeletedMessageFallback(t *testing.T) {
	a := &App{}
	calls := []Row{}
	a.TelegramCall = func(_ context.Context, method string, p Row) (Row, error) {
		calls = append(calls, p)
		if len(calls) == 1 {
			return nil, &TelegramError{Code: 400, Description: "Bad Request: message to be replied not found"}
		}
		return Row{"message_id": 5}, nil
	}
	m := Row{"message_id": 42, "message_thread_id": 9}
	p := replyContext(m)
	p["chat_id"] = -123
	p["text"] = "hello"
	out, e := a.sendReply(context.Background(), "sendMessage", p)
	if e != nil || integer(out["message_id"]) != 5 {
		t.Fatal("fallback failed", e)
	}
	if len(calls) != 2 || calls[1]["reply_parameters"] != nil || integer(calls[1]["message_thread_id"]) != 9 {
		t.Fatal("must remove only missing reference and retain topic")
	}
}
func TestReplyDoesNotRetryUnrelatedError(t *testing.T) {
	a := &App{}
	calls := 0
	a.TelegramCall = func(_ context.Context, _ string, _ Row) (Row, error) {
		calls++
		return nil, &TelegramError{Code: 400, Description: "Bad Request: message thread not found"}
	}
	_, e := a.sendReply(context.Background(), "sendMessage", Row{"reply_parameters": Row{"message_id": 1}, "message_thread_id": 2})
	if e == nil || calls != 1 {
		t.Fatal("unrelated error must not be silently rerouted")
	}
}
func TestWelcomeTemplateOnePass(t *testing.T) {
	v := Row{"nickname": "<b>{{date}}</b>", "date": "2026-11-19"}
	got := renderTemplate("你好 {{nickname}} {{date}}", v, 1800)
	if got != "你好 <b>{{date}}</b> 2026-11-19" {
		t.Fatal(got)
	}
	if validateTemplate("{{unknown}}") == nil || validateTemplate("{{nickname}") == nil {
		t.Fatal("invalid tag accepted")
	}
	if got = renderTemplate("{{nickname}}", v, 5); len([]rune(got)) != 5 || !strings.HasSuffix(got, "…") {
		t.Fatal("template bound missing")
	}
}
func TestCompactTelegramTitle(t *testing.T) {
	got := compactTitle(" 中文\n\t 标题 \u202eabc", 80)
	if strings.ContainsAny(got, "\n\t\u202e") {
		t.Fatal("unsafe controls")
	}
	if got = compactTitle(strings.Repeat("长", 80), 80); len([]rune(got)) > 41 || !strings.HasSuffix(got, "…") {
		t.Fatal("CJK title too long")
	}
}
func TestTelegramMetadataIsolation(t *testing.T) {
	tests := []struct {
		body, url, title, kind string
		valid                  bool
	}{
		{`<meta property="og:title" content="公开频道"><div class="tgme_page_title">标题</div><div class="tgme_page_extra">100 subscribers</div>`, "https://t.me/testchannel", "公开频道", "channel", true},
		{`<div class="tgme_page_title">开发群</div><div class="tgme_page_extra">30 members</div>`, "https://t.me/testgroup", "开发群", "group", true},
		{`<div class="tgme_page_title">受邀开发群</div><div class="tgme_page_extra">1 member</div>`, "https://t.me/+Invite_1234", "受邀开发群", "group", true},
		{`<div class="tgme_widget_message" data-post="testchannel/5"><div class="tgme_widget_message_video_player"></div><div class="tgme_widget_message_text">视频标题</div></div>`, "https://t.me/testchannel/5", "视频标题", "video", true},
		{`<div class="tgme_widget_message" data-post="other/5"><div class="tgme_widget_message_text">不属于请求的帖子</div></div>`, "https://t.me/testchannel/5", "", "", false},
		{`<title>Telegram Contact</title>`, "https://t.me/privateuser", "", "", false},
	}
	for _, tc := range tests {
		got, e := parseTelegramMetadata(tc.body, tc.url)
		if !tc.valid {
			if e == nil {
				t.Fatal("unavailable page accepted")
			}
			continue
		}
		if e != nil || str(got["title"]) != tc.title || str(got["type"]) != tc.kind {
			t.Fatalf("parse mismatch %#v %v", got, e)
		}
	}
}
func TestGoIconCatalogParity(t *testing.T) {
	root, _ := filepath.Abs("../../..")
	icons := iconCatalog(root)
	if len(icons) != 400 {
		t.Fatalf("icon catalog length=%d", len(icons))
	}
	v := object(icons["telegram"])
	if str(v["id"]) != "6028346797368283073" {
		t.Fatal("emoji ID precision loss")
	}
	a := &App{Root: root}
	b := a.iconButton("telegram", Row{"text": "Telegram"})
	if !reflect.DeepEqual(b["icon_custom_emoji_id"], "6028346797368283073") {
		t.Fatal("icon missing")
	}
}
