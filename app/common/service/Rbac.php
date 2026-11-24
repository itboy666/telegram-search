<?php
namespace app\common\service;

use app\common\exception\DomainError;
use think\facade\Db;

class Rbac
{
    public static function catalog(): array
    {
        return config("admin_permissions");
    }
    public static function access(array $admin): array
    {
        $access = [
            "super" => (bool) $admin["is_super"],
            "permissions" => [],
            "fields" => [],
            "roles" => [],
        ];
        if ($access["super"]) {
            $access["permissions"][] = "access.manage";
            foreach (self::catalog() as $module => $definition) {
                foreach ($definition["actions"] as $action => $_) {
                    $access["permissions"][] = "$module.$action";
                }
                $access["fields"][$module] = [
                    "read" => array_keys($definition["fields"]),
                    "write" => $definition["writable"],
                ];
            }
            return $access;
        }
        $roles = Db::name("roles")
            ->alias("r")
            ->join(["ts_admin_roles" => "ar"], "ar.role_id=r.id")
            ->where("ar.admin_id", $admin["id"])
            ->where("r.status", 1)
            ->field("r.id,r.name")
            ->select()
            ->toArray();
        $access["roles"] = $roles;
        $ids = array_column($roles, "id");
        if (!$ids) {
            return $access;
        }
        $catalog = self::catalog();
        foreach (
            Db::name("role_permissions")
                ->whereIn("role_id", $ids)
                ->column("permission")
            as $permission
        ) {
            [$module, $action] = array_pad(explode(".", $permission, 2), 2, "");
            if (isset($catalog[$module]["actions"][$action])) {
                $access["permissions"][] = $permission;
            }
        }
        $access["permissions"] = array_values(
            array_unique($access["permissions"]),
        );
        foreach (
            Db::name("role_fields")->whereIn("role_id", $ids)->select()
            as $field
        ) {
            $module = $field["resource"];
            $key = $field["field"];
            if (!isset($catalog[$module]["fields"][$key])) {
                continue;
            }
            $access["fields"][$module] ??= ["read" => [], "write" => []];
            if ($field["can_read"]) {
                $access["fields"][$module]["read"][] = $key;
                if (
                    $field["can_write"] &&
                    in_array($key, $catalog[$module]["writable"], true)
                ) {
                    $access["fields"][$module]["write"][] = $key;
                }
            }
        }
        foreach ($access["fields"] as &$fields) {
            foreach ($fields as &$list) {
                $list = array_values(array_unique($list));
            }
            unset($list);
        }
        unset($fields);
        return $access;
    }
    public static function can(array $access, string $permission): bool
    {
        return in_array($permission, $access["permissions"], true);
    }
    public static function require(array $access, string $permission): void
    {
        if (!self::can($access, $permission)) {
            throw new DomainError("没有此操作的权限，请联系超级管理员。", 403);
        }
    }
    public static function readable(
        array $access,
        string $module,
        string $field,
    ): bool {
        return in_array($field, $access["fields"][$module]["read"] ?? [], true);
    }
    public static function writable(
        array $access,
        string $module,
        string $field,
    ): bool {
        return in_array(
            $field,
            $access["fields"][$module]["write"] ?? [],
            true,
        );
    }
    public static function checkWrite(
        array $access,
        string $module,
        array $input,
    ): void {
        foreach ($input as $field => $_) {
            if (!self::writable($access, $module, (string) $field)) {
                $label = self::catalog()[$module]["fields"][$field] ?? $field;
                throw new DomainError("无权修改字段「" . $label . "」。", 403, [
                    (string) $field => "此字段没有写入权限",
                ]);
            }
        }
    }
    public static function project(
        array $access,
        string $module,
        array $row,
    ): array {
        $fields = $access["fields"][$module]["read"] ?? [];
        if ($module === "resources") {
            if (in_array("type", $fields, true)) {
                $fields[] = "type_label";
            }
            if (in_array("category_id", $fields, true)) {
                $fields[] = "category_name";
            }
        }
        return array_intersect_key($row, array_flip(["id", ...$fields]));
    }
    public static function rows(
        array $access,
        string $module,
        array $rows,
    ): array {
        return array_map(
            fn($row) => self::project($access, $module, $row),
            $rows,
        );
    }
    public static function policy(string $action, bool $hasId): array
    {
        return match (strtolower($action)) {
            "dashboard" => ["dashboard.read", "dashboard", "dashboard"],
            "resources" => ["resources.read", "resources", "page"],
            "resource" => ["resources.read", "resources", "record"],
            "deleteresource" => ["resources.delete", "resources", "result"],
            "saveresource" => [
                $hasId ? "resources.update" : "resources.create",
                "resources",
                "record",
            ],
            "archiveresource" => ["resources.archive", "resources", "result"],
            "categories" => ["categories.read", "categories", "list"],
            "savecategory" => [
                $hasId ? "categories.update" : "categories.create",
                "categories",
                "result",
            ],
            "deletecategory" => ["categories.delete", "categories", "result"],
            "submissions" => ["submissions.read", "submissions", "page"],
            "savesubmission" => [
                $hasId ? "submissions.update" : "submissions.create",
                "submissions",
                "record",
            ],
            "deletesubmission" => [
                "submissions.delete",
                "submissions",
                "result",
            ],
            "review" => ["submissions.review", "submissions", "record"],
            "users" => ["users.read", "users", "page"],
            "saveuser" => [
                $hasId ? "users.update" : "users.create",
                "users",
                "record",
            ],
            "deleteuser" => ["users.delete", "users", "result"],
            "userstatus" => ["users.update", "users", "record"],
            "usersubmissionpolicy" => ["users.update", "users", "record"],
            "settings", "savesettings" => [
                $action === "settings" ? "settings.read" : "settings.update",
                "settings",
                "record",
            ],
            "botmenu", "savebotmenu" => [
                strtolower($action) === "botmenu"
                    ? "bot_menu.read"
                    : "bot_menu.update",
                "bot_menu",
                "menu",
            ],
            "bot" => ["bot.read", "bot", "record"],
            "savebot" => ["bot.update", "bot", "record"],
            "botsecurity" => ["bot_security.read", "bot_security", "record"],
            "savebotsecurity" => [
                "bot_security.update",
                "bot_security",
                "record",
            ],
            "botgroups" => ["bot_groups.read", "bot_groups", "page"],
            "savebotgroup" => ["bot_groups.update", "bot_groups", "record"],
            "logs" => ["logs.read", "logs", "page"],
            "keys" => ["keys.read", "keys", "list"],
            "createkey" => ["keys.create", "keys", "record"],
            "revokekey" => ["keys.revoke", "keys", "result"],
            "updatekey" => ["keys.update", "keys", "record"],
            "deletekey" => ["keys.delete", "keys", "result"],
            default => throw new DomainError("此接口尚未配置权限。", 403),
        };
    }
    public static function output(
        array $access,
        string $module,
        string $shape,
        array $data,
    ): array {
        if ($shape === "list") {
            return self::rows($access, $module, $data);
        }
        if ($shape === "page") {
            $data["items"] = self::rows($access, $module, $data["items"]);
        } elseif ($shape === "record") {
            $data = self::project($access, $module, $data);
        } elseif ($shape === "menu") {
            $data["config"] = self::project($access, $module, $data["config"]);
            // Delivery diagnostics belong to the connection permission.
            if (
                !self::can($access, "bot.read") ||
                !self::readable($access, "bot", "custom_emoji")
            ) {
                unset($data["delivery"]);
            }
        } elseif ($shape === "dashboard") {
            $data = self::project($access, "dashboard", $data);
            foreach (
                [
                    "recent_resources" => "resources",
                    "activity" => "logs",
                    "bot" => "bot",
                ]
                as $key => $source
            ) {
                if (!isset($data[$key])) {
                    continue;
                }
                if (!self::can($access, "$source.read")) {
                    unset($data[$key]);
                } else {
                    $data[$key] =
                        $key === "bot"
                            ? self::project($access, $source, $data[$key])
                            : self::rows($access, $source, $data[$key]);
                }
            }
        }
        return $data;
    }
}
