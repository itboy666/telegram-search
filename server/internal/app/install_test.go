package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func installerRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"database/migrations", "public/build", "public/icons/telegram-ios"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range migrationFiles {
		data, err := os.ReadFile(filepath.Join("../../../database/migrations", file))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(root, "database/migrations", file), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"public/build/index.html", "public/icons/telegram-ios/check.svg"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("installer fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func installHTTP(b *Bootstrap, path, token string, body any) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "http://localhost:8089"+path, bytes.NewReader(data))
	r.RemoteAddr = "127.0.0.1:41000"
	r.Header.Set("Origin", "http://localhost:8089")
	r.Header.Set("X-Install-Token", token)
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	return w
}

func TestInstallBootstrapGate(t *testing.T) {
	root := installerRoot(t)
	b, err := NewBootstrap(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	if other, err := NewBootstrap(root); err == nil {
		other.Close()
		t.Fatal("two installers acquired the same deployment")
	}
	for _, path := range []string{"/", "/admin", "/user/security"} {
		w := httptest.NewRecorder()
		b.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 303 || w.Header().Get("Location") != "/install" {
			t.Fatal("not redirected to installer", path, w.Code)
		}
	}
	for _, path := range []string{"/api/public/config", "/admin/api/login", "/healthz"} {
		w := httptest.NewRecorder()
		b.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 503 {
			t.Fatal("business route active before installation", path, w.Code)
		}
	}
	w := installHTTP(b, "/install/api/environment", "wrong", Row{})
	if w.Code != 403 || strings.Contains(w.Body.String(), b.token) {
		t.Fatal("installer leaked token or accepted missing authority")
	}
	w = installHTTP(b, "/install/api/environment", b.token, Row{})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ready":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	stat, _ := os.Stat(filepath.Join(root, ".install-token"))
	if stat.Mode().Perm() != 0600 {
		t.Fatal("installation code permissions are too broad")
	}
	for _, path := range []string{"/.env", "/.install-token", "/.install-state.json", "/build/../../.install-token", "/icons/../../.install-token"} {
		w := httptest.NewRecorder()
		b.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if strings.Contains(w.Body.String(), b.token) {
			t.Fatal("secret served through static path")
		}
	}
	// A partial environment is not marked ready.
	if err := os.Remove(filepath.Join(root, "database/migrations/013_installation.sql")); err != nil {
		t.Fatal(err)
	}
	w = installHTTP(b, "/install/api/environment", b.token, Row{})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ready":false`) {
		t.Fatal("missing migration was accepted")
	}
}

func TestInstallExistingConfigurationFailsClosed(t *testing.T) {
	for _, name := range []string{".env", "install.lock"} {
		t.Run(name, func(t *testing.T) {
			root := installerRoot(t)
			if err := os.WriteFile(filepath.Join(root, name), []byte("[DATABASE]\nHOSTNAME=127.0.0.1\nHOSTPORT=1"), 0600); err != nil {
				t.Fatal(err)
			}
			if b, err := NewBootstrap(root); err == nil {
				b.Close()
				t.Fatal("existing deployment reopened installer")
			}
			if _, err := os.Stat(filepath.Join(root, ".install-token")); !os.IsNotExist(err) {
				t.Fatal("token generated for existing installation")
			}
		})
	}
}

func TestInstallOriginAndTransport(t *testing.T) {
	b, err := NewBootstrap(installerRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, tc := range []struct {
		name, host, remote, origin, forwarded string
		tls, allowed                          bool
	}{
		{"localhost", "localhost:8089", "127.0.0.1:20", "http://localhost:8089", "", false, true},
		{"public_plaintext", "search.example.com", "1.2.3.4:20", "http://search.example.com", "", false, false},
		{"spoofed_forwarding", "search.example.com", "1.2.3.4:20", "https://search.example.com", "https", false, false},
		{"trusted_proxy", "search.example.com", "127.0.0.1:20", "https://search.example.com", "https", false, true},
		{"direct_tls", "search.example.com", "1.2.3.4:20", "https://search.example.com", "", true, true},
		{"cross_origin", "localhost:8089", "127.0.0.1:20", "https://evil.example", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://"+tc.host+"/install/api/environment", strings.NewReader("{}"))
			r.RemoteAddr = tc.remote
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-Forwarded-Proto", tc.forwarded)
			r.Header.Set("X-Install-Token", b.token)
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			w := httptest.NewRecorder()
			b.ServeHTTP(w, r)
			if (w.Code == 200) != tc.allowed {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestInstallConfigRoundTripAndValidation(t *testing.T) {
	d := installDatabase{Host: "::1", Port: 3306, Name: "starindex_test", Username: "installer", Password: `literal#'"\\$= : secret`, TLS: true}
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), ".env")
	if err := installWriteOnce(file, installEnv(d)); err != nil {
		t.Fatal(err)
	}
	env, err := readEnv(file)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range d.env() {
		if env[k] != v {
			t.Fatal("credential roundtrip changed", k)
		}
	}
	if databaseConfig(env).Addr != "[::1]:3306" || databaseConfig(env).TLSConfig != "true" {
		t.Fatal("IPv6 or TLS lost")
	}
	if err := installWriteOnce(file, []byte("replacement")); err == nil {
		t.Fatal("config overwritten")
	}
	for _, bad := range []string{"mysql", "sys", "SYS", "INFORMATION_SCHEMA", "information_schema", "name`;DROP DATABASE mysql", "name\nfoo", ""} {
		copy := d
		copy.Name = bad
		if copy.validate() == nil {
			t.Fatal("unsafe database name accepted", bad)
		}
	}
	d.Password = "secret\nHOSTNAME=attacker"
	if d.validate() == nil {
		t.Fatal("newline configuration injection accepted")
	}
}

func installerDatabaseFixture(t *testing.T) installDatabase {
	t.Helper()
	if os.Getenv("STARINDEX_INSTALL_TEST") != "1" {
		t.Skip("set STARINDEX_INSTALL_TEST=1 for isolated create/drop database tests")
	}
	env, err := readEnv("../../../.env")
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconvInstallPort(env["database.hostport"])
	d := installDatabase{Host: env["database.hostname"], Port: port, Name: "starindex_install_test_" + randomHex(6), Username: env["database.username"], Password: env["database.password"], Create: true, TLS: env["database.tls"] == "true"}
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		db, err := installDB(ctx, d, false)
		if err != nil {
			t.Error("test database cleanup connection failed")
			return
		}
		defer db.Close()
		if !strings.HasPrefix(d.Name, "starindex_install_test_") {
			t.Fatal("unsafe fixture database")
		}
		if _, err = db.ExecContext(ctx, "DROP DATABASE IF EXISTS `"+d.Name+"`"); err != nil {
			t.Error("isolated database cleanup failed")
		}
	})
	return d
}

func strconvInstallPort(s string) (int, error) {
	if s == "" {
		return 3306, nil
	}
	return strconv.Atoi(s)
}

func installTestRequest(d installDatabase) installRequest {
	return installRequest{Database: d, SiteName: "安装验收", PublicURL: "http://localhost:8089", AdminUsername: "setup_admin", AdminPassword: "Fixture-password-123", Confirm: "Fixture-password-123"}
}

func TestInstallFreshDatabaseAndRestart(t *testing.T) {
	d := installerDatabaseFixture(t)
	root := installerRoot(t)
	b, err := NewBootstrap(root)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	w := installHTTP(b, "/install/api/database", b.token, Row{"database": d})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Preflight does not create the database.
	db, err := installDB(context.Background(), d, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err = db.QueryRow("SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name=?", d.Name).Scan(&n); err != nil || n != 0 {
		t.Fatal("probe created a database", err)
	}
	w = installHTTP(b, "/install/api/complete", b.token, installTestRequest(d))
	if w.Code != 200 || !b.Installed() {
		t.Fatal(w.Code, w.Body.String())
	}
	a := b.current.Load()
	u, err := one(a.DB, "SELECT * FROM ts_admins")
	if err != nil || str(u["username"]) != "setup_admin" || !boolean(u["is_super"]) || !passwordVerify(str(u["password_hash"]), "Fixture-password-123") {
		t.Fatal("admin was not securely created", err)
	}
	if count, _ := cCount(a.DB, "SELECT COUNT(*) FROM ts_users"); count != 0 {
		t.Fatal("installer created ordinary users")
	}
	if count, _ := cCount(a.DB, "SELECT COUNT(*) FROM ts_resources"); count != 0 {
		t.Fatal("installer inserted dummy resources")
	}
	access, err := a.AccessFor(integer(u["id"]))
	if err != nil || !access.Super || !access.Has("access.manage") || len(access.Fields) < 10 {
		t.Fatal("RBAC not initialized", err)
	}
	for _, file := range []string{".env", "install.lock", ".commerce.key", ".bot-token.key"} {
		st, err := os.Stat(filepath.Join(root, file))
		if err != nil || st.Mode().Perm() != 0600 {
			t.Fatal("unsafe configuration permissions", file)
		}
	}
	for _, file := range []string{installStateFile, ".install-token", "本地登录信息-Go.txt"} {
		if _, err := os.Stat(filepath.Join(root, file)); !os.IsNotExist(err) {
			t.Fatal("secret or temporary file retained", file)
		}
	}
	// A completion response replay cannot mutate admin identity or settings.
	w = installHTTP(b, "/install/api/complete", b.token, installTestRequest(d))
	if w.Code != 409 {
		t.Fatal("reinstall allowed")
	}
	login := httptest.NewRequest("POST", "http://localhost:8089/admin/api/login", strings.NewReader(`{"username":"setup_admin","password":"Fixture-password-123"}`))
	login.Header.Set("Origin", "http://localhost:8089")
	login.RemoteAddr = "127.0.0.1:20000"
	lw := httptest.NewRecorder()
	b.ServeHTTP(lw, login)
	if lw.Code != 200 || len(lw.Result().Cookies()) != 1 {
		t.Fatal("new administrator cannot log in", lw.Code, lw.Body.String())
	}
	proxyLogin := httptest.NewRequest("POST", "http://search.example.com/admin/api/login", strings.NewReader(`{"username":"setup_admin","password":"Fixture-password-123"}`))
	proxyLogin.Header.Set("Origin", "https://search.example.com")
	proxyLogin.Header.Set("X-Forwarded-Proto", "https")
	proxyLogin.RemoteAddr = "127.0.0.1:20001"
	pw := httptest.NewRecorder()
	b.ServeHTTP(pw, proxyLogin)
	if pw.Code != 200 || len(pw.Result().Cookies()) != 1 || !pw.Result().Cookies()[0].Secure {
		t.Fatal("HTTPS reverse proxy login did not set a secure cookie", pw.Code)
	}
	// Restart requires no installation code and serves the existing database.
	b.Close()
	restarted, err := NewBootstrap(root)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if !restarted.Installed() {
		t.Fatal("restart reopened installer")
	}
	hw := httptest.NewRecorder()
	restarted.ServeHTTP(hw, httptest.NewRequest("GET", "/healthz", nil))
	if hw.Code != 200 {
		t.Fatal("restarted service unhealthy")
	}
}

func TestInstallRefusesPopulatedDatabase(t *testing.T) {
	d := installerDatabaseFixture(t)
	db, err := installDB(context.Background(), d, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE DATABASE `" + d.Name + "`"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE `" + d.Name + "`.valuable_data(id INT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	b, err := NewBootstrap(installerRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, path := range []string{"database", "complete"} {
		w := installHTTP(b, "/install/api/"+path, b.token, installTestRequest(d))
		if w.Code != 409 {
			t.Fatal("existing data was accepted", path, w.Code, w.Body.String())
		}
	}
	var n int
	if err = db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=?", d.Name).Scan(&n); err != nil || n != 1 {
		t.Fatal("existing schema modified", err)
	}
	if _, err := os.Stat(filepath.Join(b.Root, ".env")); !os.IsNotExist(err) {
		t.Fatal("config written after refused installation")
	}
}

func TestInstallResumeAfterConfigurationWriteFailure(t *testing.T) {
	d := installerDatabaseFixture(t)
	root := installerRoot(t)
	b, err := NewBootstrap(root)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a conflicting local config appearing while installation runs.
	if err = os.WriteFile(filepath.Join(root, ".env"), []byte("do not overwrite"), 0600); err != nil {
		t.Fatal(err)
	}
	w := installHTTP(b, "/install/api/complete", b.token, installTestRequest(d))
	if w.Code != 409 || b.Installed() {
		t.Fatal("unsafe activation", w.Code)
	}
	p, err := b.readPlan()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, installStateFile))
	if strings.Contains(string(data), "Fixture-password-123") {
		t.Fatal("plain administrator password journaled")
	}
	if err = os.Remove(filepath.Join(root, ".env")); err != nil {
		t.Fatal(err)
	} // isolated fixture only
	b.Close()
	b, err = NewBootstrap(root)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	w = installHTTP(b, "/install/api/resume", b.token, Row{})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var count int
	if err = b.current.Load().DB.QueryRow("SELECT COUNT(*) FROM ts_admins").Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate admin on resume", err)
	}
	row, _ := one(b.current.Load().DB, "SELECT * FROM ts_installation")
	if str(row["installation_id"]) != p.ID {
		t.Fatal("resume changed installation ownership")
	}
}

func TestInstallConcurrentDeploymentsOneOwner(t *testing.T) {
	d := installerDatabaseFixture(t)
	left, err := NewBootstrap(installerRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	right, err := NewBootstrap(installerRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for _, b := range []*Bootstrap{left, right} {
		wg.Add(1)
		go func(b *Bootstrap) {
			defer wg.Done()
			codes <- installHTTP(b, "/install/api/complete", b.token, installTestRequest(d)).Code
		}(b)
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatal("concurrent installation ownership", counts)
	}
}

func TestInstallResumePartialSchema(t *testing.T) {
	d := installerDatabaseFixture(t)
	root := installerRoot(t)
	b, err := NewBootstrap(root)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "database/migrations/006_commerce.sql")
	original, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte("INVALID MIGRATION FIXTURE"), 0600); err != nil {
		t.Fatal(err)
	}
	w := installHTTP(b, "/install/api/complete", b.token, installTestRequest(d))
	if w.Code != 503 || b.Installed() {
		t.Fatal("failed DDL exposed application", w.Code)
	}
	db, err := installDB(context.Background(), d, true)
	if err != nil {
		t.Fatal(err)
	}
	var admins int
	if err = db.QueryRow("SELECT COUNT(*) FROM ts_admins").Scan(&admins); err != nil || admins != 0 {
		t.Fatal("admin created before schema was complete", err)
	}
	db.Close()
	b.Close()
	if err = os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	b, err = NewBootstrap(root)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	w = installHTTP(b, "/install/api/resume", b.token, Row{})
	if w.Code != 200 || !b.Installed() {
		t.Fatal("partial schema did not resume", w.Code, w.Body.String())
	}
}
