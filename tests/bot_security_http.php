<?php
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();
use think\facade\Db;
use app\common\service\BotSecuritySettings;

set_exception_handler(static function (Throwable $e): never {
    fwrite(STDERR, $e->getMessage() . PHP_EOL);
    exit(1);
});
class SecurityBrowser
{
    public string $token = "";
    public string $csrf = "";
    public function call(string $path, ?array $body = null): array
    {
        $ch = curl_init("http://127.0.0.1:8088" . $path);
        curl_setopt_array($ch, [
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_TIMEOUT => 15,
            CURLOPT_PROXY => "",
            CURLOPT_HTTPHEADER => [
                "Content-Type: application/json",
                "Origin: http://127.0.0.1:8088",
                "X-CSRF-Token: " . $this->csrf,
            ],
            CURLOPT_COOKIE => "starindex_admin=" . $this->token,
        ]);
        if ($body !== null) {
            curl_setopt($ch, CURLOPT_POST, true);
            curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode($body));
        }
        $raw = curl_exec($ch);
        $status = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
        curl_close($ch);
        $json = json_decode((string) $raw, true) ?? [];
        return [$status, $json["data"] ?? $json];
    }
}
$checks = 0;
function securityHttp(bool $ok, string $message): void
{
    global $checks;
    if (!$ok) {
        throw new RuntimeException("FAIL " . $message);
    }
    $checks++;
    echo "PASS $message\n";
}
$tag = "guard_http_" . bin2hex(random_bytes(4));
$backup = Db::name("settings")->where("name", BotSecuritySettings::KEY)->find();
$admins = [];
$roleId = $groupId = 0;
try {
    $root = new SecurityBrowser();
    $limited = new SecurityBrowser();
    foreach ([$root, $limited] as $i => $browser) {
        $admin = Db::name("admins")->insertGetId([
            "username" => $tag . $i,
            "display_name" => $tag,
            "password_hash" => password_hash(
                bin2hex(random_bytes(16)),
                PASSWORD_DEFAULT,
            ),
            "is_super" => $i === 0 ? 1 : 0,
            "created_at" => date("Y-m-d H:i:s"),
        ]);
        $admins[] = $admin;
        $browser->token = bin2hex(random_bytes(32));
        $browser->csrf = bin2hex(random_bytes(24));
        Db::name("admin_sessions")->insert([
            "admin_id" => $admin,
            "token_hash" => hash("sha256", $browser->token),
            "csrf_token" => $browser->csrf,
            "created_at" => date("Y-m-d H:i:s"),
            "expires_at" => date("Y-m-d H:i:s", time() + 600),
        ]);
    }
    $roleId = Db::name("roles")->insertGetId([
        "name" => $tag,
        "description" => "",
        "status" => 1,
        "created_at" => date("Y-m-d H:i:s"),
    ]);
    Db::name("admin_roles")->insert([
        "admin_id" => $admins[1],
        "role_id" => $roleId,
    ]);
    foreach (
        [
            "bot_security.read",
            "bot_security.update",
            "bot_groups.read",
            "bot_groups.update",
        ]
        as $permission
    ) {
        Db::name("role_permissions")->insert([
            "role_id" => $roleId,
            "permission" => $permission,
        ]);
    }
    foreach (
        [
            ["bot_security", "private_mode", 0],
            ["bot_groups", "title", 0],
            ["bot_groups", "verify_mode", 1],
        ]
        as [$module, $field, $write]
    ) {
        Db::name("role_fields")->insert([
            "role_id" => $roleId,
            "resource" => $module,
            "field" => $field,
            "can_read" => 1,
            "can_write" => $write,
        ]);
    }
    $botId = random_int(90000000000, 90999999999);
    $groupId = Db::name("bot_groups")->insertGetId([
        "bot_id" => $botId,
        "chat_id" => -$botId,
        "title" => $tag,
        "ad_keywords" => "隐藏关键词",
        "allow_domains" => "",
        "created_at" => date("Y-m-d H:i:s"),
        "updated_at" => date("Y-m-d H:i:s"),
    ]);
    $guest = new SecurityBrowser();
    securityHttp(
        $guest->call("/admin/api/bot-security")[0] === 401,
        "security settings require administrator authentication",
    );
    securityHttp(
        $guest->call("/admin/api/bot-groups")[0] === 401,
        "group records require administrator authentication",
    );
    [$status, $data] = $root->call("/admin/api/bot-security");
    securityHttp(
        $status === 200 &&
            isset($data["group_mode"], $data["trust_minutes"]) &&
            !isset($data["revision"]),
        "root reads settings without internal policy revision",
    );
    [$status, $data] = $limited->call("/admin/api/bot-security");
    securityHttp(
        $status === 200 && array_keys($data) === ["private_mode"],
        "hidden security fields are removed from responses",
    );
    securityHttp(
        $limited->call("/admin/api/bot-security", [
            "private_mode" => "off",
        ])[0] === 403,
        "read-only security field cannot be changed",
    );
    $rootCsrf = $root->csrf;
    $root->csrf = "";
    securityHttp(
        $root->call("/admin/api/bot-security", ["trust_minutes" => 31])[0] ===
            403,
        "security writes require CSRF",
    );
    $root->csrf = $rootCsrf;
    [$status, $data] = $root->call("/admin/api/bot-security", [
        "trust_minutes" => 31,
    ]);
    securityHttp(
        $status === 200 &&
            $data["trust_minutes"] === 31 &&
            BotSecuritySettings::get()["trust_minutes"] === 31,
        "root saves a partial setting through HTTP",
    );
    securityHttp(
        $root->call("/admin/api/bot-security", [
            "group_limit" => 21,
            "trust_minutes" => 32,
        ])[0] === 422 && BotSecuritySettings::get()["trust_minutes"] === 31,
        "invalid settings fail atomically with field validation",
    );
    securityHttp(
        $root->call("/admin/api/bot-security", ["revision" => "forged"])[0] ===
            403,
        "unknown and internal fields cannot be mass-assigned",
    );
    [$status, $data] = $limited->call("/admin/api/bot-groups?id=" . $groupId);
    securityHttp(
        $status === 200 &&
            (int) $data["total"] === 1 &&
            (int) $data["items"][0]["id"] === (int) $groupId &&
            !isset($data["items"][0]["chat_id"]) &&
            !isset($data["items"][0]["ad_keywords"]),
        "group ID filtering retains real ID while projecting hidden fields: " .
            json_encode([
                $status,
                $data["total"] ?? null,
                $data["items"][0]["id"] ?? null,
            ]),
    );
    [$status, $data] = $limited->call("/admin/api/bot-groups/" . $groupId, [
        "verify_mode" => "sequence",
    ]);
    securityHttp(
        $status === 200 &&
            $data["verify_mode"] === "sequence" &&
            !isset($data["ad_keywords"]),
        "authorized group field can be edited and response is projected",
    );
    securityHttp(
        $limited->call("/admin/api/bot-groups/" . $groupId, [
            "ad_mode" => "strict",
        ])[0] === 403,
        "hidden group moderation field cannot be changed",
    );
    securityHttp(
        $root->call("/admin/api/bot-groups/" . $groupId, [
            "chat_id" => -1,
        ])[0] === 403,
        "Telegram identity cannot be changed through settings",
    );
    securityHttp(
        $root->call("/admin/api/bot-groups/" . $groupId, [
            "allow_domains" => "https://example.com",
        ])[0] === 422,
        "domain validation rejects protocols and paths",
    );
    securityHttp(
        $root->call("/admin/api/bot-groups/" . $groupId, [
            "flood_limit" => 2,
        ])[0] === 422,
        "invalid flood thresholds are rejected",
    );
    [$status, $data] = $root->call("/admin/api/bot-groups/" . $groupId, [
        "ad_mode" => "strict",
        "allow_domains" => "Example.COM\nexample.com",
        "ad_keywords" => "垃圾广告",
        "flood_limit" => 6,
    ]);
    securityHttp(
        $status === 200 &&
            $data["allow_domains"] === "example.com" &&
            $data["flood_limit"] === 6,
        "group rules normalize domains and persist through HTTP",
    );
    $catalog = $root->call("/admin/api/access/catalog")[1];
    securityHttp(
        isset($catalog["bot_security"], $catalog["bot_groups"]),
        "new modules appear in the role permission catalog",
    );
    securityHttp(
        Db::name("audit_logs")
            ->whereIn("actor_id", $admins)
            ->whereLike("action", "更新机器人%")
            ->count() >= 3,
        "administrator security changes are audited",
    );
    echo "\n$checks security HTTP checks passed; fixtures cleaned.\n";
} finally {
    if ($backup) {
        Db::name("settings")
            ->where("name", BotSecuritySettings::KEY)
            ->update(["value" => $backup["value"]]);
    } else {
        Db::name("settings")->where("name", BotSecuritySettings::KEY)->delete();
    }
    if ($groupId) {
        Db::name("bot_groups")->where("id", $groupId)->delete();
    }
    if ($roleId) {
        foreach (["role_permissions", "role_fields", "admin_roles"] as $table) {
            Db::name($table)->where("role_id", $roleId)->delete();
        }
        Db::name("roles")->where("id", $roleId)->delete();
    }
    if ($admins) {
        Db::name("admin_sessions")->whereIn("admin_id", $admins)->delete();
        Db::name("audit_logs")->whereIn("actor_id", $admins)->delete();
        Db::name("admins")->whereIn("id", $admins)->delete();
    }
}
