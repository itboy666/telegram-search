<?php
namespace app\common\service;

use app\common\exception\DomainError;
use think\facade\Db;

class AccessManagement
{
    private static function only(array $input, array $keys): void
    {
        if (array_diff(array_keys($input), $keys)) {
            throw new DomainError("请求包含不可修改的账号或权限字段。", 422);
        }
    }
    private static function text(
        mixed $value,
        int $max,
        string $field,
        string $label,
    ): string {
        if (
            !is_string($value) ||
            trim($value) === "" ||
            mb_strlen(trim($value)) > $max
        ) {
            throw new DomainError($label . "须为 1–" . $max . " 字。", 422, [
                $field => "请检查" . $label,
            ]);
        }
        return trim($value);
    }
    private static function status(mixed $value): int
    {
        if (!in_array($value, [0, 1, "0", "1"], true)) {
            throw new DomainError("状态值无效。", 422, [
                "status" => "请选择启用或停用",
            ]);
        }
        return (int) $value;
    }
    public static function admins(): array
    {
        $items = Db::name("admins")
            ->field(
                "id,username,display_name,status,is_super,created_at,last_login_at",
            )
            ->order("id")
            ->select()
            ->toArray();
        $assignments = Db::name("admin_roles")->select()->toArray();
        foreach ($items as &$item) {
            $item["role_ids"] = array_values(
                array_map(
                    fn($row) => (int) $row["role_id"],
                    array_filter(
                        $assignments,
                        fn($row) => (int) $row["admin_id"] ===
                            (int) $item["id"],
                    ),
                ),
            );
        }
        unset($item);
        return $items;
    }
    public static function roles(): array
    {
        $items = Db::name("roles")->order("id")->select()->toArray();
        foreach ($items as &$role) {
            $role["permissions"] = Db::name("role_permissions")
                ->where("role_id", $role["id"])
                ->column("permission");
            $role["fields"] = [];
            foreach (
                Db::name("role_fields")->where("role_id", $role["id"])->select()
                as $field
            ) {
                $role["fields"][$field["resource"]][$field["field"]] = $field[
                    "can_write"
                ]
                    ? "write"
                    : "read";
            }
            $role["admin_count"] = Db::name("admin_roles")
                ->where("role_id", $role["id"])
                ->count();
        }
        unset($role);
        return $items;
    }
    public static function saveAdmin(array $input, int $actor, ?int $id): array
    {
        self::only($input, [
            "username",
            "display_name",
            "password",
            "status",
            "role_ids",
        ]);
        $username = self::text(
            $input["username"] ?? "",
            32,
            "username",
            "账号",
        );
        if (!preg_match('/^[A-Za-z0-9_]{4,32}$/', $username)) {
            throw new DomainError("账号须为 4–32 位字母、数字或下划线。", 422, [
                "username" => "账号格式不正确",
            ]);
        }
        $display = self::text(
            $input["display_name"] ?? "",
            100,
            "display_name",
            "显示名称",
        );
        $password = $input["password"] ?? "";
        if (
            !is_string($password) ||
            ((!$id || $password !== "") &&
                (strlen($password) < 10 || strlen($password) > 128))
        ) {
            throw new DomainError("密码长度须为 10–128 位。", 422, [
                "password" => "请输入 10–128 位密码",
            ]);
        }
        $status = self::status($input["status"] ?? 1);
        $roles = $input["role_ids"] ?? [];
        if (!is_array($roles) || !array_is_list($roles) || count($roles) > 50) {
            throw new DomainError("角色列表无效。");
        }
        foreach ($roles as $role) {
            if (!is_int($role) || $role < 1) {
                throw new DomainError("角色 ID 无效。");
            }
        }
        $roles = array_values(array_unique($roles));
        return Db::transaction(function () use (
            $id,
            $actor,
            $username,
            $display,
            $password,
            $status,
            $roles,
        ) {
            $old = $id
                ? Db::name("admins")->where("id", $id)->lock(true)->find()
                : null;
            if ($id && !$old) {
                throw new DomainError("管理员不存在。", 404);
            }
            if ($old && $old["is_super"] && (!$status || $roles)) {
                throw new DomainError(
                    "超级管理员保持启用，且无需分配角色。",
                    422,
                );
            }
            if ($id === $actor && !$status) {
                throw new DomainError("不能停用当前登录的管理员。", 422);
            }
            if (
                Db::name("admins")
                    ->where("username", $username)
                    ->when($id, fn($q) => $q->where("id", "<>", $id))
                    ->count()
            ) {
                throw new DomainError("管理员账号已存在。", 422, [
                    "username" => "此管理员账号已使用",
                ]);
            }
            if (
                $roles &&
                Db::name("roles")
                    ->whereIn("id", $roles)
                    ->lock(true)
                    ->count() !== count($roles)
            ) {
                throw new DomainError("选择的角色不存在，请刷新后重试。", 422);
            }
            $data = [
                "username" => $username,
                "display_name" => $display,
                "status" => $status,
            ];
            if ($password !== "") {
                $data["password_hash"] = password_hash(
                    $password,
                    PASSWORD_DEFAULT,
                );
            }
            if ($id) {
                Db::name("admins")->where("id", $id)->update($data);
            } else {
                $id = (int) Db::name("admins")->insertGetId(
                    $data + [
                        "is_super" => 0,
                        "created_at" => date("Y-m-d H:i:s"),
                    ],
                );
            }
            Db::name("admin_roles")->where("admin_id", $id)->delete();
            foreach ($roles as $role) {
                Db::name("admin_roles")->insert([
                    "admin_id" => $id,
                    "role_id" => $role,
                ]);
            }
            if (!$status || $password !== "") {
                Db::name("admin_sessions")->where("admin_id", $id)->delete();
            }
            Settings::audit(
                "保存管理员",
                "管理员 #" . $id . " · " . $username,
                $actor,
            );
            return ["id" => $id];
        });
    }
    public static function saveRole(array $input, int $actor, ?int $id): array
    {
        self::only($input, [
            "name",
            "description",
            "status",
            "permissions",
            "fields",
        ]);
        $name = self::text($input["name"] ?? "", 80, "name", "角色名称");
        $description = $input["description"] ?? "";
        if (!is_string($description) || mb_strlen($description) > 255) {
            throw new DomainError("角色说明最多 255 字。");
        }
        $status = self::status($input["status"] ?? 1);
        $permissions = $input["permissions"] ?? [];
        $fields = $input["fields"] ?? [];
        $catalog = Rbac::catalog();
        if (
            !is_array($permissions) ||
            !array_is_list($permissions) ||
            !is_array($fields)
        ) {
            throw new DomainError("权限格式无效。", 422);
        }
        foreach ($permissions as $permission) {
            if (!is_string($permission)) {
                throw new DomainError("操作权限无效。", 422);
            }
            [$module, $action] = array_pad(explode(".", $permission, 2), 2, "");
            if (!isset($catalog[$module]["actions"][$action])) {
                throw new DomainError("未知操作权限：" . $permission, 422);
            }
            if (!in_array("$module.read", $permissions, true)) {
                throw new DomainError(
                    "授予操作前请先勾选该模块的查看权限。",
                    422,
                );
            }
        }
        $fieldRows = [];
        foreach ($fields as $module => $values) {
            if (!isset($catalog[$module]) || !is_array($values)) {
                throw new DomainError("字段权限模块无效。", 422);
            }
            foreach ($values as $field => $mode) {
                if (
                    !isset($catalog[$module]["fields"][$field]) ||
                    !in_array($mode, ["read", "write"], true)
                ) {
                    throw new DomainError("字段权限无效。", 422);
                }
                if (!in_array("$module.read", $permissions, true)) {
                    throw new DomainError(
                        "配置字段前请先开启模块查看权限。",
                        422,
                    );
                }
                if (
                    $mode === "write" &&
                    !in_array($field, $catalog[$module]["writable"], true)
                ) {
                    throw new DomainError("此字段只能查看，不能编辑。", 422);
                }
                $fieldRows[] = [
                    "resource" => $module,
                    "field" => $field,
                    "can_read" => 1,
                    "can_write" => $mode === "write" ? 1 : 0,
                ];
            }
        }
        return Db::transaction(function () use (
            $id,
            $actor,
            $name,
            $description,
            $status,
            $permissions,
            $fieldRows,
        ) {
            if (
                $id &&
                !Db::name("roles")->where("id", $id)->lock(true)->find()
            ) {
                throw new DomainError("角色不存在。", 404);
            }
            if (
                Db::name("roles")
                    ->where("name", $name)
                    ->when($id, fn($q) => $q->where("id", "<>", $id))
                    ->count()
            ) {
                throw new DomainError("角色名称已存在。", 422, [
                    "name" => "请使用其他名称",
                ]);
            }
            $data = [
                "name" => $name,
                "description" => trim($description),
                "status" => $status,
                "updated_at" => date("Y-m-d H:i:s"),
            ];
            if ($id) {
                Db::name("roles")->where("id", $id)->update($data);
            } else {
                $id = (int) Db::name("roles")->insertGetId(
                    $data + ["created_at" => $data["updated_at"]],
                );
            }
            Db::name("role_permissions")->where("role_id", $id)->delete();
            Db::name("role_fields")->where("role_id", $id)->delete();
            foreach (array_unique($permissions) as $permission) {
                Db::name("role_permissions")->insert([
                    "role_id" => $id,
                    "permission" => $permission,
                ]);
            }
            foreach ($fieldRows as $row) {
                Db::name("role_fields")->insert($row + ["role_id" => $id]);
            }
            Settings::audit(
                "保存角色权限",
                "角色 #" . $id . " · " . $name,
                $actor,
            );
            return ["id" => $id];
        });
    }
    public static function deleteAdmin(int $id, int $actor): array
    {
        return CatalogTransaction::run(function () use ($id, $actor) {
            $row = AdminRecords::record("admins", $id, true);
            if ($id === $actor || $row["is_super"]) {
                throw new DomainError(
                    "不能删除当前登录账号或超级管理员。",
                    409,
                );
            }
            Db::name("admin_sessions")->where("admin_id", $id)->delete();
            Db::name("admin_roles")->where("admin_id", $id)->delete();
            Db::name("admins")->where("id", $id)->delete();
            Settings::audit(
                "删除管理员",
                "管理员 #" . $id . " · " . $row["username"],
                $actor,
            );
            return ["deleted" => true, "id" => $id];
        });
    }
    public static function deleteRole(int $id, int $actor): array
    {
        return CatalogTransaction::run(function () use ($id, $actor) {
            AdminRecords::record("roles", $id, true);
            if (Db::name("admin_roles")->where("role_id", $id)->count()) {
                throw new DomainError(
                    "角色仍被管理员使用，请先调整管理员的角色分配。",
                    409,
                );
            }
            Db::name("roles")->where("id", $id)->delete();
            Settings::audit("删除角色", "角色 #" . $id, $actor);
            return ["deleted" => true, "id" => $id];
        });
    }
}
