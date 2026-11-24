<?php
namespace app\common\service;
use think\facade\Db;
class Settings
{
    public static function all(): array
    {
        return array_merge(
            [
                "site_name" => "星索",
                "site_description" => "发现值得收藏的 Telegram 资源",
                "public_api_enabled" => "0",
                "submissions_enabled" => "1",
                "bot_welcome" =>
                    "你好，我是星索。发送关键词，发现群组、频道与优质内容。",
            ],
            Db::name("settings")
                ->where("name", "<>", BotCredentials::SETTING)
                ->column("value", "name"),
        );
    }
    public static function get(string $key, string $default = ""): string
    {
        return self::all()[$key] ?? $default;
    }
    public static function audit(
        string $action,
        string $detail = "",
        ?int $actor = null,
    ): void {
        Db::name("audit_logs")->insert([
            "actor_id" => $actor,
            "action" => $action,
            "detail" => mb_substr($detail, 0, 500),
            "created_at" => date("Y-m-d H:i:s"),
        ]);
    }
}
