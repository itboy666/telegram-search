<?php
/** Re-check only this collector's inserted IDs against its saved public HTML. */
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();
set_exception_handler(static function (Throwable $e): never {
    fwrite(STDERR, $e->getMessage() . "\n");
    exit(1);
});

use app\command\CollectCatalog;
use app\common\service\{
    CatalogTransaction,
    Settings,
    TelegramPreviewDataset,
    TelegramLink,
};
use think\facade\Db;

$reportDir = realpath($argv[1] ?? "");
$cacheDir = realpath($argv[2] ?? "");
$apply = ($argv[3] ?? "") === "--apply";
if (!$reportDir || !$cacheDir || !is_file("$reportDir/summary.json")) {
    throw new RuntimeException(
        "Usage: php scripts/audit-public-previews.php REPORT_DIR PREVIEW_CACHE [--apply]",
    );
}
$summary = json_decode(
    file_get_contents("$reportDir/summary.json"),
    true,
    512,
    JSON_THROW_ON_ERROR,
);
if ($summary["mode"] !== "apply" || !$summary["complete"]) {
    throw new RuntimeException("只审计已完成的正式导入报告。");
}
$valid = [];
foreach (glob("$cacheDir/*.html") as $file) {
    if (
        !preg_match(
            '/^([A-Za-z0-9_]{4,32})-(?:latest|[0-9]+)\.html$/D',
            basename($file),
            $m,
        )
    ) {
        continue;
    }
    $parsed = TelegramPreviewDataset::parse(file_get_contents($file), $m[1]);
    foreach ($parsed["rows"] as $row) {
        $valid[$row["url"]] = $row["type"];
    }
}
$candidates = [];
foreach (glob("$reportDir/batch-*.json") as $file) {
    $data = json_decode(
        file_get_contents($file),
        true,
        512,
        JSON_THROW_ON_ERROR,
    );
    foreach ($data["report"]["rows"] as $entry) {
        if ($entry["status"] !== "inserted") {
            continue;
        }
        $row = $data["rows"][$entry["input_row"] - 1];
        if (!isset($valid[$row["url"]])) {
            $candidates[] = [
                "id" => (int) $entry["resource_id"],
                "url" => $row["url"],
                // ResourceService trims this field before insertion.
                "title" => trim($row["title"]),
                "type" => $row["type"],
            ];
        }
    }
}
$report = [
    "mode" => $apply ? "apply" : "dry_run",
    "created_at" => date(DATE_ATOM),
    "candidates" => $candidates,
    "hidden" => 0,
    "already_hidden" => 0,
    "preserved" => 0,
    "reason" => "公开预览未提供足够的媒体类型信息，暂不展示，待人工核验。",
];
$writer = new CollectCatalog();
$outputPath =
    "$reportDir/preview-audit-" . ($apply ? "apply" : "dry") . ".json";
$writer->save($outputPath, $report);
if ($apply && $candidates) {
    $counts = CatalogTransaction::run(function () use (
        $candidates,
        $report,
    ): array {
        TelegramLink::lock(
            array_map(
                static fn($row) => hash("sha256", $row["url"]),
                $candidates,
            ),
        );
        $counts = ["hidden" => 0, "already_hidden" => 0, "preserved" => 0];
        foreach ($candidates as $row) {
            // Preserve manual edits, favourites and review history; hiding is reversible.
            $current = Db::name("resources")
                ->where($row)
                ->where("source", TelegramPreviewDataset::SOURCE)
                ->lock(true)
                ->find();
            if (!$current || $current["status"] !== "published") {
                $key = $current &&
                    $current["status"] === "hidden" &&
                    $current["status_reason"] === $report["reason"]
                    ? "already_hidden"
                    : "preserved";
                $counts[$key]++;
                continue;
            }
            $counts["hidden"] += Db::name("resources")
                ->where("id", $row["id"])
                ->update([
                    "status" => "hidden",
                    "status_reason" => $report["reason"],
                    "updated_at" => date("Y-m-d H:i:s"),
                ]);
        }
        if ($counts["hidden"]) {
            Settings::audit("resource.preview_audit", "公开预览媒体类型不明，暂时隐藏 " . $counts["hidden"] . " 条本轮导入记录；原数据保留，可人工核验后恢复。");
        }
        return $counts;
    });
    $report = array_replace($report, $counts);
}
$writer->save($outputPath, $report);
echo json_encode(
    [
        "candidates" => count($candidates),
        "hidden" => $report["hidden"],
        "already_hidden" => $report["already_hidden"],
        "preserved" => $report["preserved"],
        "report" => $outputPath,
    ],
    JSON_UNESCAPED_UNICODE,
) . "\n";
