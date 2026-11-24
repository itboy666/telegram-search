package app

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBotCommandMenusSeparateChatRoles(t *testing.T) {
	scopes := map[string]map[string]string{}
	for _, menu := range botCommandMenus() {
		if _, duplicate := scopes[menu.Scope]; duplicate {
			t.Fatal("duplicate scope", menu.Scope)
		}
		commands := map[string]string{}
		for _, command := range menu.Commands {
			if _, duplicate := commands[command.Command]; duplicate {
				t.Fatal("duplicate command", menu.Scope, command.Command)
			}
			if !regexp.MustCompile(`^[a-z0-9_]{1,32}$`).MatchString(command.Command) || utf8.RuneCountInString(command.Description) < 1 || utf8.RuneCountInString(command.Description) > 256 {
				t.Fatal("invalid Telegram command", command)
			}
			commands[command.Command] = command.Description
		}
		if len(commands) == 0 || len(commands) > 100 {
			t.Fatal("invalid command count", menu.Scope)
		}
		scopes[menu.Scope] = commands
	}
	if len(scopes) != 4 {
		t.Fatal("missing chat role scopes")
	}
	group, private, admin := scopes["all_group_chats"], scopes["all_private_chats"], scopes["all_chat_administrators"]
	if len(group) != 4 || !reflect.DeepEqual(group, scopes["default"]) {
		t.Fatal("default and group menus must contain only search/help")
	}
	for _, command := range []string{"start", "search", "discover", "help"} {
		if group[command] == "" || admin[command] == "" || private[command] == "" {
			t.Fatal("missing search command", command)
		}
	}
	for _, command := range []string{"add", "account", "wallet", "recharge", "cancel", "login"} {
		if private[command] == "" || group[command] != "" || admin[command] != "" {
			t.Fatal("personal command escaped private scope", command)
		}
	}
	for _, command := range []string{"groupsettings", "captcha", "maxlength", "joinverify", "welcome", "adwords", "allowdomain", "flood", "media", "forwards"} {
		if admin[command] == "" || group[command] != "" || private[command] != "" {
			t.Fatal("group management command escaped administrator scope", command)
		}
	}
	if !strings.Contains(private["ads"], "购买") || !strings.Contains(admin["ads"], "拦截") || group["ads"] != "" {
		t.Fatal("/ads must describe its separate private and administrator functions")
	}
}

func TestBotCommandSynchronizationPayloadAndRecovery(t *testing.T) {
	a := &App{}
	failure := &TelegramError{Code: 429, RetryAfter: 60, Description: "fixture rate limit"}
	fail := true
	calls := []Row{}
	a.TelegramCall = func(_ context.Context, method string, params Row) (Row, error) {
		if method != "setMyCommands" {
			t.Fatal("unexpected Telegram mutation", method)
		}
		calls = append(calls, decode(encode(params)))
		if fail && str(object(params["scope"])["type"]) == "all_group_chats" {
			return nil, failure
		}
		return Row{"ok": true}, nil
	}
	if err := a.syncBotCommands(context.Background()); !errors.Is(err, failure) || !strings.Contains(err.Error(), "all_group_chats") {
		t.Fatal("scope failure must remain observable and preserve retry metadata", err)
	}
	if len(calls) != 3 {
		t.Fatal("synchronization should stop on a rejected API request")
	}
	fail, calls = false, nil
	if err := a.syncBotCommands(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 {
		t.Fatal("retry must converge all four scopes")
	}
	for i, scope := range []string{"default", "all_private_chats", "all_group_chats", "all_chat_administrators"} {
		if str(object(calls[i]["scope"])["type"]) != scope || len(rowsOf(calls[i]["commands"])) == 0 || calls[i]["language_code"] != "" {
			t.Fatal("invalid scope registration payload", calls[i])
		}
	}
	a.TelegramCall = func(context.Context, string, Row) (Row, error) { return Row{"ok": false}, nil }
	if err := a.syncBotCommands(context.Background()); err == nil {
		t.Fatal("unconfirmed registration was accepted")
	}
}

func TestBotCommandServerChatBoundary(t *testing.T) {
	for _, kind := range []string{"group", "supergroup"} {
		for _, command := range []string{"add", "account", "wallet", "recharge", "cancel", "login"} {
			if botCommandAllowed(command, kind) {
				t.Fatal("typed personal command accepted in group", command, kind)
			}
		}
		for _, command := range []string{"", "start", "search", "discover", "help", "groupsettings", "ads"} {
			if !botCommandAllowed(command, kind) {
				t.Fatal("search or role-checked group command rejected", command, kind)
			}
		}
	}
	for _, command := range botGroupAdminCommands() {
		if command.Command != "ads" && botCommandAllowed(command.Command, "private") {
			t.Fatal("private chat accepted group setting", command.Command)
		}
	}
	for _, command := range []string{"account", "wallet", "recharge", "ads", "cancel", "login", "add", "start"} {
		if !botCommandAllowed(command, "private") {
			t.Fatal("private command missing", command)
		}
	}
	for _, privateCommand := range []string{"/account", "/wallet", "/recharge", "/ads", "/cancel", "/login", "/add", "钱包", "网站登录"} {
		if strings.Contains(botGroupHelp, privateCommand) {
			t.Fatal("group help exposes personal function", privateCommand)
		}
	}
	for _, groupCommand := range botGroupAdminCommands() {
		if groupCommand.Command != "ads" && strings.Contains(botPrivateHelp, "/"+groupCommand.Command) {
			t.Fatal("private help exposes group configuration", groupCommand.Command)
		}
	}
}

func TestBotPersonalViewsRejectGroupBeforeDatabaseAccess(t *testing.T) {
	a := &App{}
	calls := []string{}
	a.TelegramCall = func(_ context.Context, method string, params Row) (Row, error) {
		calls = append(calls, method)
		if method != "answerCallbackQuery" || !boolean(params["show_alert"]) {
			t.Fatal("personal data was sent outside private chat", method)
		}
		return Row{"ok": true}, nil
	}
	for _, chat := range []int64{-123, 0, 457} {
		from := Row{"id": int64(456)}
		if err := a.botStart(context.Background(), chat, from); err == nil {
			t.Fatal("private home accepted another chat")
		}
		session := Row{"telegram_user_id": from["id"], "user_id": 2, "chat_id": chat, "state": "home"}
		if _, _, err := a.botCommerceView(session); err == nil {
			t.Fatal("wallet rendered outside its owner chat")
		}
		if err := a.renderBotCommerce(context.Background(), session, false); err == nil {
			t.Fatal("wallet sent outside its owner chat")
		}
		cb := Row{"id": "scope-fixture", "from": from, "data": "m:legacy:mine", "message": Row{"chat": Row{"id": chat}}}
		if err := a.menuCallback(context.Background(), cb); err != nil {
			t.Fatal(err)
		}
		cb["data"] = "c:" + strings.Repeat("a", 24) + ":1:recharge"
		if err := a.botCommerceCallback(context.Background(), cb, 1); err == nil {
			t.Fatal("wallet callback accepted another chat")
		}
	}
	if len(calls) != 3 || slices.Contains(calls, "sendMessage") {
		t.Fatal("guarded calls should only answer menu callbacks")
	}
}
