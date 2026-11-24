package app

import (
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

func (a *App) Search(input Row, source string) (Row, error) {
	return a.catalogSearch(input, source, nil)
}
func (a *App) catalogSearch(input Row, source string, access *Access) (Row, error) {
	start := time.Now()
	keyword := strings.TrimSpace(str(input["q"]))
	if utf8.RuneCountInString(keyword) > 120 {
		return nil, catalogErr(422, "搜索关键词最多 120 字。")
	}
	admin := access != nil
	maxPage := int64(100)
	if admin {
		maxPage = 10000000
	}
	page := catalogLimit(input["page"], 1, 1, maxPage)
	size := catalogLimit(input["page_size"], 12, 1, 50)
	where := []string{"1=1"}
	args := []any{}
	if admin {
		if str(input["id"]) != "" {
			id := integer(input["id"])
			if id < 1 {
				return nil, catalogErr(422, "ID 必须为正整数。")
			}
			where = append(where, "r.id=?")
			args = append(args, id)
		}
		for _, field := range []string{"status", "type", "category_id", "featured"} {
			if str(input[field]) != "" && str(input[field]) != "0" && !catalogReadable(access, "resources", field) {
				return nil, catalogErr(403, "无权按此字段筛选。")
			}
		}
		if str(input["days"]) != "" && !catalogReadable(access, "resources", "created_at") {
			return nil, catalogErr(403, "无权按创建时间筛选。")
		}
		if str(input["sort"]) == "members" && !catalogReadable(access, "resources", "members") {
			return nil, catalogErr(403, "无权按成员数量排序。")
		}
		if status := str(input["status"]); status != "" {
			where = append(where, "r.status=?")
			args = append(args, status)
		}
	} else {
		where = append(where, "r.status='published'")
	}
	kind := str(input["type"])
	if admin && (kind == botSearchCommunities || kind == botSearchPosts) {
		// Admin filtering remains resource-type based; a composite URL filter
		// must not bypass field-level restrictions on URL visibility.
		return nil, catalogErr(422, "请选择有效的资源类型。")
	}
	// All catalog writes normalize public Telegram messages to this URL shape.
	// A post can be text, video, audio or a file; its media type is not its scope.
	const postURL = `^https://t[.]me/[A-Za-z0-9_]{4,32}/[1-9][0-9]*$`
	switch kind {
	case botSearchCommunities:
		where = append(where, "r.type IN ('group','channel')", "r.url NOT REGEXP ?")
		args = append(args, postURL)
	case botSearchPosts:
		where = append(where, "r.url REGEXP ?")
		args = append(args, postURL)
	case "":
		// Website/API and older bot sessions retain the all-types view.
	default:
		where = append(where, "r.type=?")
		args = append(args, kind)
	}
	if c := integer(input["category_id"]); c > 0 {
		where = append(where, "r.category_id=?")
		args = append(args, c)
	}
	if boolean(input["featured"]) {
		where = append(where, "r.featured=1")
	}
	if days := integer(input["days"]); days == 3 || days == 7 || days == 30 {
		where = append(where, "r.created_at>=?")
		args = append(args, time.Now().In(location).AddDate(0, 0, -int(days)).Format("2006-01-02 15:04:05"))
	}
	if keyword != "" {
		fields := []string{"title", "description", "tags"}
		if admin {
			fields = append(fields, "url")
		}
		clauses := []string{}
		for _, f := range fields {
			if admin && !catalogReadable(access, "resources", f) {
				continue
			}
			clauses = append(clauses, "r."+f+" LIKE ? ESCAPE '!'")
			args = append(args, catalogLike(keyword))
		}
		if len(clauses) == 0 {
			return nil, catalogErr(403, "没有可搜索字段的查看权限。")
		}
		where = append(where, "("+strings.Join(clauses, " OR ")+")")
	}
	if !admin {
		result, err := a.snapshotSearch(input, strings.Join(where, " AND "), args, page, size)
		if err != nil {
			return nil, err
		}
		for _, row := range rowsOf(result["items"]) {
			delete(row, "url_hash")
			delete(row, "like_count")
			row["type_label"] = ResourceTypes[str(row["type"])]
			row["source_attribution"] = catalogAttribution(str(row["source"]))
		}
		duration := time.Since(start).Milliseconds()
		result["duration_ms"] = duration
		if source != "bot-page" && str(input["search_token"]) == "" && keyword != "" && page == 1 {
			_, err = a.DB.Exec("INSERT INTO ts_search_logs(query,source,result_count,duration_ms,created_at) VALUES (?,?,?,?,?)", keyword, source, result["total"], duration, now())
		}
		return result, err
	}
	base := " FROM ts_resources r LEFT JOIN ts_categories c ON c.id=r.category_id WHERE " + strings.Join(where, " AND ")
	count, err := one(a.DB, "SELECT COUNT(*) AS total"+base, args...)
	if err != nil {
		return nil, err
	}
	total := integer(count["total"])
	order := "r.featured DESC,r.id DESC"
	if admin && !catalogReadable(access, "resources", "featured") {
		order = "r.id DESC"
	}
	if str(input["sort"]) == "members" {
		order = "r.members DESC,r.id DESC"
	}
	listArgs := append(append([]any{}, args...), size, (page-1)*size)
	rows, err := query(a.DB, "SELECT r.*,c.name AS category_name"+base+" ORDER BY "+order+" LIMIT ? OFFSET ?", listArgs...)
	if err != nil {
		return nil, err
	}
	for i, row := range rows {
		delete(row, "url_hash")
		row["type_label"] = ResourceTypes[str(row["type"])]
		if admin {
			rows[i] = access.Project("resources", row)
		} else {
			row["source_attribution"] = catalogAttribution(str(row["source"]))
		}
	}
	duration := time.Since(start).Milliseconds()
	return Row{"items": rows, "total": total, "page": page, "page_size": size, "total_pages": (total + size - 1) / size, "has_more": page*size < total, "duration_ms": duration}, nil
}
func catalogAttribution(source string) any {
	switch source {
	case "zenodo_14749767":
		return Row{"key": source, "title": "Telegram channels graph dataset", "author": "Svitlana Shapovalova; Anton Sofiienko", "published_at": "2025-01-27", "url": "https://zenodo.org/records/14749767", "license": "CC BY 4.0", "license_url": "https://creativecommons.org/licenses/by/4.0/", "changes": "规范化链接、截短过长字段；来源快照不代表当前内容和可用性。"}
	case "curlie_20261116":
		return Row{"key": source, "title": "Curlie Directory", "author": "Curlie.org community", "url": "https://curlie.org/docs/en/rdf.html", "license": "CC BY 3.0", "license_url": "https://creativecommons.org/licenses/by/3.0/", "changes": "优先中文目录，其次英文行业目录；规范化网址、移除 HTML、截短超长字段；保留分类标签。未逐条验证网站当前可用性。"}
	case "telegram_public_20261116":
		return Row{"key": source, "title": "Telegram 公开帖子预览", "author": "各链接对应的频道作者", "url": "https://telegram.org/", "license": "原作者保留权利", "changes": "仅索引公开帖子链接和短摘要；类型来自页面实际媒体元素，未下载媒体文件。"}
	case "tgrpg_bots_20261116":
		return Row{"key": source, "title": "Awesome Telegram", "author": "TGrpg / TGBox contributors", "url": "https://github.com/TGrpg/awesome-telegram", "license": "CC BY 4.0", "license_url": "https://creativecommons.org/licenses/by/4.0/", "changes": "规范化链接、截短过长字段；来源快照不代表当前内容和可用性。"}
	case "dany_bots_20261116":
		return Row{"key": source, "title": "TelegramBotsList", "author": "danyspin97 and contributors", "url": "https://github.com/danyspin97/TelegramBotsList", "license": "Unlicense", "changes": "规范化链接、截短过长字段；来源快照不代表当前内容和可用性。"}
	case "erkcet_bots_20261116":
		return Row{"key": source, "title": "Awesome Telegram Bots", "author": "erkcet and contributors", "url": "https://github.com/erkcet/awesome-telegram-bots", "license": "CC0", "changes": "规范化链接、截短过长字段；来源快照不代表当前内容和可用性。"}
	}
	return nil
}
func catalogIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
func (a *App) RegisterCatalog() {
	a.Handle("GET /api/public/config", "", "", false, func(w http.ResponseWriter, r *http.Request, _ *Identity) {
		s := a.Settings()
		bot := a.PublicTelegram()
		count, err := one(a.DB, "SELECT COUNT(*) AS total FROM ts_resources WHERE status='published'")
		if err != nil {
			catalogReply(w, nil, err)
			return
		}
		JSON(w, Row{"site_name": s["site_name"], "site_description": s["site_description"], "submissions_enabled": s["submissions_enabled"], "bot_username": bot["username"], "telegram": bot, "types": ResourceTypes, "resource_count": count["total"]})
	})
	a.Handle("GET /api/public/categories", "", "", false, func(w http.ResponseWriter, r *http.Request, _ *Identity) {
		rows, err := query(a.DB, "SELECT * FROM ts_categories ORDER BY sort_order,id")
		catalogReply(w, rows, err)
	})
	a.Handle("GET /api/public/search", "", "", false, func(w http.ResponseWriter, r *http.Request, _ *Identity) {
		if err := a.RateLimit("web:"+catalogIP(r), 120, 60); err != nil {
			catalogReply(w, nil, err)
			return
		}
		result, err := a.Search(catalogParams(r), "web")
		if err == nil {
			result["ads"], err = a.LiveAdsSeed(r.URL.Query().Get("q"), randomHex(8))
		}
		catalogReply(w, result, err)
	})
	a.Handle("GET /api/public/resources/{id}", "", "", false, func(w http.ResponseWriter, r *http.Request, _ *Identity) {
		row, err := one(a.DB, "SELECT * FROM ts_resources WHERE id=? AND status='published'", catalogID(r))
		if err == nil && row == nil {
			err = catalogErr(404, "资源不存在或已下架。")
		}
		if row != nil {
			delete(row, "url_hash")
			delete(row, "like_count")
			row["source_attribution"] = catalogAttribution(str(row["source"]))
		}
		catalogReply(w, row, err)
	})
	a.Handle("GET /api/v1/search", "", "", false, func(w http.ResponseWriter, r *http.Request, _ *Identity) {
		if str(a.Settings()["public_api_enabled"]) != "1" {
			Fail(w, 403, "开放 API 暂未启用。")
			return
		}
		header := r.Header.Get("Authorization")
		token := ""
		if strings.HasPrefix(header, "Bearer ") {
			token = strings.TrimPrefix(header, "Bearer ")
		}
		key, err := one(a.DB, "SELECT * FROM ts_api_keys WHERE token_hash=? AND status=1", hash(token))
		if err != nil {
			catalogReply(w, nil, err)
			return
		}
		if key == nil || token == "" {
			Fail(w, 401, "API 密钥无效。")
			return
		}
		if err = a.RateLimit(fmt.Sprintf("key:%v:%s", key["id"], time.Now().In(location).Format("2006-01-02")), int(integer(key["daily_limit"])), 86400); err != nil {
			catalogReply(w, nil, err)
			return
		}
		if err = a.RateLimit(fmt.Sprintf("key-burst:%v", key["id"]), 60, 60); err != nil {
			catalogReply(w, nil, err)
			return
		}
		_, err = a.DB.Exec("UPDATE ts_api_keys SET last_used_at=? WHERE id=?", now(), key["id"])
		if err != nil {
			catalogReply(w, nil, err)
			return
		}
		result, err := a.Search(catalogParams(r), "api")
		catalogReply(w, result, err)
	})
	a.registerUserCatalog()
	a.registerAdminCatalog()
	a.registerRetiredRecommendations()
}
func (a *App) registerUserCatalog() {
	a.Handle("GET /user/api/favorites", "user", "", false, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		count, err := one(a.DB, "SELECT COUNT(*) AS total FROM ts_favorites f JOIN ts_resources r ON r.id=f.resource_id WHERE f.user_id=? AND r.status='published'", id.ID)
		if err != nil {
			catalogReply(w, nil, err)
			return
		}
		rows, err := query(a.DB, "SELECT r.id,r.title,r.url,r.type,r.description,r.tags,r.members,r.created_at,r.source FROM ts_favorites f JOIN ts_resources r ON r.id=f.resource_id WHERE f.user_id=? AND r.status='published' ORDER BY f.created_at DESC LIMIT 12 OFFSET ?", id.ID, (catalogLimit(r.URL.Query().Get("page"), 1, 1, 10000000)-1)*12)
		for _, row := range rows {
			row["source_attribution"] = catalogAttribution(str(row["source"]))
		}
		catalogReply(w, Row{"items": rows, "total": count["total"]}, err)
	})
	a.Handle("GET /user/api/favorite-ids", "user", "", false, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		values := strings.Split(r.URL.Query().Get("ids"), ",")
		ids := []int64{}
		args := []any{id.ID}
		marks := []string{}
		for i, v := range values {
			if i >= 50 {
				break
			}
			n := integer(v)
			if n > 0 {
				args = append(args, n)
				marks = append(marks, "?")
			}
		}
		if len(marks) == 0 {
			JSON(w, ids)
			return
		}
		rows, err := query(a.DB, "SELECT resource_id FROM ts_favorites WHERE user_id=? AND resource_id IN ("+strings.Join(marks, ",")+")", args...)
		for _, row := range rows {
			ids = append(ids, integer(row["resource_id"]))
		}
		catalogReply(w, ids, err)
	})
	a.Handle("POST /user/api/favorites/{id}", "user", "", true, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		b, err := Body(r)
		if err != nil {
			catalogReply(w, nil, err)
			return
		}
		saved := true
		if v, ok := b["saved"]; ok {
			saved = boolean(v)
		}
		result, err := a.catalogTransaction(func(tx *sql.Tx) (Row, error) {
			u, e := catalogRecord(tx, "users", id.ID, true)
			if e != nil {
				return nil, e
			}
			if integer(u["status"]) != 1 {
				return nil, catalogErr(403, "当前账号没有访问权限。")
			}
			res, e := catalogRecord(tx, "resources", catalogID(r), true)
			if e != nil {
				return nil, e
			}
			if str(res["status"]) != "published" {
				return nil, catalogErr(404, "资源不存在或已下架。")
			}
			if saved {
				_, e = tx.Exec("INSERT IGNORE INTO ts_favorites(user_id,resource_id,created_at) VALUES (?,?,?)", id.ID, catalogID(r), now())
			} else {
				_, e = tx.Exec("DELETE FROM ts_favorites WHERE user_id=? AND resource_id=?", id.ID, catalogID(r))
			}
			return Row{"saved": saved}, e
		})
		catalogReply(w, result, err)
	})
	a.Handle("GET /user/api/submissions", "user", "", false, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		count, err := one(a.DB, "SELECT COUNT(*) AS total FROM ts_submissions WHERE user_id=?", id.ID)
		if err != nil {
			catalogReply(w, nil, err)
			return
		}
		rows, err := query(a.DB, "SELECT id,title,url,type,description,category_id,status,review_note,resource_id,created_at,reviewed_at FROM ts_submissions WHERE user_id=? ORDER BY id DESC LIMIT 12 OFFSET ?", id.ID, (catalogLimit(r.URL.Query().Get("page"), 1, 1, 10000000)-1)*12)
		catalogReply(w, Row{"total": count["total"], "items": rows}, err)
	})
	a.Handle("POST /user/api/submissions", "user", "", true, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		body, err := Body(r)
		if err != nil {
			catalogReply(w, nil, err)
			return
		}
		out, err := a.Submit(id.ID, body)
		catalogReply(w, out, err)
	})
}
