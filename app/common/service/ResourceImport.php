<?php
namespace app\common\service;

use app\common\exception\DomainError;
use think\facade\Db;

/** Insert-only, batched imports sharing the same URL locks as user submissions. */
class ResourceImport
{
    public static function run(
        array $rows,
        string $source,
        bool $apply = false,
    ): array {
        if (!array_is_list($rows) || count($rows) > 100000) {
            throw new \InvalidArgumentException(
                "数据必须为数组，单批最多 100000 行。",
            );
        }
        if (!preg_match('/^[a-zA-Z0-9_-]{1,30}$/D', $source)) {
            throw new \InvalidArgumentException(
                "导入批次标识只能包含 1–30 位字母、数字、下划线或连字符。",
            );
        }
        $report = [
            "mode" => $apply ? "apply" : "dry_run",
            "source" => $source,
            "created_at" => date(DATE_ATOM),
            "counts" => [
                "total" => count($rows),
                "inserted" => 0,
                "planned" => 0,
                "duplicate_input" => 0,
                "duplicate_existing" => 0,
                "duplicate_submission" => 0,
                "unsupported_invite" => 0,
                "invalid" => 0,
            ],
            "inserted_ids" => [],
            "rows" => [],
        ];
        $seen = [];
        $prepared = [];
        foreach ($rows as $index => $row) {
            $entry = ["input_row" => $index + 1];
            if (is_array($row)) {
                foreach (
                    ["source_file", "source_sheet", "source_row"]
                    as $key
                ) {
                    if (isset($row[$key]) && is_scalar($row[$key])) {
                        $entry[$key] = $row[$key];
                    }
                }
            }
            try {
                if (!is_array($row)) {
                    throw new DomainError("记录必须是对象。");
                }
                foreach (
                    [
                        "title",
                        "url",
                        "description",
                        "type",
                        "tags",
                        "status",
                        "status_reason",
                    ]
                    as $key
                ) {
                    if (isset($row[$key]) && !is_string($row[$key])) {
                        throw new DomainError("文字字段必须是字符串：" . $key);
                    }
                }
                $members = $row["members"] ?? null;
                if ($members !== null && (!is_int($members) || $members < 0)) {
                    throw new DomainError("人数必须为非负整数或 null。");
                }
                $url = trim((string) ($row["url"] ?? ""));
                if (
                    preg_match(
                        "#^(?:https?://)?(?:www\.)?(?:t\.me|telegram\.me)/(?:\+|joinchat/)#i",
                        $url,
                    )
                ) {
                    $entry["status"] = "unsupported_invite";
                    $entry["reason"] = "当前资源库仅收录公开链接。";
                } else {
                    $data = ResourceService::validate([
                        "title" => $row["title"] ?? "",
                        "url" => $url,
                        "description" => $row["description"] ?? "",
                        "type" => $row["type"] ?? "group",
                        "members" => $members,
                        "tags" => $row["tags"] ?? "",
                        "status" => $row["status"] ?? "published",
                        "status_reason" => $row["status_reason"] ?? "",
                        "featured" => 0,
                    ]);
                    $hash = $data["url_hash"];
                    if (isset($seen[$hash])) {
                        $entry["status"] = "duplicate_input";
                        $entry["first_input_row"] = $seen[$hash] + 1;
                    } else {
                        $seen[$hash] = $index;
                        $prepared[$hash] = $data;
                    }
                }
            } catch (DomainError $e) {
                $entry["status"] = "invalid";
                $entry["reason"] = $e->getMessage();
            }
            $report["rows"][$index] = $entry;
        }
        return CatalogTransaction::run(
            fn() => self::persist($report, $prepared, $seen, $source, $apply),
        );
    }

    private static function persist(
        array $report,
        array $prepared,
        array $seen,
        string $source,
        bool $apply,
    ): array {
        if ($apply) {
            TelegramLink::lock(array_keys($prepared));
        }
        foreach (array_chunk($prepared, 500, true) as $batch) {
            $hashes = array_keys($batch);
            $existing = array_column(
                Db::name("resources")
                    ->whereIn("url_hash", $hashes)
                    ->lock($apply)
                    ->field("id,url_hash,status,status_reason")
                    ->select()
                    ->toArray(),
                null,
                "url_hash",
            );
            $submissions = array_column(
                Db::name("submissions")
                    ->whereIn("url_hash", $hashes)
                    ->lock($apply)
                    ->field("id,url_hash,status,review_note")
                    ->select()
                    ->toArray(),
                null,
                "url_hash",
            );
            $inserts = [];
            foreach ($batch as $hash => $data) {
                $index = $seen[$hash];
                if (isset($existing[$hash])) {
                    $report["rows"][$index] += [
                        "status" => "duplicate_existing",
                        "resource_id" => (int) $existing[$hash]["id"],
                        "resource_status" => $existing[$hash]["status"],
                        "reason" => TelegramLink::resourceMessage(
                            $existing[$hash],
                        ),
                    ];
                } elseif (isset($submissions[$hash])) {
                    $report["rows"][$index] += [
                        "status" => "duplicate_submission",
                        "submission_id" => (int) $submissions[$hash]["id"],
                        "reason" => TelegramLink::submissionMessage(
                            $submissions[$hash],
                        ),
                    ];
                } elseif (!$apply) {
                    $report["rows"][$index]["status"] = "planned";
                } else {
                    $inserts[$hash] = $data + [
                        "created_at" => date("Y-m-d H:i:s"),
                        "source" => $source,
                        "owner_id" => null,
                    ];
                }
            }
            if ($inserts) {
                Db::name("resources")->insertAll(array_values($inserts));
                $ids = Db::name("resources")
                    ->whereIn("url_hash", array_keys($inserts))
                    ->column("id", "url_hash");
                foreach ($inserts as $hash => $_) {
                    $id = (int) $ids[$hash];
                    $report["rows"][$seen[$hash]] += [
                        "status" => "inserted",
                        "resource_id" => $id,
                    ];
                    $report["inserted_ids"][] = $id;
                }
            }
        }
        foreach ($report["rows"] as $entry) {
            $report["counts"][$entry["status"]]++;
        }
        if ($apply && $report["counts"]["inserted"]) {
            Settings::audit(
                "resource.import",
                json_encode(
                    ["source" => $source, "counts" => $report["counts"]],
                    JSON_UNESCAPED_UNICODE,
                ),
            );
        }
        return $report;
    }
}
