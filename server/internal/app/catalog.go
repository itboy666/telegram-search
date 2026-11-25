package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"golang.org/x/net/idna"
)

var ResourceTypes = Row{"channel": "频道", "group": "群组", "bot": "机器人", "video": "视频", "article": "图文", "audio": "音乐", "file": "文件", "shop": "店铺", "other": "其他", "website": "网站"}
var resourceStatuses = Row{"published": "已发布", "hidden": "已下架", "archived": "已归档", "blocked": "已封禁", "invalid": "已失效"}

type catalogError struct {
	status  int
	message string
}

func (e catalogError) Error() string          { return e.message }
func catalogErr(status int, msg string) error { return &DomainError{Status: status, Message: msg} }
func catalogReply(w http.ResponseWriter, data any, err error) {
	if err == nil {
		JSON(w, data)
		return
	}
	var de *DomainError
	if errors.As(err, &de) {
		Error(w, err)
		return
	}
	var ce catalogError
	if errors.As(err, &ce) {
		Fail(w, ce.status, ce.message)
		return
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) && me.Number == 1062 {
		Fail(w, 409, "记录已存在，请刷新后重试。")
		return
	}
	Fail(w, 500, "数据操作暂时失败，请稍后重试。")
}
func catalogParams(r *http.Request) Row {
	out := Row{}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}
func catalogID(r *http.Request) int64 { return integer(r.PathValue("id")) }
func catalogLimit(v any, def, min, max int64) int64 {
	if v == nil || str(v) == "" {
		return def
	}
	n := integer(v)
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}
func catalogText(v any, max int, required bool) (string, error) {
	s, ok := v.(string)
	if !ok && v != nil {
		return "", catalogErr(422, "文本字段格式不正确。")
	}
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max || (required && s == "") {
		return "", catalogErr(422, fmt.Sprintf("文本须为 %d 字以内%s。", max, map[bool]string{true: "且不能为空", false: ""}[required]))
	}
	return s, nil
}
func catalogFlag(v any) (int, error) {
	s := str(v)
	if s == "0" || s == "false" {
		return 0, nil
	}
	if s == "1" || s == "true" {
		return 1, nil
	}
	return 0, catalogErr(422, "状态值须为启用或停用。")
}
func catalogOnly(body Row, keys string) error {
	allowed := map[string]bool{}
	for _, k := range strings.Fields(keys) {
		allowed[k] = true
	}
	for k := range body {
		if !allowed[k] {
			return catalogErr(422, "请求包含不可修改的字段："+k)
		}
	}
	return nil
}
func catalogMerge(a, b Row) Row {
	out := Row{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
func catalogLike(s string) string {
	return "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(s) + "%"
}
func catalogRecord(db Queryer, table string, id int64, lock bool) (Row, error) {
	q := "SELECT * FROM ts_" + table + " WHERE id=?"
	if lock {
		q += " FOR UPDATE"
	}
	row, err := one(db, q, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, catalogErr(404, "记录不存在或已删除，请刷新列表。")
	}
	return row, nil
}

type catalogExecutor interface {
	Exec(string, ...any) (sql.Result, error)
}

func catalogInsert(db catalogExecutor, table string, data Row) (int64, error) {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := []any{}
	marks := []string{}
	for _, k := range keys {
		args = append(args, data[k])
		marks = append(marks, "?")
	}
	res, err := db.Exec("INSERT INTO ts_"+table+" (`"+strings.Join(keys, "`,`")+"`) VALUES ("+strings.Join(marks, ",")+")", args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
func catalogUpdate(db catalogExecutor, table string, id int64, data Row) error {
	if len(data) == 0 {
		return nil
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sets := []string{}
	args := []any{}
	for _, k := range keys {
		sets = append(sets, "`"+k+"`=?")
		args = append(args, data[k])
	}
	args = append(args, id)
	_, err := db.Exec("UPDATE ts_"+table+" SET "+strings.Join(sets, ",")+" WHERE id=?", args...)
	return err
}
func catalogAudit(db catalogExecutor, actor int64, action, detail string) error {
	var who any = actor
	if actor == 0 {
		who = nil
	}
	rr := []rune(detail)
	if len(rr) > 500 {
		detail = string(rr[:500])
	}
	_, err := db.Exec("INSERT INTO ts_audit_logs(actor_id,action,detail,created_at) VALUES (?,?,?,?)", who, action, detail, now())
	return err
}
func (a *App) Audit(actor int64, action, detail string) error {
	return catalogAudit(a.DB, actor, action, detail)
}
func (a *App) catalogTransaction(fn func(*sql.Tx) (Row, error)) (Row, error) {
	for attempt := 0; attempt < 5; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		// Counts used to guard parent deletions must see children committed while
		// acquiring the parent lock. READ COMMITTED avoids a stale RR snapshot.
		tx, err := a.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			cancel()
			return nil, err
		}
		result, err := func() (Row, error) {
			defer cancel()
			defer tx.Rollback()
			result, err := fn(tx)
			if err != nil {
				return nil, err
			}
			return result, tx.Commit()
		}()
		if err == nil {
			return result, nil
		}
		var me *mysql.MySQLError
		if errors.As(err, &me) && (me.Number == 1213 || me.Number == 1205) && attempt < 4 {
			time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
			continue
		}
		return nil, err
	}
	return nil, catalogErr(409, "数据正在更新，请稍后重试。")
}
func (a *App) Settings() Row {
	out := Row{"site_name": "星索", "site_description": "发现值得收藏的 Telegram 资源", "public_api_enabled": "0", "submissions_enabled": "1"}
	rows, err := query(a.DB, "SELECT name,value FROM ts_settings WHERE name IN ('site_name','site_description','public_api_enabled','submissions_enabled')")
	if err == nil {
		for _, r := range rows {
			out[str(r["name"])] = r["value"]
		}
	}
	return out
}
func (a *App) RateLimit(bucket string, limit, seconds int) error {
	if limit < 1 {
		return catalogErr(429, "请求过于频繁，请稍后重试。")
	}
	_, err := a.catalogTransaction(func(tx *sql.Tx) (Row, error) {
		exp := time.Now().In(location).Add(time.Duration(seconds) * time.Second).Format("2006-01-02 15:04:05")
		_, err := tx.Exec("INSERT INTO ts_rate_limits(bucket,hits,expires_at) VALUES (?,1,?) ON DUPLICATE KEY UPDATE hits=IF(expires_at<=?,1,hits+1),expires_at=IF(expires_at<=?, ?, expires_at)", bucket, exp, now(), now(), exp)
		if err != nil {
			return nil, err
		}
		r, err := one(tx, "SELECT hits FROM ts_rate_limits WHERE bucket=? FOR UPDATE", bucket)
		if err != nil {
			return nil, err
		}
		if integer(r["hits"]) > int64(limit) {
			return nil, catalogErr(429, "请求过于频繁或已达到调用额度，请稍后重试。")
		}
		return Row{}, nil
	})
	return err
}

var telegramPath = regexp.MustCompile(`^/(?:s/)?([A-Za-z0-9_]{4,32})(?:/([0-9]+))?/?$`)
var userNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{4,32}$`)
var publicHostPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)
var telegramInvitePath = regexp.MustCompile(`^/(?:\+|(?i:joinchat)/)([A-Za-z0-9_-]{8,128})/?$`)
var telegramInviteCode = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

func telegramLinkURL(raw string) (*url.URL, bool) {
	s := strings.TrimSpace(raw)
	lower := strings.ToLower(s)
	for _, prefix := range []string{"t.me/", "telegram.me/", "www.t.me/", "www.telegram.me/"} {
		if strings.HasPrefix(lower, prefix) {
			s = "https://" + s
			break
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.User != nil || u.Port() != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, false
	}
	switch strings.ToLower(u.Host) {
	case "t.me", "www.t.me", "telegram.me", "www.telegram.me":
		return u, true
	default:
		return nil, false
	}
}

// Invite codes are case-sensitive. Canonicalize legacy and native links to
// one HTTPS URL so they share the same moderation and duplicate checks.
func telegramInviteURL(raw string) (string, bool) {
	var code string
	if u, ok := telegramLinkURL(raw); ok {
		if match := telegramInvitePath.FindStringSubmatch(u.EscapedPath()); match != nil {
			code = match[1]
		}
	} else if u, err := url.Parse(strings.TrimSpace(raw)); err == nil && u.Scheme == "tg" && u.Host == "join" && u.User == nil && u.Path == "" {
		if params, err := url.ParseQuery(u.RawQuery); err == nil && len(params["invite"]) == 1 {
			code = params.Get("invite")
		}
	}
	if !telegramInviteCode.MatchString(code) || strings.Trim(code, "0123456789") == "" {
		return "", false
	}
	return "https://t.me/+" + code, true
}

func catalogValidEscapes(value string) bool {
	hex := func(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }
	for i := 0; i < len(value); i++ {
		if value[i] == '%' {
			if i+2 >= len(value) || !hex(value[i+1]) || !hex(value[i+2]) {
				return false
			}
			i += 2
		}
	}
	return true
}

func NormalizeResourceURL(raw, kind string) (string, error) {
	s := strings.TrimSpace(raw)
	bad := catalogErr(422, "请填写有效的 Telegram 用户名、公开链接或群组／频道邀请链接。")
	if kind != "website" {
		if invite, ok := telegramInviteURL(s); ok {
			switch kind {
			case "", "group", "channel", "other":
				return invite, nil
			default:
				return "", catalogErr(422, "邀请链接的类型请选择群组、频道或其他。")
			}
		}
		if strings.HasPrefix(s, "@") && userNamePattern.MatchString(s[1:]) {
			s = "https://t.me/" + s[1:]
		}
		u, ok := telegramLinkURL(s)
		if !ok {
			return "", bad
		}
		match := telegramPath.FindStringSubmatch(u.EscapedPath())
		if match == nil {
			return "", bad
		}
		name := strings.ToLower(match[1])
		reserved := " joinchat addstickers addemoji share proxy socks login invoice setlanguage addtheme contact boost giftcode confirmphone "
		if strings.Contains(reserved, " "+name+" ") {
			return "", bad
		}
		out := "https://t.me/" + name
		if match[2] != "" {
			post := strings.TrimLeft(match[2], "0")
			if post == "" || len(post) > 20 {
				return "", catalogErr(422, "帖子编号无效。")
			}
			out += "/" + post
		}
		return out, nil
	}
	bad = catalogErr(422, "请填写有效的公开网站地址，不支持本地地址、账号密码或非网页协议。")
	if utf8.RuneCountInString(s) > 500 || !utf8.ValidString(s) || !catalogValidEscapes(s) || strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\\' }) {
		return "", bad
	}
	u, err := url.Parse(s)
	if err != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", bad
	}
	host := strings.ToLower(strings.TrimRight(u.Hostname(), "."))
	if host == "" {
		return "", bad
	}
	if net.ParseIP(host) == nil {
		host, err = idna.Lookup.ToASCII(host)
		if err != nil {
			return "", bad
		}
	}
	if host == "t.me" || host == "www.t.me" || host == "telegram.me" || host == "www.telegram.me" {
		return "", catalogErr(422, "Telegram 链接请选择对应的 Telegram 资源类型。")
	}
	ip := net.ParseIP(host)
	if ip != nil {
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
			return "", bad
		}
	} else {
		if !publicHostPattern.MatchString(host) || !strings.Contains(host, ".") || strings.Contains(host, "..") {
			return "", bad
		}
		for _, suffix := range []string{".local", ".localhost", ".internal", ".test", ".invalid"} {
			if strings.HasSuffix(host, suffix) {
				return "", bad
			}
		}
		numeric := true
		for _, r := range host {
			if (r < '0' || r > '9') && r != '.' {
				numeric = false
			}
		}
		if numeric {
			return "", bad
		}
	}
	port := u.Port()
	if port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", bad
		}
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	u.Host = host
	if port != "" && !((u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443")) {
		u.Host += ":" + port
	}
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}
func catalogLock(tx *sql.Tx, hashes ...string) error {
	sort.Strings(hashes)
	last := ""
	for _, h := range hashes {
		if h == "" || h == last {
			continue
		}
		last = h
		_, err := tx.Exec("INSERT INTO ts_link_locks(url_hash) VALUES (?) ON DUPLICATE KEY UPDATE url_hash=ts_link_locks.url_hash", h)
		if err != nil {
			return err
		}
	}
	return nil
}
func catalogDuplicateMessage(row Row, submission bool) string {
	status := str(row["status"])
	if submission {
		switch status {
		case "pending":
			return "该链接已存在收录申请，正在审核，请勿重复提交。"
		case "rejected":
			return "该链接的收录申请已被拒绝，不能重复提交。 " + str(row["review_note"])
		default:
			return "该链接已有收录记录，请勿重复提交。"
		}
	}
	text := map[string]string{"published": "该链接已存在，已正常收录，请勿重复提交。", "hidden": "该链接已存在，目前已下架，不能重复提交。", "archived": "该链接已存在，目前已归档，不能重复提交。", "blocked": "该链接已被封禁，不能提交收录。", "invalid": "该链接已标记为失效，不能提交收录。"}[status]
	if text == "" {
		text = "该链接已存在，当前状态不允许重复提交。"
	}
	if status != "published" && str(row["status_reason"]) != "" {
		text += " 原因：" + str(row["status_reason"])
	}
	return text
}
func catalogAvailable(tx *sql.Tx, h string, resourceID, submissionID int64) error {
	r, err := one(tx, "SELECT * FROM ts_resources WHERE url_hash=? FOR UPDATE", h)
	if err != nil {
		return err
	}
	if r != nil && integer(r["id"]) != resourceID {
		message := catalogDuplicateMessage(r, false)
		return &DomainError{Status: 422, Message: message, Fields: map[string]string{"url": message}}
	}
	s, err := one(tx, "SELECT * FROM ts_submissions WHERE url_hash=? FOR UPDATE", h)
	if err != nil {
		return err
	}
	if s != nil && integer(s["id"]) != submissionID && !(resourceID > 0 && integer(s["resource_id"]) == resourceID) {
		message := catalogDuplicateMessage(s, true)
		return &DomainError{Status: 422, Message: message, Fields: map[string]string{"url": message}}
	}
	return nil
}
func catalogValidateResource(db Queryer, input Row) (Row, error) {
	kind := str(input["type"])
	if kind == "" {
		kind = "channel"
	}
	if _, ok := ResourceTypes[kind]; !ok {
		return nil, catalogErr(422, "请选择有效的资源类型。")
	}
	link, err := NormalizeResourceURL(str(input["url"]), kind)
	if err != nil {
		return nil, err
	}
	title, err := catalogText(input["title"], 180, true)
	if err != nil {
		return nil, err
	}
	description, err := catalogText(input["description"], 5000, false)
	if err != nil {
		return nil, err
	}
	reason, err := catalogText(input["status_reason"], 500, false)
	if err != nil {
		return nil, err
	}
	tags, err := catalogText(input["tags"], 255, false)
	if err != nil {
		return nil, err
	}
	status := str(input["status"])
	if status == "" {
		status = "published"
	}
	if _, ok := resourceStatuses[status]; !ok {
		return nil, catalogErr(422, "无效的资源状态。")
	}
	var category any
	cat := integer(input["category_id"])
	if input["category_id"] != nil && str(input["category_id"]) != "" && (cat < 0 || fmt.Sprint(cat) != str(input["category_id"])) {
		return nil, catalogErr(422, "分类 ID 必须是有效整数。")
	}
	if cat > 0 {
		if _, err := catalogRecord(db, "categories", cat, false); err != nil {
			return nil, catalogErr(422, "分类不存在，请重新选择。")
		}
		category = cat
	}
	var members any
	if input["members"] != nil && str(input["members"]) != "" {
		n := integer(input["members"])
		if n < 0 || fmt.Sprint(n) != str(input["members"]) {
			return nil, catalogErr(422, "成员数须为非负整数。")
		}
		members = n
	}
	featured := 0
	if boolean(input["featured"]) {
		featured = 1
	}
	return Row{"title": title, "url": link, "url_hash": hash(link), "description": description, "type": kind, "status": status, "status_reason": reason, "tags": tags, "category_id": category, "members": members, "featured": featured, "updated_at": now()}, nil
}
func (a *App) Submit(userID int64, input Row) (Row, error) {
	return a.catalogSubmit(userID, input, 0)
}

func (a *App) catalogSubmit(userID int64, input Row, actor int64) (Row, error) {
	if str(a.Settings()["submissions_enabled"]) != "1" {
		return nil, catalogErr(403, "暂未开放资源提交，请稍后再来。")
	}
	if err := a.RateLimit(fmt.Sprintf("submission:%d", userID), 10, 3600); err != nil {
		return nil, err
	}
	return a.catalogTransaction(func(tx *sql.Tx) (Row, error) {
		link, err := NormalizeResourceURL(str(input["url"]), str(input["type"]))
		if err != nil {
			return nil, err
		}
		if err = catalogLock(tx, hash(link)); err != nil {
			return nil, err
		}
		u, err := catalogRecord(tx, "users", userID, true)
		if err != nil {
			return nil, err
		}
		if integer(u["status"]) != 1 {
			return nil, catalogErr(403, "当前账号已停用，不能提交收录。")
		}
		if integer(u["submission_allowed"]) != 1 {
			return nil, catalogErr(403, "你已被禁止提交收录。 "+str(u["submission_note"]))
		}
		if err = catalogAvailable(tx, hash(link), 0, 0); err != nil {
			return nil, err
		}
		data, err := catalogValidateResource(tx, input)
		if err != nil {
			return nil, err
		}
		if cat := integer(data["category_id"]); cat > 0 {
			if _, err = catalogRecord(tx, "categories", cat, true); err != nil {
				return nil, err
			}
		}
		s := Row{"user_id": userID, "created_at": now(), "status": "pending"}
		for _, k := range []string{"title", "url", "url_hash", "type", "description", "category_id"} {
			s[k] = data[k]
		}
		id, err := catalogInsert(tx, "submissions", s)
		if err == nil && actor > 0 {
			err = catalogAudit(tx, actor, "新增收录申请", fmt.Sprintf("申请 #%d · 用户 #%d", id, userID))
		}
		return Row{"id": id, "status": "pending", "title": data["title"]}, err
	})
}
