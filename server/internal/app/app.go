package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/go-sql-driver/mysql"
)

type Row = map[string]any
type Queryer interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

type App struct {
	DB                    *sql.DB
	Root                  string
	Mux                   *http.ServeMux
	Env                   map[string]string
	HTTP                  *http.Client
	TelegramCall          func(context.Context, string, Row) (Row, error)
	TelegramProofDownload func(context.Context, int64, string) ([]byte, error)
}

var location, _ = time.LoadLocation("Asia/Shanghai")

func now() string { return time.Now().In(location).Format("2006-01-02 15:04:05") }
func future(d time.Duration) string {
	return time.Now().In(location).Add(d).Format("2006-01-02 15:04:05")
}
func expired(v any) bool {
	t, e := time.ParseInLocation("2006-01-02 15:04:05", str(v), location)
	return e != nil || !t.After(time.Now())
}
func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case json.Number:
		return string(x)
	case bool:
		if x {
			return "1"
		}
		return "0"
	case time.Time:
		return x.In(location).Format("2006-01-02 15:04:05")
	default:
		return fmt.Sprint(v)
	}
}
func integer(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	case bool:
		if x {
			return 1
		}
		return 0
	}
	n, _ := strconv.ParseInt(str(v), 10, 64)
	return n
}
func boolean(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	}
	s := strings.ToLower(str(v))
	return s == "true" || s == "1"
}
func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func randomHex(n int) string {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func object(v any) Row {
	if x, ok := v.(map[string]any); ok {
		return x
	}
	return Row{}
}
func array(v any) []any {
	if x, ok := v.([]any); ok {
		return x
	}
	return []any{}
}
func decode(s string) Row {
	m := Row{}
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	_ = d.Decode(&m)
	return m
}
func encode(v any) string { b, _ := json.Marshal(v); return string(b) }
func query(db Queryer, statement string, args ...any) ([]Row, error) {
	rs, e := db.Query(statement, args...)
	if e != nil {
		return nil, e
	}
	defer rs.Close()
	cols, e := rs.Columns()
	if e != nil {
		return nil, e
	}
	out := []Row{}
	for rs.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if e = rs.Scan(ptrs...); e != nil {
			return nil, e
		}
		m := Row{}
		for i, k := range cols {
			v := values[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			if t, ok := v.(time.Time); ok {
				v = t.In(location).Format("2006-01-02 15:04:05")
			}
			m[k] = v
		}
		out = append(out, m)
	}
	return out, rs.Err()
}
func one(db Queryer, statement string, args ...any) (Row, error) {
	rows, e := query(db, statement, args...)
	if e != nil {
		return nil, e
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}
func JSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(Row{"code": 0, "message": "ok", "data": data})
}
func Fail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Row{"code": status, "message": msg})
}

type DomainError struct {
	Status  int
	Message string
	Fields  map[string]string
}

func (e *DomainError) Error() string { return e.Message }
func Error(w http.ResponseWriter, e error) {
	var d *DomainError
	if errors.As(e, &d) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(d.Status)
		_ = json.NewEncoder(w).Encode(Row{"code": d.Status, "message": d.Message, "errors": d.Fields})
		return
	}
	var me *mysql.MySQLError
	if errors.As(e, &me) && me.Number == 1062 {
		Fail(w, 409, "记录已存在，请勿重复提交。")
		return
	}
	log.Printf("request failed (%T)", e)
	Fail(w, 500, "服务暂时无法完成请求，请稍后重试。")
}
func Body(r *http.Request) (Row, error) {
	d := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 2<<20))
	d.UseNumber()
	var value any
	bad := &DomainError{Status: 422, Message: "请求内容不是有效 JSON 对象。"}
	if e := d.Decode(&value); e != nil {
		return nil, bad
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return nil, bad
	}
	if m, ok := value.(map[string]any); ok && m != nil {
		return m, nil
	}
	// PHP's JSON encoding of an empty action payload was []; keep that
	// transport convention, while rejecting all nonempty arrays and scalars.
	if items, ok := value.([]any); ok && len(items) == 0 {
		return Row{}, nil
	}
	return nil, bad
}

func readEnv(file string) (map[string]string, error) {
	b, e := os.ReadFile(file)
	if e != nil {
		return nil, e
	}
	out := map[string]string{}
	section := ""
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.Trim(line, "[]")) + "."
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		out[section+k] = v
	}
	return out, nil
}
func New(root string) (*App, error) {
	env, e := readEnv(filepath.Join(root, ".env"))
	if e != nil {
		return nil, fmt.Errorf("read local configuration: %w", e)
	}
	db, e := openDatabase(env)
	if e != nil {
		return nil, e
	}
	return appWithDatabase(root, env, db), nil
}

func appWithDatabase(root string, env map[string]string, db *sql.DB) *App {
	return &App{DB: db, Root: root, Env: env, Mux: http.NewServeMux(), HTTP: &http.Client{Timeout: 35 * time.Second}}
}

func databaseConfig(env map[string]string) *mysql.Config {
	c := mysql.NewConfig()
	c.User = env["database.username"]
	c.Passwd = env["database.password"]
	c.Net = "tcp"
	port := env["database.hostport"]
	if port == "" {
		port = "3306"
	}
	host := env["database.hostname"]
	if host == "" {
		host = "127.0.0.1"
	}
	c.Addr = net.JoinHostPort(host, port)
	c.DBName = env["database.database"]
	c.TLSConfig = env["database.tls"]
	c.Loc = location
	c.ParseTime = true
	c.Timeout = 5 * time.Second
	c.ReadTimeout = 30 * time.Second
	c.WriteTimeout = 30 * time.Second
	_ = c.Apply(mysql.Charset("utf8mb4", ""))
	c.Params = map[string]string{"time_zone": "'+08:00'"}
	return c
}

func openDatabase(env map[string]string) (*sql.DB, error) {
	c := databaseConfig(env)
	connector, e := mysql.NewConnector(c)
	if e != nil {
		return nil, errors.New("database configuration invalid")
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e = db.PingContext(ctx); e != nil {
		db.Close()
		return nil, errors.New("database unavailable; verify local .env")
	}
	return db, nil
}
func (a *App) Register() {
	a.RegisterAuth()
	a.RegisterCatalog()
	a.RegisterAccess()
	a.registerDatabaseUpgrade()
	a.RegisterCommerce()
	a.RegisterBotAdmin()
	a.Mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, c := context.WithTimeout(r.Context(), 2*time.Second)
		defer c()
		if a.DB.PingContext(ctx) != nil {
			Fail(w, 503, "数据库暂时不可用")
			return
		}
		JSON(w, Row{"status": "ok", "backend": "go", "database": "mysql", "time": now()})
	})
	a.Mux.HandleFunc("/", a.static)
}
func (a *App) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		if strings.Contains(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		defer func() {
			if recover() != nil {
				log.Print("request recovered from unexpected error")
				Fail(w, 500, "请求处理失败，请稍后重试。")
			}
		}()
		a.Mux.ServeHTTP(w, r)
	})
}
func (a *App) static(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.Contains(r.URL.Path, "/api/") {
		Fail(w, 404, "接口不存在")
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		Fail(w, 405, "请求方法不支持")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/icons/") {
		http.FileServer(http.Dir(filepath.Join(a.Root, "public"))).ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		http.FileServer(http.Dir(filepath.Join(a.Root, "public/build"))).ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/build/") || r.URL.Path == "/favicon.svg" {
		http.FileServer(http.Dir(filepath.Join(a.Root, "public"))).ServeHTTP(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(a.Root, "public/build/index.html"))
}
