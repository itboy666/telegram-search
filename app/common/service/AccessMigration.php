<?php
namespace app\common\service;

use think\facade\Db;

class AccessMigration
{
    public static function run(): array
    {
        $locked = Db::query(
            "SELECT GET_LOCK('starindex_admin_rbac_migration', 10) AS acquired",
        )[0]["acquired"];
        if (!$locked) {
            throw new \RuntimeException("另一个权限迁移正在运行。");
        }
        try {
            foreach (
                explode(
                    ";",
                    file_get_contents(
                        root_path() . "database/migrations/002_admin_rbac.sql",
                    ),
                )
                as $sql
            ) {
                if (trim($sql) !== "") {
                    Db::execute($sql);
                }
            }
            if (
                Db::name("schema_migrations")
                    ->where("version", "002_admin_rbac")
                    ->count()
            ) {
                return ["migrated" => 0, "already_applied" => true];
            }
            $legacy = Db::query("SHOW COLUMNS FROM ts_users LIKE 'role'");
            $count = 0;
            $backup = null;
            if ($legacy) {
                $admins = Db::name("users")
                    ->where("role", "admin")
                    ->select()
                    ->toArray();
                if ($admins) {
                    $directory = root_path() . "runtime/migrations";
                    if (!is_dir($directory)) {
                        mkdir($directory, 0700, true);
                    }
                    $backup =
                        $directory .
                        "/admin-split-" .
                        date("Ymd-His") .
                        "-" .
                        bin2hex(random_bytes(3)) .
                        ".json";
                    $oldMask = umask(0077);
                    try {
                        $written = file_put_contents(
                            $backup,
                            json_encode(
                                [
                                    "admins" => $admins,
                                    "sessions" => Db::name("sessions")
                                        ->whereIn(
                                            "user_id",
                                            array_column($admins, "id"),
                                        )
                                        ->select()
                                        ->toArray(),
                                ],
                                JSON_UNESCAPED_UNICODE |
                                    JSON_PRETTY_PRINT |
                                    JSON_THROW_ON_ERROR,
                            ),
                            LOCK_EX,
                        );
                        if ($written === false) {
                            throw new \RuntimeException("迁移备份写入失败。");
                        }
                    } finally {
                        umask($oldMask);
                    }
                }
                Db::transaction(function () use ($admins, &$count) {
                    foreach ($admins as $admin) {
                        if (
                            Db::name("admins")
                                ->where("id", $admin["id"])
                                ->whereOr("username", $admin["username"])
                                ->find()
                        ) {
                            throw new \RuntimeException(
                                "管理员目标 ID 或账号已存在，迁移已回滚，请先检查冲突。",
                            );
                        }
                        Db::name("admins")->insert([
                            "id" => $admin["id"],
                            "username" => $admin["username"],
                            "password_hash" => $admin["password_hash"],
                            "display_name" => $admin["display_name"],
                            "status" => $admin["status"],
                            "is_super" => 1,
                            "created_at" => $admin["created_at"],
                            "last_login_at" => $admin["last_login_at"],
                        ]);
                        foreach (
                            Db::name("sessions")
                                ->where("user_id", $admin["id"])
                                ->where("scope", "admin")
                                ->select()
                            as $session
                        ) {
                            unset(
                                $session["id"],
                                $session["user_id"],
                                $session["scope"],
                            );
                            Db::name("admin_sessions")->insert(
                                $session + ["admin_id" => $admin["id"]],
                            );
                        }
                        Db::name("sessions")
                            ->where("user_id", $admin["id"])
                            ->delete();
                        $referenced =
                            Db::name("favorites")
                                ->where("user_id", $admin["id"])
                                ->count() ||
                            Db::name("submissions")
                                ->where("user_id", $admin["id"])
                                ->count() ||
                            Db::name("resources")
                                ->where("owner_id", $admin["id"])
                                ->count() ||
                            $admin["telegram_id"] !== null;
                        if ($referenced) {
                            // Keep business ownership, but never preserve administrator login credentials in users.
                            Db::name("users")
                                ->where("id", $admin["id"])
                                ->update([
                                    "role" => "user",
                                    "status" => 0,
                                    "password_hash" => "",
                                ]);
                        } else {
                            Db::name("users")
                                ->where("id", $admin["id"])
                                ->delete();
                        }
                        $count++;
                    }
                    Db::name("sessions")
                        ->where("scope", "<>", "user")
                        ->delete();
                });
                // MySQL DDL commits implicitly; perform only after the data transaction.
                Db::execute("ALTER TABLE ts_users DROP COLUMN role");
            }
            Db::name("schema_migrations")->insert([
                "version" => "002_admin_rbac",
                "applied_at" => date("Y-m-d H:i:s"),
            ]);
            return [
                "migrated" => $count,
                "already_applied" => false,
                "backup" => $backup,
            ];
        } finally {
            Db::query("SELECT RELEASE_LOCK('starindex_admin_rbac_migration')");
        }
    }
}
