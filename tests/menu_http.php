<?php
// Focused endpoint authorization check; no real menu changes or Telegram calls.
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();
use think\facade\Db;

$cookies = [];
$csrf = "";
$checks = 0;
function menuRequest(
    string $route,
    ?array $body = null,
    bool $authenticated = true,
    bool $withCsrf = true,
): array {
    global $cookies, $csrf;
    $curl = curl_init("http://127.0.0.1:8088" . $route);
    $headers = [
        "Content-Type: application/json",
        "Accept: application/json",
        "Origin: http://127.0.0.1:8088",
    ];
    if ($withCsrf && $csrf) {
        $headers[] = "X-CSRF-Token: " . $csrf;
    }
    curl_setopt_array($curl, [
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT => 15,
        CURLOPT_PROXY => "",
        CURLOPT_HTTPHEADER => $headers,
        CURLOPT_COOKIE => $authenticated ? implode("; ", $cookies) : "",
        CURLOPT_HEADERFUNCTION => function ($curl, $line) use (&$cookies) {
            if (preg_match("/^Set-Cookie:\s*([^;]+)/i", $line, $match)) {
                $cookies[] = $match[1];
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
    return [$status, json_decode((string) $raw, true)];
}
function assertMenuHttp(bool $ok, string $label): void
{
    global $checks;
    if (!$ok) {
        throw new RuntimeException("FAIL " . $label);
    }
    $checks++;
    echo "PASS " . $label . PHP_EOL;
}
$tag = "menu_http_" . bin2hex(random_bytes(5));
$password = bin2hex(random_bytes(24));
$id = Db::name("admins")->insertGetId([
    "username" => $tag,
    "display_name" => $tag,
    "password_hash" => password_hash($password, PASSWORD_DEFAULT),
    "is_super" => 1,
    "created_at" => date("Y-m-d H:i:s"),
]);
$before = app\common\service\BotMenu::get();
try {
    assertMenuHttp(
        menuRequest("/admin/api/bot-menu", null, false)[0] === 401,
        "anonymous menu read denied",
    );
    assertMenuHttp(
        menuRequest("/admin/api/bot-menu", [], false)[0] === 401,
        "anonymous menu write denied",
    );
    [$status, $login] = menuRequest("/admin/api/login", [
        "username" => $tag,
        "password" => $password,
    ]);
    assertMenuHttp($status === 200, "temporary administrator login");
    $csrf = $login["data"]["csrf_token"];
    [$status, $payload] = menuRequest("/admin/api/bot-menu");
    assertMenuHttp(
        $status === 200 &&
            $payload["data"]["icons"] === app\common\telegram\Icons::catalog(),
        "authenticated menu returns the complete icon catalog",
    );
    assertMenuHttp(
        count($payload["data"]["template"]["tags"]) === 13 &&
            $payload["data"]["template"]["timezone"] === "Asia/Shanghai" &&
            $payload["data"]["template"]["sample_values"]["username"] ===
                "@lin_demo",
        "template tags and explicit preview context are returned",
    );
    assertMenuHttp(
        menuRequest("/admin/api/bot-menu", $before, true, false)[0] === 403,
        "CSRF required for menu write",
    );
    $invalid = $before;
    $invalid["welcome_icon"] = "unknown";
    assertMenuHttp(
        menuRequest("/admin/api/bot-menu", $invalid)[0] === 422,
        "invalid menu rejected through HTTP",
    );
    $invalid = $before;
    $invalid["welcome"] = "你好 {{nicknme}}";
    [$status, $response] = menuRequest("/admin/api/bot-menu", $invalid);
    assertMenuHttp(
        $status === 422 && isset($response["errors"]["welcome"]),
        "unknown template tag returns a field error through HTTP",
    );
    assertMenuHttp(
        app\common\service\BotMenu::get() === $before,
        "failed requests preserve saved menu",
    );
    echo "ALL " . $checks . " MENU HTTP CHECKS PASSED" . PHP_EOL;
} finally {
    Db::name("admin_sessions")->where("admin_id", $id)->delete();
    Db::name("audit_logs")->where("actor_id", $id)->delete();
    Db::name("admins")->where("id", $id)->delete();
}
