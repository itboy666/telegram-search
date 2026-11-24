package app

import (
	"database/sql"
	"fmt"
	"strings"
)

func (a *App) catalogSaveResource(input Row, id, actor int64) (Row, error) {
	return a.catalogTransaction(func(tx *sql.Tx) (Row, error) {
		id := id
		old := Row{}
		var err error
		if id > 0 {
			old, err = catalogRecord(tx, "resources", id, false)
			if err != nil {
				return nil, err
			}
		}
		merged := catalogMerge(old, input)
		kind := str(merged["type"])
		if kind == "" {
			kind = "channel"
		}
		link, err := NormalizeResourceURL(str(merged["url"]), kind)
		if err != nil {
			return nil, err
		}
		if err = catalogLock(tx, str(old["url_hash"]), hash(link)); err != nil {
			return nil, err
		}
		if id > 0 {
			current, e := catalogRecord(tx, "resources", id, true)
			if e != nil {
				return nil, e
			}
			if str(current["url_hash"]) != str(old["url_hash"]) {
				return nil, catalogErr(409, "资源已更新，请刷新后重试。")
			}
			merged = catalogMerge(current, input)
		}
		data, err := catalogValidateResource(tx, merged)
		if err != nil {
			return nil, err
		}
		if str(data["url"]) != link {
			return nil, catalogErr(409, "资源已更新，请刷新后重试。")
		}
		if cat := integer(data["category_id"]); cat > 0 {
			if _, err = catalogRecord(tx, "categories", cat, true); err != nil {
				return nil, err
			}
		}
		if err = catalogAvailable(tx, str(data["url_hash"]), id, 0); err != nil {
			return nil, err
		}
		if id > 0 {
			err = catalogUpdate(tx, "resources", id, data)
		} else {
			data["source"] = "admin"
			data["created_at"] = now()
			id, err = catalogInsert(tx, "resources", data)
		}
		if err != nil {
			return nil, err
		}
		if err = catalogAudit(tx, actor, "保存资源", fmt.Sprintf("资源 #%d · %s", id, str(data["title"]))); err != nil {
			return nil, err
		}
		return catalogRecord(tx, "resources", id, false)
	})
}
func (a *App) catalogSaveCategory(input Row, id, actor int64) (Row, error) {
	if err := catalogOnly(input, "name description sort_order"); err != nil {
		return nil, err
	}
	return a.catalogTransaction(func(tx *sql.Tx) (Row, error) {
		id := id
		old := Row{}
		var err error
		if id > 0 {
			old, err = catalogRecord(tx, "categories", id, true)
			if err != nil {
				return nil, err
			}
		}
		body := catalogMerge(old, input)
		name, err := catalogText(body["name"], 80, true)
		if err != nil {
			return nil, err
		}
		description, err := catalogText(body["description"], 255, false)
		if err != nil {
			return nil, err
		}
		data := Row{"name": name, "description": description, "sort_order": integer(body["sort_order"])}
		existing, err := one(tx, "SELECT id FROM ts_categories WHERE name=? AND id<>?", name, id)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return nil, catalogErr(422, "分类名称已存在。")
		}
		if id > 0 {
			err = catalogUpdate(tx, "categories", id, data)
		} else {
			data["created_at"] = now()
			id, err = catalogInsert(tx, "categories", data)
		}
		if err != nil {
			return nil, err
		}
		err = catalogAudit(tx, actor, "保存分类", fmt.Sprintf("分类 #%d · %s", id, name))
		return Row{"id": id}, err
	})
}
func (a *App) catalogSaveSubmission(input Row, id, actor int64) (Row, error) {
	if err := catalogOnly(input, "title url type description category_id user_id"); err != nil {
		return nil, err
	}
	if id == 0 {
		user := integer(input["user_id"])
		if user < 1 {
			return nil, catalogErr(422, "请填写真实的提交用户 ID。")
		}
		result, err := a.catalogSubmit(user, input, actor)
		if err != nil {
			return nil, err
		}
		return catalogRecord(a.DB, "submissions", integer(result["id"]), false)
	}
	return a.catalogTransaction(func(tx *sql.Tx) (Row, error) {
		old, err := catalogRecord(tx, "submissions", id, false)
		if err != nil {
			return nil, err
		}
		merged := catalogMerge(old, input)
		merged["status"] = "published"
		data, err := catalogValidateResource(tx, merged)
		if err != nil {
			return nil, err
		}
		if err = catalogLock(tx, str(old["url_hash"]), str(data["url_hash"])); err != nil {
			return nil, err
		}
		current, err := catalogRecord(tx, "submissions", id, true)
		if err != nil {
			return nil, err
		}
		if str(current["status"]) != "pending" || str(current["url_hash"]) != str(old["url_hash"]) {
			return nil, catalogErr(409, "仅能编辑尚未审核且未被他人更新的申请，请刷新列表。")
		}
		if input["user_id"] != nil && integer(input["user_id"]) != integer(current["user_id"]) {
			return nil, catalogErr(422, "不能更改原申请的提交用户。")
		}
		merged = catalogMerge(current, input)
		merged["status"] = "published"
		data, err = catalogValidateResource(tx, merged)
		if err != nil {
			return nil, err
		}
		if err = catalogAvailable(tx, str(data["url_hash"]), 0, id); err != nil {
			return nil, err
		}
		if cat := integer(data["category_id"]); cat > 0 {
			if _, err = catalogRecord(tx, "categories", cat, true); err != nil {
				return nil, err
			}
		}
		fields := Row{}
		for _, k := range []string{"title", "url", "url_hash", "type", "description", "category_id"} {
			fields[k] = data[k]
		}
		if err = catalogUpdate(tx, "submissions", id, fields); err != nil {
			return nil, err
		}
		if err = catalogAudit(tx, actor, "编辑收录申请", fmt.Sprintf("申请 #%d", id)); err != nil {
			return nil, err
		}
		return catalogRecord(tx, "submissions", id, false)
	})
}
func (a *App) catalogReview(body Row, id, actor int64) (Row, error) {
	if err := catalogOnly(body, "status review_note"); err != nil {
		return nil, err
	}
	status := str(body["status"])
	if status != "approved" && status != "rejected" {
		return nil, catalogErr(422, "无效的审核操作。")
	}
	return a.catalogTransaction(func(tx *sql.Tx) (Row, error) {
		snapshot, err := catalogRecord(tx, "submissions", id, false)
		if err != nil {
			return nil, err
		}
		if err = catalogLock(tx, str(snapshot["url_hash"])); err != nil {
			return nil, err
		}
		row, err := catalogRecord(tx, "submissions", id, true)
		if err != nil {
			return nil, err
		}
		if str(row["status"]) != "pending" || row["url_hash"] != snapshot["url_hash"] {
			return nil, catalogErr(409, "这条申请已经处理或更新，请刷新列表。")
		}
		noteValue := body["review_note"]
		if noteValue == nil {
			noteValue = row["review_note"]
		}
		note, err := catalogText(noteValue, 500, false)
		if err != nil {
			return nil, err
		}
		data := Row{"status": status, "review_note": note, "reviewer_id": actor, "reviewed_at": now()}
		if status == "approved" {
			input := catalogMerge(row, Row{"status": "published"})
			resource, err := catalogValidateResource(tx, input)
			if err != nil {
				return nil, err
			}
			if err = catalogAvailable(tx, str(resource["url_hash"]), 0, id); err != nil {
				return nil, err
			}
			if cat := integer(resource["category_id"]); cat > 0 {
				if _, err = catalogRecord(tx, "categories", cat, true); err != nil {
					return nil, err
				}
			}
			resource["source"] = "submission"
			resource["owner_id"] = row["user_id"]
			resource["created_at"] = now()
			resourceID, err := catalogInsert(tx, "resources", resource)
			if err != nil {
				return nil, err
			}
			data["resource_id"] = resourceID
		}
		if err = catalogUpdate(tx, "submissions", id, data); err != nil {
			return nil, err
		}
		if err = catalogAudit(tx, actor, "审核收录", fmt.Sprintf("申请 #%d · %s", id, status)); err != nil {
			return nil, err
		}
		return catalogRecord(tx, "submissions", id, false)
	})
}
func (a *App) catalogSaveUser(input Row, id, actor int64) (Row, error) {
	if err := catalogOnly(input, "username display_name password status submission_allowed submission_note"); err != nil {
		return nil, err
	}
	password := str(input["password"])
	if (id == 0 || password != "") && (len(password) < 10 || len(password) > 72) {
		return nil, catalogErr(422, "密码长度须为 10–72 字节。")
	}
	encoded := ""
	var err error
	if password != "" {
		encoded, err = passwordHash(password)
		if err != nil {
			return nil, err
		}
	}
	return a.catalogTransaction(func(tx *sql.Tx) (Row, error) {
		id := id
		old := Row{}
		var err error
		if id > 0 {
			old, err = catalogRecord(tx, "users", id, true)
			if err != nil {
				return nil, err
			}
		}
		body := catalogMerge(Row{"status": 1, "submission_allowed": 1, "submission_note": ""}, catalogMerge(old, input))
		username, err := catalogText(body["username"], 80, true)
		if err != nil {
			return nil, err
		}
		botOnly := integer(old["telegram_id"]) > 0 && strings.HasPrefix(str(old["username"]), "tg_")
		if botOnly {
			if username != str(old["username"]) || password != "" {
				return nil, catalogErr(422, "机器人创建的 Telegram 账号身份由机器人维护，仅可编辑昵称、状态和投稿权限。")
			}
		} else if (!userNamePattern.MatchString(username) || strings.HasPrefix(strings.ToLower(username), "tg_")) && !(id > 0 && username == str(old["username"])) {
			return nil, catalogErr(422, "账号须为 4–32 位字母、数字或下划线，不能使用 tg_ 前缀。")
		}
		display, err := catalogText(body["display_name"], 100, true)
		if err != nil {
			return nil, err
		}
		status, err := catalogFlag(body["status"])
		if err != nil {
			return nil, err
		}
		allowed, err := catalogFlag(body["submission_allowed"])
		if err != nil {
			return nil, err
		}
		note, err := catalogText(body["submission_note"], 500, false)
		if err != nil {
			return nil, err
		}
		data := Row{"username": username, "display_name": display, "status": status, "submission_allowed": allowed, "submission_note": note}
		existing, err := one(tx, "SELECT id FROM ts_users WHERE username=? AND id<>?", username, id)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return nil, catalogErr(422, "用户账号已存在。")
		}
		if encoded != "" {
			data["password_hash"] = encoded
		}
		if id > 0 {
			err = catalogUpdate(tx, "users", id, data)
		} else {
			data["created_at"] = now()
			id, err = catalogInsert(tx, "users", data)
		}
		if err != nil {
			return nil, err
		}
		if status == 0 || encoded != "" {
			if _, err = tx.Exec("DELETE FROM ts_sessions WHERE user_id=?", id); err != nil {
				return nil, err
			}
		}
		if err = catalogAudit(tx, actor, "保存用户", fmt.Sprintf("用户 #%d", id)); err != nil {
			return nil, err
		}
		row, err := catalogRecord(tx, "users", id, false)
		if row != nil {
			delete(row, "password_hash")
		}
		return row, err
	})
}
func (a *App) catalogDelete(module string, id, actor int64) (Row, error) {
	table := module
	if module == "keys" {
		table = "api_keys"
	}
	return a.catalogTransaction(func(tx *sql.Tx) (Row, error) {
		snapshot, err := catalogRecord(tx, table, id, false)
		if err != nil {
			return nil, err
		}
		if module == "resources" || module == "submissions" {
			if err = catalogLock(tx, str(snapshot["url_hash"])); err != nil {
				return nil, err
			}
		}
		row, err := catalogRecord(tx, table, id, true)
		if err != nil {
			return nil, err
		}
		if (module == "resources" || module == "submissions") && row["url_hash"] != snapshot["url_hash"] {
			return nil, catalogErr(409, "记录已更新，请刷新后重试。")
		}
		switch module {
		case "resources":
			if str(row["status"]) == "blocked" || str(row["status"]) == "invalid" {
				return nil, catalogErr(409, "封禁或失效资源需保留记录用于拦截重复投稿，请核实后调整状态。")
			}
			c, err := one(tx, "SELECT COUNT(*) AS total FROM ts_submissions WHERE resource_id=? OR url_hash=?", id, row["url_hash"])
			if err != nil {
				return nil, err
			}
			if integer(c["total"]) > 0 {
				return nil, catalogErr(409, "资源有关联收录记录，请使用归档以保留审核历史。")
			}
			if _, err = tx.Exec("DELETE FROM ts_favorites WHERE resource_id=?", id); err != nil {
				return nil, err
			}
			if _, err = tx.Exec("DELETE FROM ts_resource_likes WHERE resource_id=?", id); err != nil {
				return nil, err
			}
		case "submissions":
			if str(row["status"]) != "pending" {
				return nil, catalogErr(409, "仅待审核申请可删除；已审核记录需保留用于去重和追溯。")
			}
		case "categories":
			for _, t := range []string{"resources", "submissions"} {
				c, err := one(tx, "SELECT COUNT(*) AS total FROM ts_"+t+" WHERE category_id=?", id)
				if err != nil {
					return nil, err
				}
				if integer(c["total"]) > 0 {
					return nil, catalogErr(422, "这个分类仍有关联资源或申请，请先调整所属分类。")
				}
			}
		case "users":
			if integer(row["status"]) != 1 || integer(row["submission_allowed"]) != 1 {
				return nil, catalogErr(409, "受限用户需保留账号记录以维持拦截，请先核实并解除限制。")
			}
			wallet, err := one(tx, "SELECT balance FROM ts_wallets WHERE user_id=? FOR UPDATE", id)
			if err != nil {
				return nil, err
			}
			if wallet != nil && integer(wallet["balance"]) != 0 {
				return nil, catalogErr(409, "此用户仍有余额，请保留账号和资金记录。")
			}
			for _, relation := range []struct{ table, field string }{{"resources", "owner_id"}, {"submissions", "user_id"}, {"wallet_entries", "user_id"}, {"recharges", "user_id"}, {"ad_orders", "user_id"}, {"reward_claims", "user_id"}} {
				c, err := one(tx, "SELECT COUNT(*) AS total FROM ts_"+relation.table+" WHERE "+relation.field+"=?", id)
				if err != nil {
					return nil, err
				}
				if integer(c["total"]) > 0 {
					return nil, catalogErr(409, "此用户仍有关联资源、收录或资金记录，可使用停用功能保留历史。")
				}
			}
			for _, t := range []string{"favorites", "sessions", "bot_login_codes", "wallets", "telegram_auth_challenges", "telegram_profiles"} {
				if _, err = tx.Exec("DELETE FROM ts_"+t+" WHERE user_id=?", id); err != nil {
					return nil, err
				}
			}
			if integer(row["telegram_id"]) > 0 {
				if _, err = tx.Exec("DELETE FROM ts_bot_sessions WHERE telegram_user_id=?", row["telegram_id"]); err != nil {
					return nil, err
				}
			}
		case "keys":
			if integer(row["status"]) == 1 {
				return nil, catalogErr(409, "请先撤销密钥，再删除记录。")
			}
		default:
			return nil, catalogErr(422, "不支持的删除类型。")
		}
		if _, err = tx.Exec("DELETE FROM ts_"+table+" WHERE id=?", id); err != nil {
			return nil, err
		}
		err = catalogAudit(tx, actor, "删除记录", fmt.Sprintf("%s #%d", module, id))
		return Row{"deleted": true, "id": id}, err
	})
}
