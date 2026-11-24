<?php
namespace app\common\service;

use app\common\exception\DomainError;
use think\facade\Db;

/** Administrative mutations retain review history and enforce link identity locks. */
class AdminRecords
{
    public static function filter(
        $query,
        array $input,
        array $access,
        string $module,
        string $alias = "",
    ): void {
        $prefix = $alias === "" ? "" : $alias . ".";
        if (isset($input["id"]) && $input["id"] !== "") {
            $id = filter_var($input["id"], FILTER_VALIDATE_INT, [
                "options" => ["min_range" => 1],
            ]);
            if (!$id) {
                throw new DomainError("ID 必须为正整数。");
            }
            $query->where($prefix . "id", $id);
        }
        $keyword = trim((string) ($input["q"] ?? ""));
        if ($keyword === "") {
            return;
        }
        if (mb_strlen($keyword) > 120) {
            throw new DomainError("搜索关键词最多 120 字。");
        }
        $fields = match ($module) {
            "categories" => ["name", "description"],
            "submissions" => ["title", "url", "description"],
            "users" => ["username", "display_name", "telegram_id"],
            "keys" => ["name", "prefix"],
            "logs" => ["action", "detail", "actor_id"],
            default => [],
        };
        $fields = array_values(
            array_filter(
                $fields,
                fn($field) => Rbac::readable($access, $module, $field),
            ),
        );
        $id = filter_var($keyword, FILTER_VALIDATE_INT, [
            "options" => ["min_range" => 1],
        ]);
        if (!$fields && !$id) {
            throw new DomainError(
                "没有可搜索字段的查看权限，请使用 ID 查询。",
                403,
            );
        }
        $query->where(function ($q) use ($fields, $keyword, $id, $prefix) {
            $clauses = [];
            if ($id) {
                $clauses[] = [$prefix . "id", "=", $id];
            }
            foreach ($fields as $field) {
                $clauses[] = [
                    $prefix . $field,
                    "like",
                    "%" . addcslashes($keyword, "\\%_") . "%",
                ];
            }
            foreach ($clauses as $index => [$field, $operator, $value]) {
                $index === 0
                    ? $q->where($field, $operator, $value)
                    : $q->whereOr($field, $operator, $value);
            }
        });
    }

    public static function record(
        string $table,
        int $id,
        bool $lock = false,
    ): array {
        $row = Db::name($table)->where("id", $id)->lock($lock)->find();
        if (!$row) {
            throw new DomainError("记录不存在或已删除，请刷新列表。", 404);
        }
        return $row;
    }

    private static function only(array $input, array $fields): void
    {
        if (array_diff(array_keys($input), $fields)) {
            throw new DomainError("请求包含此操作不可修改的字段。");
        }
    }

    private static function text(
        mixed $value,
        int $max,
        string $field,
        string $label,
        bool $required = true,
    ): string {
        if (
            !is_string($value) ||
            mb_strlen(trim($value)) > $max ||
            ($required && trim($value) === "")
        ) {
            throw new DomainError($label . "格式不正确。", 422, [
                $field => $required ? "请填写 1–{$max} 字" : "最多 {$max} 字",
            ]);
        }
        return trim($value);
    }

    private static function flag(mixed $value, string $field): int
    {
        if (!in_array($value, [0, 1, "0", "1"], true)) {
            throw new DomainError("状态值无效。", 422, [
                $field => "请选择有效状态",
            ]);
        }
        return (int) $value;
    }

    public static function saveUser(array $input, ?int $id, int $actor): array
    {
        self::only($input, [
            "username",
            "display_name",
            "password",
            "status",
            "submission_allowed",
            "submission_note",
        ]);
        return CatalogTransaction::run(function () use ($input, $id, $actor) {
            $old = $id ? self::record("users", $id, true) : [];
            $merged = array_replace($old, $input);
            $username = self::text(
                $merged["username"] ?? "",
                80,
                "username",
                "账号",
            );
            if (!empty($old["telegram_id"])) {
                if (
                    $username !== $old["username"] ||
                    !empty($input["password"])
                ) {
                    throw new DomainError(
                        "Telegram 账号身份由机器人维护，仅可编辑昵称、状态和投稿权限。",
                    );
                }
            } elseif (
                !preg_match('/^[A-Za-z0-9_]{4,32}$/D', $username) ||
                str_starts_with(strtolower($username), "tg_")
            ) {
                throw new DomainError(
                    "账号须为 4–32 位字母、数字或下划线，不能使用 tg_ 前缀。",
                    422,
                    ["username" => "请检查账号格式"],
                );
            }
            if (
                Db::name("users")
                    ->where("username", $username)
                    ->when($id, fn($q) => $q->where("id", "<>", $id))
                    ->count()
            ) {
                throw new DomainError("用户账号已存在。", 422, [
                    "username" => "此账号已被使用",
                ]);
            }
            $data = [
                "username" => $username,
                "display_name" => self::text(
                    $merged["display_name"] ?? "",
                    100,
                    "display_name",
                    "昵称",
                ),
                "status" => self::flag($merged["status"] ?? 1, "status"),
                "submission_allowed" => self::flag(
                    $merged["submission_allowed"] ?? 1,
                    "submission_allowed",
                ),
                "submission_note" => self::text(
                    $merged["submission_note"] ?? "",
                    500,
                    "submission_note",
                    "投稿说明",
                    false,
                ),
            ];
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
            if ($password !== "") {
                $data["password_hash"] = password_hash(
                    $password,
                    PASSWORD_DEFAULT,
                );
            }
            if ($id) {
                Db::name("users")->where("id", $id)->update($data);
                if (!$data["status"] || $password !== "") {
                    Db::name("sessions")->where("user_id", $id)->delete();
                }
            } else {
                $id = (int) Db::name("users")->insertGetId(
                    $data + ["created_at" => date("Y-m-d H:i:s")],
                );
            }
            Settings::audit("保存用户", "用户 #" . $id, $actor);
            $row = self::record("users", $id);
            unset($row["password_hash"]);
            return $row;
        });
    }

    public static function deleteUser(int $id, int $actor): array
    {
        return CatalogTransaction::run(function () use ($id, $actor) {
            $row = self::record("users", $id, true);
            if (!$row["status"] || !$row["submission_allowed"]) {
                throw new DomainError(
                    "受限用户需保留账号记录以维持拦截；请先核实并解除限制后再删除。",
                    409,
                );
            }
            if (
                Db::name("resources")->where("owner_id", $id)->count() ||
                Db::name("submissions")->where("user_id", $id)->count() ||
                Db::name("wallet_entries")->where("user_id", $id)->count() ||
                Db::name("recharges")->where("user_id", $id)->count() ||
                Db::name("ad_orders")->where("user_id", $id)->count()
            ) {
                throw new DomainError(
                    "此用户仍有关联资源、收录或资金订单记录，可使用停用功能保留历史。",
                    409,
                );
            }
            Db::name("favorites")->where("user_id", $id)->delete();
            Db::name("sessions")->where("user_id", $id)->delete();
            Db::name("bot_login_codes")->where("user_id", $id)->delete();
            Db::name("wallets")->where("user_id", $id)->delete();
            if ($row["telegram_id"]) {
                Db::name("bot_sessions")
                    ->where("telegram_user_id", $row["telegram_id"])
                    ->delete();
            }
            Db::name("users")->where("id", $id)->delete();
            Settings::audit("删除用户", "用户 #" . $id, $actor);
            return ["deleted" => true, "id" => $id];
        });
    }

    public static function deleteResource(int $id, int $actor): array
    {
        return CatalogTransaction::run(function () use ($id, $actor) {
            $snapshot = self::record("resources", $id);
            TelegramLink::lock([$snapshot["url_hash"]]);
            $row = self::record("resources", $id, true);
            if ($snapshot["url_hash"] !== $row["url_hash"]) {
                throw new DomainError("资源已更新，请刷新后重试。", 409);
            }
            if (in_array($row["status"], ["blocked", "invalid"], true)) {
                throw new DomainError(
                    "封禁或失效资源保留记录用于拦截重复投稿；请先核实并调整状态后再删除。",
                    409,
                );
            }
            if (
                Db::name("submissions")
                    ->where(function ($q) use ($row, $id) {
                        $q->where("resource_id", $id)->whereOr(
                            "url_hash",
                            $row["url_hash"],
                        );
                    })
                    ->count()
            ) {
                throw new DomainError(
                    "资源有关联收录记录，请使用归档以保留审核历史。",
                    409,
                );
            }
            Db::name("favorites")->where("resource_id", $id)->delete();
            Db::name("resources")->where("id", $id)->delete();
            Settings::audit(
                "删除资源",
                "资源 #" . $id . " · " . $row["title"],
                $actor,
            );
            return ["deleted" => true, "id" => $id];
        });
    }

    public static function saveSubmission(
        array $input,
        ?int $id,
        int $actor,
    ): array {
        self::only($input, [
            "title",
            "url",
            "type",
            "description",
            "category_id",
            "user_id",
        ]);
        if (!$id) {
            $userId = filter_var(
                $input["user_id"] ?? null,
                FILTER_VALIDATE_INT,
                ["options" => ["min_range" => 1]],
            );
            if (!$userId) {
                throw new DomainError("请填写真实的提交用户 ID。", 422, [
                    "user_id" => "请先在用户管理中确认 ID",
                ]);
            }
            return CatalogTransaction::run(function () use (
                $input,
                $userId,
                $actor,
            ) {
                $result = SubmissionService::create($userId, $input);
                Settings::audit(
                    "新增收录申请",
                    "申请 #" . $result["id"] . " · 用户 #" . $userId,
                    $actor,
                );
                return self::record("submissions", $result["id"]);
            });
        }
        return CatalogTransaction::run(function () use ($input, $id, $actor) {
            $old = self::record("submissions", $id);
            $data = ResourceService::validate(
                array_replace($old, $input, ["status" => "published"]),
            );
            TelegramLink::lock([$old["url_hash"], $data["url_hash"]]);
            $row = self::record("submissions", $id, true);
            if (
                $row["status"] !== "pending" ||
                $row["url_hash"] !== $old["url_hash"]
            ) {
                throw new DomainError(
                    "仅能编辑尚未审核且未被他人更新的申请，请刷新列表。",
                    409,
                );
            }
            if (
                isset($input["user_id"]) &&
                (int) $input["user_id"] !== (int) $row["user_id"]
            ) {
                throw new DomainError("不能更改原申请的提交用户。");
            }
            if (
                $resource = Db::name("resources")
                    ->where("url_hash", $data["url_hash"])
                    ->find()
            ) {
                throw new DomainError(TelegramLink::resourceMessage($resource));
            }
            if (
                $other = Db::name("submissions")
                    ->where("url_hash", $data["url_hash"])
                    ->where("id", "<>", $id)
                    ->find()
            ) {
                throw new DomainError(TelegramLink::submissionMessage($other));
            }
            if ($data["category_id"]) {
                self::record("categories", $data["category_id"], true);
            }
            $fields = array_intersect_key(
                $data,
                array_flip([
                    "title",
                    "url",
                    "url_hash",
                    "type",
                    "description",
                    "category_id",
                ]),
            );
            Db::name("submissions")->where("id", $id)->update($fields);
            Settings::audit("编辑收录申请", "申请 #" . $id, $actor);
            return self::record("submissions", $id);
        });
    }

    public static function deleteSubmission(int $id, int $actor): array
    {
        return CatalogTransaction::run(function () use ($id, $actor) {
            $snapshot = self::record("submissions", $id);
            TelegramLink::lock([$snapshot["url_hash"]]);
            $row = self::record("submissions", $id, true);
            if (
                $row["url_hash"] !== $snapshot["url_hash"] ||
                $row["status"] !== "pending"
            ) {
                throw new DomainError(
                    "仅待审核申请可删除；已审核记录需保留用于去重和追溯。",
                    409,
                );
            }
            Db::name("submissions")->where("id", $id)->delete();
            Settings::audit("删除待审核申请", "申请 #" . $id, $actor);
            return ["deleted" => true, "id" => $id];
        });
    }

    public static function updateKey(array $input, int $id, int $actor): array
    {
        self::only($input, ["name", "daily_limit"]);
        return CatalogTransaction::run(function () use ($input, $id, $actor) {
            $row = self::record("api_keys", $id, true);
            $input = array_replace($row, $input);
            $limit = filter_var($input["daily_limit"], FILTER_VALIDATE_INT, [
                "options" => ["min_range" => 1, "max_range" => 100000],
            ]);
            if (!$limit) {
                throw new DomainError("每日额度须为 1–100000 的整数。");
            }
            Db::name("api_keys")
                ->where("id", $id)
                ->update([
                    "name" => self::text(
                        $input["name"],
                        80,
                        "name",
                        "密钥名称",
                    ),
                    "daily_limit" => $limit,
                ]);
            Settings::audit("编辑 API 密钥", "密钥 #" . $id, $actor);
            $result = self::record("api_keys", $id);
            unset($result["token_hash"]);
            return $result;
        });
    }

    public static function deleteKey(int $id, int $actor): array
    {
        return CatalogTransaction::run(function () use ($id, $actor) {
            $row = self::record("api_keys", $id, true);
            if ($row["status"]) {
                throw new DomainError("请先撤销密钥，再删除记录。", 409);
            }
            Db::name("api_keys")->where("id", $id)->delete();
            Settings::audit("删除 API 密钥", "密钥 #" . $id, $actor);
            return ["deleted" => true, "id" => $id];
        });
    }
}
