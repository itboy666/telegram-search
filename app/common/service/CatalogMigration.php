<?php
namespace app\common\service;

use think\facade\Db;

class CatalogMigration
{
    public static function run(): array
    {
        if (
            !Db::query(
                "SELECT GET_LOCK('starindex_catalog_migration', 10) AS acquired",
            )[0]["acquired"]
        ) {
            throw new \RuntimeException("另一个资源库迁移正在运行。");
        }
        try {
            if (
                Db::name("schema_migrations")
                    ->where("version", "004_catalog_identity")
                    ->count()
            ) {
                return ["already_applied" => true];
            }
            // Validate all canonical identities before changing historical records.
            $normalized = [];
            foreach (["resources", "submissions"] as $table) {
                $seen = [];
                foreach (
                    Db::name($table)
                        ->field("id,url,type")
                        ->order("id")
                        ->select()
                    as $row
                ) {
                    $url = ResourceLink::normalize($row["url"], $row["type"]);
                    $hash = hash("sha256", $url);
                    if (isset($seen[$hash])) {
                        throw new \RuntimeException(
                            "$table 中记录 #{$seen[$hash]} 和 #{$row["id"]} 链接重复，请先合并；原记录未删除。",
                        );
                    }
                    $seen[$hash] = $row["id"];
                    $normalized[$table][] = [
                        "id" => $row["id"],
                        "url" => $url,
                        "url_hash" => $hash,
                    ];
                }
            }
            foreach (
                [
                    "users" => [
                        "submission_allowed" =>
                            "TINYINT UNSIGNED NOT NULL DEFAULT 1",
                        "submission_note" => "VARCHAR(500) NOT NULL DEFAULT ''",
                    ],
                    "resources" => [
                        "status_reason" => "VARCHAR(500) NOT NULL DEFAULT ''",
                    ],
                    "submissions" => ["url_hash" => "CHAR(64) NULL"],
                ]
                as $table => $definitions
            ) {
                $columns = array_column(
                    Db::query("SHOW COLUMNS FROM ts_" . $table),
                    "Field",
                );
                foreach ($definitions as $column => $definition) {
                    if (!in_array($column, $columns, true)) {
                        Db::execute(
                            "ALTER TABLE ts_$table ADD COLUMN $column $definition",
                        );
                    }
                }
            }
            Db::execute(
                "CREATE TABLE IF NOT EXISTS ts_link_locks (url_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY) ENGINE=InnoDB",
            );
            Db::transaction(function () use ($normalized) {
                foreach ($normalized as $table => $rows) {
                    foreach ($rows as $row) {
                        Db::name($table)
                            ->where("id", $row["id"])
                            ->update([
                                "url" => $row["url"],
                                "url_hash" => $row["url_hash"],
                            ]);
                    }
                }
            });
            $indexes = array_column(
                Db::query("SHOW INDEX FROM ts_submissions"),
                "Key_name",
            );
            if (!in_array("uq_submission_url", $indexes, true)) {
                Db::execute(
                    "ALTER TABLE ts_submissions ADD UNIQUE KEY uq_submission_url (url_hash)",
                );
            }
            Db::execute(
                "ALTER TABLE ts_submissions MODIFY COLUMN url_hash CHAR(64) NOT NULL",
            );
            Db::name("schema_migrations")->insert([
                "version" => "004_catalog_identity",
                "applied_at" => date("Y-m-d H:i:s"),
            ]);
            return ["already_applied" => false];
        } finally {
            Db::query("SELECT RELEASE_LOCK('starindex_catalog_migration')");
        }
    }
}
