<?php
/** Real independent MySQL connections race over uniquely owned, cleaned fixtures. */
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();

use app\common\exception\DomainError;
use app\common\service\{ResourceImport, SubmissionService};
use think\facade\Db;

set_exception_handler(static function (Throwable $e): never {
    fwrite(STDERR, $e->getMessage() . "\n");
    exit(1);
});

if (($argv[1] ?? "") === "--worker") {
    $input = json_decode($argv[2], true, 512, JSON_THROW_ON_ERROR);
    echo "READY\n";
    fflush(STDOUT);
    if (trim((string) fgets(STDIN)) !== "GO") {
        exit(2);
    }
    try {
        if ($input["kind"] === "submit") {
            SubmissionService::create($input["user"], $input["rows"][0]);
            $result = ["kind" => "inserted", "count" => 1];
        } else {
            $report = ResourceImport::run($input["rows"], $input["tag"], true);
            $result = [
                "kind" => "import",
                "count" => $report["counts"]["inserted"],
            ];
        }
        echo json_encode($result) . "\n";
    } catch (DomainError $e) {
        echo json_encode(
            [
                "kind" => "domain",
                "status" => $e->status,
                "message" => $e->getMessage(),
            ],
            JSON_UNESCAPED_UNICODE,
        ) . "\n";
    } catch (Throwable $e) {
        fwrite(STDERR, get_class($e) . ": " . $e->getMessage() . "\n");
        exit(1);
    }
    exit(0);
}

$checks = 0;
function concurrentCheck(bool $ok, string $message): void
{
    global $checks;
    if (!$ok) {
        throw new RuntimeException("FAIL " . $message);
    }
    echo "PASS " . $message . "\n";
    $checks++;
}
function raceCatalog(array $jobs): array
{
    $children = [];
    try {
        foreach ($jobs as $job) {
            $process = proc_open(
                [PHP_BINARY, __FILE__, "--worker", json_encode($job)],
                [0 => ["pipe", "r"], 1 => ["pipe", "w"], 2 => ["pipe", "w"]],
                $pipes,
            );
            if (!is_resource($process)) {
                throw new RuntimeException("Cannot start concurrency worker");
            }
            stream_set_timeout($pipes[1], 30);
            $children[] = ["process" => $process, "pipes" => $pipes];
        }
        foreach ($children as $child) {
            if (trim((string) fgets($child["pipes"][1])) !== "READY") {
                throw new RuntimeException("Worker did not reach barrier");
            }
        }
        foreach ($children as &$child) {
            fwrite($child["pipes"][0], "GO\n");
            fclose($child["pipes"][0]);
            unset($child["pipes"][0]);
        }
        unset($child);
        $results = [];
        foreach ($children as &$child) {
            $raw = stream_get_contents($child["pipes"][1]);
            $error = stream_get_contents($child["pipes"][2]);
            $code = proc_close($child["process"]);
            unset($child["process"]);
            if ($code !== 0) {
                throw new RuntimeException(
                    "Concurrent worker failed: " . $error . $raw,
                );
            }
            $results[] = json_decode($raw, true, 512, JSON_THROW_ON_ERROR);
        }
        unset($child);
        return $results;
    } finally {
        foreach ($children as $child) {
            foreach ($child["pipes"] as $pipe) {
                if (is_resource($pipe)) {
                    fclose($pipe);
                }
            }
            if (isset($child["process"]) && is_resource($child["process"])) {
                proc_terminate($child["process"]);
                proc_close($child["process"]);
            }
        }
    }
}

$tag = "race_" . bin2hex(random_bytes(4));
$users = [];
$hashes = [];
$setting = Db::name("settings")
    ->where("name", "submissions_enabled")
    ->value("value");
try {
    Db::name("settings")
        ->where("name", "submissions_enabled")
        ->update(["value" => "1"]);
    for ($i = 0; $i < 8; $i++) {
        $users[] = Db::name("users")->insertGetId([
            "username" => $tag . "_" . $i,
            "display_name" => "Concurrency fixture",
            "created_at" => date("Y-m-d H:i:s"),
        ]);
    }
    $row = function (string $suffix) use ($tag, &$hashes): array {
        $url = "https://t.me/" . $tag . "_" . $suffix;
        $hashes[] = hash("sha256", $url);
        return ["title" => $tag . " concurrency", "url" => $url];
    };
    $job = fn(string $kind, int $user, array $rows): array => [
        "kind" => $kind,
        "user" => $user,
        "rows" => $rows,
        "tag" => $tag,
    ];
    foreach (
        [["submit", "submit"], ["import", "submit"], ["import", "import"]]
        as $round => $kinds
    ) {
        $input = $row("same" . $round);
        $result = raceCatalog([
            $job($kinds[0], $users[0], [$input]),
            $job($kinds[1], $users[1], [$input]),
        ]);
        $hash = hash("sha256", $input["url"]);
        concurrentCheck(
            Db::name("resources")->where("url_hash", $hash)->count() +
                Db::name("submissions")->where("url_hash", $hash)->count() ===
                1,
            implode(" vs ", $kinds) . ": only one catalog claim commits",
        );
        concurrentCheck(
            array_sum(array_column($result, "count")) === 1,
            "the losing writer reports a duplicate, not another insert",
        );
        foreach ($result as $response) {
            if ($response["kind"] === "domain") {
                concurrentCheck(
                    $response["status"] === 422 &&
                        str_contains($response["message"], "已存在"),
                    "concurrent submission receives understandable duplicate feedback",
                );
            }
        }
    }
    $jobs = [];
    foreach ($users as $i => $user) {
        $jobs[] = $job("submit", $user, [$row("distinct" . $i)]);
    }
    $results = raceCatalog($jobs);
    concurrentCheck(
        array_sum(array_column($results, "count")) === 8,
        "eight unrelated links can be submitted concurrently",
    );
    $left = [$row("batch0"), $row("batch1"), $row("batch2")];
    $right = [$left[2], $left[1], $row("batch3")];
    $results = raceCatalog([
        $job("import", $users[0], $left),
        $job("import", $users[1], $right),
    ]);
    concurrentCheck(
        array_sum(array_column($results, "count")) === 4,
        "overlapping imports in opposite input orders retain four unique links",
    );
    echo "\n$checks concurrency checks passed.\n";
} finally {
    if ($users) {
        Db::name("submissions")->whereIn("user_id", $users)->delete();
        foreach ($users as $user) {
            Db::name("rate_limits")
                ->whereLike(
                    "bucket",
                    hash("sha256", "submission:" . $user) . ":%",
                )
                ->delete();
        }
        Db::name("users")->whereIn("id", $users)->delete();
    }
    if ($hashes) {
        Db::name("resources")->whereIn("url_hash", $hashes)->delete();
        Db::name("link_locks")->whereIn("url_hash", $hashes)->delete();
    }
    Db::name("audit_logs")
        ->where("action", "resource.import")
        ->whereLike("detail", "%" . $tag . "%")
        ->delete();
    Db::name("settings")
        ->where("name", "submissions_enabled")
        ->update(["value" => $setting]);
}
