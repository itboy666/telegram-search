package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeResourceURL(t *testing.T) {
	tests := []struct{ input, kind, want string }{
		{"@ExampleBot", "bot", "https://t.me/examplebot"},
		{"http://telegram.me/ExampleBot?start=abc", "bot", "https://t.me/examplebot"},
		{"https://t.me/s/TestChannel/0012/?single", "video", "https://t.me/testchannel/12"},
		{"www.t.me/Telegram/", "channel", "https://t.me/telegram"},
		{"https://EXAMPLE.com:443/a?x=1&b=2#part", "website", "https://example.com/a?x=1&b=2"},
		{"http://example.com:80", "website", "http://example.com/"},
		{"https://例子.中国", "website", "https://xn--fsqu00a.xn--fiqs8s/"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := NormalizeResourceURL(tt.input, tt.kind)
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
	invalid := []struct{ input, kind string }{
		{"https://t.me/+secret", "channel"}, {"https://t.me/joinchat/secret", "channel"}, {"https://t.me/login", "channel"},
		{"https://t.me/name/0", "channel"}, {"https://t.me/name/000", "channel"}, {"https://t.me/name/123456789012345678901", "channel"},
		{"https://evil.test/name", "channel"}, {"https://user:pass@t.me/name", "channel"}, {"https://t.me:443/name", "channel"}, {"https://t.me/name/1/more", "channel"},
		{"https://t.me/telegram", "website"}, {"file:///etc/passwd", "website"}, {"javascript:alert(1)", "website"}, {"http://localhost", "website"},
		{"http://127.0.0.1", "website"}, {"http://10.0.0.1", "website"}, {"https://[::1]", "website"}, {"https://example.local", "website"},
		{"http://u:p@example.com", "website"}, {"https://exam ple.com", "website"}, {"https://example.com/a\\b", "website"}, {"https://example.com/%xx", "website"},
		{"https://example.com/?q=%xx", "website"}, {"https://example.com/?q=%", "website"},
	}
	for _, tt := range invalid {
		t.Run("reject "+tt.input, func(t *testing.T) {
			if got, err := NormalizeResourceURL(tt.input, tt.kind); err == nil {
				t.Fatalf("accepted %q", got)
			}
		})
	}
}
func TestRoleValidation(t *testing.T) {
	valid := Row{"name": "内容运营", "description": "仅标题", "status": json.Number("1"), "permissions": []any{"resources.read", "resources.update"}, "fields": Row{"resources": Row{"title": "write", "status": "read"}}}
	_, p, f, err := validateRole(valid)
	if err != nil || len(p) != 2 || len(f) != 2 {
		t.Fatalf("valid role: %v", err)
	}
	cases := []Row{
		catalogMerge(valid, Row{"permissions": []any{"access.manage"}}),
		catalogMerge(valid, Row{"permissions": []any{"resources.update"}}),
		catalogMerge(valid, Row{"permissions": []any{"resources.read"}, "fields": Row{"resources": Row{"created_at": "write"}}}),
		catalogMerge(valid, Row{"fields": Row{"resources": Row{"password_hash": "read"}}}),
		catalogMerge(valid, Row{"fields": Row{"admins": Row{"password_hash": "read"}}}),
		catalogMerge(valid, Row{"fields": Row{"resources": Row{"title": "all"}}}),
		catalogMerge(valid, Row{"status": 2}),
		catalogMerge(valid, Row{"is_super": 1}),
	}
	for i, c := range cases {
		if _, _, _, err := validateRole(c); err == nil {
			t.Errorf("unsafe role %d accepted", i)
		}
	}
}
func TestPermissionCatalogCompleteness(t *testing.T) {
	if len(PermissionCatalog) != 18 {
		t.Fatalf("expected 18 modules, got %d", len(PermissionCatalog))
	}
	seen := map[string]bool{}
	for _, m := range PermissionCatalog {
		if seen[m.Key] {
			t.Errorf("duplicate module %s", m.Key)
		}
		seen[m.Key] = true
		for _, p := range m.Actions {
			if !strings.HasPrefix(p.Key, m.Key+".") {
				t.Errorf("unscoped permission %s", p.Key)
			}
		}
		for _, f := range m.Fields {
			if f.Key == "password_hash" || f.Key == "token_hash" {
				t.Errorf("secret included %s", f.Key)
			}
		}
	}
	catalog := permissionCatalogJSON()
	if _, ok := catalog["resources"].(Row)["actions"].(Row)["update"]; !ok {
		t.Fatal("legacy permission editor contract missing")
	}
}
func TestCatalogFieldFiltering(t *testing.T) {
	access := &Access{Permissions: []string{"resources.read", "users.read"}, Fields: map[string]FieldAccess{"resources": {Read: []string{"title"}, Write: []string{}}, "users": {Read: []string{"display_name"}, Write: []string{}}}}
	where, args, err := catalogFilter(Row{"q": "alice"}, access, "users", "u")
	if err != nil || !strings.Contains(where, "u.display_name") || strings.Contains(where, "username") || strings.Contains(where, "telegram_id") || len(args) != 1 {
		t.Fatalf("field leak %s %v %v", where, args, err)
	}
	if _, _, err = catalogFilter(Row{"status": "1"}, access, "users", "u"); err == nil {
		t.Fatal("hidden state filter accepted")
	}
	where, _, err = catalogFilter(Row{"id": "42"}, &Access{Fields: map[string]FieldAccess{}}, "users", "u")
	if err != nil || !strings.Contains(where, "u.id=?") {
		t.Fatal("explicit ID lookup blocked")
	}
	if _, _, err = catalogFilter(Row{"q": "secret"}, &Access{Fields: map[string]FieldAccess{}}, "users", "u"); err == nil {
		t.Fatal("keyword searches hidden fields")
	}
	if catalogLike("a%_!") != "%a!%!_!!%" {
		t.Fatal("LIKE wildcard escape failed")
	}
}
