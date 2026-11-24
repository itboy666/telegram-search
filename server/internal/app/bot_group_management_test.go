package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"image/png"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"
)

func TestGroupManagementSettingsAndTemplates(t *testing.T) {
	for _, input := range []Row{
		{"message_max_chars": 4097}, {"message_max_chars": -1}, {"message_max_chars": "1.1"},
		{"join_timeout_seconds": 29}, {"join_timeout_seconds": 3601}, {"join_max_attempts": 0},
		{"join_verify_mode": "inherit"}, {"join_failure_action": "ban_forever"},
		{"welcome_enabled": 2}, {"block_media": -1}, {"block_forwards": "yes"},
		{"welcome_template": "{{unknown}}"}, {"welcome_template": "{{nickname}"}, {"welcome_template": strings.Repeat("长", 2001)},
	} {
		if _, err := validateGroupSettings(input); err == nil {
			t.Fatalf("invalid group setting accepted: %#v", input)
		}
	}
	if _, err := validateGroupSettings(Row{"message_max_chars": 1, "join_timeout_seconds": 30, "join_max_attempts": 10, "join_verify_mode": "random", "join_failure_action": "mute", "welcome_enabled": true, "block_media": false, "block_forwards": 1, "welcome_template": "{{mention}} {{datetime}}"}); err != nil {
		t.Fatal(err)
	}
	user := Row{"id": 42, "first_name": "<b>{{date}}</b>", "username": "test&name"}
	got := renderGroupWelcome("欢迎 {{mention}} {{nickname}} {{username}} {{chat_title}} <script>", user, Row{"id": -99, "title": "<群>"}, Row{"username": "demo_bot"})
	if !strings.Contains(got, `<a href="tg://user?id=42">`) || strings.Contains(got, "<script>") || strings.Contains(got, "<b>") || !strings.Contains(got, "&lt;群&gt;") || !strings.Contains(got, "{{date}}") {
		t.Fatal("welcome expansion did not escape values or expanded recursively")
	}
	if err := validateGroupWelcomeTemplate(strings.Join(func() []string {
		values := []string{}
		for _, key := range groupWelcomeTags {
			values = append(values, "{{"+key+"}}")
		}
		return values
	}(), " ")); err != nil {
		t.Fatal(err)
	}
	expanded := renderGroupWelcome(strings.Repeat("{{mention}}{{chat_title}}", 80), Row{"id": 42, "first_name": strings.Repeat("😀", 64)}, Row{"title": strings.Repeat("群", 255)}, Row{})
	visible := html.UnescapeString(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(expanded, ""))
	if len(utf16.Encode([]rune(visible))) > 3000 || strings.Count(expanded, "<a ") != strings.Count(expanded, "</a>") || !strings.HasSuffix(expanded, "…") {
		t.Fatal("expanded welcome exceeded safe visible length or broke mention markup")
	}
}

func TestGroupManagementUnicodeAndMediaRules(t *testing.T) {
	group := Row{"message_max_chars": 3, "ad_mode": "off"}
	if groupMessageReason(Row{"text": "中😀a"}, group) != "" {
		t.Fatal("character limit counted UTF-8 bytes")
	}
	if got := groupMessageReason(Row{"caption": "中😀ab"}, group); !strings.Contains(got, "4 字 / 上限 3 字") {
		t.Fatal("caption length was not counted as Unicode runes", got)
	}
	for _, media := range []string{"photo", "video", "document", "audio", "voice", "sticker", "animation", "video_note", "contact", "location", "venue", "poll", "dice", "game"} {
		if groupMessageReason(Row{media: Row{}}, Row{"block_media": 1}) != "媒体消息" {
			t.Fatal("media not blocked:", media)
		}
	}
	if groupMessageReason(Row{"forward_origin": Row{"type": "user"}}, Row{"block_forwards": 1}) != "转发消息" {
		t.Fatal("forward not blocked")
	}
	if got := groupMessageReason(Row{"text": "1234", "photo": []Row{{}}, "forward_origin": Row{}}, Row{"message_max_chars": 3, "block_media": 1, "block_forwards": 1}); !strings.Contains(got, "超长") {
		t.Fatal("length rule must have priority")
	}
}

type groupManagementFixture struct {
	*botGroupFixture
	member       Row
	defaults     Row
	seq          int64
	deleteFails  bool
	restrictFail bool
	selfRestrict bool
	photo        []byte
}

func newGroupManagementFixture(t *testing.T) *groupManagementFixture {
	t.Helper()
	base := newBotGroupFixture(t, "supergroup", 0)
	// A synthetic bot ID also isolates expiration scans from real operating groups.
	base.bot = Row{"id": int64(9_400_000_000_000) + int64(securityRandom(1_000_000_000)), "username": "group_fixture_bot"}
	f := &groupManagementFixture{botGroupFixture: base, member: Row{"status": "member"}, defaults: Row{"can_send_messages": true, "can_send_photos": true, "can_send_audios": false, "can_send_documents": false}, selfRestrict: true, seq: time.Now().UnixMicro()}
	base.a.TelegramCall = func(_ context.Context, method string, p Row) (Row, error) {
		base.calls = append(base.calls, Row{"method": method, "params": decode(encode(p))})
		switch method {
		case "getChatMember":
			if integer(p["user_id"]) == integer(base.bot["id"]) {
				return Row{"status": "administrator", "can_delete_messages": true, "can_restrict_members": f.selfRestrict}, nil
			}
			return catalogMerge(f.member, Row{"user": base.from}), nil
		case "getChat":
			return catalogMerge(base.chat, Row{"permissions": f.defaults}), nil
		case "restrictChatMember":
			if f.restrictFail {
				return nil, errors.New("fixture restriction transport failure")
			}
			f.member = catalogMerge(groupPermissions(object(p["permissions"])), Row{"status": "restricted", "is_member": true, "until_date": p["until_date"]})
			return Row{"ok": true}, nil
		case "deleteMessage":
			if f.deleteFails {
				return nil, &TelegramError{Code: 400, Description: "not enough rights"}
			}
			return Row{"ok": true}, nil
		case "banChatMember":
			f.member = Row{"status": "kicked", "until_date": p["until_date"]}
			return Row{"ok": true}, nil
		case "sendMessage", "sendPhoto":
			if p["photo"] != nil {
				f.photo, _ = p["photo"].([]byte)
			}
			base.nextMessage++
			return Row{"message_id": base.nextMessage}, nil
		default:
			return Row{"ok": true}, nil
		}
	}
	base.join()
	t.Cleanup(func() {
		_, _ = base.a.DB.Exec("DELETE FROM ts_bot_join_challenges WHERE bot_id=? AND chat_id=?", base.bot["id"], base.chat["id"])
		for _, key := range []string{"join-start:" + str(base.bot["id"]) + ":" + str(base.from["id"]), "join-answer:" + str(base.bot["id"]) + ":" + str(base.from["id"])} {
			_, _ = base.a.DB.Exec("DELETE FROM ts_rate_limits WHERE bucket=?", key)
		}
	})
	return f
}
func (f *groupManagementFixture) event(actor int64, old, member Row) {
	f.t.Helper()
	f.seq++
	member = catalogMerge(member, Row{"user": f.from})
	if err := f.a.GroupMemberEvent(context.Background(), Row{"chat": f.chat, "from": Row{"id": actor}, "date": time.Now().Unix(), "_update_id": f.seq, "old_chat_member": old, "new_chat_member": member}, integer(f.bot["id"])); err != nil {
		f.t.Fatal(err)
	}
}
func (f *groupManagementFixture) begin(mode string) Row {
	f.t.Helper()
	if _, err := f.a.DB.Exec("UPDATE ts_bot_groups SET join_verify_mode=?,welcome_enabled=1,join_max_attempts=2 WHERE bot_id=? AND chat_id=?", mode, f.bot["id"], f.chat["id"]); err != nil {
		f.t.Fatal(err)
	}
	f.event(integer(f.from["id"])+100, Row{"status": "left"}, Row{"status": "member"})
	return f.row()
}
func (f *groupManagementFixture) row() Row {
	f.t.Helper()
	r, err := one(f.a.DB, "SELECT * FROM ts_bot_join_challenges WHERE bot_id=? AND chat_id=? AND user_id=?", f.bot["id"], f.chat["id"], f.from["id"])
	if err != nil || r == nil {
		f.t.Fatal("missing join challenge", err)
	}
	return r
}
func (f *groupManagementFixture) confirmRestriction() {
	f.event(integer(f.bot["id"]), Row{"status": "member"}, f.member)
	if !boolean(f.row()["restriction_confirmed"]) {
		f.t.Fatal("own restriction event not recorded")
	}
}
func (f *groupManagementFixture) start() Row {
	f.t.Helper()
	m := Row{"chat": Row{"id": f.from["id"], "type": "private"}, "from": f.from}
	if err := f.a.JoinVerificationStart(context.Background(), m, integer(f.bot["id"]), str(f.row()["challenge_id"])); err != nil {
		f.t.Fatal(err)
	}
	return f.row()
}
func (f *groupManagementFixture) answerKey(index int) string {
	f.t.Helper()
	r := f.row()
	var hashes []string
	_ = json.Unmarshal([]byte(str(r["answer_hashes"])), &hashes)
	for key := range object(decode(str(r["payload"]))["choices"]) {
		if hash(key) == hashes[index] {
			return key
		}
	}
	f.t.Fatal("no matching choice")
	return ""
}
func (f *groupManagementFixture) callback(key string, user int64) {
	f.t.Helper()
	r := f.row()
	cb := Row{"id": "join-test", "data": "j:" + str(r["challenge_id"]) + ":" + key, "from": Row{"id": user}, "message": Row{"chat": Row{"id": user}, "message_id": r["private_message_id"]}}
	if err := f.a.JoinVerificationCallback(context.Background(), cb, integer(f.bot["id"])); err != nil {
		f.t.Fatal(err)
	}
}
func (f *groupManagementFixture) methodCount(method string) int {
	n := 0
	for _, call := range f.calls {
		if str(call["method"]) == method {
			n++
		}
	}
	return n
}

func TestGroupManagementMySQLJoinPrivateVerificationAndDedupe(t *testing.T) {
	for _, mode := range []string{"button", "emoji", "sequence"} {
		t.Run(mode, func(t *testing.T) {
			f := newGroupManagementFixture(t)
			row := f.begin(mode)
			if str(row["state"]) != "pending" || f.methodCount("restrictChatMember") != 1 || f.methodCount("sendMessage") != 1 || !groupHasMute(f.member) {
				t.Fatal("new member was not muted and welcomed exactly once")
			}
			f.seq++
			handled, err := f.a.GroupServiceMessage(context.Background(), Row{"chat": f.chat, "date": time.Now().Unix(), "_update_id": f.seq, "new_chat_members": []Row{f.from}}, integer(f.bot["id"]))
			if err != nil || !handled || f.methodCount("restrictChatMember") != 1 || f.methodCount("sendMessage") != 1 {
				t.Fatal("duplicate service event reissued welcome or restriction", err)
			}
			f.confirmRestriction()
			f.start()
			key := f.answerKey(0)
			f.callback(key, integer(f.from["id"])+1)
			if str(f.row()["state"]) != "pending" || f.methodCount("restrictChatMember") != 1 {
				t.Fatal("another user passed admission verification")
			}
			steps := 1
			if mode == "sequence" {
				steps = 3
			}
			for n := 0; n < steps; n++ {
				f.callback(f.answerKey(n), integer(f.from["id"]))
			}
			if str(f.row()["state"]) != "passed" || f.methodCount("restrictChatMember") != 2 {
				t.Fatal("verified member was not released exactly once")
			}
			if !boolean(f.member["can_send_messages"]) || !boolean(f.member["can_send_photos"]) || boolean(f.member["can_send_documents"]) {
				t.Fatal("release did not restore current group defaults")
			}
			f.callback(key, integer(f.from["id"]))
			if f.methodCount("restrictChatMember") != 2 {
				t.Fatal("challenge replay changed member permissions")
			}
			if count, _ := cCount(f.a.DB, "SELECT COUNT(*) FROM ts_bot_sessions WHERE chat_id=?", f.chat["id"]); count != 0 {
				t.Fatal("admission verification unexpectedly launched a search")
			}
		})
	}
}

func TestGroupManagementMySQLPrivateTextAndMath(t *testing.T) {
	for _, mode := range []string{"text", "math"} {
		t.Run(mode, func(t *testing.T) {
			f := newGroupManagementFixture(t)
			f.begin(mode)
			f.confirmRestriction()
			row := f.start()
			if mode == "text" {
				if image, err := png.Decode(bytes.NewReader(f.photo)); err != nil || image.Bounds().Dx() != 300 {
					t.Fatal("text captcha did not deliver a PNG", err)
				}
			}
			// Known hashed fixture answer exercises the private reply routing without
			// OCR or storing the generated random image's plaintext answer.
			if _, err := f.a.DB.Exec("UPDATE ts_bot_join_challenges SET answer_hashes=? WHERE id=?", encode([]string{hash("ABC234")}), row["id"]); err != nil {
				t.Fatal(err)
			}
			m := Row{"chat": Row{"id": f.from["id"], "type": "private"}, "from": f.from, "text": "abc234", "message_id": 6000, "reply_to_message": Row{"message_id": row["private_message_id"]}}
			handled, err := f.a.JoinVerificationText(context.Background(), m, integer(f.bot["id"]))
			if err != nil || !handled || str(f.row()["state"]) != "passed" {
				t.Fatal("muted newcomer could not complete a private text reply", err)
			}
		})
	}
}

func TestGroupManagementMySQLManualRestrictionAndMissingProvenance(t *testing.T) {
	for _, variant := range []string{"other_admin", "missing_event", "changed_permissions"} {
		t.Run(variant, func(t *testing.T) {
			f := newGroupManagementFixture(t)
			f.begin("button")
			if variant != "missing_event" {
				f.confirmRestriction()
			}
			f.start()
			if variant == "other_admin" {
				f.event(integer(f.from["id"])+20, f.member, f.member)
			}
			if variant == "changed_permissions" {
				f.member["until_date"] = time.Now().Unix() + 600
			}
			f.callback(f.answerKey(0), integer(f.from["id"]))
			if f.methodCount("restrictChatMember") != 1 {
				t.Fatal("verification overrode an unproven or manually changed restriction")
			}
			if variant != "other_admin" && str(f.row()["state"]) != "manual_review" {
				t.Fatal("unproven permission state did not require manual review")
			}
		})
	}
}

func TestGroupManagementMySQLTimeoutAttemptsAndRejoin(t *testing.T) {
	for _, action := range []string{"mute", "kick"} {
		t.Run(action, func(t *testing.T) {
			f := newGroupManagementFixture(t)
			if _, err := f.a.DB.Exec("UPDATE ts_bot_groups SET join_failure_action=? WHERE bot_id=? AND chat_id=?", action, f.bot["id"], f.chat["id"]); err != nil {
				t.Fatal(err)
			}
			old := f.begin("button")
			f.confirmRestriction()
			if _, err := f.a.DB.Exec("UPDATE ts_bot_join_challenges SET expires_at=? WHERE id=?", future(-time.Second), old["id"]); err != nil {
				t.Fatal(err)
			}
			if err := f.a.groupManagementForBot(context.Background(), integer(f.bot["id"])); err != nil {
				t.Fatal(err)
			}
			if str(f.row()["state"]) != "failed" {
				t.Fatal("expired admission challenge not finalized")
			}
			if action == "mute" && (!groupHasMute(f.member) || f.methodCount("restrictChatMember") != 1) {
				t.Fatal("timeout must retain indefinite mute")
			}
			if action == "kick" && (str(f.member["status"]) != "kicked" || f.methodCount("banChatMember") != 1) {
				t.Fatal("kick timeout did not remove the member")
			}
			f.event(integer(f.from["id"]), f.member, Row{"status": "left"})
			f.member = Row{"status": "member"}
			f.event(integer(f.from["id"]), Row{"status": "left"}, f.member)
			next := f.row()
			if str(next["challenge_id"]) == str(old["challenge_id"]) || str(next["state"]) != "pending" {
				t.Fatal("rejoin reused the old admission challenge")
			}
			result, err := f.a.verifyGroupJoin(integer(f.bot["id"]), str(old["challenge_id"]), integer(f.from["id"]), 1, "old", true)
			if err != nil || str(result["status"]) != "refused" {
				t.Fatal("old challenge survived leave/rejoin", err)
			}
		})
	}
}

func TestGroupManagementMySQLLengthDeleteFailureNoSearch(t *testing.T) {
	f := newGroupManagementFixture(t)
	if _, err := f.a.DB.Exec("UPDATE ts_bot_groups SET message_max_chars=3 WHERE bot_id=? AND chat_id=?", f.bot["id"], f.chat["id"]); err != nil {
		t.Fatal(err)
	}
	f.deleteFails = true
	m := catalogMerge(f.message(), Row{"text": "中😀ab"})
	if err := f.a.handleUpdate(context.Background(), Row{"message": m}, f.bot); err != nil {
		t.Fatal(err)
	}
	if f.methodCount("deleteMessage") != 1 || f.methodCount("sendMessage") != 0 {
		t.Fatal("delete failure echoed the overlength message as search")
	}
	group, _ := one(f.a.DB, "SELECT last_error FROM ts_bot_groups WHERE bot_id=? AND chat_id=?", f.bot["id"], f.chat["id"])
	if !strings.Contains(str(group["last_error"]), "4 字 / 上限 3 字") {
		t.Fatal("length failure diagnostics missing actual and allowed character count")
	}
	f.member = Row{"status": "administrator"}
	handled, err := f.a.Moderate(context.Background(), m, integer(f.bot["id"]))
	if err != nil || handled || f.methodCount("deleteMessage") != 1 {
		t.Fatal("administrator lost moderation exemption", err)
	}
}

func TestGroupManagementMySQLAttemptLimitAndConcurrentReplay(t *testing.T) {
	f := newGroupManagementFixture(t)
	f.begin("math")
	f.confirmRestriction()
	row := f.start()
	for i := 0; i < 2; i++ {
		m := Row{"chat": Row{"id": f.from["id"], "type": "private"}, "from": f.from, "text": "NOT-A-NUMBER", "message_id": 7000 + i, "reply_to_message": Row{"message_id": row["private_message_id"]}}
		if handled, err := f.a.JoinVerificationText(context.Background(), m, integer(f.bot["id"])); err != nil || !handled {
			t.Fatal(err)
		}
	}
	if str(f.row()["state"]) != "failed" || !groupHasMute(f.member) || f.methodCount("restrictChatMember") != 1 {
		t.Fatal("failed attempts did not preserve the initial indefinite mute")
	}
	f.event(integer(f.from["id"]), f.member, Row{"status": "left"})
	f.member = Row{"status": "member"}
	f.begin("button")
	f.confirmRestriction()
	row = f.start()
	key := f.answerKey(0)
	var succeeded atomic.Int64
	var wg sync.WaitGroup
	var claimed Row
	var mu sync.Mutex
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := f.a.verifyGroupJoin(integer(f.bot["id"]), str(row["challenge_id"]), integer(f.from["id"]), integer(row["private_message_id"]), key, true)
			if err != nil {
				t.Error(err)
				return
			}
			if str(result["status"]) == "passed" {
				succeeded.Add(1)
				mu.Lock()
				claimed = object(result["row"])
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if succeeded.Load() != 1 {
		t.Fatalf("same admission answer claimed %d permission operations", succeeded.Load())
	}
	if _, err := f.a.finishGroupJoin(context.Background(), claimed, true); err != nil || str(f.row()["state"]) != "passed" {
		t.Fatal("claimed release did not complete", err)
	}
}

func TestGroupManagementMySQLMissedLeaveAndPermissionFailure(t *testing.T) {
	f := newGroupManagementFixture(t)
	old := f.begin("button")
	f.confirmRestriction()
	f.start()
	f.callback(f.answerKey(0), integer(f.from["id"]))
	if str(f.row()["state"]) != "passed" {
		t.Fatal("initial admission not complete")
	}
	// The leave event was absent; a newer explicit membership transition proves
	// this is a fresh join instead of a duplicate service notification.
	if _, err := f.a.DB.Exec("UPDATE ts_bot_join_challenges SET event_at=event_at-10 WHERE id=?", old["id"]); err != nil {
		t.Fatal(err)
	}
	f.member = Row{"status": "member"}
	f.selfRestrict = false
	f.event(integer(f.from["id"]), Row{"status": "left"}, Row{"status": "member"})
	next := f.row()
	if str(next["challenge_id"]) == str(old["challenge_id"]) || str(next["state"]) != "error" || !strings.Contains(str(next["last_error"]), "权限") {
		t.Fatal("new join did not recover the missing leave or observe insufficient permissions")
	}
}

func TestGroupManagementMySQLBatchWelcomeAndStaleLease(t *testing.T) {
	f := newGroupManagementFixture(t)
	if _, err := f.a.DB.Exec("UPDATE ts_bot_groups SET welcome_enabled=1 WHERE bot_id=? AND chat_id=?", f.bot["id"], f.chat["id"]); err != nil {
		t.Fatal(err)
	}
	f.seq++
	m := Row{"chat": f.chat, "date": time.Now().Unix(), "_update_id": f.seq, "new_chat_members": []Row{f.from, {"id": integer(f.from["id"]) + 1, "first_name": "Second fixture"}, {"id": integer(f.from["id"]) + 2, "is_bot": true}}}
	for i := 0; i < 2; i++ {
		if handled, err := f.a.GroupServiceMessage(context.Background(), m, integer(f.bot["id"])); err != nil || !handled {
			t.Fatal(err)
		}
	}
	if f.methodCount("sendMessage") != 2 || f.methodCount("restrictChatMember") != 0 {
		t.Fatal("batch welcome did not greet each human exactly once")
	}
	f.event(integer(f.from["id"]), Row{"status": "member"}, Row{"status": "left"})
	f.member = Row{"status": "member"}
	r := f.begin("button")
	if _, err := f.a.DB.Exec("UPDATE ts_bot_join_challenges SET state='restricting',lease_until=? WHERE id=?", future(-time.Second), r["id"]); err != nil {
		t.Fatal(err)
	}
	before := len(f.calls)
	if err := f.a.groupManagementForBot(context.Background(), integer(f.bot["id"])); err != nil {
		t.Fatal(err)
	}
	if str(f.row()["state"]) != "manual_review" || len(f.calls) != before {
		t.Fatal("stale operation lease repeated an ambiguous Telegram request")
	}
}

func TestGroupManagementMySQLAdmissionGateAndAdminRelease(t *testing.T) {
	f := newGroupManagementFixture(t)
	row := f.begin("button")
	before := len(f.calls)
	for _, state := range []string{"issuing", "restricting", "pending", "releasing", "failing", "failed", "manual_review", "error", "left"} {
		if _, err := f.a.DB.Exec("UPDATE ts_bot_join_challenges SET state=? WHERE id=?", state, row["id"]); err != nil {
			t.Fatal(err)
		}
		if allowed, err := f.a.GroupSearchAllowed(integer(f.bot["id"]), integer(f.chat["id"]), integer(f.from["id"])); err != nil || allowed {
			t.Fatalf("unapproved admission state %s allowed search: %v", state, err)
		}
	}
	for _, state := range []string{"passed", "exempt", "welcomed", "admin_released"} {
		if _, err := f.a.DB.Exec("UPDATE ts_bot_join_challenges SET state=? WHERE id=?", state, row["id"]); err != nil {
			t.Fatal(err)
		}
		if allowed, err := f.a.GroupSearchAllowed(integer(f.bot["id"]), integer(f.chat["id"]), integer(f.from["id"])); err != nil || !allowed {
			t.Fatalf("approved admission state %s denied search: %v", state, err)
		}
	}
	if allowed, err := f.a.GroupSearchAllowed(integer(f.bot["id"]), integer(f.chat["id"]), integer(f.from["id"])+10); err != nil || !allowed {
		t.Fatal("pre-existing member without an admission record was blocked", err)
	}
	if len(f.calls) != before {
		t.Fatal("search gate made per-message external requests")
	}
	if _, err := f.a.DB.Exec("UPDATE ts_bot_join_challenges SET state='failed' WHERE id=?", row["id"]); err != nil {
		t.Fatal(err)
	}
	f.event(integer(f.from["id"])+30, f.member, Row{"status": "member"})
	if str(f.row()["state"]) != "admin_released" {
		t.Fatal("explicit administrator release was not recorded")
	}
	if allowed, err := f.a.GroupSearchAllowed(integer(f.bot["id"]), integer(f.chat["id"]), integer(f.from["id"])); err != nil || !allowed {
		t.Fatal("administrator-released member still could not search", err)
	}
	if _, err := f.a.DB.Exec("UPDATE ts_bot_join_challenges SET state='pending' WHERE id=?", row["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.DB.Exec("UPDATE ts_bot_groups SET join_verify_mode='off' WHERE bot_id=? AND chat_id=?", f.bot["id"], f.chat["id"]); err != nil {
		t.Fatal(err)
	}
	if allowed, err := f.a.GroupSearchAllowed(integer(f.bot["id"]), integer(f.chat["id"]), integer(f.from["id"])); err != nil || !allowed {
		t.Fatal("explicitly disabling admission verification did not open search", err)
	}
}

func TestGroupManagementMySQLModeratesPendingMemberBeforeSearchGate(t *testing.T) {
	f := newGroupManagementFixture(t)
	f.begin("button")
	if _, err := f.a.DB.Exec("UPDATE ts_bot_groups SET message_max_chars=3 WHERE bot_id=? AND chat_id=?", f.bot["id"], f.chat["id"]); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	m := catalogMerge(f.message(), Row{"text": "中😀ab"})
	if err := f.a.handleUpdate(context.Background(), Row{"message": m}, f.bot); err != nil {
		t.Fatal(err)
	}
	if f.methodCount("deleteMessage") != 1 || f.methodCount("sendMessage") != 0 {
		t.Fatal("pending-member guard bypassed content moderation")
	}
	// An administrator's manual change must not exempt ordinary content from
	// moderation, including edits to messages that were already in the group.
	if _, err := f.a.DB.Exec("UPDATE ts_bot_join_challenges SET manual_override=1 WHERE bot_id=? AND chat_id=?", f.bot["id"], f.chat["id"]); err != nil {
		t.Fatal(err)
	}
	f.member = Row{"status": "member"}
	for _, kind := range []string{"message", "edited_message"} {
		t.Run(kind, func(t *testing.T) {
			f.calls = nil
			if err := f.a.handleUpdate(context.Background(), Row{kind: m}, f.bot); err != nil {
				t.Fatal(err)
			}
			if f.methodCount("deleteMessage") != 1 || f.methodCount("sendMessage") != 0 {
				t.Fatal("manual permission override bypassed length moderation")
			}
			if _, err := f.a.DB.Exec("UPDATE ts_bot_groups SET message_max_chars=0,ad_mode='links' WHERE bot_id=? AND chat_id=?", f.bot["id"], f.chat["id"]); err != nil {
				t.Fatal(err)
			}
			f.calls = nil
			advert := catalogMerge(m, Row{"text": "https://fixture.invalid/advert"})
			if err := f.a.handleUpdate(context.Background(), Row{kind: advert}, f.bot); err != nil {
				t.Fatal(err)
			}
			if f.methodCount("deleteMessage") != 1 || f.methodCount("sendMessage") != 0 {
				t.Fatal("manual permission override bypassed advertisement moderation")
			}
			if _, err := f.a.DB.Exec("UPDATE ts_bot_groups SET message_max_chars=3,ad_mode='off' WHERE bot_id=? AND chat_id=?", f.bot["id"], f.chat["id"]); err != nil {
				t.Fatal(err)
			}
		})
	}
	f.calls = nil
	m["text"] = "Go"
	if err := f.a.handleUpdate(context.Background(), Row{"message": m}, f.bot); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Fatal("pending member's ordinary text triggered search or a reply")
	}
}
