package app

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type catalogAdminAction func(*http.Request, *Identity, Row) (any, error)

func (a *App) catalogAdmin(pattern, module, action, shape string, write bool, fn catalogAdminAction) {
	a.Handle(pattern, "admin", module+"."+action, write, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		body := Row{}
		if write && r.ContentLength != 0 {
			var err error
			body, err = Body(r)
			if err != nil {
				catalogReply(w, nil, err)
				return
			}
		}
		if write {
			check := body
			if action == "archive" || action == "revoke" || strings.HasSuffix(r.URL.Path, "/status") || action == "review" {
				check = catalogMerge(body, Row{"status": body["status"]})
			}
			if err := id.Access.CheckWrite(module, check); err != nil {
				catalogReply(w, nil, err)
				return
			}
		}
		out, err := fn(r, id, body)
		if err == nil {
			switch shape {
			case "record":
				if row, ok := out.(Row); ok {
					out = id.Access.Project(module, row)
				}
			case "rows":
				if rows, ok := out.([]Row); ok {
					for i, row := range rows {
						rows[i] = id.Access.Project(module, row)
					}
				}
			case "page":
				if row, ok := out.(Row); ok {
					if rows, ok := row["items"].([]Row); ok {
						for i, v := range rows {
							rows[i] = id.Access.Project(module, v)
						}
					}
				}
			}
		}
		catalogReply(w, out, err)
	})
}
func catalogFilter(input Row, access *Access, module, alias string) (string, []any, error) {
	where := []string{"1=1"}
	args := []any{}
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	if raw := str(input["id"]); raw != "" {
		id := integer(raw)
		if id < 1 {
			return "", nil, catalogErr(422, "ID 必须为正整数。")
		}
		where = append(where, prefix+"id=?")
		args = append(args, id)
	}
	q, err := catalogText(input["q"], 120, false)
	if err != nil {
		return "", nil, err
	}
	if q != "" {
		fields := map[string][]string{"categories": {"name", "description"}, "submissions": {"title", "url", "description"}, "users": {"username", "display_name", "telegram_id"}, "keys": {"name", "prefix"}, "logs": {"action", "detail", "actor_id"}}[module]
		clauses := []string{}
		if n := integer(q); n > 0 && fmt.Sprint(n) == q {
			clauses = append(clauses, prefix+"id=?")
			args = append(args, n)
		}
		for _, field := range fields {
			if catalogReadable(access, module, field) {
				clauses = append(clauses, prefix+field+" LIKE ? ESCAPE '!'")
				args = append(args, catalogLike(q))
			}
		}
		if len(clauses) == 0 {
			return "", nil, catalogErr(403, "没有可搜索字段的查看权限，请使用 ID 查询。")
		}
		where = append(where, "("+strings.Join(clauses, " OR ")+")")
	}
	if status := str(input["status"]); status != "" {
		if !catalogReadable(access, module, "status") {
			return "", nil, catalogErr(403, "无权按状态筛选。")
		}
		where = append(where, prefix+"status=?")
		args = append(args, status)
	}
	return strings.Join(where, " AND "), args, nil
}
func (a *App) registerAdminCatalog() {
	a.catalogAdmin("GET /admin/api/dashboard", "dashboard", "read", "record", false, func(r *http.Request, id *Identity, _ Row) (any, error) { return a.catalogDashboard(id.Access) })
	a.catalogAdmin("GET /admin/api/resources", "resources", "read", "page", false, func(r *http.Request, id *Identity, _ Row) (any, error) {
		return a.catalogSearch(catalogParams(r), "admin", id.Access)
	})
	a.catalogAdmin("GET /admin/api/resources/{id}", "resources", "read", "record", false, func(r *http.Request, id *Identity, _ Row) (any, error) {
		return catalogRecord(a.DB, "resources", catalogID(r), false)
	})
	for _, entry := range []struct{ suffix, action string }{{"", "create"}, {"/{id}", "update"}} {
		a.catalogAdmin("POST /admin/api/resources"+entry.suffix, "resources", entry.action, "record", true, func(r *http.Request, id *Identity, body Row) (any, error) {
			if catalogID(r) == 0 && !catalogWritable(id.Access, "resources", "status") {
				body["status"] = "hidden"
			}
			return a.catalogSaveResource(body, catalogID(r), id.ID)
		})
		a.catalogAdmin("POST /admin/api/categories"+entry.suffix, "categories", entry.action, "result", true, func(r *http.Request, id *Identity, body Row) (any, error) {
			return a.catalogSaveCategory(body, catalogID(r), id.ID)
		})
		a.catalogAdmin("POST /admin/api/submissions"+entry.suffix, "submissions", entry.action, "record", true, func(r *http.Request, id *Identity, body Row) (any, error) {
			return a.catalogSaveSubmission(body, catalogID(r), id.ID)
		})
		a.catalogAdmin("POST /admin/api/users"+entry.suffix, "users", entry.action, "record", true, func(r *http.Request, id *Identity, body Row) (any, error) {
			if catalogID(r) == 0 {
				for _, f := range []string{"status", "submission_allowed"} {
					if !catalogWritable(id.Access, "users", f) {
						body[f] = 0
					}
				}
			}
			return a.catalogSaveUser(body, catalogID(r), id.ID)
		})
	}
	a.catalogAdmin("POST /admin/api/resources/{id}/archive", "resources", "archive", "result", true, func(r *http.Request, id *Identity, body Row) (any, error) {
		if err := catalogOnly(body, "status"); err != nil {
			return nil, err
		}
		return a.catalogTransaction(func(tx *sql.Tx) (Row, error) {
			row, err := catalogRecord(tx, "resources", catalogID(r), true)
			if err != nil {
				return nil, err
			}
			if err = catalogUpdate(tx, "resources", catalogID(r), Row{"status": "archived", "updated_at": now()}); err != nil {
				return nil, err
			}
			err = catalogAudit(tx, id.ID, "归档资源", str(row["title"]))
			return Row{"archived": true}, err
		})
	})
	for _, module := range []string{"resources", "categories", "submissions", "users", "keys"} {
		a.catalogAdmin("DELETE /admin/api/"+module+"/{id}", module, "delete", "result", true, func(r *http.Request, id *Identity, body Row) (any, error) {
			if len(body) > 0 {
				return nil, catalogErr(422, "删除操作不接受修改字段。")
			}
			return a.catalogDelete(module, catalogID(r), id.ID)
		})
	}
	a.catalogAdmin("GET /admin/api/categories", "categories", "read", "rows", false, func(r *http.Request, id *Identity, _ Row) (any, error) {
		where, args, err := catalogFilter(catalogParams(r), id.Access, "categories", "c")
		if err != nil {
			return nil, err
		}
		return query(a.DB, "SELECT c.*,(SELECT COUNT(*) FROM ts_resources r WHERE r.category_id=c.id AND r.status<>'archived') AS resource_count FROM ts_categories c WHERE "+where+" ORDER BY c.sort_order,c.id", args...)
	})
	a.Handle("GET /admin/api/lookups/categories", "admin", "", false, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		if !(id.Access.Has("resources.read") && catalogReadable(id.Access, "resources", "category_id")) && !(id.Access.Has("submissions.read") && catalogReadable(id.Access, "submissions", "category_id")) {
			Fail(w, 403, "没有读取资源分类选项的权限。")
			return
		}
		rows, err := query(a.DB, "SELECT id,name FROM ts_categories ORDER BY sort_order,id")
		catalogReply(w, rows, err)
	})
	for _, module := range []string{"submissions", "users", "logs"} {
		a.catalogAdmin("GET /admin/api/"+module, module, "read", "page", false, func(r *http.Request, id *Identity, _ Row) (any, error) {
			alias := "x"
			table := "ts_" + module
			columns := "x.*"
			join := ""
			if module == "logs" {
				table = "ts_audit_logs"
			}
			if module == "users" {
				columns = "x.id,x.username,x.display_name,x.telegram_id,x.status,x.submission_allowed,x.submission_note,x.created_at,x.last_login_at"
			}
			if module == "submissions" {
				columns = "x.*,u.display_name AS submitter"
				join = " LEFT JOIN ts_users u ON u.id=x.user_id"
			}
			where, args, err := catalogFilter(catalogParams(r), id.Access, module, alias)
			if err != nil {
				return nil, err
			}
			base := " FROM " + table + " x" + join + " WHERE " + where
			count, err := one(a.DB, "SELECT COUNT(*) AS total"+base, args...)
			if err != nil {
				return nil, err
			}
			page := catalogLimit(r.URL.Query().Get("page"), 1, 1, 10000000)
			rows, err := query(a.DB, "SELECT "+columns+base+" ORDER BY x.id DESC LIMIT 20 OFFSET ?", append(args, (page-1)*20)...)
			return Row{"items": rows, "total": count["total"], "page": page, "page_size": 20}, err
		})
	}
	a.catalogAdmin("POST /admin/api/submissions/{id}/review", "submissions", "review", "record", true, func(r *http.Request, id *Identity, body Row) (any, error) {
		return a.catalogReview(body, catalogID(r), id.ID)
	})
	for _, endpoint := range []string{"status", "submission-policy"} {
		a.catalogAdmin("POST /admin/api/users/{id}/"+endpoint, "users", "update", "record", true, func(r *http.Request, id *Identity, body Row) (any, error) {
			keys := "status"
			if endpoint == "submission-policy" {
				keys = "submission_allowed submission_note"
			}
			if err := catalogOnly(body, keys); err != nil {
				return nil, err
			}
			return a.catalogSaveUser(body, catalogID(r), id.ID)
		})
	}
	a.catalogAdmin("GET /admin/api/settings", "settings", "read", "record", false, func(r *http.Request, id *Identity, _ Row) (any, error) { return a.Settings(), nil })
	a.catalogAdmin("POST /admin/api/settings", "settings", "update", "record", true, func(r *http.Request, id *Identity, body Row) (any, error) {
		return a.catalogTransaction(func(tx *sql.Tx) (Row, error) {
			for k, v := range body {
				max := 500
				if k == "site_name" {
					max = 80
				}
				text, err := catalogText(str(v), max, k == "site_name")
				if err != nil {
					return nil, err
				}
				if strings.HasSuffix(k, "_enabled") {
					if _, err = catalogFlag(v); err != nil {
						return nil, err
					}
					text = fmt.Sprint(integer(v))
				}
				if _, err = tx.Exec("INSERT INTO ts_settings(name,value) VALUES (?,?) ON DUPLICATE KEY UPDATE value=?", k, text, text); err != nil {
					return nil, err
				}
			}
			if err := catalogAudit(tx, id.ID, "更新系统设置", "站点与访问设置已保存"); err != nil {
				return nil, err
			}
			return body, nil
		})
	})
	a.catalogAdmin("GET /admin/api/keys", "keys", "read", "rows", false, func(r *http.Request, id *Identity, _ Row) (any, error) {
		where, args, err := catalogFilter(catalogParams(r), id.Access, "keys", "")
		if err != nil {
			return nil, err
		}
		return query(a.DB, "SELECT id,name,prefix,status,daily_limit,created_at,last_used_at FROM ts_api_keys WHERE "+where+" ORDER BY id DESC", args...)
	})
	a.catalogAdmin("POST /admin/api/keys", "keys", "create", "result", true, func(r *http.Request, id *Identity, body Row) (any, error) {
		if !catalogReadable(id.Access, "keys", "token") {
			return nil, catalogErr(403, "创建密钥需要查看完整密钥的权限。")
		}
		if err := catalogOnly(body, "name daily_limit"); err != nil {
			return nil, err
		}
		name, err := catalogText(body["name"], 80, true)
		if err != nil {
			return nil, err
		}
		limit := catalogLimit(body["daily_limit"], 1000, 1, 100000)
		token := "si_" + randomHex(24)
		return a.catalogTransaction(func(tx *sql.Tx) (Row, error) {
			key, err := catalogInsert(tx, "api_keys", Row{"name": name, "token_hash": hash(token), "prefix": token[:11], "daily_limit": limit, "status": 1, "created_at": now()})
			if err != nil {
				return nil, err
			}
			err = catalogAudit(tx, id.ID, "创建 API 密钥", fmt.Sprintf("密钥 #%d · %s", key, name))
			return Row{"id": key, "token": token}, err
		})
	})
	a.catalogAdmin("POST /admin/api/keys/{id}", "keys", "update", "record", true, func(r *http.Request, id *Identity, body Row) (any, error) {
		if err := catalogOnly(body, "name daily_limit"); err != nil {
			return nil, err
		}
		return a.catalogTransaction(func(tx *sql.Tx) (Row, error) {
			old, err := catalogRecord(tx, "api_keys", catalogID(r), true)
			if err != nil {
				return nil, err
			}
			merged := catalogMerge(old, body)
			name, err := catalogText(merged["name"], 80, true)
			if err != nil {
				return nil, err
			}
			limit := integer(merged["daily_limit"])
			if limit < 1 || limit > 100000 {
				return nil, catalogErr(422, "每日额度须为 1–100000 的整数。")
			}
			if err = catalogUpdate(tx, "api_keys", catalogID(r), Row{"name": name, "daily_limit": limit}); err != nil {
				return nil, err
			}
			if err = catalogAudit(tx, id.ID, "编辑 API 密钥", fmt.Sprintf("密钥 #%d", catalogID(r))); err != nil {
				return nil, err
			}
			return catalogRecord(tx, "api_keys", catalogID(r), false)
		})
	})
	a.catalogAdmin("POST /admin/api/keys/{id}/revoke", "keys", "revoke", "result", true, func(r *http.Request, id *Identity, body Row) (any, error) {
		if err := catalogOnly(body, "status"); err != nil {
			return nil, err
		}
		return a.catalogTransaction(func(tx *sql.Tx) (Row, error) {
			if _, err := catalogRecord(tx, "api_keys", catalogID(r), true); err != nil {
				return nil, err
			}
			if err := catalogUpdate(tx, "api_keys", catalogID(r), Row{"status": 0}); err != nil {
				return nil, err
			}
			err := catalogAudit(tx, id.ID, "撤销 API 密钥", fmt.Sprintf("密钥 #%d", catalogID(r)))
			return Row{"revoked": true}, err
		})
	})
}
func (a *App) catalogDashboard(access *Access) (Row, error) {
	out := Row{"updated_at": now()}
	if catalogReadable(access, "dashboard", "stats") {
		stats := Row{}
		for key, statement := range map[string]string{"resources": "SELECT COUNT(*) AS total FROM ts_resources WHERE status='published'", "searches_today": "SELECT COUNT(*) AS total FROM ts_search_logs WHERE created_at>=CURDATE()", "pending": "SELECT COUNT(*) AS total FROM ts_submissions WHERE status='pending'", "users": "SELECT COUNT(*) AS total FROM ts_users"} {
			row, err := one(a.DB, statement)
			if err != nil {
				return nil, err
			}
			stats[key] = row["total"]
		}
		out["stats"] = stats
	}
	since := time.Now().In(location).AddDate(0, 0, -6).Format("2006-01-02") + " 00:00:00"
	if catalogReadable(access, "dashboard", "trend") {
		rows, err := query(a.DB, "SELECT DATE_FORMAT(created_at,'%Y-%m-%d') AS day,COUNT(*) AS total FROM ts_search_logs WHERE created_at>=? GROUP BY day", since)
		if err != nil {
			return nil, err
		}
		days := map[string]int64{}
		for _, row := range rows {
			days[str(row["day"])] = integer(row["total"])
		}
		trend := []Row{}
		for i := 6; i >= 0; i-- {
			day := time.Now().In(location).AddDate(0, 0, -i).Format("2006-01-02")
			trend = append(trend, Row{"date": day, "count": days[day]})
		}
		out["trend"] = trend
	}
	if catalogReadable(access, "dashboard", "popular") {
		rows, err := query(a.DB, "SELECT query,COUNT(*) AS searches FROM ts_search_logs WHERE created_at>=? GROUP BY query ORDER BY searches DESC LIMIT 6", since)
		if err != nil {
			return nil, err
		}
		out["popular"] = rows
	}
	if catalogReadable(access, "dashboard", "recent_resources") && access.Has("resources.read") {
		rows, err := query(a.DB, "SELECT * FROM ts_resources WHERE status='published' ORDER BY id DESC LIMIT 5")
		if err != nil {
			return nil, err
		}
		for i, row := range rows {
			rows[i] = access.Project("resources", row)
		}
		out["recent_resources"] = rows
	}
	if catalogReadable(access, "dashboard", "activity") && access.Has("logs.read") {
		rows, err := query(a.DB, "SELECT * FROM ts_audit_logs ORDER BY id DESC LIMIT 6")
		if err != nil {
			return nil, err
		}
		for i, row := range rows {
			rows[i] = access.Project("logs", row)
		}
		out["activity"] = rows
	}
	if catalogReadable(access, "dashboard", "bot") && access.Has("bot.read") {
		out["bot"] = access.Project("bot", a.botState())
	}
	return out, nil
}
