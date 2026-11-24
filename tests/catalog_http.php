<?php
/** HTTP security, field grants and user feedback; fixtures are removed in finally. */
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();

use app\common\service\ResourceService;
use think\facade\Db;

set_exception_handler(static function (Throwable $e): never {
    fwrite(STDERR, $e->getMessage() . "\n");
    exit(1);
});

class CatalogBrowser
{
    public string $cookie = "";
    public string $csrf = "";
    public function call(string $path, ?array $body = null): array
    {
        $base = rtrim(getenv("STARINDEX_TEST_URL") ?: "http://127.0.0.1:8088", "/");
        $curl = curl_init($base . $path);
        curl_setopt_array($curl, [
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_PROXY => "",
            CURLOPT_TIMEOUT => 20,
            CURLOPT_COOKIE => $this->cookie,
            CURLOPT_HTTPHEADER => [
                "Content-Type: application/json",
                "Origin: " . $base,
                "X-CSRF-Token: " . $this->csrf,
            ],
            CURLOPT_HEADERFUNCTION => function ($handle, $line) {
                if (preg_match("/^Set-Cookie:\s*([^;]+)/i", $line, $match)) {
                    $this->cookie = $match[1];
                }
                return strlen($line);
            },
        ]);
        if ($body !== null) {
            curl_setopt($curl, CURLOPT_POST, true);
            curl_setopt($curl, CURLOPT_POSTFIELDS, json_encode($body));
        }
        $raw = curl_exec($curl);
        $status = curl_getinfo($curl, CURLINFO_HTTP_CODE);
        curl_close($curl);
        $result = json_decode((string) $raw, true, 512, JSON_THROW_ON_ERROR);
        $this->csrf = $result["data"]["csrf_token"] ?? $this->csrf;
        return [(int) $status, $result];
    }
}
$checks = 0;
function catalogHttpCheck(bool $ok, string $message): void
{
    global $checks;
    if (!$ok) {
        throw new RuntimeException("FAIL " . $message);
    }
    echo "PASS " . $message . "\n";
    $checks++;
}
function catalogHttpResponse(
    array $response,
    int $status,
    string $message,
): array {
    catalogHttpCheck(
        $response[0] === $status,
        $message . " (HTTP " . $response[0] . ")",
    );
    return $response[1]["data"] ?? $response[1];
}
$tag = "http_" . bin2hex(random_bytes(4));
$adminIds = [];
$userId = $roleId = $resourceId = 0;
$hashes = [];
$setting = Db::name("settings")
    ->where("name", "submissions_enabled")
    ->value("value");
try {
    Db::name("settings")
        ->where("name", "submissions_enabled")
        ->update(["value" => "1"]);
    $password = bin2hex(random_bytes(18));
    $userId = Db::name("users")->insertGetId([
        "username" => $tag,
        "display_name" => "HTTP fixture",
        "password_hash" => password_hash($password, PASSWORD_DEFAULT),
        "created_at" => date("Y-m-d H:i:s"),
    ]);
    $makeAdmin = function (bool $super) use (&$adminIds, $tag): CatalogBrowser {
        $id = Db::name("admins")->insertGetId([
            "username" => $tag . ($super ? "_root" : "_limited"),
            "display_name" => "HTTP fixture",
            "is_super" => (int) $super,
            "created_at" => date("Y-m-d H:i:s"),
        ]);
        $adminIds[] = $id;
        $token = bin2hex(random_bytes(32));
        $csrf = bin2hex(random_bytes(24));
        Db::name("admin_sessions")->insert([
            "admin_id" => $id,
            "token_hash" => hash("sha256", $token),
            "csrf_token" => $csrf,
            "created_at" => date("Y-m-d H:i:s"),
            "expires_at" => date("Y-m-d H:i:s", time() + 600),
        ]);
        $browser = new CatalogBrowser();
        $browser->cookie = "starindex_admin=" . $token;
        $browser->csrf = $csrf;
        return $browser;
    };
    $root = $makeAdmin(true);
    $limited = $makeAdmin(false);
    $path = "/admin/api/users/$userId/submission-policy";
    catalogHttpResponse(
        new CatalogBrowser()->call($path, ["submission_allowed" => 0]),
        401,
        "anonymous user cannot set submission policy",
    );
    $noCsrf = clone $root;
    $noCsrf->csrf = "";
    catalogHttpResponse(
        $noCsrf->call($path, ["submission_allowed" => 0]),
        403,
        "submission policy requires CSRF",
    );
    catalogHttpResponse(
        $limited->call($path, ["submission_allowed" => 0]),
        403,
        "submission policy requires users.update",
    );
    $roleId = Db::name("roles")->insertGetId([
        "name" => $tag,
        "created_at" => date("Y-m-d H:i:s"),
        "updated_at" => date("Y-m-d H:i:s"),
    ]);
    Db::name("admin_roles")->insert([
        "admin_id" => $adminIds[1],
        "role_id" => $roleId,
    ]);
    foreach (
        ["users.read", "users.update", "resources.read", "resources.update"]
        as $permission
    ) {
        Db::name("role_permissions")->insert([
            "role_id" => $roleId,
            "permission" => $permission,
        ]);
    }
    foreach (
        ["users" => ["submission_allowed"], "resources" => ["title"]]
        as $module => $fields
    ) {
        foreach ($fields as $field) {
            Db::name("role_fields")->insert([
                "role_id" => $roleId,
                "resource" => $module,
                "field" => $field,
                "can_read" => 1,
                "can_write" => 1,
            ]);
        }
    }
    catalogHttpResponse(
        $limited->call($path, ["submission_note" => "unauthorized"]),
        403,
        "reason has independent field write permission",
    );
    catalogHttpResponse(
        $limited->call($path, ["status" => 0]),
        403,
        "submission endpoint cannot bypass account-status field permissions",
    );
    $result = catalogHttpResponse(
        $root->call($path, [
            "submission_allowed" => 0,
            "submission_note" => "请勿反复提交无关链接",
        ]),
        200,
        "administrator prohibits submissions with a reason",
    );
    catalogHttpCheck(
        $result["status"] === 1 && $result["submission_allowed"] === 0,
        "submission ban leaves account active",
    );
    $list = catalogHttpResponse(
        $limited->call("/admin/api/users"),
        200,
        "limited operator can read granted user fields",
    );
    $record = array_values(
        array_filter(
            $list["items"],
            fn($u) => (int) $u["id"] === (int) $userId,
        ),
    )[0];
    catalogHttpCheck(
        isset($record["submission_allowed"]) &&
            !isset($record["submission_note"]) &&
            !isset($record["password_hash"]),
        "user policy reason and password are not leaked",
    );
    catalogHttpResponse(
        $root->call($path, ["submission_allowed" => 2]),
        422,
        "invalid policy flag rejected",
    );
    catalogHttpResponse(
        $root->call($path, ["submission_note" => str_repeat("字", 501)]),
        422,
        "overlong policy reason rejected",
    );
    $user = new CatalogBrowser();
    catalogHttpResponse(
        $user->call("/user/api/login", [
            "username" => $tag,
            "password" => $password,
        ]),
        200,
        "prohibited submitter can still log in",
    );
    $denial = catalogHttpResponse(
        $user->call("/user/api/submissions", ["url" => "https://t.me/" . $tag]),
        403,
        "prohibited submitter receives HTTP403",
    );
    catalogHttpCheck(
        str_contains($denial["message"], "请勿反复提交无关链接"),
        "user sees the configured restriction reason",
    );
    catalogHttpResponse(
        $user->call("/user/api/me"),
        200,
        "restriction does not revoke the user session",
    );
    catalogHttpResponse(
        $limited->call($path, ["submission_allowed" => 1]),
        200,
        "field-authorized operator restores submissions",
    );
    catalogHttpCheck(
        Db::name("users")->where("id", $userId)->value("submission_note") ===
            "请勿反复提交无关链接",
        "field-limited write preserves protected reason",
    );
    $resource = ResourceService::save([
        "title" => $tag,
        "url" => "https://t.me/" . $tag,
        "status" => "blocked",
        "status_reason" => "频道已被封禁",
    ]);
    $resourceId = (int) $resource["id"];
    $hashes[] = $resource["url_hash"];
    $denial = catalogHttpResponse(
        $user->call("/user/api/submissions", [
            "url" => "https://telegram.me/" . strtoupper($tag) . "?ref=test",
        ]),
        422,
        "link-only canonical duplicate is rejected over HTTP",
    );
    catalogHttpCheck(
        str_contains($denial["message"], "已被封禁") &&
            str_contains($denial["errors"]["url"], "频道已被封禁"),
        "blocked-link reason is also attached to the URL field",
    );
    catalogHttpResponse(
        $limited->call("/admin/api/resources/$resourceId", [
            "status_reason" => "bypass",
        ]),
        403,
        "resource reason has independent field write permission",
    );
    $resourceResult = catalogHttpResponse(
        $limited->call("/admin/api/resources/$resourceId", [
            "title" => $tag . " edited",
        ]),
        200,
        "limited operator can edit allowed resource field",
    );
    catalogHttpCheck(
        !isset($resourceResult["status_reason"]) &&
            Db::name("resources")
                ->where("id", $resourceId)
                ->value("status_reason") === "频道已被封禁",
        "limited resource edit preserves and hides the reason",
    );
    $input = [
        "title" => $tag . " pending",
        "url" => "https://t.me/" . $tag . "_new",
    ];
    $hashes[] = hash("sha256", $input["url"]);
    $submission = catalogHttpResponse(
        $user->call("/user/api/submissions", $input),
        200,
        "restored user submits a new link",
    );
    $review = catalogHttpResponse(
        $root->call("/admin/api/submissions/" . $submission["id"] . "/review", [
            "status" => "approved",
        ]),
        200,
        "review can publish its own pending link",
    );
    catalogHttpResponse(
        $user->call("/user/api/submissions", $input),
        422,
        "approved submission cannot be resubmitted",
    );
    catalogHttpCheck(
        Db::name("resources")
            ->where("id", $review["resource_id"])
            ->value("source") === "submission",
        "reviewed resource keeps submission provenance",
    );
    echo "\n$checks catalog HTTP checks passed.\n";
} finally {
    if ($userId) {
        Db::name("submissions")->where("user_id", $userId)->delete();
        Db::name("sessions")->where("user_id", $userId)->delete();
        Db::name("users")->where("id", $userId)->delete();
        Db::name("rate_limits")
            ->whereLike(
                "bucket",
                hash("sha256", "submission:" . $userId) . ":%",
            )
            ->delete();
    }
    if ($hashes) {
        Db::name("resources")->whereIn("url_hash", $hashes)->delete();
        Db::name("link_locks")->whereIn("url_hash", $hashes)->delete();
    }
    if ($adminIds) {
        foreach (["admin_sessions", "admin_roles"] as $table) {
            Db::name($table)->whereIn("admin_id", $adminIds)->delete();
        }
        Db::name("audit_logs")->whereIn("actor_id", $adminIds)->delete();
        Db::name("admins")->whereIn("id", $adminIds)->delete();
    }
    if ($roleId) {
        foreach (["role_permissions", "role_fields"] as $table) {
            Db::name($table)->where("role_id", $roleId)->delete();
        }
        Db::name("roles")->where("id", $roleId)->delete();
    }
    Db::name("settings")
        ->where("name", "submissions_enabled")
        ->update(["value" => $setting]);
}
