package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type botGroupFixture struct {
	t               *testing.T
	a               *App
	bot, chat, from Row
	keyword         string
	thread          int64
	userID          int64
	calls           []Row
	resourceIDs     []int64
	updateIDs       []int64
	nextMessage     int64
}

func newBotGroupFixture(t *testing.T, chatType string, thread int64) *botGroupFixture {
	t.Helper()
	a := botWorkerTestApp(t)
	credentials, err := a.botCredentials()
	if err != nil {
		t.Fatal(err)
	}
	bot := object(credentials["bot"])
	if integer(bot["id"]) == 0 {
		t.Skip("group flow fixtures require an existing local bot identity; no network call is made")
	}
	id := int64(9_300_000_000_000) + int64(securityRandom(1_000_000_000))
	f := &botGroupFixture{
		t: t, a: a, bot: bot, thread: thread, nextMessage: 1000,
		chat:    Row{"id": -id, "type": chatType, "title": "Go isolated group fixture"},
		from:    Row{"id": id, "first_name": "Group fixture", "is_bot": false},
		keyword: "服务器_" + randomHex(8),
	}
	t.Cleanup(func() {
		botID, chatID, tgID := integer(f.bot["id"]), integer(f.chat["id"]), integer(f.from["id"])
		for _, table := range []string{"bot_chat_views", "bot_message_cleanup"} {
			_, _ = a.DB.Exec("DELETE FROM ts_"+table+" WHERE bot_id=? AND chat_id IN(?,?)", botID, chatID, tgID)
		}
		snapshots, err := query(a.DB, "SELECT search_token FROM ts_bot_sessions WHERE chat_id=?", chatID)
		if err != nil {
			t.Error("read test snapshots for cleanup", err)
		}
		for _, snapshot := range snapshots {
			if token := str(snapshot["search_token"]); token != "" {
				if _, err = a.DB.Exec("DELETE FROM ts_search_snapshots WHERE token_hash=?", hash(token)); err != nil {
					t.Error("snapshot cleanup", err)
				}
			}
		}
		_, _ = a.DB.Exec("DELETE FROM ts_bot_sessions WHERE chat_id=?", chatID)
		_, _ = a.DB.Exec("DELETE FROM ts_bot_verifications WHERE bot_id=? AND chat_id=?", botID, chatID)
		_, _ = a.DB.Exec("DELETE FROM ts_bot_join_challenges WHERE bot_id=? AND chat_id=?", botID, chatID)
		_, _ = a.DB.Exec("DELETE FROM ts_bot_groups WHERE bot_id=? AND chat_id=?", botID, chatID)
		_, _ = a.DB.Exec("DELETE FROM ts_search_logs WHERE query=?", f.keyword)
		if f.userID > 0 {
			for _, table := range []string{"telegram_profiles", "reward_claims", "wallet_entries", "wallets", "sessions"} {
				_, _ = a.DB.Exec("DELETE FROM ts_"+table+" WHERE user_id=?", f.userID)
			}
			_, _ = a.DB.Exec("DELETE FROM ts_users WHERE id=? AND telegram_id=?", f.userID, tgID)
		}
		for _, resource := range f.resourceIDs {
			_, _ = a.DB.Exec("DELETE FROM ts_resources WHERE id=?", resource)
		}
		for _, update := range f.updateIDs {
			_, _ = a.DB.Exec("DELETE FROM ts_bot_updates WHERE bot_id=? AND update_id=?", botID, update)
		}
		for _, key := range []string{
			"bot:" + str(tgID), "bot-callback:" + str(tgID),
			fmt.Sprintf("group-welcome:%d:%d", botID, chatID),
			fmt.Sprintf("guard-search:%d:%d", botID, tgID),
			fmt.Sprintf("guard-chat:%d:%d", botID, chatID),
			fmt.Sprintf("guard-issue:%d:%d:%d", botID, chatID, tgID),
			fmt.Sprintf("guard-answer:%d:%d:%d", botID, chatID, tgID),
			fmt.Sprintf("guard-answer-notice:%d:%d:%d", botID, chatID, tgID),
		} {
			_, _ = a.DB.Exec("DELETE FROM ts_rate_limits WHERE bucket=?", key)
		}
	})
	f.userID, err = catalogInsert(a.DB, "users", Row{"username": "go_group_" + randomHex(8), "display_name": "Group fixture", "telegram_id": id, "status": 1, "created_at": now()})
	if err != nil {
		t.Fatal(err)
	}
	// A fixture-only zero claim prevents a live reward policy from changing its
	// global daily total while the search path is exercised.
	if _, err = a.DB.Exec("INSERT INTO ts_reward_claims(user_id,day,amount,source,created_at) VALUES(?,?,0,'bot',?)", f.userID, now()[:10], now()); err != nil {
		t.Fatal(err)
	}
	a.TelegramCall = func(_ context.Context, method string, p Row) (Row, error) {
		f.calls = append(f.calls, Row{"method": method, "params": decode(encode(p))})
		switch method {
		case "sendMessage", "sendPhoto":
			f.nextMessage++
			return Row{"message_id": f.nextMessage}, nil
		case "getChatMember":
			return Row{"status": "member"}, nil
		default:
			return Row{"ok": true}, nil
		}
	}
	return f
}

func (f *botGroupFixture) update(update Row) {
	f.t.Helper()
	id := time.Now().UnixMicro() + int64(securityRandom(1_000_000))
	f.updateIDs = append(f.updateIDs, id)
	update["update_id"] = id
	if err := f.a.processBotUpdate(context.Background(), update, f.bot); err != nil {
		f.t.Fatal(err)
	}
	state, err := one(f.a.DB, "SELECT state FROM ts_bot_updates WHERE bot_id=? AND update_id=?", f.bot["id"], id)
	if err != nil || str(state["state"]) != "done" {
		f.t.Fatal("group update was not completed", err)
	}
}

func (f *botGroupFixture) join() {
	f.t.Helper()
	f.joinAs("administrator")
}

func (f *botGroupFixture) joinAs(status string) {
	f.t.Helper()
	admin := status == "administrator"
	f.update(Row{"my_chat_member": Row{
		"chat": f.chat, "from": f.from,
		"old_chat_member": Row{"status": "left", "user": Row{"id": f.bot["id"], "is_bot": true}},
		"new_chat_member": Row{"status": status, "user": Row{"id": f.bot["id"], "is_bot": true}, "can_delete_messages": admin},
	}})
	group, err := one(f.a.DB, "SELECT * FROM ts_bot_groups WHERE bot_id=? AND chat_id=?", f.bot["id"], f.chat["id"])
	if err != nil || group == nil || integer(group["inviter_id"]) != integer(f.from["id"]) || str(group["status"]) != status || boolean(group["can_delete"]) != admin {
		f.t.Fatal("membership event did not persist the group and inviter", err)
	}
	if len(f.calls) != 1 || str(f.calls[0]["method"]) != "sendMessage" || integer(object(f.calls[0]["params"])["chat_id"]) != integer(f.chat["id"]) {
		f.t.Fatal("membership onboarding was not sent to the group")
	}
	assertBotGroupWelcome(f.t, str(object(f.calls[0]["params"])["text"]), admin)
	f.calls = nil
}

func assertBotGroupWelcome(t *testing.T, text string, admin bool) {
	t.Helper()
	for _, required := range []string{"服务器", "无需输入 /search", "回复对应消息", "仅限发起搜索的人操作", "/groupsettings", "/captcha emoji", "/ads on", "/flood on"} {
		if !strings.Contains(text, required) {
			t.Fatalf("membership welcome omitted %q", required)
		}
	}
	for _, hint := range []string{"BotFather", "/setprivacy", "群隐私模式", "重新添加机器人入群"} {
		if strings.Contains(text, hint) == admin {
			t.Fatalf("privacy hint %q present=%t for administrator=%t", hint, strings.Contains(text, hint), admin)
		}
	}
}

func TestBotGroupSearchWelcome(t *testing.T) {
	for _, status := range []string{"member", "administrator"} {
		t.Run(status, func(t *testing.T) {
			assertBotGroupWelcome(t, groupSearchWelcome(status), status == "administrator")
		})
	}
}

func TestBotGroupPersonalCommandsAndMenuIsolation(t *testing.T) {
	for _, kind := range []string{"group", "supergroup"} {
		t.Run(kind, func(t *testing.T) {
			f := newBotGroupFixture(t, kind, 77)
			f.join()
			t.Cleanup(func() {
				for _, table := range []string{"bot_commerce_sessions", "bot_login_codes"} {
					_, _ = f.a.DB.Exec("DELETE FROM ts_"+table+" WHERE user_id=?", f.userID)
				}
			})
			private := catalogMerge(f.message(), Row{"chat": Row{"id": f.from["id"], "type": "private"}, "text": "/recharge"})
			f.update(Row{"message": private})
			before, err := one(f.a.DB, "SELECT * FROM ts_bot_commerce_sessions WHERE bot_id=? AND telegram_user_id=?", f.bot["id"], f.from["id"])
			if err != nil || str(before["state"]) != "recharge_methods" {
				t.Fatal("private recharge did not open", err)
			}
			f.calls = nil
			for _, command := range []string{"account", "wallet", "recharge", "cancel", "login", "add", "submit"} {
				for _, suffix := range []string{"", "@" + str(f.bot["username"])} {
					f.update(Row{"message": catalogMerge(f.message(), Row{"text": "/" + command + suffix})})
				}
			}
			after, err := one(f.a.DB, "SELECT * FROM ts_bot_commerce_sessions WHERE id=?", before["id"])
			if err != nil || encode(before) != encode(after) || len(f.calls) != 0 {
				t.Fatal("group personal command exposed data or altered private checkout", err)
			}
			for _, table := range []string{"bot_login_codes", "recharges", "ad_orders"} {
				if n, err := cCount(f.a.DB, "SELECT COUNT(*) FROM ts_"+table+" WHERE user_id=?", f.userID); err != nil || n != 0 {
					t.Fatal("group personal command created state", table, err)
				}
			}
			for _, command := range []string{"/start", "/help", "/start auth_fixture"} {
				f.calls = nil
				f.update(Row{"message": catalogMerge(f.message(), Row{"text": command})})
				if len(f.calls) != 1 || str(f.calls[0]["method"]) != "sendMessage" {
					t.Fatal("group start/help did not send one search-only help message")
				}
				params := object(f.calls[0]["params"])
				if str(params["text"]) != botGroupHelp || params["reply_markup"] != nil || integer(params["message_thread_id"]) != 77 {
					t.Fatal("group help leaked personal navigation or lost topic context")
				}
			}
			// A private home or wallet forwarded to a group cannot be operated.
			for _, data := range []string{"m:legacy:mine", "c:" + str(before["id"]) + ":" + str(before["revision"]) + ":recharge"} {
				f.calls = nil
				f.update(Row{"callback_query": Row{"id": "group-private-fixture", "from": f.from, "data": data, "message": Row{"chat": f.chat, "message_id": before["message_id"]}}})
				if len(f.calls) != 1 || str(f.calls[0]["method"]) != "answerCallbackQuery" || !boolean(object(f.calls[0]["params"])["show_alert"]) {
					t.Fatal("forwarded personal callback was not rejected privately")
				}
			}
		})
	}
}

func TestBotGroupCommandRolesRemainEnforced(t *testing.T) {
	f := newBotGroupFixture(t, "supergroup", 0)
	f.join()
	key := fmt.Sprintf("group-config:%v:%v:%v", f.bot["id"], f.chat["id"], f.from["id"])
	t.Cleanup(func() { _, _ = f.a.DB.Exec("DELETE FROM ts_rate_limits WHERE bucket=?", key) })
	transport := f.a.TelegramCall
	role := "member"
	f.a.TelegramCall = func(ctx context.Context, method string, params Row) (Row, error) {
		if method == "getChatMember" {
			f.calls = append(f.calls, Row{"method": method, "params": decode(encode(params))})
			return Row{"status": role}, nil
		}
		return transport(ctx, method, params)
	}
	for _, command := range []string{"/groupsettings", "/ads on"} {
		f.calls = nil
		f.update(Row{"message": catalogMerge(f.message(), Row{"text": command})})
		if len(f.calls) != 2 || str(f.calls[0]["method"]) != "getChatMember" || !strings.Contains(str(object(f.calls[1]["params"])["text"]), "只有本群群主和当前管理员") {
			t.Fatal("ordinary member bypassed group management authorization")
		}
	}
	role, f.calls = "administrator", nil
	f.update(Row{"message": catalogMerge(f.message(), Row{"text": "/groupsettings"})})
	if len(f.calls) != 2 || str(f.calls[0]["method"]) != "getChatMember" || !strings.Contains(str(object(f.calls[1]["params"])["text"]), "本群安全设置") {
		t.Fatal("administrator lost access to existing group management")
	}
	// Hand-typed group commands in private cannot open group configuration.
	f.calls = nil
	for _, command := range []string{"/groupsettings", "/captcha emoji", "/maxlength 200", "/joinverify emoji", "/welcome on"} {
		f.update(Row{"message": catalogMerge(f.message(), Row{"chat": Row{"id": f.from["id"], "type": "private"}, "text": command})})
	}
	if len(f.calls) != 0 {
		t.Fatal("private chat displayed group-only functions")
	}
}

func TestBotGroupMembershipWelcome(t *testing.T) {
	for _, kind := range []string{"group", "supergroup"} {
		for _, status := range []string{"member", "administrator"} {
			t.Run(kind+"/"+status, func(t *testing.T) {
				f := newBotGroupFixture(t, kind, 0)
				f.joinAs(status)
				// Membership refreshes must update status without repeating onboarding.
				f.update(Row{"my_chat_member": Row{
					"chat": f.chat, "from": f.from,
					"old_chat_member": Row{"status": status, "user": Row{"id": f.bot["id"], "is_bot": true}},
					"new_chat_member": Row{"status": status, "user": Row{"id": f.bot["id"], "is_bot": true}, "can_delete_messages": status == "administrator"},
				}})
				if len(f.calls) != 0 {
					t.Fatal("unchanged membership repeated the welcome")
				}
			})
		}
	}
}

func (f *botGroupFixture) seedResources() {
	f.t.Helper()
	for i := int64(0); i <= f.a.botPageSize(); i++ {
		link := "https://t.me/go_group_" + randomHex(8)
		id, err := catalogInsert(f.a.DB, "resources", Row{"title": f.keyword + " " + str(i), "description": "", "url": link, "url_hash": hash(link), "type": "channel", "status": "published", "created_at": now(), "updated_at": now()})
		if err != nil {
			f.t.Fatal(err)
		}
		f.resourceIDs = append(f.resourceIDs, id)
	}
}

func (f *botGroupFixture) message() Row {
	return Row{"chat": f.chat, "from": f.from, "message_id": 101, "message_thread_id": f.thread, "text": f.keyword}
}

func (f *botGroupFixture) trustIfRequired() {
	f.t.Helper()
	policy := f.a.securityPolicy(integer(f.bot["id"]), integer(f.chat["id"]))
	if str(policy["mode"]) == "off" {
		return
	}
	_, err := f.a.DB.Exec("INSERT INTO ts_bot_verifications(bot_id,chat_id,user_id,policy_hash,state,answer_hashes,payload,reply_context,verified_until,expires_at) VALUES(?,?,?,?,'verified','[]','{}','{}',?,?)",
		f.bot["id"], f.chat["id"], f.from["id"], policy["hash"], time.Now().Unix()+3600, future(time.Hour))
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *botGroupFixture) session() Row {
	f.t.Helper()
	rows, err := query(f.a.DB, "SELECT * FROM ts_bot_sessions WHERE chat_id=? AND query=?", f.chat["id"], f.keyword)
	if err != nil || len(rows) != 1 {
		f.t.Fatalf("expected one search session, got %d: %v", len(rows), err)
	}
	return rows[0]
}

func (f *botGroupFixture) assertSearchReply(params Row) {
	f.t.Helper()
	if !strings.Contains(str(params["text"]), "搜索："+f.keyword) || !strings.Contains(str(params["text"]), "https://t.me/go_group_") {
		f.t.Fatal("plain keyword did not produce the seeded search results")
	}
	if integer(params["chat_id"]) != integer(f.chat["id"]) || integer(object(params["reply_parameters"])["message_id"]) != 101 || !boolean(object(params["reply_parameters"])["allow_sending_without_reply"]) || integer(params["message_thread_id"]) != f.thread {
		f.t.Fatal("search reply lost its group, original message or topic")
	}
}

func TestBotGroupPlainKeywordFlow(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		thread     int64
	}{{"group", "group", 0}, {"supergroup", "supergroup", 0}, {"forum_topic", "supergroup", 77}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBotGroupFixture(t, tc.kind, tc.thread)
			f.join()
			f.seedResources()
			f.trustIfRequired()
			f.update(Row{"message": f.message()})
			if len(f.calls) != 1 || str(f.calls[0]["method"]) != "sendMessage" {
				t.Fatal("plain human keyword must send exactly one search reply")
			}
			f.assertSearchReply(object(f.calls[0]["params"]))
			session := f.session()
			if integer(session["telegram_user_id"]) != integer(f.from["id"]) || integer(session["message_id"]) != f.nextMessage || integer(session["page"]) != 1 {
				t.Fatal("search session ownership or message reference is incorrect")
			}
			callback := Row{"id": "group-page-fixture", "data": "s:" + str(session["id"]) + ":1:p2", "from": Row{"id": integer(f.from["id"]) + 1}, "message": Row{"chat": f.chat, "message_id": session["message_id"], "message_thread_id": f.thread}}
			f.calls = nil
			f.update(Row{"callback_query": callback})
			if len(f.calls) != 1 || str(f.calls[0]["method"]) != "answerCallbackQuery" || !boolean(object(f.calls[0]["params"])["show_alert"]) || !strings.Contains(str(object(f.calls[0]["params"])["text"]), "不能操作别人的") {
				t.Fatal("another group member was not refused with an alert")
			}
			if integer(f.session()["revision"]) != 1 {
				t.Fatal("another member changed the search session")
			}
			callback["from"] = f.from
			f.calls = nil
			f.update(Row{"callback_query": callback})
			if len(f.calls) != 2 || str(f.calls[1]["method"]) != "editMessageText" {
				t.Fatal("search owner could not turn the page")
			}
			params := object(f.calls[1]["params"])
			if integer(params["chat_id"]) != integer(f.chat["id"]) || integer(params["message_id"]) != integer(session["message_id"]) || integer(f.session()["page"]) != 2 {
				t.Fatal("owner pagination edited another message or did not advance")
			}
		})
	}
}

func TestBotGroupIgnoresBotsAndServiceMessages(t *testing.T) {
	f := newBotGroupFixture(t, "supergroup", 0)
	f.join()
	cases := []struct {
		name   string
		update Row
	}{
		{"third_party_bot", Row{"message": catalogMerge(f.message(), Row{"from": Row{"id": integer(f.from["id"]) + 1, "is_bot": true}})}},
		{"own_bot", Row{"message": catalogMerge(f.message(), Row{"from": Row{"id": f.bot["id"], "is_bot": true}})}},
		{"new_member_service", Row{"message": catalogMerge(f.message(), Row{"text": "", "new_chat_members": []Row{{"id": integer(f.from["id"]) + 1}}})}},
		{"left_member_service", Row{"message": catalogMerge(f.message(), Row{"text": "", "left_chat_member": Row{"id": integer(f.from["id"]) + 1}})}},
		{"pinned_message_service", Row{"message": catalogMerge(f.message(), Row{"text": "", "pinned_message": f.message()})}},
		{"forum_topic_service", Row{"message": catalogMerge(f.message(), Row{"text": "", "forum_topic_created": Row{"name": "Fixture topic", "icon_color": 0}})}},
		{"caption_only", Row{"message": catalogMerge(f.message(), Row{"text": "", "caption": f.keyword, "photo": []Row{{"file_id": "fixture"}}})}},
		{"automatic_forward", Row{"message": catalogMerge(f.message(), Row{"is_automatic_forward": true})}},
		{"anonymous_sender", Row{"message": catalogMerge(f.message(), Row{"sender_chat": f.chat})}},
		{"channel_sender", Row{"message": catalogMerge(f.message(), Row{"sender_chat": Row{"id": integer(f.chat["id"]) - 1, "type": "channel"}})}},
		{"missing_human_identity", Row{"message": catalogMerge(f.message(), Row{"from": Row{}})}},
		{"edited_text", Row{"edited_message": f.message()}},
		{"channel_post", Row{"channel_post": f.message()}},
		{"edited_channel_post", Row{"edited_channel_post": f.message()}},
		{"other_bot_command", Row{"message": catalogMerge(f.message(), Row{"text": "/search@unrelated_fixture_bot " + f.keyword})}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.update(tc.update)
			if len(f.calls) != 0 {
				t.Fatal("ignored message produced a Telegram interaction")
			}
		})
	}
	sessions, err := cCount(f.a.DB, "SELECT COUNT(*) FROM ts_bot_sessions WHERE chat_id=?", f.chat["id"])
	logs, logErr := cCount(f.a.DB, "SELECT COUNT(*) FROM ts_search_logs WHERE query=?", f.keyword)
	if err != nil || logErr != nil || sessions != 0 || logs != 0 {
		t.Fatal("ignored messages reached the search path", err, logErr)
	}
}

func TestBotGroupCaptchaResumesOriginalTopicSearch(t *testing.T) {
	f := newBotGroupFixture(t, "supergroup", 77)
	f.join()
	f.seedResources()
	// Only this synthetic group row is changed; global security settings stay intact.
	_, err := f.a.DB.Exec("UPDATE ts_bot_groups SET verify_mode='button' WHERE bot_id=? AND chat_id=?", f.bot["id"], f.chat["id"])
	if err != nil {
		t.Fatal(err)
	}
	if str(f.a.securityPolicy(integer(f.bot["id"]), integer(f.chat["id"]))["mode"]) != "button" {
		t.Skip("global policy disallows per-group button verification; real settings are preserved")
	}
	f.update(Row{"message": f.message()})
	count, _ := cCount(f.a.DB, "SELECT COUNT(*) FROM ts_bot_sessions WHERE chat_id=?", f.chat["id"])
	if count != 0 || len(f.calls) != 1 || str(f.calls[0]["method"]) != "sendMessage" {
		t.Fatal("unverified keyword should issue one challenge before searching")
	}
	challenge, err := one(f.a.DB, "SELECT * FROM ts_bot_verifications WHERE bot_id=? AND chat_id=? AND user_id=?", f.bot["id"], f.chat["id"], f.from["id"])
	if err != nil || challenge == nil || str(challenge["state"]) != "pending" || str(challenge["pending_query"]) != f.keyword {
		t.Fatal("challenge did not retain the original keyword", err)
	}
	key := ""
	for choice := range object(decode(str(challenge["payload"]))["choices"]) {
		key = choice
	}
	if len(key) != 12 {
		t.Fatal("button verification did not expose its callback choice")
	}
	callback := Row{"id": "group-verify-fixture", "data": "v:" + str(challenge["challenge_id"]) + ":" + key, "from": Row{"id": integer(f.from["id"]) + 1}, "message": Row{"chat": f.chat, "message_id": challenge["message_id"], "message_thread_id": f.thread}}
	f.calls = nil
	f.update(Row{"callback_query": callback})
	if len(f.calls) != 1 || !boolean(object(f.calls[0]["params"])["show_alert"]) || !strings.Contains(str(object(f.calls[0]["params"])["text"]), "别人的验证") {
		t.Fatal("another member completed the captcha")
	}
	callback["from"] = f.from
	f.calls = nil
	f.update(Row{"callback_query": callback})
	if len(f.calls) != 3 || str(f.calls[2]["method"]) != "sendMessage" {
		t.Fatal("verified human did not resume the original search")
	}
	f.assertSearchReply(object(f.calls[2]["params"]))
	f.session()
	f.calls = nil
	f.update(Row{"callback_query": callback})
	if len(f.calls) != 1 || str(f.calls[0]["method"]) != "answerCallbackQuery" {
		t.Fatal("replayed captcha executed the search again")
	}
	f.session()
}

func TestBotGroupModerationStopsLinkButAllowsKeyword(t *testing.T) {
	f := newBotGroupFixture(t, "supergroup", 0)
	f.join()
	f.seedResources()
	f.trustIfRequired()
	if _, err := f.a.DB.Exec("UPDATE ts_bot_groups SET ad_mode='links' WHERE bot_id=? AND chat_id=?", f.bot["id"], f.chat["id"]); err != nil {
		t.Fatal(err)
	}
	f.update(Row{"message": catalogMerge(f.message(), Row{"text": "https://fixture.invalid/advert"})})
	if len(f.calls) != 2 || str(f.calls[0]["method"]) != "getChatMember" || str(f.calls[1]["method"]) != "deleteMessage" {
		t.Fatal("member advertising link did not pass through moderation")
	}
	if count, _ := cCount(f.a.DB, "SELECT COUNT(*) FROM ts_bot_sessions WHERE chat_id=?", f.chat["id"]); count != 0 {
		t.Fatal("moderated advertisement also triggered a search")
	}
	f.calls = nil
	f.update(Row{"message": f.message()})
	if len(f.calls) != 1 || str(f.calls[0]["method"]) != "sendMessage" {
		t.Fatal("moderation blocked an ordinary keyword")
	}
	f.assertSearchReply(object(f.calls[0]["params"]))
}

func (f *botGroupFixture) pendingAdmission() {
	f.t.Helper()
	if _, err := f.a.DB.Exec("UPDATE ts_bot_groups SET join_verify_mode='button' WHERE bot_id=? AND chat_id=?", f.bot["id"], f.chat["id"]); err != nil {
		f.t.Fatal(err)
	}
	_, err := catalogInsert(f.a.DB, "bot_join_challenges", Row{
		"bot_id": f.bot["id"], "chat_id": f.chat["id"], "user_id": f.from["id"],
		"challenge_id": randomHex(16), "state": "pending", "method": "button",
		"answer_hashes": "[]", "payload": "{}", "user_profile": encode(f.from),
		"chat_profile": encode(f.chat), "expected_permissions": "{}",
		"expires_at": future(time.Minute), "created_at": now(), "updated_at": now(),
	})
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *botGroupFixture) assertAdmissionRefusal() {
	f.t.Helper()
	if len(f.calls) != 1 || str(f.calls[0]["method"]) != "answerCallbackQuery" ||
		!boolean(object(f.calls[0]["params"])["show_alert"]) ||
		str(object(f.calls[0]["params"])["text"]) != groupSearchVerificationRequired {
		f.t.Fatal("pending admission must only receive a callback alert")
	}
}

func TestBotGroupRejoinCannotUseOldSearchButtons(t *testing.T) {
	f := newBotGroupFixture(t, "supergroup", 0)
	f.join()
	f.seedResources()
	f.trustIfRequired()
	f.update(Row{"message": f.message()})
	session := f.session()
	// A fresh admission is required after leaving and rejoining. The old
	// search message and its still-valid token remain in the chat.
	f.pendingAdmission()
	for _, action := range []string{"p2", "tchannel", "f1", "l" + str(f.resourceIDs[0])} {
		t.Run(action, func(t *testing.T) {
			f.calls = nil
			f.update(Row{"callback_query": Row{
				"id": "old-search-after-rejoin", "data": "s:" + str(session["id"]) + ":1:" + action,
				"from": f.from, "message": Row{"chat": f.chat, "message_id": session["message_id"]},
			}})
			f.assertAdmissionRefusal()
			if integer(f.session()["revision"]) != 1 {
				t.Fatal("old button changed the search session during admission")
			}
		})
	}
	votes, err := cCount(f.a.DB, "SELECT COUNT(*) FROM ts_resource_likes WHERE user_id=?", f.userID)
	if err != nil || votes != 0 {
		t.Fatal("old recommendation button registered a vote during admission", err)
	}
	f.calls = nil
	err = f.a.startSearch(context.Background(), f.message(), f.keyword, "")
	var de *DomainError
	if !errors.As(err, &de) || de.Status != 403 || len(f.calls) != 0 {
		t.Fatal("direct search entry bypassed admission", err)
	}
}

func TestBotGroupRejoinCannotResumeOldSearchCaptcha(t *testing.T) {
	f := newBotGroupFixture(t, "supergroup", 77)
	f.join()
	f.seedResources()
	if _, err := f.a.DB.Exec("UPDATE ts_bot_groups SET verify_mode='button' WHERE bot_id=? AND chat_id=?", f.bot["id"], f.chat["id"]); err != nil {
		t.Fatal(err)
	}
	if str(f.a.securityPolicy(integer(f.bot["id"]), integer(f.chat["id"]))["mode"]) != "button" {
		t.Skip("global policy disallows per-group button verification; real settings are preserved")
	}
	f.update(Row{"message": f.message()})
	challenge, err := one(f.a.DB, "SELECT * FROM ts_bot_verifications WHERE bot_id=? AND chat_id=? AND user_id=?", f.bot["id"], f.chat["id"], f.from["id"])
	if err != nil || challenge == nil || str(challenge["state"]) != "pending" {
		t.Fatal("search captcha was not issued", err)
	}
	key := ""
	for choice := range object(decode(str(challenge["payload"]))["choices"]) {
		key = choice
	}
	if key == "" {
		t.Fatal("search captcha has no answer choice")
	}
	f.pendingAdmission()
	f.calls = nil
	f.update(Row{"callback_query": Row{
		"id": "old-captcha-after-rejoin", "data": "v:" + str(challenge["challenge_id"]) + ":" + key,
		"from": f.from, "message": Row{"chat": f.chat, "message_id": challenge["message_id"], "message_thread_id": f.thread},
	}})
	f.assertAdmissionRefusal()
	after, err := one(f.a.DB, "SELECT state FROM ts_bot_verifications WHERE id=?", challenge["id"])
	if err != nil || str(after["state"]) != "pending" {
		t.Fatal("old search captcha was consumed before admission", err)
	}
	f.calls = nil
	err = f.a.resumeSecurity(context.Background(), challenge)
	var de *DomainError
	if !errors.As(err, &de) || de.Status != 403 || len(f.calls) != 0 {
		t.Fatal("restored search bypassed admission", err)
	}
	count, err := cCount(f.a.DB, "SELECT COUNT(*) FROM ts_bot_sessions WHERE chat_id=?", f.chat["id"])
	if err != nil || count != 0 {
		t.Fatal("old search captcha created a result after rejoin", err)
	}
}
