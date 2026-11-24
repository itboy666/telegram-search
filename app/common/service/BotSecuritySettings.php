<?php
namespace app\common\service;

use app\common\exception\DomainError;
use think\facade\Db;

class BotSecuritySettings
{
    public const KEY = "bot_security";
    public const MODES = [
        "off" => "不验证",
        "emoji" => "选对 emoji",
        "button" => "点击确认（轻量）",
        "text" => "图片字母数字",
        "math" => "四则运算",
        "sequence" => "按顺序点击 emoji",
        "random" => "随机组合",
    ];
    public const DEFAULTS = [
        "private_mode" => "off",
        "group_mode" => "off",
        "allow_group_override" => 1,
        "trust_minutes" => 30,
        "challenge_seconds" => 180,
        "max_attempts" => 3,
        "cooldown_seconds" => 300,
        "search_limit" => 30,
        "group_limit" => 20,
    ];
    public static function get(): array
    {
        $stored = json_decode(
            (string) Db::name("settings")
                ->where("name", self::KEY)
                ->value("value"),
            true,
        );
        return array_replace(self::DEFAULTS, is_array($stored) ? $stored : []);
    }
    public static function save(array $input, int $actor): array
    {
        $values = [];
        foreach ($input as $key => $value) {
            if (!array_key_exists($key, self::DEFAULTS)) {
                throw new DomainError("未知安全设置。");
            }
            if (in_array($key, ["private_mode", "group_mode"], true)) {
                if (!is_string($value) || !isset(self::MODES[$value])) {
                    throw new DomainError("请选择有效的验证方式。", 422, [
                        $key => "验证方式无效",
                    ]);
                }
                if (
                    in_array($value, ["text", "random"], true) &&
                    !extension_loaded("gd")
                ) {
                    throw new DomainError("图片验证码需要 PHP GD 扩展。");
                }
                $values[$key] = $value;
            } else {
                [$min, $max] = [
                    "allow_group_override" => [0, 1],
                    "trust_minutes" => [1, 1440],
                    "challenge_seconds" => [30, 600],
                    "max_attempts" => [1, 5],
                    "cooldown_seconds" => [30, 3600],
                    "search_limit" => [1, 60],
                    "group_limit" => [1, 20],
                ][$key];
                if (
                    !(
                        is_int($value) ||
                        (is_string($value) && preg_match('/^\d+$/D', $value))
                    ) ||
                    $value < $min ||
                    $value > $max
                ) {
                    throw new DomainError("请检查安全设置的范围。", 422, [
                        $key => "请输入 {$min}–{$max} 的整数",
                    ]);
                }
                $values[$key] = (int) $value;
            }
        }
        return Db::transaction(function () use ($values, $actor) {
            Db::execute(
                "INSERT IGNORE INTO ts_settings (name,value) VALUES (?,?)",
                [self::KEY, "{}"],
            );
            $row = Db::name("settings")
                ->where("name", self::KEY)
                ->lock(true)
                ->find();
            $old = json_decode($row["value"], true) ?: [];
            $next = array_replace(self::DEFAULTS, $old, $values, [
                "revision" => bin2hex(random_bytes(8)),
            ]);
            Db::name("settings")
                ->where("name", self::KEY)
                ->update(["value" => json_encode($next)]);
            Settings::audit(
                "更新机器人安全策略",
                "更新字段：" . implode("、", array_keys($values)),
                $actor,
            );
            return $next;
        });
    }
    public static function policy(int $botId, int $chatId): array
    {
        $p = self::get();
        $p["mode"] = $chatId < 0 ? $p["group_mode"] : $p["private_mode"];
        if ($chatId < 0 && $p["allow_group_override"]) {
            $override = Db::name("bot_groups")
                ->where(["bot_id" => $botId, "chat_id" => $chatId])
                ->value("verify_mode");
            if (
                $override &&
                $override !== "inherit" &&
                isset(self::MODES[$override])
            ) {
                $p["mode"] = $override;
            }
        }
        $p["hash"] = hash(
            "sha256",
            json_encode([
                $p["revision"] ?? "initial",
                $p["mode"],
                $p["trust_minutes"],
            ]),
        );
        return $p;
    }
    public static function migrate(): array
    {
        $lock = Db::query(
            "SELECT GET_LOCK('starindex_bot_security_migration',10) AS acquired",
        )[0]["acquired"];
        if (!$lock) {
            throw new \RuntimeException("另一个安全策略迁移正在运行。");
        }
        try {
            if (
                Db::name("schema_migrations")
                    ->where("version", "005_bot_security")
                    ->count()
            ) {
                return ["already_applied" => true];
            }
            foreach (
                explode(
                    ";",
                    file_get_contents(
                        root_path() .
                            "database/migrations/005_bot_security.sql",
                    ),
                )
                as $sql
            ) {
                if (trim($sql) !== "") {
                    Db::execute($sql);
                }
            }
            Db::name("schema_migrations")->insert([
                "version" => "005_bot_security",
                "applied_at" => date("Y-m-d H:i:s"),
            ]);
            return ["already_applied" => false];
        } finally {
            Db::query(
                "SELECT RELEASE_LOCK('starindex_bot_security_migration')",
            );
        }
    }
}
