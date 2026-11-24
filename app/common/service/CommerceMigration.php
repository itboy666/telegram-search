<?php
namespace app\common\service;

use think\facade\Db;

class CommerceMigration
{
    public static function run(): void
    {
        if (
            !(
                Db::query(
                    "SELECT GET_LOCK('starindex_commerce_migration',10) AS n",
                )[0]["n"] ?? 0
            )
        ) {
            throw new \RuntimeException("财务模块正在升级，请稍后重试。");
        }
        try {
            if (
                Db::name("schema_migrations")
                    ->where("version", "006_commerce")
                    ->count()
            ) {
                return;
            }
            foreach (
                explode(
                    ";",
                    file_get_contents(
                        root_path() . "database/migrations/006_commerce.sql",
                    ),
                )
                as $sql
            ) {
                if (trim($sql) !== "") {
                    Db::execute($sql);
                }
            }
            Db::execute(
                "INSERT IGNORE INTO ts_commerce_config (id,config,updated_at) VALUES (1,'{}',NOW())",
            );
            if (!Db::name("ad_slots")->count()) {
                foreach (Ads::KINDS as $kind => $label) {
                    Db::name("ad_slots")->insert([
                        "name" => $label,
                        "kind" => $kind,
                        "description" => Ads::DESCRIPTIONS[$kind],
                        "price" => 10000000,
                        "duration_days" => 7,
                        "capacity" => in_array($kind, [
                            "search_top",
                            "search_bottom",
                        ])
                            ? 5
                            : 1,
                        "enabled" => 0,
                        "created_at" => date("Y-m-d H:i:s"),
                        "updated_at" => date("Y-m-d H:i:s"),
                    ]);
                }
            }
            Db::name("schema_migrations")->insert([
                "version" => "006_commerce",
                "applied_at" => date("Y-m-d H:i:s"),
            ]);
        } finally {
            Db::query("SELECT RELEASE_LOCK('starindex_commerce_migration')");
        }
    }
}
