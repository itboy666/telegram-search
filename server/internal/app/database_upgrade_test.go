package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestDatabaseUpgrade(t *testing.T) {
	d := installerDatabaseFixture(t)
	ctx := context.Background()
	connection, err := installDB(ctx, d, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connection.ExecContext(ctx, "CREATE DATABASE `"+d.Name+"`"); err != nil {
		connection.Close()
		t.Fatal(err)
	}
	connection.Close()

	db, err := installDB(ctx, d, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a := appWithDatabase(installerRoot(t), d.env(), db)
	if err = a.migrate(ctx, false); err != nil {
		t.Fatal(err)
	}
	a.Register()
	handler := a.Handler()
	target := migrationVersions[len(migrationVersions)-1]
	previous := migrationVersions[len(migrationVersions)-2]
	if _, err = db.Exec("DELETE FROM ts_schema_migrations WHERE version=?", target); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("DROP TABLE ts_installation"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO ts_settings(name,value) VALUES('site_name','Upgrade fixture')"); err != nil {
		t.Fatal(err)
	}

	csrf := randomHex(24)
	createSession := func(name string, super bool) string {
		t.Helper()
		res, err := db.Exec("INSERT INTO ts_admins(username,password_hash,display_name,is_super,created_at) VALUES(?,'existing-hash',?,?,?)", name, name, super, now())
		if err != nil {
			t.Fatal(err)
		}
		adminID, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		token := randomHex(32)
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err := insertSession(tx, "admin", adminID, token, csrf); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return token
	}
	superToken := createSession("upgrade_super", true)
	operatorToken := createSession("upgrade_operator", false)

	call := func(method, token, csrf, body string, wantCode int) databaseUpgradeStatus {
		t.Helper()
		req := httptest.NewRequest(method, "/admin/api/database-upgrade", strings.NewReader(body))
		req.Header.Set("X-CSRF-Token", csrf)
		if token != "" {
			req.AddCookie(&http.Cookie{Name: "starindex_admin", Value: token})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != wantCode {
			t.Fatalf("%s returned %d, want %d: %s", method, w.Code, wantCode, w.Body)
		}
		var out struct{ Data databaseUpgradeStatus }
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Data
	}
	call("GET", "", "", "", 401)
	call("GET", operatorToken, "", "", 403)
	call("POST", operatorToken, csrf, "{}", 403)
	call("POST", superToken, "", "{}", 403)
	call("POST", superToken, csrf, `{"version":"arbitrary"}`, 422)
	status := call("GET", superToken, "", "", 200)
	if status.CurrentVersion != previous || status.TargetVersion != target || status.UpToDate || status.Running || !slices.Equal(status.PendingVersions, []string{target}) {
		t.Fatalf("unexpected pending migration status: %+v", status)
	}
	if err := a.withMigrationLock(ctx, 0, func(*sql.Conn) error {
		if status := call("GET", superToken, "", "", 200); !status.Running {
			t.Fatal("migration lock was not reported")
		}
		call("POST", superToken, csrf, "{}", 409)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		status = call("POST", superToken, csrf, "{}", 200)
		if status.CurrentVersion != target || status.TargetVersion != target || !status.UpToDate || status.Running || status.PendingVersions == nil || len(status.PendingVersions) != 0 {
			t.Fatalf("unexpected completed migration status: %+v", status)
		}
	}
	var installRows, auditRows int
	var siteName, passwordHash string
	if err := db.QueryRow("SELECT COUNT(*) FROM ts_installation").Scan(&installRows); err != nil || installRows != 0 {
		t.Fatalf("upgrade should create the missing installation table without claiming installation: %d, %v", installRows, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM ts_audit_logs l JOIN ts_admins a ON a.id=l.actor_id WHERE l.action='升级数据库' AND l.detail=? AND a.username='upgrade_super'", target).Scan(&auditRows); err != nil || auditRows != 1 {
		t.Fatalf("expected one upgrade audit entry: %d, %v", auditRows, err)
	}
	if err := db.QueryRow("SELECT value FROM ts_settings WHERE name='site_name'").Scan(&siteName); err != nil || siteName != "Upgrade fixture" {
		t.Fatalf("site setting changed: %q, %v", siteName, err)
	}
	if err := db.QueryRow("SELECT password_hash FROM ts_admins WHERE username='upgrade_super'").Scan(&passwordHash); err != nil || passwordHash != "existing-hash" {
		t.Fatalf("admin password changed: %v", err)
	}
}
