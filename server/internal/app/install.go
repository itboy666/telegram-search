package app

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
)

const installStateFile = ".install-state.json"

// Bootstrap serves only the installer until a complete configuration is durable.
// An existing .env always selects normal startup (even when its DB is offline).
// A pending installation journal takes precedence so interrupted DDL is resumable.
type Bootstrap struct {
	Root       string
	current    atomic.Pointer[App]
	ready      chan struct{}
	lease      *os.File
	token      string
	mu         sync.Mutex
	authMu     sync.Mutex
	badAuth    int
	authWindow time.Time
}

type installDatabase struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Name     string `json:"name"`
	Username string `json:"username"`
	Password string `json:"password"`
	TLS      bool   `json:"tls"`
	Create   bool   `json:"create_database"`
}

type installRequest struct {
	Database      installDatabase `json:"database"`
	SiteName      string          `json:"site_name"`
	PublicURL     string          `json:"public_url"`
	AdminUsername string          `json:"admin_username"`
	AdminPassword string          `json:"admin_password"`
	Confirm       string          `json:"confirm_password"`
}

// Credentials are local-only, mode 0600; the administrator password is hashed.
// Retrying uses this fixed plan, never a replacement identity or destination DB.
type installPlan struct {
	ID            string          `json:"id"`
	Database      installDatabase `json:"database"`
	SiteName      string          `json:"site_name"`
	PublicURL     string          `json:"public_url"`
	AdminUsername string          `json:"admin_username"`
	PasswordHash  string          `json:"password_hash"`
}

func NewBootstrap(root string) (*Bootstrap, error) {
	b := &Bootstrap{Root: root, ready: make(chan struct{})}
	pending, err := installFileExists(root, installStateFile)
	if err != nil {
		return nil, err
	}
	configured, err := installFileExists(root, ".env")
	if err != nil {
		return nil, err
	}
	if configured && !pending {
		a, err := New(root)
		if err != nil {
			return nil, errors.New("已有配置无法启动，请检查数据库与 .env；安装入口保持关闭。")
		}
		b.activate(a)
		return b, nil
	}
	locked, err := installFileExists(root, "install.lock")
	if err != nil {
		return nil, err
	}
	if locked && !pending {
		return nil, errors.New("安装已锁定但 .env 缺失，请恢复原配置文件。")
	}
	f, err := os.OpenFile(filepath.Join(root, ".install-run.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("安装目录不可写，请为运行用户授予项目目录写权限。")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("另一个安装进程正在运行，请仅启动一个安装实例。")
	}
	b.lease = f
	if pending {
		if _, err = b.readPlan(); err != nil {
			b.Close()
			return nil, err
		}
	}
	path := filepath.Join(root, ".install-token")
	token, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		token = []byte(randomHex(32))
		err = installWriteOnce(path, token)
	}
	if err != nil || !regexp.MustCompile(`^[a-f0-9]{64}$`).Match(token) {
		b.Close()
		return nil, errors.New("安装码无法读取或已损坏，请检查 .install-token 文件。")
	}
	b.token = string(token)
	return b, nil
}

func installFileExists(root, name string) (bool, error) {
	s, err := os.Lstat(filepath.Join(root, name))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil || !s.Mode().IsRegular() {
		return false, errors.New("安装文件无法安全读取，请检查配置文件权限和类型。")
	}
	return true, nil
}

func (b *Bootstrap) activate(a *App) {
	a.Register()
	b.current.Store(a)
	close(b.ready)
}

func (b *Bootstrap) WaitReady(ctx context.Context) *App {
	select {
	case <-ctx.Done():
		return nil
	case <-b.ready:
		return b.current.Load()
	}
}

func (b *Bootstrap) Installed() bool { return b.current.Load() != nil }

// Called only after the HTTP server and workers have stopped.
func (b *Bootstrap) Close() {
	if a := b.current.Load(); a != nil {
		a.DB.Close()
	}
	if b.lease != nil {
		_ = syscall.Flock(int(b.lease.Fd()), syscall.LOCK_UN)
		b.lease.Close()
		b.lease = nil
	}
}

func (b *Bootstrap) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if strings.HasPrefix(r.URL.Path, "/install") {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	}
	if r.URL.Path == "/install/api/status" && r.Method == "GET" {
		JSON(w, Row{"installed": b.Installed()})
		return
	}
	if a := b.current.Load(); a != nil {
		if strings.HasPrefix(r.URL.Path, "/install/api/") {
			Fail(w, 409, "系统已经安装，安装接口已锁定。")
		} else if r.URL.Path == "/install" || r.URL.Path == "/install/" {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		} else {
			a.Handler().ServeHTTP(w, r)
		}
		return
	}
	if strings.HasPrefix(r.URL.Path, "/install/api/") {
		b.installAPI(w, r)
		return
	}
	if r.URL.Path == "/healthz" {
		Fail(w, 503, "系统等待初始化，请访问 /install。")
		return
	}
	if strings.Contains(r.URL.Path, "/api/") {
		Fail(w, 503, "系统尚未安装，请访问 /install。")
		return
	}
	if r.URL.Path == "/install" || r.URL.Path == "/install/" || strings.HasPrefix(r.URL.Path, "/build/") || strings.HasPrefix(r.URL.Path, "/icons/") || r.URL.Path == "/favicon.svg" {
		(&App{Root: b.Root}).static(w, r)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		Fail(w, 405, "请求方法不支持")
		return
	}
	http.Redirect(w, r, "/install", http.StatusSeeOther)
}

func (b *Bootstrap) authorized(w http.ResponseWriter, r *http.Request) bool {
	if !sameOrigin(r) {
		Fail(w, 403, "请在当前域名的安装页面提交。")
		return false
	}
	remote := net.ParseIP(clientIP(r))
	host := r.URL.Hostname()
	if host == "" {
		u, _ := url.Parse("http://" + r.Host)
		if u != nil {
			host = u.Hostname()
		}
	}
	hostIP := net.ParseIP(host)
	local := remote != nil && remote.IsLoopback() && (host == "localhost" || (hostIP != nil && hostIP.IsLoopback()))
	if !requestHTTPS(r) && !local {
		Fail(w, 403, "请配置 HTTPS 后安装；本机 localhost 调试可使用 HTTP。")
		return false
	}
	b.authMu.Lock()
	defer b.authMu.Unlock()
	if time.Since(b.authWindow) >= time.Minute {
		b.authWindow, b.badAuth = time.Now(), 0
	}
	valid := len(r.Header.Values("X-Install-Token")) == 1 && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Install-Token")), []byte(b.token)) == 1
	if !valid {
		b.badAuth++
		if b.badAuth > 30 {
			w.Header().Set("Retry-After", "60")
			Fail(w, 429, "安装码校验过于频繁，请一分钟后重试。")
		} else {
			Fail(w, 403, "安装码不正确，请读取服务器项目目录中的 .install-token 文件。")
		}
		return false
	}
	return true
}

func (b *Bootstrap) installAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		Fail(w, 405, "请使用 POST 提交。")
		return
	}
	if !b.authorized(w, r) {
		return
	}
	if !b.mu.TryLock() {
		Fail(w, 409, "安装任务正在处理，请稍后检查结果。")
		return
	}
	defer b.mu.Unlock()
	if b.Installed() {
		Fail(w, 409, "系统已经安装，安装接口已锁定。")
		return
	}
	switch r.URL.Path {
	case "/install/api/environment":
		checks, ready := b.environment()
		pending, err := installFileExists(b.Root, installStateFile)
		if err != nil {
			Error(w, err)
			return
		}
		JSON(w, Row{"checks": checks, "ready": ready, "resumable": pending})
	case "/install/api/database":
		var req installRequest
		if err := installDecode(r, &req); err != nil {
			Error(w, err)
			return
		}
		if err := req.Database.validate(); err != nil {
			Error(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		result, err := b.probeDatabase(ctx, req.Database)
		if err != nil {
			Error(w, err)
			return
		}
		JSON(w, result)
	case "/install/api/complete", "/install/api/resume":
		_, ready := b.environment()
		if !ready {
			Fail(w, 422, "环境检查尚未通过，请修复后重试。")
			return
		}
		pending, err := installFileExists(b.Root, installStateFile)
		if err != nil {
			Error(w, err)
			return
		}
		var plan *installPlan
		if pending {
			plan, err = b.readPlan()
		} else if r.URL.Path == "/install/api/resume" {
			err = &DomainError{Status: 409, Message: "没有需要恢复的安装任务。"}
		} else {
			var req installRequest
			if err = installDecode(r, &req); err == nil {
				err = req.validate(r)
			}
			if err == nil {
				var pw string
				pw, err = passwordHash(req.AdminPassword)
				plan = &installPlan{ID: randomHex(32), Database: req.Database, SiteName: strings.TrimSpace(req.SiteName), PublicURL: strings.TrimRight(req.PublicURL, "/"), AdminUsername: req.AdminUsername, PasswordHash: pw}
			}
		}
		if err != nil {
			Error(w, err)
			return
		}
		// The durable plan makes a client disconnect safe to retry; no cleartext
		// administrator password is stored or returned.
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		if err = b.performInstall(ctx, plan); err != nil {
			Error(w, err)
			return
		}
		JSON(w, Row{"installed": true, "admin_url": "/admin/login", "site_url": "/"})
	default:
		Fail(w, 404, "安装接口不存在。")
	}
}

func installDecode(r *http.Request, out any) error {
	d := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 16<<10))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return &DomainError{Status: 422, Message: "安装参数格式不正确。"}
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return &DomainError{Status: 422, Message: "安装参数格式不正确。"}
	}
	return nil
}

func (d installDatabase) env() map[string]string {
	return map[string]string{"database.hostname": d.Host, "database.hostport": strconv.Itoa(d.Port), "database.database": d.Name, "database.username": d.Username, "database.password": d.Password, "database.tls": strconv.FormatBool(d.TLS)}
}

func (d installDatabase) validate() error {
	fields := map[string]string{}
	if !regexp.MustCompile(`^[a-zA-Z0-9_.:\-]{1,253}$`).MatchString(d.Host) {
		fields["host"] = "请输入数据库主机名或 IP，不含协议和端口。"
	}
	if d.Port < 1 || d.Port > 65535 {
		fields["port"] = "端口须为 1–65535。"
	}
	name := strings.ToLower(d.Name)
	if !regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_\-]{0,63}$`).MatchString(d.Name) || strings.HasPrefix(name, "mysql") || name == "sys" || name == "information_schema" || name == "performance_schema" {
		fields["name"] = "请使用独立业务库名，以字母开头，仅含字母、数字、下划线或短横线。"
	}
	if len(d.Username) == 0 || len(d.Username) > 128 || strings.ContainsAny(d.Username, "\x00\r\n") {
		fields["username"] = "请填写有效的数据库用户。"
	}
	if len(d.Password) > 1024 || strings.ContainsAny(d.Password, "\x00\r\n") {
		fields["password"] = "数据库密码过长或含换行符。"
	}
	if len(fields) > 0 {
		return &DomainError{Status: 422, Message: "请检查数据库配置。", Fields: fields}
	}
	return nil
}

func (req installRequest) validate(r *http.Request) error {
	if err := req.Database.validate(); err != nil {
		return err
	}
	fields := map[string]string{}
	if n := utf8.RuneCountInString(strings.TrimSpace(req.SiteName)); n < 1 || n > 80 || strings.ContainsAny(req.SiteName, "\x00\r\n") {
		fields["site_name"] = "站点名称须为 1–80 个字符。"
	}
	scheme := "http"
	if requestHTTPS(r) {
		scheme = "https"
	}
	if strings.TrimRight(req.PublicURL, "/") != scheme+"://"+r.Host {
		fields["public_url"] = "网站地址须为当前安装页面的域名与协议。"
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9_]{4,32}$`).MatchString(req.AdminUsername) {
		fields["admin_username"] = "管理员账号须为 4–32 位字母、数字或下划线。"
	}
	if len(req.AdminPassword) < 10 || len(req.AdminPassword) > 72 {
		fields["admin_password"] = "密码须为 10–72 字节。"
	}
	if req.Confirm != req.AdminPassword {
		fields["confirm_password"] = "两次密码不一致。"
	}
	if len(fields) > 0 {
		return &DomainError{Status: 422, Message: "请检查站点和管理员信息。", Fields: fields}
	}
	return nil
}

func (b *Bootstrap) environment() ([]Row, bool) {
	checks := []Row{}
	ready := true
	add := func(key, label string, ok bool, detail string) {
		checks = append(checks, Row{"key": key, "label": label, "ok": ok, "detail": detail})
		ready = ready && ok
	}
	_, err := os.Stat(filepath.Join(b.Root, "public/build/index.html"))
	add("frontend", "前端页面", err == nil, "需要 public/build 中的完整前端构建产物")
	_, err = os.Stat(filepath.Join(b.Root, "public/icons/telegram-ios/check.svg"))
	add("icons", "Telegram iOS 图标", err == nil, "需要 public/icons/telegram-ios 图标目录")
	ok := true
	for _, file := range migrationFiles {
		if _, err := os.ReadFile(filepath.Join(b.Root, "database/migrations", file)); err != nil {
			ok = false
		}
	}
	add("migrations", "数据库初始化文件", ok, "需要 database/migrations 中的完整迁移文件")
	f, err := os.CreateTemp(b.Root, ".install-write-*")
	if err == nil {
		name := f.Name()
		err = f.Close()
		_ = os.Remove(name)
	}
	add("config", "配置写入权限", err == nil, "运行用户需要写入配置文件与安装锁")
	err = os.MkdirAll(filepath.Join(b.Root, "runtime"), 0700)
	if err == nil {
		f, err = os.CreateTemp(filepath.Join(b.Root, "runtime"), "install-check-*")
		if err == nil {
			name := f.Name()
			err = f.Close()
			_ = os.Remove(name)
		}
	}
	add("runtime", "运行目录权限", err == nil, "运行用户需要写入 runtime 目录")
	return checks, ready
}

func installDB(ctx context.Context, d installDatabase, selectDB bool) (*sql.DB, error) {
	env := d.env()
	if !selectDB {
		env["database.database"] = ""
	}
	c := databaseConfig(env)
	c.Timeout, c.ReadTimeout, c.WriteTimeout = 5*time.Second, 20*time.Second, 20*time.Second
	connector, err := mysql.NewConnector(c)
	if err != nil {
		return nil, &DomainError{Status: 422, Message: "数据库连接配置无效。"}
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(3)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, &DomainError{Status: 422, Message: "无法连接 MySQL，请检查地址、端口、账号密码、授权与 TLS 设置。"}
	}
	return db, nil
}

func installMySQLVersion(ctx context.Context, db *sql.DB) (string, error) {
	var version, comment string
	if err := db.QueryRowContext(ctx, "SELECT VERSION(), @@version_comment").Scan(&version, &comment); err != nil {
		return "", err
	}
	major, err := strconv.Atoi(strings.Split(version, ".")[0])
	if err != nil || major < 8 || strings.Contains(strings.ToLower(version+comment), "mariadb") {
		return "", &DomainError{Status: 422, Message: "需要 MySQL 8.0 或更新版本，当前不支持 MariaDB。"}
	}
	return version, nil
}

func (b *Bootstrap) probeDatabase(ctx context.Context, d installDatabase) (Row, error) {
	db, err := installDB(ctx, d, false)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	version, err := installMySQLVersion(ctx, db)
	if err != nil {
		return nil, err
	}
	var exists int
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name=?", d.Name).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		if !d.Create {
			return nil, &DomainError{Status: 422, Message: "数据库不存在或当前账号无权访问，请先创建空库并授权，或勾选自动创建。"}
		}
		return Row{"version": version, "create_required": true, "message": "连接成功。安装时将尝试创建独立数据库，账号需要 CREATE 权限。"}, nil
	}
	var tables int
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=?", d.Name).Scan(&tables); err != nil {
		return nil, err
	}
	if tables > 0 {
		return nil, &DomainError{Status: 409, Message: "目标数据库已有数据表，请选择空数据库。安装程序不会清空或覆盖现有数据。"}
	}
	return Row{"version": version, "create_required": false, "message": "连接成功，目标数据库为空，可以继续。"}, nil
}

func (b *Bootstrap) readPlan() (*installPlan, error) {
	data, err := os.ReadFile(filepath.Join(b.Root, installStateFile))
	var p installPlan
	if err != nil || json.Unmarshal(data, &p) != nil || len(p.ID) != 64 || p.Database.validate() != nil || p.PasswordHash == "" {
		return nil, errors.New("安装恢复文件无法读取，请恢复 .install-state.json，勿直接清空数据库。")
	}
	return &p, nil
}

// Atomic no-clobber publication: a failed or competing install never truncates
// an existing .env, journal, token or completion lock.
func installWriteOnce(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".install-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Link(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func installEnsureFile(path string, data []byte) error {
	existing, err := os.ReadFile(path)
	if err == nil {
		if string(existing) == string(data) {
			return nil
		}
		return &DomainError{Status: 409, Message: "已有配置与本次安装不一致，已停止以避免覆盖。请核对服务器配置后继续原安装任务。"}
	}
	if !os.IsNotExist(err) {
		return err
	}
	return installWriteOnce(path, data)
}

func installEnv(d installDatabase) []byte {
	return []byte(fmt.Sprintf("APP_DEBUG = false\n\n[DATABASE]\nTYPE = mysql\nHOSTNAME = \"%s\"\nHOSTPORT = %d\nDATABASE = \"%s\"\nUSERNAME = \"%s\"\nPASSWORD = \"%s\"\nTLS = %t\nCHARSET = utf8mb4\nPREFIX = ts_\nDEBUG = false\n\n[TELEGRAM]\nTOKEN =\nPOLL_TIMEOUT = 20\n", d.Host, d.Port, d.Name, d.Username, d.Password, d.TLS))
}

func (b *Bootstrap) performInstall(ctx context.Context, p *installPlan) error {
	db, err := installDB(ctx, p.Database, false)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err = installMySQLVersion(ctx, db); err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	lock := "starindex-install:" + hash(strings.ToLower(p.Database.Name))[:40]
	var acquired int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", lock).Scan(&acquired); err != nil {
		return err
	}
	if acquired != 1 {
		return &DomainError{Status: 409, Message: "目标数据库正在被另一个安装任务使用，请稍后重试。"}
	}
	defer conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", lock)
	if p.Database.Create {
		if _, err = conn.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS `"+p.Database.Name+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
			return &DomainError{Status: 422, Message: "数据库创建失败，请检查 CREATE 权限，或先手动创建并授权空数据库。"}
		}
	}
	if _, err = conn.ExecContext(ctx, "USE `"+p.Database.Name+"`"); err != nil {
		return &DomainError{Status: 422, Message: "无法访问目标数据库，请检查数据库名称与权限。"}
	}
	var tables, marker int
	if err = conn.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(table_name='ts_installation'),0) FROM information_schema.tables WHERE table_schema=DATABASE()").Scan(&tables, &marker); err != nil {
		return err
	}
	if tables > 0 {
		var owner string
		if marker == 1 {
			err = conn.QueryRowContext(ctx, "SELECT installation_id FROM ts_installation WHERE id=1").Scan(&owner)
		}
		// A crash directly between CREATE TABLE and INSERT can leave only an
		// empty marker. The durable local journal must exist before either.
		pending, _ := installFileExists(b.Root, installStateFile)
		owned := owner == p.ID || (marker == 1 && tables == 1 && errors.Is(err, sql.ErrNoRows) && pending)
		if !owned {
			return &DomainError{Status: 409, Message: "目标数据库不是本次安装创建的空库，已停止以保护现有数据。"}
		}
	}
	data, _ := json.Marshal(p)
	if err = installEnsureFile(filepath.Join(b.Root, installStateFile), data); err != nil {
		return err
	}
	ddl, err := os.ReadFile(filepath.Join(b.Root, "database/migrations/013_installation.sql"))
	if err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, string(ddl)); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, "INSERT IGNORE INTO ts_installation(id,installation_id,created_at) VALUES(1,?,?)", p.ID, now()); err != nil {
		return err
	}
	target, err := installDB(ctx, p.Database, true)
	if err != nil {
		return err
	}
	defer target.Close()
	a := appWithDatabase(b.Root, p.Database.env(), target)
	if err = a.migrate(ctx, false); err != nil {
		return &DomainError{Status: 503, Message: "数据库初始化未完成，请检查数据库的建表、索引及修改表权限，然后继续安装。已完成的步骤会保留。"}
	}
	if err = installIdentity(ctx, a, p); err != nil {
		return err
	}
	for _, file := range []string{".bot-token.key", ".commerce.key"} {
		if _, err = secretKey(b.Root, file, true); err != nil {
			return err
		}
	}
	if err = installEnsureFile(filepath.Join(b.Root, ".env"), installEnv(p.Database)); err != nil {
		return err
	}
	if err = installEnsureFile(filepath.Join(b.Root, "install.lock"), []byte("StarIndex installed\n"+p.ID+"\n")); err != nil {
		return err
	}
	// Prove the durable configuration can be reopened before exposing any API.
	live, err := New(b.Root)
	if err != nil {
		return err
	}
	if err = os.Remove(filepath.Join(b.Root, installStateFile)); err != nil {
		live.DB.Close()
		return err
	}
	_ = os.Remove(filepath.Join(b.Root, ".install-token"))
	b.activate(live)
	return nil
}

func installIdentity(ctx context.Context, a *App, p *installPlan) error {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner string
	var installed sql.NullTime
	if err = tx.QueryRowContext(ctx, "SELECT installation_id,installed_at FROM ts_installation WHERE id=1 FOR UPDATE").Scan(&owner, &installed); err != nil {
		return err
	}
	if owner != p.ID {
		return errors.New("安装记录不匹配。")
	}
	if installed.Valid {
		return tx.Commit()
	}
	var admins int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM ts_admins").Scan(&admins); err != nil {
		return err
	}
	if admins > 0 {
		return &DomainError{Status: 409, Message: "数据库已存在管理员，安装程序不会重置账号。"}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO ts_admins(username,password_hash,display_name,is_super,status,created_at) VALUES(?,?,'系统管理员',1,1,?)", p.AdminUsername, p.PasswordHash, now()); err != nil {
		return err
	}
	for key, val := range map[string]string{"site_name": p.SiteName, "public_api_enabled": "0", "submissions_enabled": "1", "bot_page_size": "12"} {
		if _, err = tx.ExecContext(ctx, "INSERT INTO ts_settings(name,value) VALUES(?,?)", key, val); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE ts_commerce_config SET config=?,updated_at=? WHERE id=1", encode(Row{"public_url": p.PublicURL}), now()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE ts_installation SET installed_at=? WHERE id=1", now()); err != nil {
		return err
	}
	return tx.Commit()
}
