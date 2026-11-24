package app

import (
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

func (a *App) RegisterAccess() {
	a.Handle("GET /admin/api/access/catalog", "admin", "access.manage", false, func(w http.ResponseWriter, r *http.Request, id *Identity) { JSON(w, permissionCatalogJSON()) })
	a.Handle("GET /admin/api/admins", "admin", "access.manage", false, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		rows, err := a.catalogAdmins()
		catalogReply(w, rows, err)
	})
	a.Handle("GET /admin/api/roles", "admin", "access.manage", false, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		rows, err := a.catalogRoles()
		catalogReply(w, rows, err)
	})
	for _, resource := range []string{"admins", "roles"} {
		for _, suffix := range []string{"", "/{id}"} {
			a.Handle("POST /admin/api/"+resource+suffix, "admin", "access.manage", true, func(w http.ResponseWriter, r *http.Request, identity *Identity) {
				b, err := Body(r)
				if err != nil {
					catalogReply(w, nil, err)
					return
				}
				var out Row
				if resource == "admins" {
					out, err = a.catalogSaveAdmin(b, identity.ID, catalogID(r))
				} else {
					out, err = a.catalogSaveRole(b, identity.ID, catalogID(r))
				}
				catalogReply(w, out, err)
			})
		}
		a.Handle("DELETE /admin/api/"+resource+"/{id}", "admin", "access.manage", true, func(w http.ResponseWriter, r *http.Request, identity *Identity) {
			out, err := a.catalogTransaction(func(tx *sql.Tx) (Row, error) {
				id := catalogID(r)
				row, err := catalogRecord(tx, resource, id, true)
				if err != nil {
					return nil, err
				}
				if resource == "admins" {
					if id == identity.ID || integer(row["is_super"]) == 1 {
						return nil, catalogErr(409, "不能删除当前登录账号或超级管理员。")
					}
					if _, err = tx.Exec("DELETE FROM ts_admin_sessions WHERE admin_id=?", id); err != nil {
						return nil, err
					}
					if _, err = tx.Exec("DELETE FROM ts_admin_roles WHERE admin_id=?", id); err != nil {
						return nil, err
					}
				} else {
					count, err := one(tx, "SELECT COUNT(*) AS total FROM ts_admin_roles WHERE role_id=?", id)
					if err != nil {
						return nil, err
					}
					if integer(count["total"]) > 0 {
						return nil, catalogErr(409, "角色仍被管理员使用，请先调整管理员的角色分配。")
					}
				}
				if _, err = tx.Exec("DELETE FROM ts_"+resource+" WHERE id=?", id); err != nil {
					return nil, err
				}
				if err = catalogAudit(tx, identity.ID, "删除权限记录", fmt.Sprintf("%s #%d", resource, id)); err != nil {
					return nil, err
				}
				return Row{"deleted": true, "id": id}, nil
			})
			catalogReply(w, out, err)
		})
	}
}
func (a *App) catalogAdmins() ([]Row, error) {
	rows, err := query(a.DB, "SELECT id,username,display_name,status,is_super,created_at,last_login_at FROM ts_admins ORDER BY id")
	if err != nil {
		return nil, err
	}
	assignments, err := query(a.DB, "SELECT admin_id,role_id FROM ts_admin_roles ORDER BY role_id")
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		ids := []int64{}
		for _, v := range assignments {
			if integer(v["admin_id"]) == integer(row["id"]) {
				ids = append(ids, integer(v["role_id"]))
			}
		}
		row["role_ids"] = ids
	}
	return rows, nil
}
func (a *App) catalogRoles() ([]Row, error) {
	rows, err := query(a.DB, "SELECT r.*,(SELECT COUNT(*) FROM ts_admin_roles ar WHERE ar.role_id=r.id) AS admin_count FROM ts_roles r ORDER BY id")
	if err != nil {
		return nil, err
	}
	perms, err := query(a.DB, "SELECT role_id,permission FROM ts_role_permissions ORDER BY permission")
	if err != nil {
		return nil, err
	}
	fields, err := query(a.DB, "SELECT * FROM ts_role_fields ORDER BY resource,field")
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		p := []string{}
		f := Row{}
		id := integer(row["id"])
		for _, v := range perms {
			if integer(v["role_id"]) == id {
				p = append(p, str(v["permission"]))
			}
		}
		for _, v := range fields {
			if integer(v["role_id"]) != id || integer(v["can_read"]) != 1 {
				continue
			}
			m := str(v["resource"])
			if m == "resources" && str(v["field"]) == "like_count" {
				continue
			}
			if f[m] == nil {
				f[m] = Row{}
			}
			mode := "read"
			if integer(v["can_write"]) == 1 {
				mode = "write"
			}
			f[m].(Row)[str(v["field"])] = mode
		}
		row["permissions"] = p
		row["fields"] = f
	}
	return rows, nil
}
func (a *App) catalogSaveAdmin(input Row, actor, id int64) (Row, error) {
	if err := catalogOnly(input, "username display_name password status role_ids"); err != nil {
		return nil, err
	}
	username := strings.TrimSpace(str(input["username"]))
	if !userNamePattern.MatchString(username) {
		return nil, catalogErr(422, "账号须为 4–32 位字母、数字或下划线。")
	}
	display, err := catalogText(input["display_name"], 100, true)
	if err != nil {
		return nil, err
	}
	password := str(input["password"])
	if (id == 0 || password != "") && (len(password) < 10 || len(password) > 72) {
		return nil, catalogErr(422, "密码长度须为 10–72 字节。")
	}
	statusValue := input["status"]
	if statusValue == nil {
		statusValue = 1
	}
	status, err := catalogFlag(statusValue)
	if err != nil {
		return nil, err
	}
	roles, ok := input["role_ids"].([]any)
	if !ok && input["role_ids"] != nil {
		return nil, catalogErr(422, "角色列表无效。")
	}
	if len(roles) > 50 {
		return nil, catalogErr(422, "最多分配 50 个角色。")
	}
	ids := []int64{}
	seen := map[int64]bool{}
	for _, v := range roles {
		n := integer(v)
		if n < 1 || str(v) != fmt.Sprint(n) {
			return nil, catalogErr(422, "角色 ID 无效。")
		}
		if !seen[n] {
			ids = append(ids, n)
			seen[n] = true
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	passwordEncoded := ""
	if password != "" {
		passwordEncoded, err = passwordHash(password)
		if err != nil {
			return nil, err
		}
	}
	return a.catalogTransaction(func(tx *sql.Tx) (Row, error) {
		id := id
		if id > 0 {
			old, err := catalogRecord(tx, "admins", id, true)
			if err != nil {
				return nil, err
			}
			if integer(old["is_super"]) == 1 && (status != 1 || len(ids) > 0) {
				return nil, catalogErr(422, "超级管理员保持启用，且无需分配角色。")
			}
			if id == actor && status != 1 {
				return nil, catalogErr(422, "不能停用当前登录的管理员。")
			}
		}
		existing, err := one(tx, "SELECT id FROM ts_admins WHERE username=? AND id<>?", username, id)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return nil, catalogErr(422, "管理员账号已存在。")
		}
		for _, roleID := range ids {
			if _, err = catalogRecord(tx, "roles", roleID, true); err != nil {
				return nil, catalogErr(422, "选择的角色不存在，请刷新后重试。")
			}
		}
		data := Row{"username": username, "display_name": display, "status": status}
		if passwordEncoded != "" {
			data["password_hash"] = passwordEncoded
		}
		if id == 0 {
			data["is_super"] = 0
			data["created_at"] = now()
			id, err = catalogInsert(tx, "admins", data)
		} else {
			err = catalogUpdate(tx, "admins", id, data)
		}
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec("DELETE FROM ts_admin_roles WHERE admin_id=?", id); err != nil {
			return nil, err
		}
		for _, roleID := range ids {
			if _, err = tx.Exec("INSERT INTO ts_admin_roles(admin_id,role_id) VALUES (?,?)", id, roleID); err != nil {
				return nil, err
			}
		}
		if status == 0 || passwordEncoded != "" {
			if _, err = tx.Exec("DELETE FROM ts_admin_sessions WHERE admin_id=?", id); err != nil {
				return nil, err
			}
		}
		if err = catalogAudit(tx, actor, "保存管理员", fmt.Sprintf("管理员 #%d · %s", id, username)); err != nil {
			return nil, err
		}
		return Row{"id": id}, nil
	})
}
func validateRole(input Row) (Row, []string, []Row, error) {
	if err := catalogOnly(input, "name description status permissions fields"); err != nil {
		return nil, nil, nil, err
	}
	name, err := catalogText(input["name"], 80, true)
	if err != nil {
		return nil, nil, nil, err
	}
	description, err := catalogText(input["description"], 255, false)
	if err != nil {
		return nil, nil, nil, err
	}
	s := input["status"]
	if s == nil {
		s = 1
	}
	status, err := catalogFlag(s)
	if err != nil {
		return nil, nil, nil, err
	}
	values, ok := input["permissions"].([]any)
	if !ok && input["permissions"] != nil {
		return nil, nil, nil, catalogErr(422, "操作权限格式无效。")
	}
	permissions := []string{}
	allowed := map[string]bool{}
	modules := map[string]ModuleSpec{}
	set := map[string]bool{}
	for _, m := range PermissionCatalog {
		modules[m.Key] = m
		for _, p := range m.Actions {
			allowed[p.Key] = true
		}
	}
	for _, p := range values {
		key, ok := p.(string)
		if !ok || !allowed[key] {
			return nil, nil, nil, catalogErr(422, "未知操作权限。")
		}
		if !set[key] {
			permissions = append(permissions, key)
			set[key] = true
		}
	}
	for _, p := range permissions {
		module, _, _ := strings.Cut(p, ".")
		if !set[module+".read"] {
			return nil, nil, nil, catalogErr(422, "授予操作前请先开启模块查看权限。")
		}
	}
	fields, ok := input["fields"].(map[string]any)
	if empty, isArray := input["fields"].([]any); isArray && len(empty) == 0 {
		fields, ok = map[string]any{}, true
	}
	if !ok && input["fields"] != nil {
		return nil, nil, nil, catalogErr(422, "字段权限格式无效。")
	}
	fieldRows := []Row{}
	for key, raw := range fields {
		module, exists := modules[key]
		values, ok := raw.(map[string]any)
		if empty, isArray := raw.([]any); isArray && len(empty) == 0 {
			values, ok = map[string]any{}, true
		}
		if !exists || !ok {
			return nil, nil, nil, catalogErr(422, "字段权限模块无效。")
		}
		for field, value := range values {
			mode := str(value)
			spec := FieldSpec{}
			found := false
			for _, f := range module.Fields {
				if f.Key == field {
					spec = f
					found = true
					break
				}
			}
			if !found || (mode != "read" && mode != "write") {
				return nil, nil, nil, catalogErr(422, "字段权限无效。")
			}
			if !set[key+".read"] {
				return nil, nil, nil, catalogErr(422, "配置字段前请先开启模块查看权限。")
			}
			if mode == "write" && spec.ReadOnly {
				return nil, nil, nil, catalogErr(422, "此字段只能查看，不能编辑。")
			}
			write := 0
			if mode == "write" {
				write = 1
			}
			fieldRows = append(fieldRows, Row{"resource": key, "field": field, "can_read": 1, "can_write": write})
		}
	}
	return Row{"name": name, "description": description, "status": status, "updated_at": now()}, permissions, fieldRows, nil
}
func (a *App) catalogSaveRole(input Row, actor, id int64) (Row, error) {
	data, permissions, fields, err := validateRole(input)
	if err != nil {
		return nil, err
	}
	return a.catalogTransaction(func(tx *sql.Tx) (Row, error) {
		id := id
		if id > 0 {
			if _, err := catalogRecord(tx, "roles", id, true); err != nil {
				return nil, err
			}
		}
		existing, err := one(tx, "SELECT id FROM ts_roles WHERE name=? AND id<>?", data["name"], id)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return nil, catalogErr(422, "角色名称已存在。")
		}
		if id == 0 {
			data["created_at"] = now()
			id, err = catalogInsert(tx, "roles", data)
		} else {
			err = catalogUpdate(tx, "roles", id, data)
		}
		if err != nil {
			return nil, err
		}
		for _, table := range []string{"role_permissions", "role_fields"} {
			if _, err = tx.Exec("DELETE FROM ts_"+table+" WHERE role_id=?", id); err != nil {
				return nil, err
			}
		}
		for _, p := range permissions {
			if _, err = tx.Exec("INSERT INTO ts_role_permissions(role_id,permission) VALUES (?,?)", id, p); err != nil {
				return nil, err
			}
		}
		for _, f := range fields {
			f["role_id"] = id
			if _, err = catalogInsert(tx, "role_fields", f); err != nil {
				return nil, err
			}
		}
		if err = catalogAudit(tx, actor, "保存角色权限", fmt.Sprintf("角色 #%d · %s", id, str(data["name"]))); err != nil {
			return nil, err
		}
		return Row{"id": id}, nil
	})
}
