<?php
/** Real MySQL connections. Only uniquely tagged test rows are committed and removed. */
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();
use think\facade\Db;
use app\common\service\{Ads, Payments, Wallet, CatalogTransaction};
use app\common\exception\DomainError;

if (($argv[1] ?? "") === "--worker") {
    $job = json_decode($argv[2], true);
    echo "READY\n";
    fflush(STDOUT);
    if (trim((string) fgets(STDIN)) !== "GO") {
        exit(2);
    }
    try {
        if ($job["mode"] === "buy") {
            Ads::buy($job["user"], [
                "slot_id" => $job["slot"],
                "request_key" => bin2hex(random_bytes(12)),
                "title" => "并发测试",
                "url" => "https://t.me/telegram",
                "keyword" => "race",
            ]);
        } else {
            Payments::review(
                $job["order"],
                "approve",
                "并发测试",
                $job["actor"],
            );
        }
        echo "OK";
    } catch (DomainError) {
        echo "REJECTED";
    } catch (Throwable $e) {
        fwrite(STDERR, get_class($e) . ":" . $e->getMessage());
        exit(1);
    }
    exit(0);
}
function commerceRace(array $job): array
{
    $processes = [];
    try {
        for ($i = 0; $i < 8; $i++) {
            $process = proc_open(
                [PHP_BINARY, __FILE__, "--worker", json_encode($job)],
                [0 => ["pipe", "r"], 1 => ["pipe", "w"], 2 => ["pipe", "w"]],
                $pipes,
            );
            if (!$process) {
                throw new RuntimeException("Worker could not start");
            }
            $processes[] = [$process, $pipes];
        }
        foreach ($processes as [$process, $pipes]) {
            stream_set_timeout($pipes[1], 15);
            if (trim((string) fgets($pipes[1])) !== "READY") {
                throw new RuntimeException("Barrier failure");
            }
        }
        foreach ($processes as [$process, $pipes]) {
            fwrite($pipes[0], "GO\n");
        }
        $answers = [];
        foreach ($processes as [$process, $pipes]) {
            $answers[] = trim(stream_get_contents($pipes[1]));
            $error = stream_get_contents($pipes[2]);
            if ($error !== "") {
                throw new RuntimeException($error);
            }
        }
        return $answers;
    } finally {
        foreach ($processes as [$process, $pipes]) {
            foreach ($pipes as $p) {
                fclose($p);
            }
            proc_close($process);
        }
    }
}
$user = $slot = $order = 0;
try {
    $tag = "commerce_race_" . bin2hex(random_bytes(6));
    $actor = (int) Db::name("admins")->where("is_super", 1)->value("id");
    $user = Db::name("users")->insertGetId([
        "username" => $tag,
        "display_name" => $tag,
        "created_at" => date("Y-m-d H:i:s"),
    ]);
    CatalogTransaction::run(
        fn() => Wallet::post($user, 100000000, "test", $tag, "并发测试资金"),
    );
    // Disabled slot is made visible only inside this test. Name is clearly a fixture.
    $slot = Db::name("ad_slots")->insertGetId([
        "name" => $tag,
        "kind" => "keyword",
        "price" => 20000000,
        "capacity" => 50,
        "enabled" => 1,
        "created_at" => date("Y-m-d H:i:s"),
        "updated_at" => date("Y-m-d H:i:s"),
    ]);
    $answers = commerceRace([
        "mode" => "buy",
        "user" => $user,
        "slot" => $slot,
    ]);
    if (
        count(array_filter($answers, fn($a) => $a === "OK")) !== 5 ||
        Wallet::balance($user) !== "0.000000" ||
        Db::name("ad_orders")->where("user_id", $user)->count() !== 5
    ) {
        throw new RuntimeException(
            "Parallel purchase overdraft or orphan order",
        );
    }
    echo "PASS eight parallel purchases debit at most available balance; five orders commit.\n";
    $order = Db::name("recharges")->insertGetId([
        "user_id" => $user,
        "request_key" => $tag,
        "method" => "offline",
        "amount" => 12345678,
        "status" => "review",
        "proof" => "fixture",
        "created_at" => date("Y-m-d H:i:s"),
        "expires_at" => date("Y-m-d H:i:s", time() + 60),
    ]);
    $answers = commerceRace([
        "mode" => "approve",
        "order" => $order,
        "actor" => $actor,
    ]);
    if (
        count(array_filter($answers, fn($a) => $a === "OK")) !== 1 ||
        Wallet::balance($user) !== "12.345678"
    ) {
        throw new RuntimeException("Concurrent approval double credited");
    }
    echo "PASS eight parallel approvals create one credit.\n";
    if (
        (int) Db::name("wallets")->where("user_id", $user)->value("balance") !==
        (int) Db::name("wallet_entries")->where("user_id", $user)->sum("amount")
    ) {
        throw new RuntimeException("Reconciliation failed");
    }
    echo "PASS concurrent ledger and wallet reconcile exactly.\n";
} catch (Throwable $e) {
    fwrite(STDERR, $e->getMessage() . "\n");
    $failed = true;
} finally {
    if ($user) {
        Db::name("ad_orders")->where("user_id", $user)->delete();
        Db::name("wallet_entries")->where("user_id", $user)->delete();
        Db::name("wallets")->where("user_id", $user)->delete();
        Db::name("recharges")->where("user_id", $user)->delete();
        Db::name("users")->where("id", $user)->delete();
    }
    if ($slot) {
        Db::name("ad_slots")->where("id", $slot)->delete();
    }
    // Remove only the exact audit descriptions produced by this test order.
    if ($order) {
        Db::name("audit_logs")
            ->where("action", "审核线下充值")
            ->where("detail", "订单 #{$order}，approve")
            ->delete();
    }
    echo "All concurrency fixtures removed.\n";
}
exit(empty($failed) ? 0 : 1);
