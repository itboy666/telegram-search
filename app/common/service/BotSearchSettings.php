<?php
namespace app\common\service;

use app\common\exception\DomainError;
use think\facade\Db;

class BotSearchSettings
{
    public const SETTING = "bot_page_size";
    public const DEFAULT_PAGE_SIZE = 12;
    public const MAX_PAGE_SIZE = 30;

    public static function pageSize(): int
    {
        $value = Db::name("settings")
            ->where("name", self::SETTING)
            ->value("value");
        try {
            return self::validate($value ?? self::DEFAULT_PAGE_SIZE);
        } catch (DomainError) {
            return self::DEFAULT_PAGE_SIZE;
        }
    }

    public static function validate(mixed $value): int
    {
        if (
            !(
                is_int($value) ||
                (is_string($value) && preg_match('/^\d{1,2}$/D', $value))
            ) ||
            (int) $value < 1 ||
            (int) $value > self::MAX_PAGE_SIZE
        ) {
            throw new DomainError("每页显示条数须为 1–30 的整数。", 422, [
                "page_size" => "请输入 1–30 的整数。",
            ]);
        }
        return (int) $value;
    }

    public static function save(mixed $value, int $actor): void
    {
        $size = self::validate($value);
        Db::transaction(function () use ($size, $actor) {
            Db::execute(
                "INSERT INTO ts_settings (name,value) VALUES (?,?) ON DUPLICATE KEY UPDATE value=?",
                [self::SETTING, (string) $size, (string) $size],
            );
            Settings::audit(
                "更新机器人搜索设置",
                "每页显示 " . $size . " 条，新搜索生效",
                $actor,
            );
        });
    }
}
