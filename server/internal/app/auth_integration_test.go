package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type authTestSession struct {
	UserID      int64
	Token, CSRF string
}
type authTestReply struct {
	Status  int
	Data    Row
	Cookies []*http.Cookie
}
type authTestFixture struct {
	a    *App
	ip   string
	t    *testing.T
	mu   sync.Mutex
	sent []Row
}

func newAuthFixture(t *testing.T) *authTestFixture {
	t.Helper()
	if os.Getenv("STARINDEX_AUTH_TEST") != "1" {
		t.Skip("set STARINDEX_AUTH_TEST=1 for local isolated Telegram authentication fixtures")
	}
	root := os.Getenv("STARINDEX_ROOT")
	if root == "" {
		_, file, _, _ := runtime.Caller(0)
		root = filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	}
	a, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	f := &authTestFixture{a: a, t: t, ip: fmt.Sprintf("198.18.%d.%d", securityRandom(255), securityRandom(254)+1)}
	a.TelegramCall = func(ctx context.Context, method string, p Row) (Row, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.sent = append(f.sent, Row{"method": method, "params": p})
		return Row{"message_id": 123, "ok": true}, nil
	}
	a.RegisterAuth()
	t.Cleanup(func() {
		for _, prefix := range []string{"tg-challenge:", "tg-poll:", "tg-consume:", "bot-login:", "register:", "login:user:", "login:admin:"} {
			_, _ = a.DB.Exec("DELETE FROM ts_rate_limits WHERE bucket=?", prefix+f.ip)
		}
		a.DB.Close()
	})
	return f
}
func (f *authTestFixture) request(method, path string, body Row, session *authTestSession, headers map[string]string) authTestReply {
	f.t.Helper()
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, "http://starindex.test"+path, bytes.NewReader(data))
	req.RemoteAddr = f.ip + ":12345"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://starindex.test")
	if session != nil {
		req.AddCookie(&http.Cookie{Name: "starindex_user", Value: session.Token})
		req.Header.Set("X-CSRF-Token", session.CSRF)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	f.a.Handler().ServeHTTP(recorder, req)
	result := Row{}
	decoder := json.NewDecoder(recorder.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		f.t.Fatalf("response was not JSON (%d)", recorder.Code)
	}
	return authTestReply{Status: recorder.Code, Data: object(result["data"]), Cookies: recorder.Result().Cookies()}
}
func authExpect(t *testing.T, r authTestReply, status int) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("HTTP status=%d, expected %d", r.Status, status)
	}
}
func (f *authTestFixture) cleanupUser(id int64) {
	for _, table := range []string{"telegram_profiles", "telegram_auth_challenges", "bot_login_codes", "sessions", "favorites"} {
		_, _ = f.a.DB.Exec("DELETE FROM ts_"+table+" WHERE user_id=?", id)
	}
	_, _ = f.a.DB.Exec("DELETE FROM ts_audit_logs WHERE actor_id=0 AND action IN ('用户绑定 Telegram','用户解绑 Telegram') AND detail=?", "用户 ID "+str(id))
	for _, key := range []string{"password:" + str(id), "unbind:" + str(id)} {
		_, _ = f.a.DB.Exec("DELETE FROM ts_rate_limits WHERE bucket=?", key)
	}
	_, _ = f.a.DB.Exec("DELETE FROM ts_users WHERE id=?", id)
}
func (f *authTestFixture) user(password string) int64 {
	f.t.Helper()
	pw := ""
	var err error
	if password != "" {
		pw, err = passwordHash(password)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	res, err := f.a.DB.Exec("INSERT INTO ts_users(username,display_name,password_hash,status,created_at) VALUES(?,'Go auth fixture',?,1,?)", "go_auth_"+randomHex(8), pw, now())
	if err != nil {
		f.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	f.t.Cleanup(func() { f.cleanupUser(id) })
	return id
}
func (f *authTestFixture) session(id int64) authTestSession {
	f.t.Helper()
	s := authTestSession{UserID: id, Token: randomHex(32), CSRF: randomHex(24)}
	_, err := cTx(f.a, func(tx *sql.Tx) (Row, error) { return Row{}, insertSession(tx, "user", id, s.Token, s.CSRF) })
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}
func (f *authTestFixture) telegram() Row {
	f.t.Helper()
	id := int64(9_000_000_000_000) + int64(securityRandom(1_000_000_000))
	f.t.Cleanup(func() {
		rows, _ := query(f.a.DB, "SELECT id FROM ts_users WHERE telegram_id=?", id)
		for _, r := range rows {
			f.cleanupUser(integer(r["id"]))
		}
	})
	return Row{"id": id, "first_name": "Go", "last_name": "登录测试", "username": "go_fixture", "is_bot": false}
}
func (f *authTestFixture) challenge(purpose string, session *authTestSession) Row {
	f.t.Helper()
	response := f.request("POST", "/user/api/telegram/challenges", Row{"purpose": purpose}, session, nil)
	authExpect(f.t, response, 200)
	ch := response.Data
	f.t.Cleanup(func() { _, _ = f.a.DB.Exec("DELETE FROM ts_telegram_auth_challenges WHERE id=?", ch["id"]) })
	return ch
}
func (f *authTestFixture) confirm(ch, profile Row, action string) {
	f.t.Helper()
	u, err := url.Parse(str(ch["url"]))
	if err != nil {
		f.t.Fatal(err)
	}
	token := strings.TrimPrefix(u.Query().Get("start"), "auth_")
	if err = f.a.promptTelegramAuth(context.Background(), integer(profile["id"]), token, profile); err != nil {
		f.t.Fatal(err)
	}
	cb := Row{"id": "test-callback", "data": "auth:" + str(ch["id"]) + ":" + action, "from": profile, "message": Row{"chat": Row{"id": profile["id"]}, "message_id": 123}}
	if err = f.a.confirmTelegramAuth(context.Background(), cb); err != nil {
		f.t.Fatal(err)
	}
}
func (f *authTestFixture) consume(ch Row, session *authTestSession) authTestReply {
	return f.request("POST", "/user/api/telegram/challenges/"+str(ch["id"])+"/consume", Row{"token": ch["token"]}, session, nil)
}
func authSessionFrom(reply authTestReply) authTestSession {
	s := authTestSession{UserID: integer(object(reply.Data["user"])["id"]), CSRF: str(reply.Data["csrf_token"])}
	for _, c := range reply.Cookies {
		if c.Name == "starindex_user" {
			s.Token = c.Value
		}
	}
	return s
}

func TestAuthPHPBcryptCompatibility(t *testing.T) {
	// Generated with PHP password_hash(PASSWORD_BCRYPT); this is not an actual account.
	php := "$2y$04$F8w2BoZ.yEEJ.IaPr2sxkuMVHauHLaFpxckv5xB5IqUpyuRCbBW3K"
	if !passwordVerify(php, "GoMigrationFixture42!") || passwordVerify(php, "wrong") {
		t.Fatal("PHP bcrypt compatibility failed")
	}
}

func TestAuthTelegramLoginLifecycle(t *testing.T) {
	f := newAuthFixture(t)
	profile := f.telegram()
	ch := f.challenge("login", nil)
	authExpect(t, f.consume(ch, nil), 401)
	poll := "/user/api/telegram/challenges/" + str(ch["id"])
	authExpect(t, f.request("GET", poll+"?token="+str(ch["token"]), nil, nil, nil), 404)
	status := f.request("GET", poll, nil, nil, map[string]string{"X-Telegram-Challenge-Token": str(ch["token"])})
	authExpect(t, status, 200)
	if str(status.Data["status"]) != "pending" {
		t.Fatal("challenge not pending")
	}
	f.confirm(ch, profile, "yes")
	reply := f.consume(ch, nil)
	authExpect(t, reply, 200)
	s := authSessionFrom(reply)
	if s.UserID < 1 || s.Token == "" || s.CSRF == "" {
		t.Fatal("missing authenticated session")
	}
	if integer(object(reply.Data["user"])["telegram_id"]) != integer(profile["id"]) {
		t.Fatal("wrong Telegram identity")
	}
	authExpect(t, f.consume(ch, nil), 401)
	authExpect(t, f.request("GET", "/user/api/me", nil, &s, nil), 200)
	f.mu.Lock()
	calls := len(f.sent)
	f.mu.Unlock()
	if calls != 2 {
		t.Fatalf("unexpected fake transport call count %d", calls)
	}
}

func TestAuthTelegramBindOriginalSession(t *testing.T) {
	f := newAuthFixture(t)
	user := f.user("")
	first := f.session(user)
	second := f.session(user)
	other := f.session(f.user(""))
	ch := f.challenge("bind", &first)
	profile := f.telegram()
	f.confirm(ch, profile, "yes")
	authExpect(t, f.consume(ch, &second), 403)
	authExpect(t, f.consume(ch, &other), 403)
	reply := f.consume(ch, &first)
	authExpect(t, reply, 200)
	if !boolean(reply.Data["bound"]) || integer(object(reply.Data["user"])["id"]) != user {
		t.Fatal("binding moved user identity")
	}
	authExpect(t, f.consume(ch, &first), 401)
	next := f.challenge("login", nil)
	f.confirm(next, profile, "yes")
	login := f.consume(next, nil)
	authExpect(t, login, 200)
	if integer(object(login.Data["user"])["id"]) != user {
		t.Fatal("bound Telegram created a different account")
	}
}

func TestAuthTelegramConflictDisabledAndCancelled(t *testing.T) {
	f := newAuthFixture(t)
	profile := f.telegram()
	linked, err := f.a.telegramUser(profile)
	if err != nil {
		t.Fatal(err)
	}
	s := f.session(f.user(""))
	ch := f.challenge("bind", &s)
	f.confirm(ch, profile, "yes")
	authExpect(t, f.consume(ch, &s), 409)
	_, _ = f.a.DB.Exec("UPDATE ts_users SET status=0 WHERE id=?", linked["id"])
	login := f.challenge("login", nil)
	f.confirm(login, profile, "yes")
	authExpect(t, f.consume(login, nil), 403)
	cancelled := f.challenge("login", nil)
	f.confirm(cancelled, f.telegram(), "no")
	authExpect(t, f.consume(cancelled, nil), 401)
	expired := f.challenge("login", nil)
	f.confirm(expired, f.telegram(), "yes")
	_, _ = f.a.DB.Exec("UPDATE ts_telegram_auth_challenges SET expires_at=? WHERE id=?", future(-time.Minute), expired["id"])
	authExpect(t, f.consume(expired, nil), 401)
}

func TestAuthTelegramConcurrentLoginAndConsumption(t *testing.T) {
	f := newAuthFixture(t)
	profile := f.telegram()
	var wg sync.WaitGroup
	var failures atomic.Int64
	ids := sync.Map{}
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, err := f.a.telegramUser(profile)
			if err != nil {
				failures.Add(1)
				return
			}
			ids.Store(integer(u["id"]), true)
		}()
	}
	wg.Wait()
	count := 0
	ids.Range(func(key, value any) bool { count++; return true })
	if failures.Load() != 0 || count != 1 {
		t.Fatalf("concurrent first login failures=%d identities=%d", failures.Load(), count)
	}
	ch := f.challenge("login", nil)
	f.confirm(ch, profile, "yes")
	var won atomic.Int64
	for n := 0; n < 6; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reply := f.consume(ch, nil)
			if reply.Status == 200 {
				won.Add(1)
			} else if reply.Status != 401 {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 1 || failures.Load() != 0 {
		t.Fatalf("challenge consumed %d times; failures=%d", won.Load(), failures.Load())
	}
}

func TestAuthTelegramRevokedSessionAndCSRF(t *testing.T) {
	f := newAuthFixture(t)
	user := f.user("")
	s := f.session(user)
	authExpect(t, f.request("POST", "/user/api/telegram/challenges", Row{"purpose": "login"}, nil, map[string]string{"Origin": "https://evil.example"}), 403)
	authExpect(t, f.request("POST", "/user/api/telegram/challenges", Row{"purpose": "bind"}, &s, map[string]string{"X-CSRF-Token": "wrong"}), 403)
	ch := f.challenge("bind", &s)
	f.confirm(ch, f.telegram(), "yes")
	authExpect(t, f.request("POST", "/user/api/logout", Row{}, &s, nil), 200)
	authExpect(t, f.consume(ch, &s), 401)
	after, _ := one(f.a.DB, "SELECT telegram_id FROM ts_users WHERE id=?", user)
	if integer(after["telegram_id"]) != 0 {
		t.Fatal("revoked session bound an identity")
	}
}

func TestAuthRevocationWhileSensitiveRequestWaits(t *testing.T) {
	f := newAuthFixture(t)
	user := f.user("")
	s := f.session(user)
	req := httptest.NewRequest("POST", "http://starindex.test/user/api/telegram/unbind", nil)
	req.AddCookie(&http.Cookie{Name: "starindex_user", Value: s.Token})
	req.Header.Set("X-CSRF-Token", s.CSRF)
	id, err := f.a.Identify(req, "user", true)
	if err != nil {
		t.Fatal(err)
	}
	// Capture the initially valid identity, then revoke it before a sensitive
	// transaction rechecks it. This is the logout/queued-request race.
	_, _ = f.a.DB.Exec("DELETE FROM ts_sessions WHERE id=?", id.Session["id"])
	_, err = cTx(f.a, func(tx *sql.Tx) (Row, error) {
		if _, e := one(tx, "SELECT id FROM ts_users WHERE id=? FOR UPDATE", user); e != nil {
			return nil, e
		}
		return Row{}, recheckUserSession(tx, id, req)
	})
	if err == nil {
		t.Fatal("revoked in-flight session still authorized")
	}
}

func TestAuthPasswordUnbindAndAccountCode(t *testing.T) {
	f := newAuthFixture(t)
	profile := f.telegram()
	user, err := f.a.telegramUser(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.a.botWebsiteLogin(context.Background(), integer(profile["id"]), user); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	text := str(object(f.sent[len(f.sent)-1]["params"])["text"])
	f.mu.Unlock()
	code := regexp.MustCompile(`<code>([A-F0-9]{20})</code>`).FindStringSubmatch(text)
	if len(code) != 2 {
		t.Fatal("explicit website login did not issue a login code")
	}
	login := f.request("POST", "/user/api/bot-login", Row{"code": code[1]}, nil, nil)
	authExpect(t, login, 200)
	s := authSessionFrom(login)
	authExpect(t, f.request("POST", "/user/api/bot-login", Row{"code": code[1]}, nil, nil), 401)
	other := f.session(integer(user["id"]))
	authExpect(t, f.request("POST", "/user/api/telegram/unbind", Row{"password": "not set"}, &s, nil), 422)
	authExpect(t, f.request("POST", "/user/api/password", Row{"new_password": "GoAuthFixturePassword42!"}, &s, nil), 200)
	authExpect(t, f.request("GET", "/user/api/me", nil, &other, nil), 401)
	authExpect(t, f.request("POST", "/user/api/telegram/unbind", Row{"password": "wrong"}, &s, nil), 422)
	authExpect(t, f.request("POST", "/user/api/telegram/unbind", Row{"password": "GoAuthFixturePassword42!"}, &s, nil), 200)
	f.t.Cleanup(func() { f.cleanupUser(integer(user["id"])) })
	stored, _ := one(f.a.DB, "SELECT telegram_id,password_hash,username FROM ts_users WHERE id=?", user["id"])
	if integer(stored["telegram_id"]) != 0 || !passwordVerify(str(stored["password_hash"]), "GoAuthFixturePassword42!") {
		t.Fatal("unbind damaged website login")
	}
	reply := f.request("POST", "/user/api/login", Row{"username": stored["username"], "password": "GoAuthFixturePassword42!"}, nil, nil)
	authExpect(t, reply, 200)
	recreated, err := f.a.telegramUser(profile)
	if err != nil {
		t.Fatal(err)
	}
	if integer(recreated["id"]) == integer(user["id"]) {
		t.Fatal("unbound account implicitly relinked")
	}
}
