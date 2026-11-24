package app

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type FieldAccess struct {
	Read  []string `json:"read"`
	Write []string `json:"write"`
}
type Access struct {
	Super       bool                   `json:"super"`
	Permissions []string               `json:"permissions"`
	Fields      map[string]FieldAccess `json:"fields"`
	Roles       []Row                  `json:"roles"`
}
type Identity struct {
	ID      int64
	User    Row
	Access  *Access
	CSRF    string
	Session Row
}

func (a *Access) Has(p string) bool { return a != nil && slices.Contains(a.Permissions, p) }
func (a *Access) Project(module string, row Row) Row {
	out := Row{}
	if row == nil {
		return nil
	}
	for k, v := range row {
		if k == "id" || slices.Contains(a.Fields[module].Read, k) || (module == "resources" && ((k == "type_label" && slices.Contains(a.Fields[module].Read, "type")) || (k == "category_name" && slices.Contains(a.Fields[module].Read, "category_id")))) {
			out[k] = v
		}
	}
	return out
}
func (a *Access) CheckWrite(module string, body Row) error {
	for k := range body {
		if !slices.Contains(a.Fields[module].Write, k) {
			return &DomainError{Status: 403, Message: "无权修改字段「" + k + "」。", Fields: map[string]string{k: "此字段没有写入权限"}}
		}
	}
	return nil
}
func (a *App) AccessFor(id int64) (*Access, error) {
	u, e := one(a.DB, "SELECT is_super FROM ts_admins WHERE id=? AND status=1", id)
	if e != nil {
		return nil, e
	}
	if u == nil {
		return nil, &DomainError{Status: 403, Message: "管理员账号已停用。"}
	}
	x := &Access{Super: boolean(u["is_super"]), Permissions: []string{}, Fields: map[string]FieldAccess{}, Roles: []Row{}}
	valid := map[string]bool{}
	fieldMap := map[string]map[string]bool{}
	for _, mod := range PermissionCatalog {
		fieldMap[mod.Key] = map[string]bool{}
		for _, act := range mod.Actions {
			key := act.Key
			if !strings.Contains(key, ".") {
				key = mod.Key + "." + key
			}
			valid[key] = true
			if x.Super {
				x.Permissions = append(x.Permissions, key)
			}
		}
		f := FieldAccess{Read: []string{}, Write: []string{}}
		for _, v := range mod.Fields {
			fieldMap[mod.Key][v.Key] = !v.ReadOnly
			if x.Super {
				f.Read = append(f.Read, v.Key)
				if !v.ReadOnly {
					f.Write = append(f.Write, v.Key)
				}
			}
		}
		x.Fields[mod.Key] = f
	}
	if x.Super {
		x.Permissions = append(x.Permissions, "access.manage")
		return x, nil
	}
	x.Roles, e = query(a.DB, "SELECT r.id,r.name FROM ts_roles r JOIN ts_admin_roles ar ON ar.role_id=r.id WHERE ar.admin_id=? AND r.status=1", id)
	if e != nil {
		return nil, e
	}
	perms, e := query(a.DB, "SELECT DISTINCT p.permission FROM ts_role_permissions p JOIN ts_roles r ON r.id=p.role_id JOIN ts_admin_roles ar ON ar.role_id=r.id WHERE ar.admin_id=? AND r.status=1", id)
	if e != nil {
		return nil, e
	}
	for _, p := range perms {
		if valid[str(p["permission"])] {
			x.Permissions = append(x.Permissions, str(p["permission"]))
		}
	}
	fields, e := query(a.DB, "SELECT f.resource,f.field,MAX(f.can_read) AS rd,MAX(f.can_read*f.can_write) AS wr FROM ts_role_fields f JOIN ts_roles r ON r.id=f.role_id JOIN ts_admin_roles ar ON ar.role_id=r.id WHERE ar.admin_id=? AND r.status=1 GROUP BY f.resource,f.field", id)
	if e != nil {
		return nil, e
	}
	for _, r := range fields {
		mod, key := str(r["resource"]), str(r["field"])
		wr, ok := fieldMap[mod][key]
		if !ok {
			continue
		}
		f := x.Fields[mod]
		if boolean(r["rd"]) {
			f.Read = append(f.Read, key)
			if boolean(r["wr"]) && wr {
				f.Write = append(f.Write, key)
			}
		}
		x.Fields[mod] = f
	}
	return x, nil
}
func passwordHash(p string) (string, error) {
	if len(p) < 10 || len(p) > 72 {
		return "", &DomainError{Status: 422, Message: "密码长度须为 10–72 字节。"}
	}
	v, e := bcrypt.GenerateFromPassword([]byte(p), 12)
	return string(v), e
}
func passwordVerify(h, p string) bool {
	if strings.HasPrefix(h, "$2y$") {
		h = "$2a$" + h[4:]
	}
	return bcrypt.CompareHashAndPassword([]byte(h), []byte(p)) == nil
}
func publicUser(r Row) Row {
	out := Row{}
	for _, k := range []string{"id", "username", "display_name", "is_super", "telegram_id", "submission_allowed", "submission_note", "created_at"} {
		if v, ok := r[k]; ok {
			out[k] = v
		}
	}
	out["has_password"] = str(r["password_hash"]) != ""
	return out
}
func tables(scope string) (string, string, string) {
	if scope == "admin" {
		return "ts_admins", "ts_admin_sessions", "admin_id"
	}
	return "ts_users", "ts_sessions", "user_id"
}
func clientIP(r *http.Request) string {
	h, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		return r.RemoteAddr
	}
	return h
}
func sameOrigin(r *http.Request) bool {
	if s := r.Header.Get("Sec-Fetch-Site"); s == "cross-site" {
		return false
	}
	// An Origin request header represents one origin. Reject ambiguous
	// duplicates rather than accepting whichever value a proxy keeps first.
	if len(r.Header.Values("Origin")) > 1 {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	scheme := "http"
	if requestHTTPS(r) {
		scheme = "https"
	}
	return origin == scheme+"://"+r.Host
}

// Only a reverse proxy on this host may supply the original HTTPS scheme.
// Public clients cannot turn a spoofed forwarding header into trusted TLS.
func requestHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	ip := net.ParseIP(clientIP(r))
	return ip != nil && ip.IsLoopback() && len(r.Header.Values("X-Forwarded-Proto")) == 1 && r.Header.Get("X-Forwarded-Proto") == "https"
}
func (a *App) Identify(r *http.Request, scope string, write bool) (*Identity, error) {
	c, e := r.Cookie("starindex_" + scope)
	if e != nil || len(c.Value) != 64 {
		return nil, &DomainError{Status: 401, Message: "请先登录后继续。"}
	}
	table, st, fk := tables(scope)
	q := "SELECT * FROM " + st + " WHERE token_hash=? AND expires_at>?"
	if scope == "user" {
		q += " AND scope='user'"
	}
	s, e := one(a.DB, q, hash(c.Value), now())
	if e != nil {
		return nil, e
	}
	if s == nil {
		return nil, &DomainError{Status: 401, Message: "登录已过期，请重新登录。"}
	}
	u, e := one(a.DB, "SELECT * FROM "+table+" WHERE id=? AND status=1", s[fk])
	if e != nil {
		return nil, e
	}
	if u == nil {
		return nil, &DomainError{Status: 403, Message: "当前账号没有访问权限。"}
	}
	if write && (!sameOrigin(r) || subtle.ConstantTimeCompare([]byte(str(s["csrf_token"])), []byte(r.Header.Get("X-CSRF-Token"))) != 1) {
		return nil, &DomainError{Status: 403, Message: "页面验证已失效，请刷新后重试。"}
	}
	id := &Identity{ID: integer(u["id"]), User: u, CSRF: str(s["csrf_token"]), Session: s}
	if scope == "admin" {
		id.Access, e = a.AccessFor(id.ID)
		if e != nil {
			return nil, e
		}
	}
	return id, nil
}
func (a *App) Handle(pattern, scope, permission string, write bool, fn func(http.ResponseWriter, *http.Request, *Identity)) {
	a.Mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		var id *Identity
		var e error
		if scope != "" {
			id, e = a.Identify(r, scope, write)
			if e != nil {
				Error(w, e)
				return
			}
			if permission != "" && !id.Access.Has(permission) {
				Fail(w, 403, "没有此操作的权限，请联系管理员。")
				return
			}
		} else if write && !sameOrigin(r) {
			Fail(w, 403, "请求来源验证失败。")
			return
		}
		fn(w, r, id)
	})
}
func setSessionCookie(w http.ResponseWriter, r *http.Request, scope, token string) {
	http.SetCookie(w, &http.Cookie{Name: "starindex_" + scope, Value: token, Path: "/", MaxAge: 43200, HttpOnly: true, Secure: requestHTTPS(r), SameSite: http.SameSiteLaxMode})
}
func insertSession(tx *sql.Tx, scope string, id int64, token, csrf string) error {
	_, st, fk := tables(scope)
	q := "INSERT INTO " + st + " (" + fk + ",token_hash,csrf_token,expires_at,created_at"
	values := " VALUES (?,?,?,?,?"
	args := []any{id, hash(token), csrf, future(12 * time.Hour), now()}
	if scope == "user" {
		q += ",scope"
		values += ",?"
		args = append(args, "user")
	}
	_, e := tx.Exec(q+")"+values+")", args...)
	return e
}

// Call after locking the user row. This makes sensitive writes linearizable with
// logout, password reset and session revocation, including requests already waiting.
func recheckUserSession(tx *sql.Tx, id *Identity, r *http.Request) error {
	session, e := one(tx, "SELECT * FROM ts_sessions WHERE id=? AND user_id=? AND scope='user' FOR UPDATE", id.Session["id"], id.ID)
	if e != nil {
		return e
	}
	cookie, e := r.Cookie("starindex_user")
	if e != nil || session == nil || expired(session["expires_at"]) || !sameOrigin(r) ||
		subtle.ConstantTimeCompare([]byte(str(session["token_hash"])), []byte(hash(cookie.Value))) != 1 ||
		subtle.ConstantTimeCompare([]byte(str(session["csrf_token"])), []byte(r.Header.Get("X-CSRF-Token"))) != 1 {
		return catalogErr(401, "原登录会话已失效，请重新登录后操作。")
	}
	return nil
}
func (a *App) loginResult(w http.ResponseWriter, r *http.Request, scope string, u Row, token, csrf string) {
	data := Row{"user": publicUser(u), "csrf_token": csrf}
	if scope == "admin" {
		acc, e := a.AccessFor(integer(u["id"]))
		if e != nil {
			Error(w, e)
			return
		}
		data["access"] = acc
	}
	setSessionCookie(w, r, scope, token)
	JSON(w, data)
}
func (a *App) RegisterAuth() {
	for _, scope := range []string{"admin", "user"} {
		a.Handle("POST /"+scope+"/api/login", "", "", true, func(w http.ResponseWriter, r *http.Request, _ *Identity) { a.login(w, r, scope) })
		a.Handle("GET /"+scope+"/api/me", scope, "", false, func(w http.ResponseWriter, r *http.Request, id *Identity) {
			out := Row{"user": publicUser(id.User), "csrf_token": id.CSRF}
			if scope == "admin" {
				out["access"] = id.Access
			}
			JSON(w, out)
		})
		a.Handle("POST /"+scope+"/api/logout", scope, "", true, func(w http.ResponseWriter, r *http.Request, id *Identity) {
			_, st, _ := tables(scope)
			_, e := a.DB.Exec("DELETE FROM "+st+" WHERE id=?", id.Session["id"])
			if e != nil {
				Error(w, e)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "starindex_" + scope, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
			JSON(w, Row{"logged_out": true})
		})
	}
	a.Handle("POST /user/api/register", "", "", true, a.registerUser)
	a.Handle("POST /user/api/bot-login", "", "", true, a.codeLogin)
	a.Handle("POST /user/api/password", "user", "", true, a.changePassword)
	a.RegisterTelegramLogin()
}
func (a *App) login(w http.ResponseWriter, r *http.Request, scope string) {
	if e := a.RateLimit("login:"+scope+":"+clientIP(r), 12, 300); e != nil {
		Error(w, e)
		return
	}
	b, e := Body(r)
	if e != nil {
		Error(w, e)
		return
	}
	table, _, _ := tables(scope)
	u, e := one(a.DB, "SELECT * FROM "+table+" WHERE username=?", strings.TrimSpace(str(b["username"])))
	if e != nil {
		Error(w, e)
		return
	}
	if u == nil || !boolean(u["status"]) || !passwordVerify(str(u["password_hash"]), str(b["password"])) {
		Fail(w, 401, "账号或密码不正确。")
		return
	}
	token, csrf := randomHex(32), randomHex(24)
	_, e = cTx(a, func(tx *sql.Tx) (Row, error) {
		locked, e := one(tx, "SELECT * FROM "+table+" WHERE id=? FOR UPDATE", u["id"])
		if e != nil {
			return nil, e
		}
		if locked == nil || !boolean(locked["status"]) || str(locked["password_hash"]) != str(u["password_hash"]) {
			return nil, catalogErr(401, "账号状态已变化，请重新登录。")
		}
		if e = insertSession(tx, scope, integer(u["id"]), token, csrf); e != nil {
			return nil, e
		}
		_, e = tx.Exec("UPDATE "+table+" SET last_login_at=? WHERE id=?", now(), u["id"])
		return Row{}, e
	})
	if e != nil {
		Error(w, e)
		return
	}
	a.loginResult(w, r, scope, u, token, csrf)
}
func (a *App) registerUser(w http.ResponseWriter, r *http.Request, _ *Identity) {
	if e := a.RateLimit("register:"+clientIP(r), 5, 3600); e != nil {
		Error(w, e)
		return
	}
	b, e := Body(r)
	if e != nil {
		Error(w, e)
		return
	}
	name := strings.TrimSpace(str(b["username"]))
	if !regexp.MustCompile(`^[A-Za-z0-9_]{4,32}$`).MatchString(name) || strings.HasPrefix(strings.ToLower(name), "tg_") {
		Fail(w, 422, "账号须为 4–32 位字母、数字或下划线，不能以 tg_ 开头。")
		return
	}
	pw, e := passwordHash(str(b["password"]))
	if e != nil {
		Error(w, e)
		return
	}
	tx, e := a.DB.Begin()
	if e != nil {
		Error(w, e)
		return
	}
	defer tx.Rollback()
	res, e := tx.Exec("INSERT INTO ts_users(username,display_name,password_hash,created_at) VALUES(?,?,?,?)", name, name, pw, now())
	if e != nil {
		Error(w, e)
		return
	}
	id, _ := res.LastInsertId()
	token, csrf := randomHex(32), randomHex(24)
	if e = insertSession(tx, "user", id, token, csrf); e != nil {
		Error(w, e)
		return
	}
	if e = tx.Commit(); e != nil {
		Error(w, e)
		return
	}
	u, _ := one(a.DB, "SELECT * FROM ts_users WHERE id=?", id)
	a.loginResult(w, r, "user", u, token, csrf)
}
func (a *App) codeLogin(w http.ResponseWriter, r *http.Request, _ *Identity) {
	if e := a.RateLimit("bot-login:"+clientIP(r), 8, 300); e != nil {
		Error(w, e)
		return
	}
	b, e := Body(r)
	if e != nil {
		Error(w, e)
		return
	}
	code := strings.ToUpper(strings.TrimSpace(str(b["code"])))
	token, csrf := randomHex(32), randomHex(24)
	u, e := cTx(a, func(tx *sql.Tx) (Row, error) {
		c, e := one(tx, "SELECT * FROM ts_bot_login_codes WHERE code_hash=? FOR UPDATE", hash(code))
		if e != nil {
			return nil, e
		}
		if c == nil || c["used_at"] != nil || expired(c["expires_at"]) {
			return nil, catalogErr(401, "登录码已失效，请在机器人私聊中重新发送 /account。")
		}
		u, e := one(tx, "SELECT * FROM ts_users WHERE id=? AND status=1 FOR UPDATE", c["user_id"])
		if e != nil {
			return nil, e
		}
		if u == nil {
			return nil, catalogErr(403, "账号不可用。")
		}
		if _, e = tx.Exec("UPDATE ts_bot_login_codes SET used_at=? WHERE id=?", now(), c["id"]); e != nil {
			return nil, e
		}
		if e = insertSession(tx, "user", integer(u["id"]), token, csrf); e != nil {
			return nil, e
		}
		return u, nil
	})
	if e != nil {
		Error(w, e)
		return
	}
	a.loginResult(w, r, "user", u, token, csrf)
}
func (a *App) changePassword(w http.ResponseWriter, r *http.Request, id *Identity) {
	if e := a.RateLimit("password:"+str(id.ID), 5, 300); e != nil {
		Error(w, e)
		return
	}
	b, e := Body(r)
	if e != nil {
		Error(w, e)
		return
	}
	if str(id.User["password_hash"]) != "" {
		if !passwordVerify(str(id.User["password_hash"]), str(b["current_password"])) {
			Fail(w, 422, "当前密码不正确。")
			return
		}
	} else {
		t, _ := time.ParseInLocation("2006-01-02 15:04:05", str(id.Session["created_at"]), location)
		if time.Since(t) > 15*time.Minute {
			Fail(w, 403, "请通过 Telegram 重新登录后，在 15 分钟内设置密码。")
			return
		}
	}
	pw, e := passwordHash(str(b["new_password"]))
	if e != nil {
		Error(w, e)
		return
	}
	_, e = cTx(a, func(tx *sql.Tx) (Row, error) {
		u, e := one(tx, "SELECT * FROM ts_users WHERE id=? FOR UPDATE", id.ID)
		if e != nil {
			return nil, e
		}
		if u == nil || !boolean(u["status"]) || str(u["password_hash"]) != str(id.User["password_hash"]) {
			return nil, catalogErr(409, "账号已变化，请重新登录。")
		}
		if e = recheckUserSession(tx, id, r); e != nil {
			return nil, e
		}
		if _, e = tx.Exec("UPDATE ts_users SET password_hash=? WHERE id=?", pw, id.ID); e != nil {
			return nil, e
		}
		_, e = tx.Exec("DELETE FROM ts_sessions WHERE user_id=? AND id<>?", id.ID, id.Session["id"])
		return Row{}, e
	})
	if e != nil {
		Error(w, e)
		return
	}
	JSON(w, Row{"updated": true})
}

var _ = errors.Is
