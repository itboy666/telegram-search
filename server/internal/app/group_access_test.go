package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestGroupAndRetiredFieldsMySQLFieldAccess(t *testing.T) {
	if os.Getenv("STARINDEX_TEST_MYSQL") != "1" {
		t.Skip("set STARINDEX_TEST_MYSQL=1 for isolated field-access fixtures")
	}
	root, _ := filepath.Abs("../../..")
	a, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.DB.Close() })
	a.TelegramCall = func(context.Context, string, Row) (Row, error) {
		t.Fatal("field access checks must not call Telegram")
		return nil, nil
	}
	tag := "go_group_access_" + randomHex(6)
	ids := map[string][]int64{}
	t.Cleanup(func() {
		for _, id := range ids["admins"] {
			_, _ = a.DB.Exec("DELETE FROM ts_audit_logs WHERE actor_id=?", id)
		}
		for _, table := range []string{"bot_groups", "resources", "roles", "admins"} {
			for _, id := range ids[table] {
				if _, err := a.DB.Exec("DELETE FROM ts_"+table+" WHERE id=?", id); err != nil {
					t.Error("field fixture cleanup", err)
				}
			}
		}
	})
	insert := func(table string, data Row) int64 {
		t.Helper()
		id, err := catalogInsert(a.DB, table, data)
		if err != nil {
			t.Fatal(err)
		}
		ids[table] = append(ids[table], id)
		return id
	}
	super := insert("admins", Row{"username": tag, "display_name": tag, "is_super": 1, "status": 1, "password_hash": "", "created_at": now()})
	operator := insert("admins", Row{"username": tag + "_op", "display_name": tag, "is_super": 0, "status": 1, "password_hash": "", "created_at": now()})
	role, err := a.catalogSaveRole(Row{
		"name": tag, "status": 1,
		"permissions": []any{"bot_groups.read", "bot_groups.update", "resources.read", "resources.update"},
		"fields": Row{
			"bot_groups": Row{"welcome_template": "write", "message_max_chars": "read"},
			"resources":  Row{"title": "write"},
		},
	}, super, 0)
	if err != nil {
		t.Fatal(err)
	}
	ids["roles"] = append(ids["roles"], integer(role["id"]))
	if _, err = a.DB.Exec("INSERT INTO ts_admin_roles(admin_id,role_id) VALUES(?,?)", operator, role["id"]); err != nil {
		t.Fatal(err)
	}
	fakeBot := int64(9_500_000_000_000) + int64(securityRandom(1_000_000_000))
	group := insert("bot_groups", Row{"bot_id": fakeBot, "chat_id": -fakeBot, "title": tag, "created_at": now(), "updated_at": now()})
	link := "https://t.me/" + tag
	resource := insert("resources", Row{"title": tag, "url": link, "url_hash": hash(link), "type": "channel", "status": "published", "created_at": now(), "updated_at": now()})
	access, err := a.AccessFor(operator)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"message_max_chars", "block_media", "block_forwards", "join_verify_mode", "join_timeout_seconds", "join_max_attempts", "join_failure_action", "welcome_enabled", "can_restrict"} {
		if err := access.CheckWrite("bot_groups", Row{field: 0}); err == nil {
			t.Fatal("ungranted group field was writable:", field)
		}
	}
	if err := access.CheckWrite("bot_groups", Row{"welcome_template": "欢迎 {{mention}}"}); err != nil {
		t.Fatal("explicitly granted field rejected", err)
	}
	superAccess, err := a.AccessFor(super)
	if err != nil {
		t.Fatal(err)
	}
	for _, acc := range []*Access{access, superAccess} {
		if acc.CheckWrite("resources", Row{"like_count": 1000}) == nil || acc.CheckWrite("bot_groups", Row{"can_restrict": 1}) == nil {
			t.Fatal("system-derived count or capability became writable")
		}
	}
	// Historical grants for a retired field must neither expose the field nor
	// prevent editing an existing role after the feature has been removed.
	if _, err := a.DB.Exec("INSERT INTO ts_role_fields(role_id,resource,field,can_read,can_write) VALUES(?,'resources','like_count',1,1)", role["id"]); err != nil {
		t.Fatal(err)
	}
	access, err = a.AccessFor(operator)
	if err != nil || access.CheckWrite("resources", Row{"like_count": 1}) == nil || catalogReadable(access, "resources", "like_count") {
		t.Fatal("historical role exposed a retired field", err)
	}
	roles, err := a.catalogRoles()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range roles {
		if integer(row["id"]) == integer(role["id"]) && object(object(row["fields"])["resources"])["like_count"] != nil {
			t.Fatal("retired field remains in the role editor payload")
		}
	}
	if object(object(permissionCatalogJSON()["resources"])["fields"])["like_count"] != nil {
		t.Fatal("permission catalog advertises retired recommendations")
	}
	a.Register()
	session := func(id int64) (string, string) {
		t.Helper()
		token, csrf := randomHex(32), randomHex(24)
		tx, err := a.DB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err = insertSession(tx, "admin", id, token, csrf); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return token, csrf
	}
	call := func(token, csrf, path string, body Row) (int, Row) {
		t.Helper()
		var payload bytes.Buffer
		method := http.MethodGet
		if body != nil {
			method = http.MethodPost
			_ = json.NewEncoder(&payload).Encode(body)
		}
		req := httptest.NewRequest(method, path, &payload)
		req.AddCookie(&http.Cookie{Name: "starindex_admin", Value: token})
		req.Header.Set("X-CSRF-Token", csrf)
		rec := httptest.NewRecorder()
		a.Handler().ServeHTTP(rec, req)
		result := Row{}
		_ = json.Unmarshal(rec.Body.Bytes(), &result)
		return rec.Code, object(result["data"])
	}
	token, csrf := session(operator)
	if status, _ := call(token, csrf, "/admin/api/bot-groups/"+str(group), Row{"message_max_chars": 100}); status != 403 {
		t.Fatal("HTTP allowed ungranted group field", status)
	}
	if status, _ := call(token, csrf, "/admin/api/bot-groups/"+str(group), Row{"welcome_template": "欢迎 {{mention}}"}); status != 200 {
		t.Fatal("HTTP rejected granted welcome field", status)
	}
	status, page := call(token, csrf, "/admin/api/bot-groups?id="+str(group), nil)
	items := rowsOf(page["items"])
	if status != 200 || len(items) != 1 || len(items[0]) != 3 || items[0]["join_verify_mode"] != nil {
		t.Fatal("group response exposed hidden policy fields")
	}
	for _, id := range []int64{operator, super} {
		token, csrf := session(id)
		if status, _ := call(token, csrf, "/admin/api/resources/"+str(resource), Row{"like_count": 999}); status != 403 {
			t.Fatal("HTTP accepted recommendation count mutation", status)
		}
	}
	stored, err := one(a.DB, "SELECT like_count FROM ts_resources WHERE id=?", resource)
	if err != nil || integer(stored["like_count"]) != 0 {
		t.Fatal("rejected recommendation count changed the database", err)
	}
}
