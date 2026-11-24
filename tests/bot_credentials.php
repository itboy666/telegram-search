<?php
/** Secret storage + real HTTP authorization. --live also re-saves the current token. */
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();
set_exception_handler(static function (Throwable $e): never {
    fwrite(STDERR, "FAIL " . $e->getMessage() . PHP_EOL);
    exit(1);
});

use app\common\exception\DomainError;
use app\common\service\{BotCredentials, BotSearchSettings, BotState, Settings};
use app\common\telegram\{TelegramError, Transport};
use think\facade\Db;

final class CredentialVerifier implements Transport
{
    public int $calls = 0;
    public function __construct(private mixed $result) {}
    public function call(string $method, array $params = []): mixed
    {
        $this->calls++;
        if ($method !== "getMe" || $params !== []) {
            throw new RuntimeException("Unexpected Telegram operation");
        }
        if ($this->result instanceof Throwable) {
            throw $this->result;
        }
        return $this->result;
    }
}

final class CredentialBrowser
{
    public string $cookie = "";
    public string $csrf = "";
    public function request(
        string $route,
        ?array $body = null,
        bool $csrf = true,
    ): array {
        $handle = curl_init("http://127.0.0.1:8088" . $route);
        curl_setopt_array($handle, [
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_TIMEOUT => 18,
            CURLOPT_PROXY => "",
            CURLOPT_COOKIE => $this->cookie,
            CURLOPT_HTTPHEADER => [
                "Content-Type: application/json",
                "Origin: http://127.0.0.1:8088",
                "X-CSRF-Token: " . ($csrf ? $this->csrf : ""),
            ],
        ]);
        if ($body !== null) {
            curl_setopt($handle, CURLOPT_POST, true);
            curl_setopt($handle, CURLOPT_POSTFIELDS, json_encode($body));
        }
        $raw = curl_exec($handle);
        $status = (int) curl_getinfo($handle, CURLINFO_HTTP_CODE);
        curl_close($handle);
        return [$status, json_decode((string) $raw, true) ?? []];
    }
}

$checks = 0;
function credentialCheck(bool $passed, string $label): void
{
    global $checks;
    if (!$passed) {
        throw new RuntimeException("FAIL " . $label);
    }
    $checks++;
    echo "PASS " . $label . PHP_EOL;
}
function credentialFailure(callable $fn, int $status, string $label): void
{
    try {
        $fn();
    } catch (DomainError $e) {
        credentialCheck($e->status === $status, $label);
        return;
    }
    throw new RuntimeException("FAIL expected rejection: " . $label);
}
function credentialSession(int $adminId): CredentialBrowser
{
    $browser = new CredentialBrowser();
    $session = bin2hex(random_bytes(32));
    $browser->cookie = "starindex_admin=" . $session;
    $browser->csrf = bin2hex(random_bytes(24));
    Db::name("admin_sessions")->insert([
        "admin_id" => $adminId,
        "token_hash" => hash("sha256", $session),
        "csrf_token" => $browser->csrf,
        "created_at" => date("Y-m-d H:i:s"),
        "expires_at" => date("Y-m-d H:i:s", time() + 600),
    ]);
    return $browser;
}

$initial = BotCredentials::snapshot();
$initialStored = Db::name("settings")
    ->where("name", BotCredentials::SETTING)
    ->value("value");
$initialPageSize = Db::name("settings")
    ->where("name", BotSearchSettings::SETTING)
    ->value("value");
$tag = "bot_credentials_" . bin2hex(random_bytes(5));
$adminIds = [];
$roleId = 0;
$transaction = false;
try {
    foreach ([1, 0] as $super) {
        $adminIds[] = Db::name("admins")->insertGetId([
            "username" => $tag . "_" . $super,
            "display_name" => $tag,
            "password_hash" => password_hash(
                bin2hex(random_bytes(24)),
                PASSWORD_DEFAULT,
            ),
            "is_super" => $super,
            "created_at" => date("Y-m-d H:i:s"),
        ]);
    }
    $root = credentialSession($adminIds[0]);
    $restricted = credentialSession($adminIds[1]);
    $anonymous = new CredentialBrowser();

    credentialCheck(
        $anonymous->request("/admin/api/bot")[0] === 401,
        "anonymous cannot read connection",
    );
    credentialCheck(
        $anonymous->request("/admin/api/bot", ["token" => "invalid"])[0] ===
            401,
        "anonymous cannot save token",
    );
    credentialCheck(
        $root->request("/admin/api/bot", ["token" => "invalid"], false)[0] ===
            403,
        "token change requires CSRF",
    );
    credentialCheck(
        $restricted->request("/admin/api/bot", ["token" => "invalid"])[0] ===
            403,
        "default deny rejects token update",
    );

    $roleId = Db::name("roles")->insertGetId([
        "name" => $tag,
        "description" => "Temporary credential test",
        "created_at" => date("Y-m-d H:i:s"),
        "updated_at" => date("Y-m-d H:i:s"),
    ]);
    Db::name("admin_roles")->insert([
        "admin_id" => $adminIds[1],
        "role_id" => $roleId,
    ]);
    Db::name("role_permissions")->insert([
        "role_id" => $roleId,
        "permission" => "bot.read",
    ]);
    Db::name("role_fields")->insert([
        "role_id" => $roleId,
        "resource" => "bot",
        "field" => "username",
        "can_read" => 1,
        "can_write" => 0,
    ]);
    [$code, $body] = $restricted->request("/admin/api/bot");
    credentialCheck(
        $code === 200 && !isset($body["data"]["token"]),
        "hidden token field omits configuration metadata",
    );
    Db::name("role_permissions")->insert([
        "role_id" => $roleId,
        "permission" => "bot.update",
    ]);
    credentialCheck(
        $restricted->request("/admin/api/bot", ["token" => "invalid"])[0] ===
            403,
        "action grant alone cannot change token",
    );
    Db::name("role_fields")->insert([
        "role_id" => $roleId,
        "resource" => "bot",
        "field" => "token",
        "can_read" => 1,
        "can_write" => 0,
    ]);
    [$code, $body] = $restricted->request("/admin/api/bot");
    credentialCheck(
        $code === 200 &&
            is_array($body["data"]["token"]) &&
            !isset($body["data"]["credential_version"]),
        "read-only token field returns safe status only",
    );
    credentialCheck(
        $restricted->request("/admin/api/bot", ["token" => "invalid"])[0] ===
            403,
        "read-only field rejects writes",
    );
    Db::name("role_fields")
        ->where(["role_id" => $roleId, "field" => "token"])
        ->update(["can_write" => 1]);
    Db::name("role_permissions")
        ->where(["role_id" => $roleId, "permission" => "bot.update"])
        ->delete();
    credentialCheck(
        $restricted->request("/admin/api/bot", ["token" => "invalid"])[0] ===
            403,
        "field write alone cannot change token",
    );
    Db::name("role_permissions")->insert([
        "role_id" => $roleId,
        "permission" => "bot.update",
    ]);
    [$code, $body] = $restricted->request("/admin/api/bot", [
        "token" => "invalid",
    ]);
    credentialCheck(
        $code === 422 && isset($body["errors"]["token"]),
        "authorized invalid token receives an inline field error",
    );
    credentialCheck(
        $root->request("/admin/api/bot", ["token" => []])[0] === 422,
        "non-string token rejected",
    );
    credentialCheck(
        $root->request("/admin/api/bot", [])[0] === 422,
        "missing token rejected",
    );
    credentialCheck(
        $root->request("/admin/api/bot", [
            "token" => "invalid",
            "is_super" => 1,
        ])[0] === 403,
        "unknown write fields rejected",
    );

    [$code, $body] = $root->request("/admin/api/bot");
    credentialCheck(
        $code === 200 &&
            $body["data"]["page_size"] === BotSearchSettings::pageSize(),
        "admin reads current search page size",
    );
    credentialCheck(
        $anonymous->request("/admin/api/bot", ["page_size" => 17])[0] === 401 &&
            $root->request("/admin/api/bot", ["page_size" => 17], false)[0] ===
                403,
        "search settings require admin authentication and CSRF",
    );
    [$code, $body] = $restricted->request("/admin/api/bot");
    credentialCheck(
        $code === 200 && !isset($body["data"]["page_size"]),
        "page size is omitted without field read permission",
    );
    credentialCheck(
        $restricted->request("/admin/api/bot", ["page_size" => 17])[0] === 403,
        "token permission cannot change search settings",
    );
    Db::name("role_fields")->insert([
        "role_id" => $roleId,
        "resource" => "bot",
        "field" => "page_size",
        "can_read" => 1,
        "can_write" => 0,
    ]);
    [$code, $body] = $restricted->request("/admin/api/bot");
    credentialCheck(
        $code === 200 &&
            isset($body["data"]["page_size"]) &&
            $restricted->request("/admin/api/bot", ["page_size" => 17])[0] ===
                403,
        "read-only search settings are visible but cannot be changed",
    );
    Db::name("role_fields")
        ->where(["role_id" => $roleId, "field" => "page_size"])
        ->update(["can_write" => 1]);
    Db::name("role_permissions")
        ->where(["role_id" => $roleId, "permission" => "bot.update"])
        ->delete();
    credentialCheck(
        $restricted->request("/admin/api/bot", ["page_size" => 17])[0] === 403,
        "page-size field write still requires action permission",
    );
    Db::name("role_permissions")->insert([
        "role_id" => $roleId,
        "permission" => "bot.update",
    ]);
    [$code, $body] = $restricted->request("/admin/api/bot", [
        "page_size" => 17,
    ]);
    credentialCheck(
        $code === 200 &&
            $body["data"]["page_size"] === 17 &&
            BotSearchSettings::pageSize() === 17,
        "authorized page-size-only save works without supplying a token",
    );
    foreach ([0, 31, null, [], true, 1.5, "2.5", "12x"] as $invalidSize) {
        [$code, $body] = $root->request("/admin/api/bot", [
            "page_size" => $invalidSize,
        ]);
        credentialCheck(
            $code === 422 &&
                isset($body["errors"]["page_size"]) &&
                BotSearchSettings::pageSize() === 17,
            "HTTP rejects invalid page size without changing config: " .
                json_encode($invalidSize),
        );
    }
    credentialCheck(
        $root->request("/admin/api/bot", [
            "page_size" => 14,
            "token" => "invalid",
        ])[0] === 422 && BotSearchSettings::pageSize() === 17,
        "invalid token prevents a partially saved combined configuration",
    );
    credentialCheck(
        BotCredentials::snapshot()["version"] === $initial["version"],
        "search configuration changes preserve encrypted token and worker identity",
    );
    credentialCheck(
        $root->request("/admin/api/bot", ["page_size" => 12])[0] === 200,
        "default page size can be saved via HTTP",
    );

    $botId = (int) (explode(":", $initial["token"])[0] ?: 123456789);
    $candidate = $botId . ":" . str_repeat("x", 35);
    $me = [
        "id" => $botId,
        "username" => "credential_fixture_bot",
        "first_name" => "Fixture",
        "is_bot" => true,
    ];
    $verifier = new CredentialVerifier($me);
    credentialFailure(
        fn() => BotCredentials::verify("", $verifier),
        422,
        "empty token rejected before Telegram request",
    );
    credentialCheck(
        $verifier->calls === 0,
        "format failures never call Telegram",
    );
    credentialCheck(
        BotCredentials::verify(" " . $candidate . "\n", $verifier)["id"] ===
            $botId,
        "whitespace trimmed and identity verified",
    );
    foreach ([401, 404, 429, 0] as $apiCode) {
        credentialFailure(
            fn() => BotCredentials::save(
                $candidate,
                $adminIds[0],
                new CredentialVerifier(new TelegramError($apiCode)),
            ),
            422,
            "Telegram failure " . $apiCode . " does not save configuration",
        );
    }
    credentialFailure(
        fn() => BotCredentials::verify(
            $candidate,
            new CredentialVerifier(array_replace($me, ["id" => $botId + 1])),
        ),
        422,
        "mismatched bot identity rejected",
    );
    credentialFailure(
        fn() => BotCredentials::verify(
            $candidate,
            new CredentialVerifier(array_replace($me, ["is_bot" => false])),
        ),
        422,
        "non-bot identity rejected",
    );
    credentialCheck(
        Db::name("settings")
            ->where("name", BotCredentials::SETTING)
            ->value("value") === $initialStored,
        "all rejected requests leave original configuration unchanged",
    );

    // Other connections (including the live worker) cannot see these fictional tokens.
    Db::startTrans();
    $transaction = true;
    $beforeState = BotState::read();
    BotCredentials::save($candidate, $adminIds[0], new CredentialVerifier($me));
    $stored = Db::name("settings")
        ->where("name", BotCredentials::SETTING)
        ->value("value");
    $snapshot = BotCredentials::snapshot();
    credentialCheck(
        !str_contains($stored, $candidate),
        "database stores encrypted token only",
    );
    credentialCheck(
        $snapshot["token"] === $candidate && $snapshot["source"] === "database",
        "encrypted credential round trip and database precedence",
    );
    credentialCheck(
        (fileperms(root_path() . ".bot-token.key") & 0777) === 0600,
        "encryption key file has owner-only permissions",
    );
    credentialCheck(
        !isset(Settings::all()[BotCredentials::SETTING]),
        "general settings exclude private credential record",
    );
    credentialCheck(
        !BotCredentials::current($initial["version"]),
        "worker detects credential generation change",
    );
    $state = BotState::read();
    credentialCheck(
        !isset($beforeState["offset"]) ||
            (int) ($state["offset"] ?? -1) >= (int) $beforeState["offset"],
        "same-bot rotation preserves its previous cursor",
    );
    credentialCheck(
        !$state["running"] && !isset($state["custom_emoji"]),
        "new generation clears old running and icon state",
    );
    credentialCheck(
        !str_contains(json_encode($state), $candidate),
        "connection state never contains the token",
    );
    credentialCheck(
        !BotState::write(["state" => "running"], $initial["version"]),
        "in-flight old worker cannot overwrite new state",
    );
    $version = $snapshot["version"];
    BotCredentials::save($candidate, $adminIds[0], new CredentialVerifier($me));
    credentialCheck(
        BotCredentials::snapshot()["version"] !== $version,
        "re-saving creates a generation to recover a blocked worker",
    );
    $otherId = $botId + 1;
    BotCredentials::save(
        $otherId . ":" . str_repeat("z", 35),
        $adminIds[0],
        new CredentialVerifier(array_replace($me, ["id" => $otherId])),
    );
    $state = BotState::read();
    credentialCheck(
        (int) $state["id"] === $otherId && !isset($state["offset"]),
        "different bot starts with its own cursor",
    );
    credentialCheck(
        !isset($state["last_update_at"]) && !$state["running"],
        "different bot does not inherit old activity",
    );
    $corrupt = json_decode($stored, true);
    $corrupt["encrypted"] = base64_encode(str_repeat("x", 60));
    Db::name("settings")
        ->where("name", BotCredentials::SETTING)
        ->update(["value" => json_encode($corrupt)]);
    credentialFailure(
        fn() => BotCredentials::snapshot(),
        503,
        "tampered ciphertext fails closed without .env fallback",
    );
    $recovery = BotState::read();
    credentialCheck(
        $recovery["state"] === "error" &&
            !$recovery["running"] &&
            isset($recovery["token"]),
        "credential recovery leaves the backend configuration form accessible",
    );
    Db::rollback();
    $transaction = false;
    credentialCheck(
        BotCredentials::snapshot()["version"] === $initial["version"],
        "test transaction restores original configuration",
    );

    if (in_array("--live", $argv, true)) {
        // Only the currently active token is submitted; no Telegram messages are sent.
        [$code, $body] = $root->request("/admin/api/bot", [
            "token" => $initial["token"],
        ]);
        credentialCheck(
            $code === 200,
            "real Telegram getMe verification and HTTP save succeeded",
        );
        credentialCheck(
            !str_contains(json_encode($body), $initial["token"]),
            "successful save never echoes the secret",
        );
        $snapshot = BotCredentials::snapshot();
        credentialCheck(
            $snapshot["source"] === "database" &&
                $snapshot["token"] === $initial["token"],
            "current bot preserved with encrypted backend configuration",
        );
    }
    foreach (
        [
            "/admin/api/bot",
            "/admin/api/settings",
            "/admin/api/dashboard",
            "/admin/api/logs",
            "/api/public/config",
        ]
        as $route
    ) {
        [$code, $body] = $root->request($route);
        credentialCheck(
            $code === 200 &&
                ($initial["token"] === "" ||
                    !str_contains(json_encode($body), $initial["token"])),
            $route . " never exposes the token",
        );
    }
    echo "ALL " . $checks . " BOT CREDENTIAL CHECKS PASSED" . PHP_EOL;
} finally {
    if ($transaction) {
        Db::rollback();
    }
    if ($initialPageSize === null) {
        Db::name("settings")
            ->where("name", BotSearchSettings::SETTING)
            ->delete();
    } else {
        Db::execute(
            "INSERT INTO ts_settings (name,value) VALUES (?,?) ON DUPLICATE KEY UPDATE value=?",
            [BotSearchSettings::SETTING, $initialPageSize, $initialPageSize],
        );
    }
    if ($roleId) {
        Db::name("admin_roles")->where("role_id", $roleId)->delete();
        Db::name("role_permissions")->where("role_id", $roleId)->delete();
        Db::name("role_fields")->where("role_id", $roleId)->delete();
        Db::name("roles")->where("id", $roleId)->delete();
    }
    if ($adminIds) {
        Db::name("admin_sessions")->whereIn("admin_id", $adminIds)->delete();
        Db::name("audit_logs")->whereIn("actor_id", $adminIds)->delete();
        Db::name("admins")->whereIn("id", $adminIds)->delete();
    }
}
