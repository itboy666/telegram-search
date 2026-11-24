package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestCatalogMySQL(t *testing.T) {
	if os.Getenv("STARINDEX_TEST_MYSQL") != "1" {
		t.Skip("set STARINDEX_TEST_MYSQL=1 to use the local test database")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer a.DB.Close()
	tag := "gocat_" + randomHex(5)
	ids := map[string][]int64{}
	hashes := []string{}
	remember := func(table string, id int64) { ids[table] = append(ids[table], id) }
	defer func() {
		_, _ = a.DB.Exec("DELETE FROM ts_search_logs WHERE query=?", tag)
		for _, id := range ids["admins"] {
			_, _ = a.DB.Exec("DELETE FROM ts_audit_logs WHERE actor_id=?", id)
		}
		for _, table := range []string{"resources", "categories", "roles", "admins"} {
			for _, id := range ids[table] {
				_, _ = a.DB.Exec("DELETE FROM ts_"+table+" WHERE id=?", id)
			}
		}
		for _, h := range hashes {
			_, _ = a.DB.Exec("DELETE FROM ts_link_locks WHERE url_hash=?", h)
		}
		_, _ = a.DB.Exec("DELETE FROM ts_rate_limits WHERE bucket=?", tag)
	}()
	admin, err := catalogInsert(a.DB, "admins", Row{"username": tag, "display_name": tag, "status": 1, "is_super": 1, "created_at": now(), "password_hash": ""})
	if err != nil {
		t.Fatal(err)
	}
	remember("admins", admin)
	operator, err := catalogInsert(a.DB, "admins", Row{"username": tag + "_op", "display_name": tag, "status": 1, "is_super": 0, "created_at": now(), "password_hash": ""})
	if err != nil {
		t.Fatal(err)
	}
	remember("admins", operator)
	role, err := a.catalogSaveRole(Row{"name": tag, "status": 1, "permissions": []any{"resources.read", "resources.update"}, "fields": Row{"resources": Row{"title": "write"}}}, admin, 0)
	if err != nil {
		t.Fatal(err)
	}
	remember("roles", integer(role["id"]))
	_, err = a.DB.Exec("INSERT INTO ts_admin_roles(admin_id,role_id) VALUES (?,?)", operator, role["id"])
	if err != nil {
		t.Fatal(err)
	}
	cat, err := a.catalogSaveCategory(Row{"name": tag, "description": "test"}, 0, admin)
	if err != nil {
		t.Fatal(err)
	}
	remember("categories", integer(cat["id"]))
	url := "https://t.me/" + tag
	hashes = append(hashes, hash(url))
	item, err := a.catalogSaveResource(Row{"title": tag, "url": url, "status": "hidden", "type": "channel", "category_id": cat["id"]}, 0, admin)
	if err != nil {
		t.Fatal(err)
	}
	resourceID := integer(item["id"])
	remember("resources", resourceID)
	if _, err = a.catalogSaveResource(Row{"title": "duplicate", "url": "http://telegram.me/" + tag + "?start=1", "type": "channel"}, 0, admin); err == nil {
		t.Fatal("canonical duplicate accepted")
	}
	result, err := a.Search(Row{"q": tag}, "web")
	if err != nil {
		t.Fatal(err)
	}
	snapshotToken := str(result["search_token"])
	defer func() {
		if snapshotToken != "" {
			if _, err := a.DB.Exec("DELETE FROM ts_search_snapshots WHERE token_hash=?", hash(snapshotToken)); err != nil {
				t.Error("snapshot cleanup", err)
			}
		}
	}()
	if integer(result["total"]) != 0 {
		t.Fatal("hidden resource exposed")
	}
	_, _ = a.DB.Exec("DELETE FROM ts_search_logs WHERE query=?", tag)
	access, err := a.AccessFor(operator)
	if err != nil {
		t.Fatal(err)
	}
	if !access.Has("resources.update") || access.Has("access.manage") || catalogReadable(access, "resources", "url") {
		t.Fatal("role scope incorrect")
	}
	if err = access.CheckWrite("resources", Row{"status": "published"}); err == nil {
		t.Fatal("hidden field writable")
	}
	result, err = a.catalogSearch(Row{"id": resourceID}, "admin", access)
	if err != nil {
		t.Fatal(err)
	}
	got := result["items"].([]Row)
	if len(got) != 1 || len(got[0]) != 2 || got[0]["url"] != nil {
		t.Fatal("response field filtering failed")
	}
	if _, err = a.catalogSearch(Row{"status": "hidden"}, "admin", access); err == nil {
		t.Fatal("hidden filter leak")
	}
	_, err = a.catalogSaveRole(Row{"name": tag, "status": 0, "permissions": []any{"resources.read", "resources.update"}, "fields": Row{"resources": Row{"title": "write"}}}, admin, integer(role["id"]))
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := a.AccessFor(operator)
	if err != nil || revoked.Has("resources.read") {
		t.Fatal("disabled role still grants access")
	}
	// Distinct workers racing to insert the same canonical URL yield one row.
	raceURL := "https://t.me/" + tag + "race"
	hashes = append(hashes, hash(raceURL))
	var successes atomic.Int64
	var raceID atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row, e := a.catalogSaveResource(Row{"title": tag, "url": raceURL, "type": "channel"}, 0, admin)
			if e == nil {
				successes.Add(1)
				raceID.Store(integer(row["id"]))
			}
		}()
	}
	wg.Wait()
	if raceID.Load() > 0 {
		remember("resources", raceID.Load())
	}
	if successes.Load() != 1 {
		t.Fatalf("race accepted %d insertions", successes.Load())
	}
	if err = a.RateLimit(tag, 1, 60); err != nil {
		t.Fatal(err)
	}
	if err = a.RateLimit(tag, 1, 60); err == nil {
		t.Fatal("rate limit bypassed")
	}
	// Exercise real HTTP authentication, field enforcement and role revocation.
	token, csrf := randomHex(32), randomHex(24)
	tx, err := a.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = insertSession(tx, "admin", operator, token, csrf); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	a.Register()
	call := func(path string, body Row) (int, Row) {
		method := "GET"
		var payload bytes.Buffer
		if body != nil {
			method = "POST"
			_ = json.NewEncoder(&payload).Encode(body)
		}
		req := httptest.NewRequest(method, path, &payload)
		req.AddCookie(&http.Cookie{Name: "starindex_admin", Value: token})
		req.Header.Set("X-CSRF-Token", csrf)
		rec := httptest.NewRecorder()
		a.Handler().ServeHTTP(rec, req)
		data := Row{}
		_ = json.Unmarshal(rec.Body.Bytes(), &data)
		return rec.Code, data
	}
	if status, _ := call("/admin/api/resources", nil); status != 403 {
		t.Fatalf("revoked role HTTP status %d", status)
	}
	if status, _ := call("/admin/api/admins", nil); status != 403 {
		t.Fatalf("operator can access administrators %d", status)
	}
	if status, _ := call("/user/api/me", nil); status != 401 {
		t.Fatalf("admin session reused for user %d", status)
	}
	// Check rollback keeps a rejected duplicate from changing existing content.
	current, err := catalogRecord(a.DB, "resources", resourceID, false)
	if err != nil || str(current["title"]) != tag || str(current["status"]) != "hidden" {
		t.Fatal("duplicate modified existing data")
	}
}

// Compile-time check: all catalog helpers accept both transaction and DB query interfaces.
var _ Queryer = (*sql.Tx)(nil)
