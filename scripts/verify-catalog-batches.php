<?php
/** Verify saved batches without fetching the network or inserting catalog rows. */
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();
set_exception_handler(static function (Throwable $e): never {
    fwrite(STDERR, $e->getMessage() . "\n");
    exit(1);
});

use app\command\CollectCatalog;
use app\common\service\ResourceImport;
use think\facade\Db;

if (count($argv) < 3) {
    throw new RuntimeException(
        "Usage: php scripts/verify-catalog-batches.php OUTPUT_JSON REPORT_DIR [REPORT_DIR ...]",
    );
}
$result = [
    "created_at" => date(DATE_ATOM),
    "mode" => "offline_dry_run",
    "reports" => [],
    "counts" => [],
];
foreach (array_slice($argv, 2) as $directory) {
    $directory = realpath($directory);
    if (!$directory || !is_file("$directory/summary.json")) {
        throw new RuntimeException("正式批次报告目录不存在。");
    }
    $summary = json_decode(
        file_get_contents("$directory/summary.json"),
        true,
        512,
        JSON_THROW_ON_ERROR,
    );
    if ($summary["mode"] !== "apply" || !$summary["complete"]) {
        throw new RuntimeException("只能核对已完成的正式导入报告。");
    }
    $item = ["directory" => $directory, "batches" => 0, "counts" => []];
    foreach (glob("$directory/*.json") as $file) {
        $batch = json_decode(file_get_contents($file), true, 512, JSON_THROW_ON_ERROR);
        if (!isset($batch["rows"], $batch["report"]["source"])) {
            continue;
        }
        $dry = ResourceImport::run($batch["rows"], $batch["report"]["source"], false);
        $item["batches"]++;
        foreach ($dry["counts"] as $key => $value) {
            $item["counts"][$key] = ($item["counts"][$key] ?? 0) + $value;
            $result["counts"][$key] = ($result["counts"][$key] ?? 0) + $value;
        }
    }
    if (!$item["batches"]) {
        throw new RuntimeException("报告目录没有可核对的批次。");
    }
    $result["reports"][] = $item;
}
$result["database"] = Db::name("resources")
    ->field("COUNT(*) AS total, COUNT(DISTINCT url) AS unique_urls, COUNT(DISTINCT url_hash) AS unique_hashes, SUM(url_hash <> SHA2(url,256)) AS hash_mismatches")
    ->select()->toArray()[0];
$result["by_type_and_status"] = Db::name("resources")
    ->field("type,status,COUNT(*) AS total")
    ->group("type,status")
    ->order("type,status")
    ->select()->toArray();
$result["by_source_and_status"] = Db::name("resources")
    ->field("source,status,COUNT(*) AS total")
    ->group("source,status")
    ->order("source,status")
    ->select()->toArray();
$result["telegram_type_non_telegram_links"] = Db::name("resources")
    ->where("type", "<>", "website")
    ->whereNotLike("url", "https://t.me/%")
    ->count();
$result["website_telegram_links"] = Db::name("resources")
    ->where("type", "website")
    ->whereRaw("url REGEXP '^https?://(www[.])?(t[.]me|telegram[.]me)([:/]|$)'")
    ->count();
$result["website_non_http_links"] = Db::name("resources")
    ->where("type", "website")
    ->whereRaw("url NOT REGEXP '^https?://'")
    ->count();
$result["fixture_users"] = Db::name("users")
    ->whereRaw("username REGEXP '^(website_[0-9a-f]{12}|http_[0-9a-f]{8}|race_[0-9a-f]{8}_[0-9]+)$'")
    ->count();
$result["fixture_resources"] = Db::name("resources")
    ->whereRaw("source REGEXP '^(website_[0-9a-f]{12}|http_[0-9a-f]{8}|race_[0-9a-f]{8})$'")
    ->count();
$result["passed"] =
    $result["counts"]["total"] === $result["counts"]["duplicate_existing"] &&
    (int) $result["database"]["total"] === (int) $result["database"]["unique_hashes"] &&
    (int) $result["database"]["total"] === (int) $result["database"]["unique_urls"] &&
    (int) $result["database"]["hash_mismatches"] === 0 &&
    $result["telegram_type_non_telegram_links"] === 0 &&
    $result["website_telegram_links"] === 0 &&
    $result["website_non_http_links"] === 0 &&
    $result["fixture_users"] === 0 &&
    $result["fixture_resources"] === 0;
(new CollectCatalog())->save($argv[1], $result);
echo json_encode($result, JSON_PRETTY_PRINT | JSON_UNESCAPED_UNICODE) . "\n";
exit($result["passed"] ? 0 : 1);
