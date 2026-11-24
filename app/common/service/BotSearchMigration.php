<?php
namespace app\common\service;

use think\facade\Db;

class BotSearchMigration
{
    public static function run(): array
    {
        $locked = Db::query(
            "SELECT GET_LOCK('starindex_bot_search_migration', 10) AS acquired",
        )[0]["acquired"];
        if (!$locked) {
            throw new \RuntimeException("另一个机器人搜索迁移正在运行。");
        }
        try {
            if (
                Db::name("schema_migrations")
                    ->where("version", "003_bot_search")
                    ->count()
            ) {
                return ["already_applied" => true];
            }
            // Old messages used five results and displayed every filter.
            // New sessions explicitly use the current settings and collapsed filters.
            $columns = array_column(
                Db::query("SHOW COLUMNS FROM ts_bot_sessions"),
                "Field",
            );
            foreach (
                [
                    "page_size" => "TINYINT UNSIGNED NOT NULL DEFAULT 5",
                    "filters_expanded" => "TINYINT UNSIGNED NOT NULL DEFAULT 1",
                ]
                as $column => $definition
            ) {
                if (!in_array($column, $columns, true)) {
                    Db::execute(
                        "ALTER TABLE ts_bot_sessions ADD COLUMN $column $definition",
                    );
                }
            }
            Db::name("schema_migrations")->insert([
                "version" => "003_bot_search",
                "applied_at" => date("Y-m-d H:i:s"),
            ]);
            return ["already_applied" => false];
        } finally {
            Db::query("SELECT RELEASE_LOCK('starindex_bot_search_migration')");
        }
    }
}
