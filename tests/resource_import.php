<?php
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();

use app\common\service\{ResourceImport, ResourceService};
use think\facade\Db;

$checks = 0;
function importCheck(bool $condition, string $message): void
{
    global $checks;
    if (!$condition) {
        throw new RuntimeException("FAIL: " . $message);
    }
    $checks++;
    echo "PASS " . $message . PHP_EOL;
}

Db::startTrans();
try {
    $key = "imp_" . bin2hex(random_bytes(8));
    $hidden = ResourceService::save([
        "title" => "保留的原始资源", "url" => "https://t.me/" . $key . "_old",
        "type" => "group", "status" => "hidden", "members" => 12,
    ]);
    $new = ["title" => "导入测试资源", "url" => "https://t.me/" . $key, "members" => 0, "type" => "group"];
    $rows = [
        $new,
        array_merge($new, ["url" => "https://telegram.me/" . strtoupper($key) . "/"]),
        ["title" => "不能覆盖原资源", "url" => $hidden["url"]],
        ["title" => "错误域名", "url" => "https://example.com/" . $key],
        ["title" => "邀请链接", "url" => "https://t.me/+exampleInvite"],
        array_merge($new, ["members" => -1]),
        "malformed",
    ];
    $before = Db::name("resources")->count();
    $auditBefore = Db::name("audit_logs")->count();
    $dry = ResourceImport::run($rows, $key);
    importCheck($dry["counts"]["planned"] === 1 && $dry["counts"]["invalid"] === 3, "dry run classifies valid and invalid rows");
    importCheck($dry["counts"]["duplicate_input"] === 1 && $dry["counts"]["duplicate_existing"] === 1, "canonical URLs deduplicate across input and existing data");
    importCheck($dry["counts"]["unsupported_invite"] === 1, "invite links remain outside the public catalog");
    importCheck(Db::name("resources")->count() === $before && Db::name("audit_logs")->count() === $auditBefore, "dry run writes neither resources nor audit records");
    $applied = ResourceImport::run($rows, $key, true);
    importCheck($applied["counts"]["inserted"] === 1 && Db::name("resources")->count() === $before + 1, "apply inserts exactly one valid unique resource");
    importCheck(Db::name("resources")->where("id", $hidden["id"])->find() === $hidden, "hidden existing resource stays unchanged");
    $created = Db::name("resources")->where("id", $applied["inserted_ids"][0])->find();
    importCheck((int) $created["members"] === 0 && $created["source"] === $key && $created["status"] === "published", "zero members and import provenance survive persistence");
    $again = ResourceImport::run($rows, $key, true);
    importCheck($again["counts"]["inserted"] === 0 && Db::name("resources")->count() === $before + 1, "repeating an import adds no duplicates");
    importCheck(Db::name("audit_logs")->count() === $auditBefore + 1, "only the effective import produces an audit entry");
} finally {
    Db::rollback();
}
echo "ALL " . $checks . " IMPORT CHECKS PASSED (rolled back)" . PHP_EOL;
