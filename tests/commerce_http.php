<?php
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();
use think\facade\Db;
use app\common\service\{BotLogin, CatalogTransaction, Wallet};

class CommerceBrowser
{
    public string $token = "",
        $csrf = "";
    public function __construct(public string $scope = "user") {}
    public function call(
        string $path,
        ?array $body = null,
        ?string $csrf = null,
    ): array {
        $base = rtrim(getenv("STARINDEX_TEST_URL") ?: "http://127.0.0.1:8088", "/");
        $curl = curl_init($base . $path);
        curl_setopt_array($curl, [
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_TIMEOUT => 15,
            CURLOPT_PROXY => "",
            CURLOPT_HTTPHEADER => [
                "Content-Type: application/json",
                "Origin: " . $base,
                "X-CSRF-Token: " . ($csrf ?? $this->csrf),
            ],
            CURLOPT_COOKIE => "starindex_" . $this->scope . "=" . $this->token,
        ]);
        if ($body !== null) {
            curl_setopt($curl, CURLOPT_POST, true);
            curl_setopt($curl, CURLOPT_POSTFIELDS, json_encode($body));
        }
        $raw = curl_exec($curl);
        $status = curl_getinfo($curl, CURLINFO_HTTP_CODE);
        curl_close($curl);
        $json = json_decode((string) $raw, true) ?? [];
        return [(int) $status, $json["data"] ?? $json];
    }
}
function commerceSession(int $id, string $scope = "user"): CommerceBrowser
{
    $b = new CommerceBrowser($scope);
    $b->token = bin2hex(random_bytes(32));
    $b->csrf = bin2hex(random_bytes(24));
    Db::name($scope === "user" ? "sessions" : "admin_sessions")->insert(
        [
            $scope === "user" ? "user_id" : "admin_id" => $id,
            "token_hash" => hash("sha256", $b->token),
            "csrf_token" => $b->csrf,
            "created_at" => date("Y-m-d H:i:s"),
            "expires_at" => date("Y-m-d H:i:s", time() + 600),
        ] + ($scope === "user" ? ["scope" => "user"] : []),
    );
    return $b;
}
$checks = 0;
function commerceHttp(array $result, int $status, string $label): array
{
    global $checks;
    if ($result[0] !== $status) {
        throw new RuntimeException("FAIL $label (HTTP {$result[0]})");
    }
    echo "PASS $label (HTTP $status)\n";
    $checks++;
    return $result[1];
}
function commerceAssert(bool $condition, string $label): void
{
    global $checks;
    if (!$condition) {
        throw new RuntimeException("FAIL $label");
    }
    echo "PASS $label\n";
    $checks++;
}
$users = [];
$admin = $role = $slot = 0;
$root = null;
try {
    $tag = "commerce_http_" . bin2hex(random_bytes(5));
    $now = date("Y-m-d H:i:s");
    foreach (["a", "b"] as $suffix) {
        $users[] = Db::name("users")->insertGetId([
            "username" => $tag . $suffix,
            "display_name" => "接口测试",
            "created_at" => $now,
        ]);
    }
    $user = commerceSession($users[0]);
    $other = commerceSession($users[1]);
    $anonymous = new CommerceBrowser();
    $rootId = (int) Db::name("admins")->where("is_super", 1)->value("id");
    $root = commerceSession($rootId, "admin");
    $admin = Db::name("admins")->insertGetId([
        "username" => $tag,
        "display_name" => "受限财务测试",
        "password_hash" => password_hash(
            bin2hex(random_bytes(24)),
            PASSWORD_DEFAULT,
        ),
        "created_at" => $now,
    ]);
    $role = Db::name("roles")->insertGetId([
        "name" => $tag,
        "created_at" => $now,
        "updated_at" => $now,
    ]);
    Db::name("admin_roles")->insert(["admin_id" => $admin, "role_id" => $role]);
    Db::name("role_permissions")->insert([
        "role_id" => $role,
        "permission" => "recharges.read",
    ]);
    Db::name("role_fields")->insert([
        "role_id" => $role,
        "resource" => "recharges",
        "field" => "status",
        "can_read" => 1,
        "can_write" => 0,
    ]);
    $limited = commerceSession($admin, "admin");
    commerceHttp(
        $anonymous->call("/user/api/commerce/summary"),
        401,
        "wallet requires user authentication",
    );
    commerceHttp(
        $user->call("/admin/api/commerce/settings"),
        401,
        "user session cannot read admin settings",
    );
    commerceHttp(
        $user->call(
            "/user/api/commerce/recharges",
            [
                "method" => "trc20",
                "amount" => "5",
                "request_key" => bin2hex(random_bytes(12)),
            ],
            "",
        ),
        403,
        "recharge creation requires CSRF",
    );
    commerceHttp(
        $user->call("/user/api/commerce/summary"),
        200,
        "wallet summary works",
    );
    $s = commerceHttp(
        $root->call("/admin/api/commerce/settings"),
        200,
        "super admin reads settings",
    );
    commerceAssert(
        !isset($s["secret"], $s["api_key"]),
        "admin settings never return credential plaintext",
    );
    commerceHttp(
        $limited->call("/admin/api/commerce/settings"),
        403,
        "settings read cannot be inferred from recharge grant",
    );
    $order = commerceHttp(
        $user->call("/user/api/commerce/recharges", [
            "method" => "trc20",
            "amount" => "5",
            "request_key" => ($key = bin2hex(random_bytes(12))),
        ]),
        200,
        "recharge endpoint creates unique-amount order",
    );
    $again = commerceHttp(
        $user->call("/user/api/commerce/recharges", [
            "method" => "trc20",
            "amount" => "5",
            "request_key" => $key,
        ]),
        200,
        "repeated recharge request returns original order",
    );
    commerceAssert(
        (int) $again["id"] === (int) $order["id"],
        "recharge idempotency across HTTP requests",
    );
    commerceHttp(
        $other->call("/user/api/commerce/recharges/{$order["id"]}/check", [
            "txid" => str_repeat("a", 64),
        ]),
        404,
        "another user cannot claim or inspect recharge",
    );
    $orders = commerceHttp(
        $other->call("/user/api/commerce/records/recharges"),
        200,
        "other user order list is scoped",
    );
    commerceAssert(
        !$orders["items"],
        "owner isolation hides payment address and amount",
    );
    $masked = commerceHttp(
        $limited->call(
            "/admin/api/commerce/records/recharges?id={$order["id"]}",
        ),
        200,
        "limited operator can read allowed status",
    );
    commerceAssert(
        array_keys($masked["items"][0]) === ["id", "status"],
        "amount and txid excluded by field permissions",
    );
    commerceHttp(
        $limited->call(
            "/admin/api/commerce/records/recharges?user_id={$users[0]}",
        ),
        403,
        "hidden finance field cannot be used for filtering",
    );
    commerceHttp(
        $limited->call("/admin/api/commerce/recharges/{$order["id"]}/review", [
            "decision" => "approve",
            "review_note" => "forged",
        ]),
        403,
        "read-only operator cannot approve money",
    );
    $slotData = commerceHttp(
        $root->call("/admin/api/commerce/slots", [
            "name" => $tag,
            "kind" => "keyword",
            "price" => "1.000001",
            "enabled" => 1,
        ]),
        200,
        "admin creates an ad slot",
    );
    $slot = (int) $slotData["id"];
    CatalogTransaction::run(
        fn() => Wallet::post($users[0], 2000000, "test", $tag, "HTTP 测试资金"),
    );
    $ad = commerceHttp(
        $user->call("/user/api/commerce/ads", [
            "slot_id" => $slot,
            "request_key" => bin2hex(random_bytes(12)),
            "title" => "接口测试",
            "keyword" => $tag,
            "url" => "https://t.me/telegram",
            "quoted_price" => "1.000001",
        ]),
        200,
        "user buys ad with server-owned price",
    );
    commerceAssert($ad["status"] === "pending", "new ad requires review");
    commerceHttp(
        $root->call("/admin/api/commerce/ads/{$ad["id"]}/review", [
            "decision" => "reject",
            "review_note" => "测试退款",
        ]),
        200,
        "rejection refunds user",
    );
    commerceAssert(
        Wallet::balance($users[0]) === "2.000000",
        "HTTP rejected order fully refunds",
    );
    commerceHttp(
        $root->call("/admin/api/commerce/ads/{$ad["id"]}/review", [
            "decision" => "reject",
            "review_note" => "retry",
        ]),
        409,
        "duplicate refund rejected",
    );
    $code = BotLogin::issue($users[0]);
    $logged = commerceHttp(
        $anonymous->call("/user/api/bot-login", ["code" => $code]),
        200,
        "one-time Telegram account login",
    );
    commerceAssert(
        (int) $logged["user"]["id"] === (int) $users[0],
        "Telegram login selects correct wallet owner",
    );
    commerceHttp(
        $anonymous->call("/user/api/bot-login", ["code" => $code]),
        401,
        "login code cannot be replayed",
    );
    $code = BotLogin::issue($users[1]);
    Db::name("bot_login_codes")
        ->where("user_id", $users[1])
        ->update(["expires_at" => date("Y-m-d H:i:s", time() - 1)]);
    commerceHttp(
        $anonymous->call("/user/api/bot-login", ["code" => $code]),
        401,
        "expired login code rejected",
    );
    echo "$checks commerce HTTP checks passed.\n";
} catch (Throwable $e) {
    fwrite(STDERR, $e->getMessage() . "\n");
    $failed = true;
} finally {
    if ($users) {
        foreach (
            [
                "sessions",
                "bot_login_codes",
                "wallet_entries",
                "wallets",
                "recharges",
                "ad_orders",
            ]
            as $table
        ) {
            Db::name($table)->whereIn("user_id", $users)->delete();
        }
        Db::name("users")->whereIn("id", $users)->delete();
    }
    if ($slot) {
        Db::name("ad_slots")->where("id", $slot)->delete();
    }
    if ($root) {
        Db::name("admin_sessions")
            ->where("token_hash", hash("sha256", $root->token))
            ->delete();
    }
    if ($admin) {
        Db::name("admin_sessions")->where("admin_id", $admin)->delete();
        Db::name("admin_roles")->where("admin_id", $admin)->delete();
        Db::name("admins")->where("id", $admin)->delete();
    }
    if ($role) {
        Db::name("role_fields")->where("role_id", $role)->delete();
        Db::name("role_permissions")->where("role_id", $role)->delete();
        Db::name("roles")->where("id", $role)->delete();
    }
    if ($slot) {
        Db::name("audit_logs")
            ->where("action", "保存广告位")
            ->where("detail", "广告位 #$slot")
            ->delete();
    }
    if (isset($ad["id"])) {
        Db::name("audit_logs")
            ->where("action", "审核广告订单")
            ->where("detail", "订单 #{$ad["id"]}，reject；测试退款")
            ->delete();
    }
    echo "Commerce HTTP fixtures removed.\n";
}
exit(empty($failed) ? 0 : 1);
