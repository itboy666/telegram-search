<?php
/** Database tests run in one rollback-only transaction; network is always a fixture. */
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();

use app\common\exception\DomainError;
use app\common\service\{
    Ads,
    AdDelivery,
    CommerceConfig,
    Money,
    Payments,
    SearchReward,
    Wallet,
    Tronscan,
    Rbac,
    BotLogin,
    AdminRecords,
};
use app\common\telegram\Transport;
use think\facade\Db;

$checks = 0;
function checkCommerce(bool $ok, string $label): void
{
    global $checks;
    if (!$ok) {
        throw new RuntimeException("FAIL: $label");
    }
    $checks++;
    echo "PASS: $label\n";
}
function rejectCommerce(callable $call, string $text): void
{
    try {
        $call();
    } catch (DomainError $e) {
        checkCommerce(str_contains($e->getMessage(), $text), "rejects $text");
        return;
    }
    throw new RuntimeException("Expected rejection: $text");
}
function keyCommerce(): string
{
    return bin2hex(random_bytes(12));
}

try {
    checkCommerce(
        Money::parse("0.000001") === 1 && Money::format(1) === "0.000001",
        "smallest USDT unit is exact",
    );
    checkCommerce(
        Money::format(Money::parse("999999.123456")) === "999999.123456",
        "large amount roundtrip",
    );
    foreach (["1e3", "-1", "0.0000001", "01", "1,000", "0"] as $bad) {
        rejectCommerce(
            fn() => Money::parse($bad),
            $bad === "0" ? "大于零" : "格式",
        );
    }
    rejectCommerce(fn() => Money::parse(1.3), "数字文本");
    checkCommerce(
        Tronscan::validAddress(Tronscan::USDT),
        "TRON base58 checksum verifies",
    );
    checkCommerce(
        !Tronscan::validAddress(substr(Tronscan::USDT, 0, -1) . "1"),
        "invalid checksum rejected",
    );

    Db::startTrans();
    $uid = Db::name("users")->insertGetId([
        "username" => "test_commerce_" . keyCommerce(),
        "display_name" => "财务事务测试",
        "created_at" => date("Y-m-d H:i:s"),
    ]);
    $other = Db::name("users")->insertGetId([
        "username" => "test_commerce_" . keyCommerce(),
        "display_name" => "隔离测试",
        "created_at" => date("Y-m-d H:i:s"),
    ]);
    $actor = (int) Db::name("admins")->where("is_super", 1)->value("id");
    if (!CommerceConfig::read()["key_configured"]) {
        Db::name("commerce_config")
            ->where("id", 1)
            ->update(["secret" => "fixture-only-no-network"]);
    }
    CommerceConfig::save([
        "offline_enabled" => true,
        "offline_instructions" => "测试夹具，不是真实收款说明",
        "trc20_enabled" => true,
        "receive_address" => Tronscan::USDT,
        "reward_enabled" => true,
        "reward_amount" => "0.100001",
        "reward_daily_cap" => "0.100001",
        "reward_user_cap" => "0.200002",
    ]);
    checkCommerce(
        !isset(
            CommerceConfig::read()["api_key"],
            CommerceConfig::read()["secret"],
        ),
        "settings never expose the API key",
    );
    $order = Payments::create($uid, [
        "method" => "offline",
        "amount" => "10.123456",
        "request_key" => ($key = keyCommerce()),
    ]);
    checkCommerce(
        (int) Payments::create($uid, [
            "method" => "offline",
            "amount" => "10.123456",
            "request_key" => $key,
        ])["id"] === (int) $order["id"],
        "recharge creation is idempotent",
    );
    rejectCommerce(
        fn() => Payments::owned((int) $order["id"], $other),
        "不存在",
    );
    rejectCommerce(
        fn() => Payments::review((int) $order["id"], "approve", "test", $actor),
        "提交凭证",
    );
    Payments::proof($uid, (int) $order["id"], "银行转账凭证：TEST-ONLY");
    Payments::review((int) $order["id"], "approve", "测试确认", $actor);
    checkCommerce(
        Wallet::balance($uid) === "10.123456",
        "offline approval credits exact amount",
    );
    rejectCommerce(
        fn() => Payments::review(
            (int) $order["id"],
            "approve",
            "retry",
            $actor,
        ),
        "提交凭证",
    );
    checkCommerce(
        Db::name("wallet_entries")
            ->where("business_key", "recharge:" . $order["id"])
            ->count() === 1,
        "repeated approval cannot duplicate ledger",
    );
    $entry = Wallet::post($uid, 1, "test", "test:" . $uid, "test");
    Wallet::post($uid, 1, "test", "test:" . $uid, "test");
    checkCommerce(
        Wallet::balance($uid) === "10.123457",
        "idempotent ledger write",
    );
    rejectCommerce(
        fn() => Wallet::post($other, 1, "test", "test:" . $uid, "test"),
        "冲突",
    );
    rejectCommerce(
        fn() => Wallet::post($uid, -20000000, "test", "overdraft:$uid", "test"),
        "余额不足",
    );

    $chain = Payments::create($uid, [
        "method" => "trc20",
        "amount" => "5",
        "request_key" => keyCommerce(),
    ]);
    $raw = Payments::owned((int) $chain["id"]);
    checkCommerce(
        (int) $raw["amount"] > 5000000 && (int) $raw["amount"] < 6000000,
        "order has unique fractional matching amount",
    );
    $hash = str_repeat("a", 64);
    $tx = [
        "hash" => $hash,
        "confirmed" => true,
        "confirmations" => 20,
        "contractRet" => "SUCCESS",
        "revert" => false,
        "timestamp" => time() * 1000,
        "trc20TransferInfo" => [
            [
                "contract_address" => Tronscan::USDT,
                "to_address" => $raw["receive_address"],
                "type" => "Transfer",
                "tokenType" => "trc20",
                "decimals" => 6,
                "amount_str" => (string) $raw["amount"],
            ],
        ],
    ];
    Tronscan::verify($tx, $raw, $hash);
    checkCommerce(true, "verified confirmed TRC20 fixture accepted");
    foreach (
        [
            ["confirmed", false],
            ["confirmations", 18],
            ["contractRet", "FAILED"],
            ["revert", true],
            ["hash", str_repeat("b", 64)],
            ["timestamp", (strtotime($raw["created_at"]) - 5) * 1000],
            ["timestamp", (strtotime($raw["expires_at"]) + 5) * 1000],
        ]
        as [$field, $value]
    ) {
        $bad = $tx;
        $bad[$field] = $value;
        rejectCommerce(
            fn() => Tronscan::verify($bad, $raw, $hash),
            $field === "timestamp" ? "时段" : "确认",
        );
    }
    foreach (
        [
            "contract_address" => "FAKE_USDT",
            "to_address" => "OTHER",
            "amount_str" => (string) ((int) $raw["amount"] + 1),
            "decimals" => 18,
        ]
        as $field => $value
    ) {
        $bad = $tx;
        $bad["trc20TransferInfo"][0][$field] = $value;
        rejectCommerce(fn() => Tronscan::verify($bad, $raw, $hash), "不符");
    }
    $client = new Tronscan(fn($path, $params) => $tx);
    $before = Money::parse(Wallet::balance($uid));
    Payments::check((int) $chain["id"], $uid, $hash, $client);
    Payments::check((int) $chain["id"], $uid, $hash, $client);
    checkCommerce(
        Money::parse(Wallet::balance($uid)) === $before + $raw["amount"],
        "chain confirmation credits once",
    );
    rejectCommerce(
        fn() => Payments::check((int) $chain["id"], $other, $hash, $client),
        "不存在",
    );
    rejectCommerce(
        fn() => Payments::create($uid, [
            "method" => "trc20",
            "amount" => "2.123456",
            "request_key" => keyCommerce(),
        ]),
        "整数金额",
    );

    $result = ["items" => [["id" => 1]], "page" => 1];
    checkCommerce(
        SearchReward::grant($uid, "", $result, "web") === null,
        "discovery earns no reward",
    );
    checkCommerce(
        SearchReward::grant($uid, "test", ["items" => []], "web") === null,
        "empty search earns no reward",
    );
    checkCommerce(
        SearchReward::grant($uid, "test", $result, "api") === null,
        "open API earns no reward",
    );
    checkCommerce(
        SearchReward::grant(
            $uid,
            "test",
            ["items" => [["id" => 1]], "page" => 2],
            "web",
        ) === null,
        "pagination earns no reward",
    );
    $reward = SearchReward::grant($uid, "test", $result, "bot");
    checkCommerce(
        $reward["amount"] === "0.100001",
        "first valid search reward exact",
    );
    checkCommerce(
        SearchReward::grant($uid, "different", $result, "web") === null,
        "cross-channel same-day reward only once",
    );
    checkCommerce(
        SearchReward::grant($other, "test", $result, "web") === null,
        "daily site cap enforced",
    );
    CommerceConfig::save(["reward_enabled" => false]);
    checkCommerce(
        SearchReward::grant($other, "test", $result, "web") === null,
        "reward default switch enforced",
    );

    $slot = Ads::saveSlot(
        null,
        [
            "name" => "事务测试广告位",
            "kind" => "keyword",
            "price" => "2.000001",
            "enabled" => true,
            "capacity" => 1,
        ],
        $actor,
    );
    $purchase = [
        "slot_id" => $slot["id"],
        "request_key" => keyCommerce(),
        "title" => "安全 <展示>",
        "url" => "https://t.me/telegram",
        "keyword" => "test",
        "quoted_price" => "2.000001",
    ];
    $before = Money::parse(Wallet::balance($uid));
    $ad = Ads::buy($uid, $purchase);
    checkCommerce(
        (int) Ads::buy($uid, $purchase)["id"] === (int) $ad["id"],
        "advertising purchase idempotent",
    );
    checkCommerce(
        Money::parse(Wallet::balance($uid)) === $before - 2000001,
        "ad charged once",
    );
    rejectCommerce(
        fn() => Ads::buy(
            $uid,
            array_replace($purchase, ["request_key" => keyCommerce()]),
        ),
        "名额",
    );
    rejectCommerce(
        fn() => Ads::buy(
            $uid,
            array_replace($purchase, [
                "request_key" => keyCommerce(),
                "quoted_price" => "1",
            ]),
        ),
        "价格",
    );
    rejectCommerce(
        fn() => Ads::buy(
            $uid,
            array_replace($purchase, [
                "request_key" => keyCommerce(),
                "url" => "javascript:alert(1)",
            ]),
        ),
        "HTTPS",
    );
    checkCommerce(
        !isset(Ads::search("test", "session")["keyword"]),
        "pending ad is not delivered",
    );
    Ads::review((int) $ad["id"], "approve", "测试通过", $actor);
    $shown = Ads::search("test", "session");
    checkCommerce(
        (int) $shown["keyword"]["id"] === (int) $ad["id"],
        "approved ad matches keyword",
    );
    checkCommerce(
        !isset(Ads::search("different", "session")["keyword"]),
        "unrelated keyword excludes ad",
    );
    checkCommerce(
        str_contains(Ads::html($shown["keyword"]), "&lt;展示&gt;"),
        "Telegram ad HTML escaped",
    );
    Ads::review((int) $ad["id"], "refund", "测试退款", $actor);
    checkCommerce(
        Money::parse(Wallet::balance($uid)) === $before,
        "refund restores balance",
    );
    rejectCommerce(
        fn() => Ads::review((int) $ad["id"], "refund", "duplicate", $actor),
        "不支持",
    );
    checkCommerce(
        !isset(Ads::search("test", "session")["keyword"]),
        "refunded ad immediately stops",
    );
    rejectCommerce(
        fn() => Ads::deleteSlot((int) $slot["id"], $actor),
        "已有订单",
    );
    rejectCommerce(fn() => AdminRecords::deleteUser($uid, $actor), "资金订单");
    rejectCommerce(
        fn() => Ads::buy(
            $other,
            array_replace($purchase, ["request_key" => keyCommerce()]),
        ),
        "余额不足",
    );
    checkCommerce(
        Db::name("ad_orders")->where("user_id", $other)->count() === 0,
        "insufficient balance leaves no orphan order",
    );
    $fake = new class implements Transport {
        public array $calls = [];
        public bool $failSend = false;
        public bool $failPin = false;
        public function call(string $method, array $params = []): mixed
        {
            $this->calls[] = $method;
            return match ($method) {
                "getMe" => ["id" => 123456],
                "getChat" => ["type" => "supergroup"],
                "getChatMember" => ["can_pin_messages" => true],
                "sendMessage" => $this->failSend
                    ? throw new RuntimeException("fixture send timeout")
                    : ["message_id" => 123],
                "pinChatMessage" => $this->failPin
                    ? throw new RuntimeException("fixture pin failure")
                    : true,
                default => true,
            };
        }
    };
    $groupSlot = Ads::saveSlot(
        null,
        [
            "name" => "测试群置顶",
            "kind" => "group_pin",
            "price" => "1",
            "enabled" => true,
            "target_chat" => "-100999999999",
        ],
        $actor,
    );
    $groupAd = Ads::buy($uid, [
        "slot_id" => $groupSlot["id"],
        "request_key" => keyCommerce(),
        "title" => "模拟投放",
        "url" => "https://t.me/telegram",
    ]);
    Ads::review((int) $groupAd["id"], "approve", "测试审批", $actor, $fake);
    $job = Db::name("ad_deliveries")->where("order_id", $groupAd["id"])->find();
    checkCommerce(
        Db::name("ad_orders")
            ->where("id", $groupAd["id"])
            ->value("starts_at") === null,
        "external ad does not start billing duration before delivery",
    );
    AdDelivery::run((int) $job["id"], $fake);
    checkCommerce(
        Db::name("ad_orders")->where("id", $groupAd["id"])->value("status") ===
            "active" && in_array("pinChatMessage", $fake->calls),
        "group ad sends then pins before activation",
    );
    AdDelivery::run((int) $job["id"], $fake);
    checkCommerce(
        count(array_filter($fake->calls, fn($x) => $x === "sendMessage")) === 1,
        "completed delivery never sends again",
    );
    Ads::review((int) $groupAd["id"], "refund", "测试退款", $actor);
    $groupAd = Ads::buy($uid, [
        "slot_id" => $groupSlot["id"],
        "request_key" => keyCommerce(),
        "title" => "超时模拟",
        "url" => "https://t.me/telegram",
    ]);
    Ads::review((int) $groupAd["id"], "approve", "测试审批", $actor, $fake);
    $job = Db::name("ad_deliveries")->where("order_id", $groupAd["id"])->find();
    $fake->calls = [];
    $fake->failSend = true;
    AdDelivery::run((int) $job["id"], $fake);
    AdDelivery::run((int) $job["id"], $fake);
    checkCommerce(
        Db::name("ad_orders")->where("id", $groupAd["id"])->value("status") ===
            "delivery_issue",
        "uncertain send becomes visible manual review",
    );
    checkCommerce(
        count(array_filter($fake->calls, fn($x) => $x === "sendMessage")) === 1,
        "ambiguous timeout cannot trigger duplicate external messages",
    );
    Ads::review((int) $groupAd["id"], "refund", "测试退还待核实订单", $actor);
    $groupAd = Ads::buy($uid, [
        "slot_id" => $groupSlot["id"],
        "request_key" => keyCommerce(),
        "title" => "置顶失败模拟",
        "url" => "https://t.me/telegram",
    ]);
    Ads::review((int) $groupAd["id"], "approve", "测试审批", $actor, $fake);
    $job = Db::name("ad_deliveries")->where("order_id", $groupAd["id"])->find();
    $fake->failSend = false;
    $fake->failPin = true;
    AdDelivery::run((int) $job["id"], $fake);
    checkCommerce(
        (int) Db::name("ad_deliveries")
            ->where("id", $job["id"])
            ->value("message_id") === 123,
        "failed pin retains message ID for recovery",
    );
    checkCommerce(
        Db::name("ad_orders")
            ->where("id", $groupAd["id"])
            ->value("starts_at") === null,
        "failed pin does not consume delivery days",
    );
    checkCommerce(
        isset(
            Rbac::catalog()["users"],
            Rbac::catalog()["commerce_settings"],
            Rbac::catalog()["ad_orders"],
        ),
        "new permission modules preserve existing catalog",
    );
    $code = BotLogin::issue($uid);
    checkCommerce(
        strlen($code) === 20 &&
            Db::name("bot_login_codes")
                ->where("user_id", $uid)
                ->value("code_hash") === hash("sha256", $code),
        "one-time login code is stored hashed",
    );
    BotLogin::issue($uid);
    checkCommerce(
        Db::name("bot_login_codes")
            ->where("code_hash", hash("sha256", $code))
            ->count() === 0,
        "new login code invalidates earlier unused code",
    );
    $access = [
        "permissions" => ["recharges.read"],
        "fields" => ["recharges" => ["read" => ["status"], "write" => []]],
    ];
    checkCommerce(
        Rbac::project($access, "recharges", $order + ["status" => "paid"]) === [
            "status" => "pending",
            "id" => $order["id"],
        ],
        "financial field projection excludes amount and address",
    );
    rejectCommerce(fn() => Rbac::require($access, "recharges.review"), "没有");
    rejectCommerce(
        fn() => Rbac::checkWrite($access, "recharges", [
            "decision" => "approve",
        ]),
        "无权修改",
    );
    checkCommerce(
        (int) Db::name("wallets")->where("user_id", $uid)->value("balance") ===
            (int) Db::name("wallet_entries")
                ->where("user_id", $uid)
                ->sum("amount"),
        "ledger reconciles exactly to wallet",
    );
    Db::rollback();
    echo "$checks commerce checks passed; all fixtures rolled back.\n";
} catch (Throwable $e) {
    Db::rollback();
    fwrite(
        STDERR,
        get_class($e) .
            ": " .
            $e->getMessage() .
            " at " .
            $e->getFile() .
            ":" .
            $e->getLine() .
            "\n",
    );
    exit(1);
}
