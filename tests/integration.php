<?php
/** Real HTTP/MySQL integration; all fixtures are uniquely owned and removed. */
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();
use think\facade\Db;
use app\common\service\{ResourceService, BotSearchSettings};
use app\common\telegram\{Transport, TelegramError, Bot, UpdateProcessor};

final class Browser
{
    public array $cookies = [];
    public string $csrf = "";
    public function call(
        string $path,
        ?array $body = null,
        array $extra = [],
        string $method = "",
    ): array {
        $base = rtrim(getenv("STARINDEX_TEST_URL") ?: "http://127.0.0.1:8088", "/");
        $curl = curl_init($base . $path);
        $headers = [
            "Accept" => "application/json",
            "Content-Type" => "application/json",
            "Origin" => $base,
        ];
        if ($this->csrf) {
            $headers["X-CSRF-Token"] = $this->csrf;
        }
        foreach ($extra as $key => $value) {
            $headers[$key] = $value;
        }
        curl_setopt_array($curl, [
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_TIMEOUT => 15,
            CURLOPT_PROXY => "",
            CURLOPT_CUSTOMREQUEST =>
                $method ?: ($body === null ? "GET" : "POST"),
            CURLOPT_HTTPHEADER => array_map(
                fn($key, $value) => $key . ": " . $value,
                array_keys($headers),
                $headers,
            ),
            CURLOPT_COOKIE => implode(
                "; ",
                array_map(
                    fn($k, $v) => $k . "=" . $v,
                    array_keys($this->cookies),
                    $this->cookies,
                ),
            ),
            CURLOPT_HEADERFUNCTION => function ($c, $line) {
                if (
                    preg_match("/^Set-Cookie:\s*([^=]+)=([^;]*)/i", $line, $m)
                ) {
                    $this->cookies[$m[1]] = $m[2];
                }
                return strlen($line);
            },
        ]);
        if ($body !== null) {
            curl_setopt($curl, CURLOPT_POSTFIELDS, json_encode($body));
        }
        $raw = curl_exec($curl);
        $status = curl_getinfo($curl, CURLINFO_HTTP_CODE);
        curl_close($curl);
        return [(int) $status, json_decode((string) $raw, true)];
    }
}
final class FakeTelegram implements Transport
{
    public array $calls = [];
    public bool $fail = false;
    public bool $notModified = false;
    public function call(string $method, array $params = []): mixed
    {
        if ($this->fail && $method === "sendMessage") {
            $this->fail = false;
            throw new TelegramError(429, 1);
        }
        $this->calls[] = [$method, $params];
        if ($this->notModified && $method === "editMessageText") {
            $this->notModified = false;
            throw new TelegramError(400, 0, true);
        }
        return $method === "sendMessage" ? ["message_id" => 9001] : true;
    }
    public function count(string $method): int
    {
        return count(array_filter($this->calls, fn($c) => $c[0] === $method));
    }
}
$checks = 0;
function check(bool $ok, string $name): void
{
    global $checks;
    if (!$ok) {
        throw new RuntimeException("FAIL: " . $name);
    }
    $checks++;
    echo "PASS " . $name . PHP_EOL;
}
function ok(array $r, string $name, int $status = 200): array
{
    check($r[0] === $status, $name . " (HTTP " . $r[0] . ")");
    return $r[1]["data"] ?? ($r[1] ?? []);
}
$tag = "qa_" . bin2hex(random_bytes(5));
$ids = [];
$adminId = 0;
$botUser = random_int(80000000000, 89999999999);
$botId = -$botUser;
$settingsBackup = Db::name("settings")
    ->whereIn("name", ["public_api_enabled", "submissions_enabled"])
    ->column("value", "name");
try {
    $password = bin2hex(random_bytes(14));
    $adminId = Db::name("admins")->insertGetId([
        "username" => $tag . "_admin",
        "display_name" => $tag,
        "password_hash" => password_hash($password, PASSWORD_DEFAULT),
        "is_super" => 1,
        "created_at" => date("Y-m-d H:i:s"),
    ]);
    $admin = new Browser();
    $guest = new Browser();
    $user = new Browser();
    $other = new Browser();
    ok($guest->call("/admin/api/resources"), "anonymous admin blocked", 401);
    $login = ok(
        $admin->call("/admin/api/login", [
            "username" => $tag . "_admin",
            "password" => $password,
        ]),
        "admin login",
    );
    $admin->csrf = $login["csrf_token"];
    ok($admin->call("/admin/api/me"), "admin session");
    ok($admin->call("/admin/api/dashboard"), "dashboard SQL aggregation");
    ok(
        $admin->call(
            "/admin/api/resources",
            [],
            ["Origin" => "https://outside.invalid"],
        ),
        "cross-origin write blocked",
        403,
    );
    $copy = new Browser();
    $copy->cookies = $admin->cookies;
    ok($copy->call("/admin/api/resources", []), "CSRF required", 403);
    ok(
        $admin->call("/admin/api/resources", [
            "title" => "bad",
            "url" => "https://example.com",
        ]),
        "resource URL validation",
        422,
    );
    $cat = ok(
        $admin->call("/admin/api/categories", [
            "name" => $tag,
            "description" => "integration",
            "sort_order" => 99,
        ]),
        "create category",
    );
    $input = [
        "title" => $tag . " published",
        "url" => "https://t.me/" . $tag . "A",
        "type" => "channel",
        "category_id" => $cat["id"],
        "description" => "integration resource",
        "status" => "published",
    ];
    $resource = ok(
        $admin->call("/admin/api/resources", $input),
        "create published resource",
    );
    $ids[] = $resource["id"];
    ok(
        $admin->call(
            "/admin/api/resources",
            array_replace($input, [
                "url" => strtolower($input["url"]) . "?tracking=1",
            ]),
        ),
        "canonical duplicate blocked",
        422,
    );
    $hidden = ok(
        $admin->call(
            "/admin/api/resources",
            array_replace($input, [
                "title" => $tag . " hidden",
                "url" => "https://t.me/" . $tag . "hidden",
                "status" => "hidden",
            ]),
        ),
        "create hidden resource",
    );
    $ids[] = $hidden["id"];
    $search = ok($guest->call("/api/public/search?q=" . $tag), "public search");
    check($search["total"] === 1, "hidden resources excluded");
    $literal = ok(
        $guest->call("/api/public/search?q=" . rawurlencode($tag . "%")),
        "escaped wildcard search",
    );
    check($literal["total"] === 0, "percent is literal");
    ok(
        $guest->call("/api/public/resources/" . $hidden["id"]),
        "hidden detail inaccessible",
        404,
    );
    $login = ok(
        $user->call("/user/api/register", [
            "username" => $tag . "_user",
            "password" => $password,
        ]),
        "user registration",
    );
    $user->csrf = $login["csrf_token"];
    ok(
        $user->call("/user/api/favorites/" . $resource["id"], [
            "saved" => true,
        ]),
        "save favorite",
    );
    ok(
        $user->call("/user/api/favorites/" . $hidden["id"], ["saved" => true]),
        "hidden favorite blocked",
        404,
    );
    $fav = ok($user->call("/user/api/favorites"), "list favorites");
    check($fav["total"] === 1, "favorite persisted");
    $favoriteIds = ok(
        $user->call(
            "/user/api/favorite-ids?ids=" .
                $resource["id"] .
                "," .
                $hidden["id"],
        ),
        "favorite state for search cards",
    );
    check(
        $favoriteIds === [(int) $resource["id"]],
        "favorite IDs match saved resource",
    );
    $login = ok(
        $other->call("/user/api/register", [
            "username" => $tag . "_other",
            "password" => $password,
        ]),
        "second account",
    );
    $other->csrf = $login["csrf_token"];
    $fav = ok($other->call("/user/api/favorites"), "other favorites");
    check($fav["total"] === 0, "favorites isolated");
    $favoriteIds = ok(
        $other->call("/user/api/favorite-ids?ids=" . $resource["id"]),
        "other account favorite state",
    );
    check($favoriteIds === [], "favorite IDs isolated");
    ok($user->call("/admin/api/settings"), "user cannot access admin", 401);
    ok(
        $admin->call("/admin/api/settings", [
            "submissions_enabled" => "1",
            "public_api_enabled" => "0",
        ]),
        "configure review/API gate",
    );
    $sub = ok(
        $user->call("/user/api/submissions", [
            "title" => $tag . " submission",
            "url" => "https://t.me/" . $tag . "submit",
            "type" => "group",
            "description" => "integration submission",
        ]),
        "submit resource",
    );
    ok(
        $user->call("/user/api/submissions", [
            "title" => $tag . " duplicate",
            "url" => "https://t.me/" . $tag . "submit",
            "type" => "group",
        ]),
        "pending duplicate blocked",
        422,
    );
    $approved = ok(
        $admin->call("/admin/api/submissions/" . $sub["id"] . "/review", [
            "status" => "approved",
            "review_note" => "approved integration",
        ]),
        "approve submission",
    );
    $ids[] = $approved["resource_id"];
    ok(
        $admin->call("/admin/api/submissions/" . $sub["id"] . "/review", [
            "status" => "approved",
        ]),
        "review is single use",
        409,
    );
    ok($guest->call("/api/v1/search?q=" . $tag), "external API disabled", 403);
    $key = ok(
        $admin->call("/admin/api/keys", ["name" => $tag, "daily_limit" => 2]),
        "create API key",
    );
    ok(
        $admin->call("/admin/api/settings", ["public_api_enabled" => "1"]),
        "enable external API",
    );
    ok(
        $guest->call("/api/v1/search?q=" . $tag, null, [
            "Authorization" => "Bearer " . $key["token"],
        ]),
        "API bearer request one",
    );
    ok(
        $guest->call("/api/v1/search?q=" . $tag, null, [
            "Authorization" => "Bearer " . $key["token"],
        ]),
        "API bearer request two",
    );
    ok(
        $guest->call("/api/v1/search?q=" . $tag, null, [
            "Authorization" => "Bearer " . $key["token"],
        ]),
        "API daily quota",
        429,
    );
    ok(
        $admin->call("/admin/api/keys/" . $key["id"] . "/revoke", []),
        "revoke key",
    );
    ok(
        $guest->call("/api/v1/search", null, [
            "Authorization" => "Bearer " . $key["token"],
        ]),
        "revoked key rejected",
        401,
    );
    ok(
        $admin->call("/admin/api/categories/" . $cat["id"], [], [], "DELETE"),
        "linked category cannot delete",
        422,
    );
    // Always exceed the configured Bot page size to exercise real pagination.
    for ($i = 0; $i < BotSearchSettings::pageSize() + 1; $i++) {
        $r = ResourceService::save([
            "title" => $tag . " bot " . $i,
            "url" => "https://t.me/" . $tag . "bot" . $i,
            "description" => "bot fixture",
            "type" => "channel",
        ]);
        $ids[] = $r["id"];
    }
    $fake = new FakeTelegram();
    $bot = new Bot($fake);
    $processor = new UpdateProcessor($bot);
    $message = [
        "update_id" => 101,
        "message" => [
            "text" => $tag . " bot",
            "chat" => ["id" => $botUser, "type" => "private"],
            "from" => ["id" => $botUser, "first_name" => $tag],
        ],
    ];
    check(
        $processor->process($botId, $message) === 102,
        "offset after success",
    );
    $processor->process($botId, $message);
    check($fake->count("sendMessage") === 1, "update deduplication");
    $s = Db::name("bot_sessions")->where("telegram_user_id", $botUser)->find();
    $cb = [
        "id" => "test-callback",
        "from" => ["id" => $botUser],
        "message" => ["chat" => ["id" => $botUser], "message_id" => 9001],
        "data" => "s:" . $s["id"] . ":1:p2",
    ];
    $wrong = $cb;
    $wrong["from"]["id"]++;
    $bot->handle(["callback_query" => $wrong]);
    check($fake->count("editMessageText") === 0, "callback owner enforced");
    $bot->handle(["callback_query" => $cb]);
    $s = Db::name("bot_sessions")->where("id", $s["id"])->find();
    check(
        (int) $s["page"] === 2 && (int) $s["revision"] === 2,
        "next page and revision persisted",
    );
    $bot->handle(["callback_query" => $cb]);
    check(
        $fake->count("editMessageText") === 1,
        "stale repeated click ignored",
    );
    $cb["data"] = "s:" . $s["id"] . ":2:p1";
    $fake->notModified = true;
    $bot->handle(["callback_query" => $cb]);
    $s = Db::name("bot_sessions")->where("id", $s["id"])->find();
    check(
        (int) $s["page"] === 1 && (int) $s["revision"] === 3,
        "not-modified retry commits previous page",
    );
    Db::name("bot_sessions")
        ->where("id", $s["id"])
        ->update(["expires_at" => "2000-01-01 00:00:00"]);
    $before = $fake->count("editMessageText");
    $cb["data"] = "s:" . $s["id"] . ":3:p2";
    $bot->handle(["callback_query" => $cb]);
    check(
        $fake->count("editMessageText") === $before,
        "expired buttons rejected",
    );
    $fake->fail = true;
    $message["update_id"] = 102;
    $thrown = false;
    try {
        $processor->process($botId, $message);
    } catch (TelegramError $e) {
        $thrown = true;
    }
    check(
        $thrown &&
            Db::name("bot_updates")
                ->where(["bot_id" => $botId, "update_id" => 102])
                ->value("state") === "processing",
        "429 leaves update retryable",
    );
    $processor->process($botId, $message);
    check(
        Db::name("bot_updates")
            ->where(["bot_id" => $botId, "update_id" => 102])
            ->value("state") === "done",
        "retry completes update",
    );
    ok($user->call("/user/api/logout", []), "logout");
    ok($user->call("/user/api/me"), "revoked session rejected", 401);
    echo "ALL " . $checks . " CHECKS PASSED" . PHP_EOL;
} finally {
    $users = Db::name("users")
        ->whereLike("username", $tag . "%")
        ->whereOr("telegram_id", $botUser)
        ->column("id");
    if ($users) {
        Db::name("sessions")->whereIn("user_id", $users)->delete();
        Db::name("favorites")->whereIn("user_id", $users)->delete();
        Db::name("submissions")->whereIn("user_id", $users)->delete();
        Db::name("users")->whereIn("id", $users)->delete();
    }
    if ($ids) {
        Db::name("resources")->whereIn("id", $ids)->delete();
    }
    Db::name("resources")
        ->whereLike("title", $tag . "%")
        ->delete();
    Db::name("categories")->where("name", $tag)->delete();
    Db::name("api_keys")->where("name", $tag)->delete();
    Db::name("audit_logs")->where("actor_id", $adminId)->delete();
    Db::name("admins")->where("id", $adminId)->delete();
    Db::name("search_logs")
        ->whereLike("query", $tag . "%")
        ->delete();
    Db::name("bot_sessions")->where("telegram_user_id", $botUser)->delete();
    Db::name("bot_updates")->where("bot_id", $botId)->delete();
    foreach (["public_api_enabled", "submissions_enabled"] as $key) {
        if (array_key_exists($key, $settingsBackup)) {
            Db::execute(
                "INSERT INTO ts_settings (name,value) VALUES (?,?) ON DUPLICATE KEY UPDATE value=?",
                [$key, $settingsBackup[$key], $settingsBackup[$key]],
            );
        } else {
            Db::name("settings")->where("name", $key)->delete();
        }
    }
    echo "Test-owned fixtures removed; settings restored." . PHP_EOL;
}
