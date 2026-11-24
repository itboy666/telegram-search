package app

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSecurityPuzzles(t *testing.T) {
	for _, mode := range []string{"emoji", "button", "text", "math", "sequence", "random"} {
		t.Run(mode, func(t *testing.T) {
			for n := 0; n < 12; n++ {
				p, err := makeSecurityPuzzle(mode)
				if err != nil {
					t.Fatal(err)
				}
				if p.Prompt == "" || len(p.Answers) == 0 {
					t.Fatal("empty challenge")
				}
				if p.Method == "text" {
					if len(p.Answers[0]) != 6 {
						t.Fatal("code length")
					}
					digit, letter := false, false
					for _, r := range p.Answers[0] {
						if r >= '2' && r <= '9' {
							digit = true
						}
						if r >= 'A' && r <= 'Z' {
							letter = true
						}
					}
					if !digit || !letter {
						t.Fatal("code must contain letters and digits")
					}
					image, err := png.Decode(bytes.NewReader(p.Photo))
					if err != nil || image.Bounds().Dx() != 300 || image.Bounds().Dy() != 96 {
						t.Fatal("invalid PNG")
					}
				}
				if p.Method == "emoji" || p.Method == "sequence" || p.Method == "button" {
					keys := map[string]bool{}
					for _, c := range p.Choices {
						if len(c.Key) != 12 || keys[c.Key] {
							t.Fatal("invalid or duplicate callback token")
						}
						keys[c.Key] = true
					}
					for _, answer := range p.Answers {
						if !keys[answer] {
							t.Fatal("answer not among choices")
						}
					}
					if p.Method == "sequence" && len(p.Answers) != 3 {
						t.Fatal("sequence length")
					}
				}
			}
		})
	}
	if _, err := makeSecurityPuzzle("unknown"); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestSecurityAdRules(t *testing.T) {
	group := Row{"ad_mode": "strict", "ad_keywords": "推广\n买粉", "allow_domains": "example.com"}
	tests := []struct {
		name    string
		message Row
		want    string
	}{
		{"plain", Row{"text": "Go 系统架构"}, ""},
		{"keyword", Row{"text": "来看看推\u200b广内容"}, "关键词"},
		{"link", Row{"text": "https://evil.test/path"}, "链接"},
		{"allowed", Row{"text": "https://api.example.com/path"}, ""},
		{"host_suffix_attack", Row{"text": "https://example.com.evil.test/"}, "链接"},
		{"userinfo_attack", Row{"text": "https://example.com@evil.test/"}, "链接"},
		{"hidden", Row{"text": "点这里", "entities": []any{Row{"type": "text_link", "url": "https://evil.test"}}}, "链接"},
		{"utf16_entity", Row{"text": "😀 evil.test", "entities": []any{Row{"type": "url", "offset": 3, "length": 9}}}, "链接"},
		{"caption", Row{"caption": "t.me/advert"}, "链接"},
		{"allow_trailing_dot", Row{"text": "https://example.com./path"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := securityAdReason(tc.message, group); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestSecurityGroupValidation(t *testing.T) {
	got, err := validateGroupSettings(Row{"verify_mode": "sequence", "ad_mode": "strict", "flood_limit": "6", "allow_domains": "Example.com\nexample.com", "ad_keywords": "买粉\n推广"})
	if err != nil || str(got["allow_domains"]) != "example.com" {
		t.Fatalf("valid settings rejected: %v", err)
	}
	for _, bad := range []Row{
		{"verify_mode": "invalid"}, {"ad_mode": "all"}, {"flood_limit": 3}, {"flood_limit": "4.1"},
		{"allow_domains": "https://example.com"}, {"allow_domains": "example.com/path"}, {"ad_keywords": "a"},
		{"unknown": "value"}, {"blocked_count": 0},
	} {
		if _, err := validateGroupSettings(bad); err == nil {
			t.Fatalf("invalid settings accepted: %v", bad)
		}
	}
}

func securityTestChallenge(t *testing.T, a *App, method string, answers []string) (Row, int64, int64, int64) {
	t.Helper()
	bot := int64(910_000_000 + securityRandom(1_000_000))
	chat := -int64(810_000_000 + securityRandom(1_000_000))
	user := int64(710_000_000 + securityRandom(1_000_000))
	challenge := randomHex(16)
	hashes := []string{}
	choices := Row{}
	for _, answer := range answers {
		hashes = append(hashes, hash(answer))
		choices[answer] = "选项"
	}
	if method == "emoji" || method == "sequence" {
		choices["cccccccccccc"] = "错误"
	}
	p := a.securityPolicy(bot, chat)
	res, err := a.DB.Exec("INSERT INTO ts_bot_verifications(bot_id,chat_id,user_id,challenge_id,method,policy_hash,state,answer_hashes,payload,pending_query,reply_context,message_id,thread_id,issued_at,challenge_until,expires_at) VALUES(?,?,?,?,?,?,'pending',?,?,'Go','{}',123,7,?,?,?)", bot, chat, user, challenge, method, p["hash"], encode(hashes), encode(Row{"choices": choices}), time.Now().Unix(), time.Now().Unix()+180, cDate(time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	t.Cleanup(func() {
		_, _ = a.DB.Exec("DELETE FROM ts_bot_verifications WHERE id=? AND challenge_id=?", id, challenge)
		for _, key := range []string{fmt.Sprintf("guard-answer:%d:%d:%d", bot, chat, user), fmt.Sprintf("guard-answer-notice:%d:%d:%d", bot, chat, user)} {
			_, _ = a.DB.Exec("DELETE FROM ts_rate_limits WHERE bucket=?", key)
		}
	})
	r, err := cOne(a.DB, "SELECT * FROM ts_bot_verifications WHERE id=?", id)
	if err != nil {
		t.Fatal(err)
	}
	return r, bot, chat, user
}

func TestSecurityDatabaseOwnershipAndReplay(t *testing.T) {
	a := commerceTestApp(t)
	challenge, bot, chat, user := securityTestChallenge(t, a, "button", []string{"aaaaaaaaaaaa"})
	for _, tc := range []struct{ user, chat, message, thread int64 }{{user + 1, chat, 123, 7}, {user, chat - 1, 123, 7}, {user, chat, 124, 7}, {user, chat, 123, 8}} {
		result, err := a.verifySecurity(bot, str(challenge["challenge_id"]), tc.user, tc.chat, tc.message, tc.thread, "aaaaaaaaaaaa", true)
		if err != nil || str(result["status"]) != "error" {
			t.Fatalf("ownership bypass: %v %v", result, err)
		}
	}
	var wins atomic.Int64
	var wg sync.WaitGroup
	for n := 0; n < 6; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := a.verifySecurity(bot, str(challenge["challenge_id"]), user, chat, 123, 7, "aaaaaaaaaaaa", true)
			if err == nil && str(result["status"]) == "passed" {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("challenge resumed %d times", wins.Load())
	}
}

func TestSecurityDatabaseSequenceAndCooldown(t *testing.T) {
	a := commerceTestApp(t)
	r, bot, chat, user := securityTestChallenge(t, a, "sequence", []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb"})
	answer := func(value string) Row {
		result, err := a.verifySecurity(bot, str(r["challenge_id"]), user, chat, 123, 7, value, true)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if str(answer("aaaaaaaaaaaa")["status"]) != "progress" {
		t.Fatal("first step rejected")
	}
	if str(answer("cccccccccccc")["status"]) != "wrong" {
		t.Fatal("wrong step accepted")
	}
	stored, _ := cOne(a.DB, "SELECT step FROM ts_bot_verifications WHERE id=?", r["id"])
	if integer(stored["step"]) != 0 {
		t.Fatal("sequence not reset")
	}
	attempts := integer(a.securitySettings()["max_attempts"])
	for n := int64(1); n < attempts; n++ {
		answer("cccccccccccc")
	}
	stored, _ = cOne(a.DB, "SELECT state,blocked_until FROM ts_bot_verifications WHERE id=?", r["id"])
	if str(stored["state"]) != "failed" || integer(stored["blocked_until"]) <= time.Now().Unix() {
		t.Fatal("cooldown not enforced")
	}
}

func TestSecurityDatabaseExpiryAndStalePolicy(t *testing.T) {
	a := commerceTestApp(t)
	for _, kind := range []string{"expiry", "policy"} {
		r, bot, chat, user := securityTestChallenge(t, a, "button", []string{"aaaaaaaaaaaa"})
		if kind == "expiry" {
			_, _ = a.DB.Exec("UPDATE ts_bot_verifications SET challenge_until=? WHERE id=?", time.Now().Unix()-1, r["id"])
		} else {
			_, _ = a.DB.Exec("UPDATE ts_bot_verifications SET policy_hash=? WHERE id=?", strings.Repeat("f", 64), r["id"])
		}
		result, err := a.verifySecurity(bot, str(r["challenge_id"]), user, chat, 123, 7, "aaaaaaaaaaaa", true)
		if err != nil || str(result["status"]) != "error" {
			t.Fatal("expired policy accepted")
		}
		stored, _ := cOne(a.DB, "SELECT state FROM ts_bot_verifications WHERE id=?", r["id"])
		if str(stored["state"]) != "expired" {
			t.Fatal("expiry not stored")
		}
	}
}

func TestSecurityDatabaseModeratorDoesNotDeleteAdmins(t *testing.T) {
	a := commerceTestApp(t)
	bot := int64(910_000_000 + securityRandom(1_000_000))
	chat := -int64(810_000_000 + securityRandom(1_000_000))
	group, err := a.rememberSecurityGroup(bot, Row{"id": chat, "title": "Go 安全测试"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.DB.Exec("DELETE FROM ts_bot_groups WHERE id=?", group["id"]) })
	_, err = a.DB.Exec("UPDATE ts_bot_groups SET ad_mode='links' WHERE id=?", group["id"])
	if err != nil {
		t.Fatal(err)
	}
	deletes := 0
	a.TelegramCall = func(ctx context.Context, method string, params Row) (Row, error) {
		if method == "getChatMember" {
			return Row{"status": "administrator"}, nil
		}
		if method == "deleteMessage" {
			deletes++
		}
		return Row{}, nil
	}
	m := Row{"chat": Row{"id": chat, "type": "supergroup"}, "from": Row{"id": int64(55)}, "message_id": 5, "text": "https://evil.test"}
	blocked, err := a.Moderate(context.Background(), m, bot)
	if err != nil || blocked || deletes != 0 {
		t.Fatal("deleted administrator message")
	}
	a.TelegramCall = func(ctx context.Context, method string, params Row) (Row, error) {
		if method == "getChatMember" {
			return nil, fmt.Errorf("network failure")
		}
		deletes++
		return Row{}, nil
	}
	blocked, err = a.Moderate(context.Background(), m, bot)
	if err != nil || !blocked || deletes != 0 {
		t.Fatal("uncertain admin lookup must suppress echo without deleting")
	}
	a.TelegramCall = func(ctx context.Context, method string, params Row) (Row, error) {
		if method == "getChatMember" {
			return Row{"status": "member"}, nil
		}
		if method == "deleteMessage" {
			deletes++
		}
		return Row{}, nil
	}
	blocked, err = a.Moderate(context.Background(), m, bot)
	if err != nil || !blocked || deletes != 1 {
		t.Fatal("ordinary advertisement not removed")
	}
}
