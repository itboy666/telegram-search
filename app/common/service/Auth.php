<?php
namespace app\common\service;
use think\facade\Db;
use think\facade\Cookie;
use app\common\exception\DomainError;
class Auth
{
    public static function login(\think\Request $request, string $scope): array
    {
        RateLimit::hit("login:" . $scope . ":" . $request->ip(), 12, 300);
        $username = trim((string) $request->param("username", ""));
        $password = (string) $request->param("password", "");
        [$table, $sessions, $foreignKey] = self::tables($scope);
        $user = Db::name($table)->where("username", $username)->find();
        if (
            !$user ||
            !$user["status"] ||
            !password_verify($password, $user["password_hash"])
        ) {
            throw new DomainError("账号或密码不正确。", 401);
        }
        $token = bin2hex(random_bytes(32));
        $csrf = bin2hex(random_bytes(24));
        $user = CatalogTransaction::run(function () use (
            $user,
            $table,
            $sessions,
            $foreignKey,
            $token,
            $csrf,
            $scope,
        ) {
            // Recheck under the same account lock used by reset, disable and delete.
            // Password verification stays outside the lock to keep its hold time short.
            $current = Db::name($table)
                ->where("id", $user["id"])
                ->lock(true)
                ->find();
            if (
                !$current ||
                !$current["status"] ||
                $current["username"] !== $user["username"] ||
                !hash_equals($current["password_hash"], $user["password_hash"])
            ) {
                throw new DomainError("账号或密码不正确。", 401);
            }
            Db::name($sessions)->insert(
                [
                    $foreignKey => $current["id"],
                    "token_hash" => hash("sha256", $token),
                    "csrf_token" => $csrf,
                    "expires_at" => date("Y-m-d H:i:s", time() + 43200),
                    "created_at" => date("Y-m-d H:i:s"),
                ] + ($scope === "user" ? ["scope" => "user"] : []),
            );
            Db::name($table)
                ->where("id", $current["id"])
                ->update(["last_login_at" => date("Y-m-d H:i:s")]);
            return $current;
        });
        Cookie::set("starindex_" . $scope, $token, [
            "expire" => 43200,
            "path" => "/",
            "httponly" => true,
            "secure" => $request->isSsl(),
            "samesite" => "Lax",
        ]);
        return ["user" => self::publicUser($user), "csrf_token" => $csrf] +
            ($scope === "admin" ? ["access" => Rbac::access($user)] : []);
    }
    public static function session(
        \think\Request $request,
        string $scope,
        bool $mutating = false,
    ): array {
        [$table, $sessions, $foreignKey] = self::tables($scope);
        $token = (string) $request->cookie("starindex_" . $scope, "");
        $session = $token
            ? Db::name($sessions)
                ->where("token_hash", hash("sha256", $token))
                ->when($scope === "user", fn($q) => $q->where("scope", "user"))
                ->where("expires_at", ">", date("Y-m-d H:i:s"))
                ->find()
            : null;
        if (!$session) {
            throw new DomainError("请先登录后继续。", 401);
        }
        $user = Db::name($table)
            ->where("id", $session[$foreignKey])
            ->where("status", 1)
            ->find();
        if (!$user) {
            throw new DomainError("当前账号没有访问权限。", 403);
        }
        if (
            $mutating &&
            !hash_equals(
                $session["csrf_token"],
                (string) $request->header("x-csrf-token", ""),
            )
        ) {
            throw new DomainError("页面验证已失效，请刷新后重试。", 403);
        }
        return [
            "user" => self::publicUser($user),
            "csrf_token" => $session["csrf_token"],
            "session_id" => $session["id"],
        ] + ($scope === "admin" ? ["access" => Rbac::access($user)] : []);
    }
    public static function logout(\think\Request $request, string $scope): array
    {
        $s = self::session($request, $scope, true);
        Db::name(self::tables($scope)[1])
            ->where("id", $s["session_id"])
            ->delete();
        Cookie::delete("starindex_" . $scope, ["path" => "/"]);
        return ["logged_out" => true];
    }
    public static function register(\think\Request $request): array
    {
        RateLimit::hit("register:" . $request->ip(), 5, 3600);
        $name = trim((string) $request->param("username", ""));
        $password = (string) $request->param("password", "");
        if (!preg_match('/^[A-Za-z0-9_]{4,32}$/', $name)) {
            throw new DomainError("账号须为 4–32 位字母、数字或下划线。");
        }
        if (str_starts_with(strtolower($name), "tg_")) {
            throw new DomainError(
                "此账号前缀保留给 Telegram 用户，请换一个账号。",
            );
        }
        if (strlen($password) < 10 || strlen($password) > 128) {
            throw new DomainError("密码长度须为 10–128 位。");
        }
        if (Db::name("users")->where("username", $name)->find()) {
            throw new DomainError("该账号已被使用。");
        }
        Db::name("users")->insert([
            "username" => $name,
            "display_name" => $name,
            "password_hash" => password_hash($password, PASSWORD_DEFAULT),
            "created_at" => date("Y-m-d H:i:s"),
        ]);
        return self::login($request, "user");
    }
    private static function publicUser(array $u): array
    {
        return array_intersect_key(
            $u,
            array_flip([
                "id",
                "username",
                "display_name",
                "is_super",
                "telegram_id",
                "submission_allowed",
                "submission_note",
                "created_at",
            ]),
        );
    }
    private static function tables(string $scope): array
    {
        return match ($scope) {
            "admin" => ["admins", "admin_sessions", "admin_id"],
            "user" => ["users", "sessions", "user_id"],
            default => throw new \InvalidArgumentException(
                "Unknown authentication scope",
            ),
        };
    }
}
