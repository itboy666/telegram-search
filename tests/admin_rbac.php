<?php
/** Real HTTP/MySQL checks. Only uniquely named fixtures are changed and cleaned. */
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();
use think\facade\Db;
use app\common\service\{AccessMigration, ResourceService};

set_exception_handler(static function (Throwable $e): never {
    fwrite(STDERR, $e->getMessage() . "\n");
    exit(1);
});

class AccessBrowser
{
    public array $cookies = [];
    public array $csrf = [];
    public function call(string $path, ?array $body = null): array
    {
        $scope = str_starts_with($path, "/admin/") ? "admin" : "user";
        $base = rtrim(getenv("STARINDEX_TEST_URL") ?: "http://127.0.0.1:8088", "/");
        $curl = curl_init($base . $path);
        curl_setopt_array($curl, [
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_TIMEOUT => 15,
            CURLOPT_PROXY => "",
            CURLOPT_HTTPHEADER => [
                "Content-Type: application/json",
                "Origin: " . $base,
                "X-CSRF-Token: " . ($this->csrf[$scope] ?? ""),
            ],
            CURLOPT_COOKIE => implode(
                "; ",
                array_map(
                    fn($key, $value) => "$key=$value",
                    array_keys($this->cookies),
                    $this->cookies,
                ),
            ),
            CURLOPT_HEADERFUNCTION => function ($handle, $line) {
                if (
                    preg_match(
                        "/^Set-Cookie:\s*([^=]+)=([^;]*)/i",
                        $line,
                        $matches,
                    )
                ) {
                    $this->cookies[$matches[1]] = $matches[2];
                }
                return strlen($line);
            },
        ]);
        if ($body !== null) {
            curl_setopt($curl, CURLOPT_POST, true);
            curl_setopt($curl, CURLOPT_POSTFIELDS, json_encode($body));
        }
        $raw = curl_exec($curl);
        $status = (int) curl_getinfo($curl, CURLINFO_HTTP_CODE);
        curl_close($curl);
        $response = json_decode((string) $raw, true) ?? [];
        if (isset($response["data"]["csrf_token"])) {
            $this->csrf[$scope] = $response["data"]["csrf_token"];
        }
        return [$status, $response["data"] ?? $response];
    }
}
$checks = 0;
function verifyAccess(bool $passed, string $message): void
{
    global $checks;
    if (!$passed) {
        throw new RuntimeException("FAIL " . $message);
    }
    $checks++;
    echo "PASS " . $message . PHP_EOL;
}
function accessResponse(array $response, int $status, string $message): array
{
    verifyAccess(
        $response[0] === $status,
        $message . " (HTTP " . $response[0] . ")",
    );
    return $response[1];
}
$tag = "rbac_" . bin2hex(random_bytes(5));
$now = date("Y-m-d H:i:s");
$rootId =
    max(
        (int) Db::name("admins")->max("id"),
        (int) Db::name("users")->max("id"),
    ) + 10;
$adminIds = [$rootId];
$roleIds = [];
$resourceId = 0;
$userId = 0;
$root = new AccessBrowser();
$operator = new AccessBrowser();
$password = bin2hex(random_bytes(16));
$userPassword = bin2hex(random_bytes(16));
try {
    verifyAccess(
        AccessMigration::run()["already_applied"],
        "migration is repeatable",
    );
    verifyAccess(
        !Db::query("SHOW COLUMNS FROM ts_users LIKE 'role'"),
        "users no longer has an administrator role column",
    );
    Db::name("admins")->insert([
        "id" => $rootId,
        "username" => $tag . "_root",
        "display_name" => $tag,
        "password_hash" => password_hash($password, PASSWORD_DEFAULT),
        "is_super" => 1,
        "created_at" => $now,
    ]);
    $token = bin2hex(random_bytes(32));
    $csrf = bin2hex(random_bytes(24));
    Db::name("admin_sessions")->insert([
        "admin_id" => $rootId,
        "token_hash" => hash("sha256", $token),
        "csrf_token" => $csrf,
        "created_at" => $now,
        "expires_at" => date("Y-m-d H:i:s", time() + 600),
    ]);
    $root->cookies["starindex_admin"] = $token;
    $root->csrf["admin"] = $csrf;
    $catalog = accessResponse(
        $root->call("/admin/api/access/catalog"),
        200,
        "super administrator reads the permission catalog",
    );
    verifyAccess(
        isset($catalog["users"]["fields"]["telegram_id"]),
        "field catalog contains user Telegram ID",
    );
    accessResponse(
        $root->call("/admin/api/roles", [
            "name" => $tag,
            "permissions" => ["access.manage"],
            "fields" => [],
        ]),
        422,
        "super-only access cannot be delegated through a role",
    );
    accessResponse(
        $root->call("/admin/api/roles", [
            "name" => $tag,
            "permissions" => ["users.read"],
            "fields" => ["users" => ["password_hash" => "read"]],
        ]),
        422,
        "password hash cannot be granted",
    );
    $roleInput = [
        "name" => $tag,
        "description" => "HTTP fixture",
        "status" => 1,
        "permissions" => [
            "resources.read",
            "resources.update",
            "resources.archive",
            "users.read",
            "dashboard.read",
            "logs.read",
        ],
        "fields" => [
            "resources" => ["title" => "write", "status" => "read"],
            "users" => ["display_name" => "read"],
            "dashboard" => [
                "recent_resources" => "read",
                "activity" => "read",
                "bot" => "read",
            ],
            "logs" => ["action" => "read"],
        ],
    ];
    $role = accessResponse(
        $root->call("/admin/api/roles", $roleInput),
        200,
        "create a role with independent field read and write grants",
    );
    $roleIds[] = $role["id"];
    $adminInput = [
        "username" => $tag,
        "display_name" => "受限运营",
        "password" => $password,
        "status" => 1,
        "role_ids" => [(int) $role["id"]],
    ];
    $admin = accessResponse(
        $root->call("/admin/api/admins", $adminInput),
        200,
        "create an independent administrator",
    );
    $adminIds[] = $userId = (int) $admin["id"];
    Db::name("users")->insert([
        "id" => $userId,
        "username" => $tag,
        "display_name" => "普通用户",
        "telegram_id" => random_int(60000000000, 69999999999),
        "password_hash" => password_hash($userPassword, PASSWORD_DEFAULT),
        "created_at" => $now,
    ]);
    $login = accessResponse(
        $operator->call("/admin/api/login", [
            "username" => $tag,
            "password" => $password,
        ]),
        200,
        "administrator login uses admins",
    );
    verifyAccess(
        !isset($login["user"]["password_hash"]) && !$login["access"]["super"],
        "administrator response excludes password and does not elevate new accounts",
    );
    accessResponse(
        new AccessBrowser()->call("/user/api/login", [
            "username" => $tag,
            "password" => $password,
        ]),
        401,
        "administrator password cannot log into ordinary user account",
    );
    accessResponse(
        new AccessBrowser()->call("/admin/api/login", [
            "username" => $tag,
            "password" => $userPassword,
        ]),
        401,
        "ordinary user password cannot log into administrator account",
    );
    accessResponse(
        $operator->call("/user/api/login", [
            "username" => $tag,
            "password" => $userPassword,
        ]),
        200,
        "same-named ordinary user logs in separately",
    );
    $swapped = clone $operator;
    $swapped->cookies["starindex_admin"] = $operator->cookies["starindex_user"];
    accessResponse(
        $swapped->call("/admin/api/me"),
        401,
        "user cookie cannot be reused as administrator cookie",
    );
    $swapped->cookies["starindex_user"] = $operator->cookies["starindex_admin"];
    accessResponse(
        $swapped->call("/user/api/me"),
        401,
        "administrator cookie cannot be reused as user cookie",
    );
    accessResponse(
        $operator->call("/admin/api/admins"),
        403,
        "ordinary administrator cannot manage administrator accounts",
    );
    accessResponse(
        $operator->call("/admin/api/roles/" . $role["id"], $roleInput),
        403,
        "ordinary administrator cannot grant itself permissions",
    );
    foreach (
        [
            "settings",
            "bot",
            "bot-menu",
            "keys",
            "categories",
            "submissions",
            "lookups/categories",
        ]
        as $endpoint
    ) {
        accessResponse(
            $operator->call("/admin/api/" . $endpoint),
            403,
            "ungranted endpoint denied: " . $endpoint,
        );
    }
    $resource = ResourceService::save([
        "title" => $tag,
        "url" => "https://t.me/" . $tag,
        "description" => "hidden_description_" . $tag,
        "status" => "hidden",
    ]);
    $resourceId = $resource["id"];
    $list = accessResponse(
        $operator->call("/admin/api/resources?q=" . $tag),
        200,
        "read resources with granted fields",
    );
    $visibleKeys = array_keys($list["items"][0]);
    sort($visibleKeys, SORT_STRING); // JSON object member order is not significant.
    verifyAccess(
        $visibleKeys === ["id", "status", "title"],
        "resource response omits unauthorized fields",
    );
    $hiddenSearch = accessResponse(
        $operator->call("/admin/api/resources?q=hidden_description_" . $tag),
        200,
        "search considers only readable fields",
    );
    verifyAccess(
        $hiddenSearch["total"] === 0,
        "hidden description cannot be inferred by search",
    );
    accessResponse(
        $operator->call("/admin/api/resources?featured=1"),
        403,
        "hidden fields cannot be used as filters",
    );
    accessResponse(
        $operator->call("/admin/api/resources?sort=members"),
        403,
        "hidden fields cannot be used as sort keys",
    );
    accessResponse(
        $operator->call("/admin/api/resources/" . $resourceId, [
            "url" => "https://t.me/changed_fixture",
        ]),
        403,
        "forged field write rejected",
    );
    accessResponse(
        $operator->call("/admin/api/resources/" . $resourceId . "/archive", []),
        403,
        "archive action also requires status write permission",
    );
    $updated = accessResponse(
        $operator->call(
            "/admin/api/resources/" . $resourceId . "?status=published",
            ["title" => $tag . "_edited"],
        ),
        200,
        "partial update changes the permitted field",
    );
    $stored = Db::name("resources")->where("id", $resourceId)->find();
    verifyAccess(
        $stored["url"] === $resource["url"] &&
            $stored["status"] === "hidden" &&
            $stored["description"] === $resource["description"],
        "partial and query-string writes preserve protected fields",
    );
    verifyAccess(
        !array_intersect_key($updated, array_flip(["url", "description"])),
        "write response uses the same field projection",
    );
    $users = accessResponse(
        $operator->call("/admin/api/users"),
        200,
        "users list permission",
    );
    foreach ($users["items"] as $user) {
        verifyAccess(
            !array_intersect_key(
                $user,
                array_flip(["telegram_id", "username", "password_hash"]),
            ),
            "user record excludes protected fields",
        );
    }
    $hiddenUserSearch = accessResponse(
        $operator->call("/admin/api/users?q=" . $tag),
        200,
        "user search accepts queries against readable fields",
    );
    verifyAccess(
        $hiddenUserSearch["total"] === 0,
        "hidden usernames cannot be inferred through search",
    );
    $dashboard = accessResponse(
        $operator->call("/admin/api/dashboard"),
        200,
        "dashboard permission",
    );
    verifyAccess(
        !array_intersect_key($dashboard, array_flip(["bot", "stats"])),
        "dashboard cannot expose ungranted modules or fields",
    );
    foreach ($dashboard["recent_resources"] as $row) {
        verifyAccess(
            !array_intersect_key($row, array_flip(["url", "description"])),
            "dashboard resources also enforce field projection",
        );
    }
    foreach ($dashboard["activity"] as $row) {
        verifyAccess(
            !array_intersect_key($row, array_flip(["detail", "actor_id"])),
            "dashboard activity also enforces field projection",
        );
    }
    $badCsrf = clone $operator;
    $badCsrf->csrf["admin"] = $operator->csrf["user"];
    accessResponse(
        $badCsrf->call("/admin/api/resources/" . $resourceId, [
            "title" => $tag,
        ]),
        403,
        "CSRF tokens are isolated between account systems",
    );
    $extraInput = [
        "name" => $tag . "_extra",
        "status" => 1,
        "permissions" => ["resources.read"],
        "fields" => ["resources" => ["url" => "read"]],
    ];
    $extra = accessResponse(
        $root->call("/admin/api/roles", $extraInput),
        200,
        "create second role",
    );
    $roleIds[] = $extra["id"];
    $adminInput["password"] = "";
    $adminInput["role_ids"][] = (int) $extra["id"];
    accessResponse(
        $root->call("/admin/api/admins/" . $userId, $adminInput),
        200,
        "assign multiple roles",
    );
    $combined = accessResponse(
        $operator->call("/admin/api/resources?q=" . $tag),
        200,
        "existing session immediately uses new roles",
    );
    verifyAccess(
        isset($combined["items"][0]["title"], $combined["items"][0]["url"]),
        "multiple roles union their field grants",
    );
    accessResponse(
        $root->call(
            "/admin/api/roles/" . $extra["id"],
            array_replace($extraInput, ["status" => 0]),
        ),
        200,
        "disable a role",
    );
    $revoked = accessResponse(
        $operator->call("/admin/api/resources?q=" . $tag),
        200,
        "remaining active role still applies",
    );
    verifyAccess(
        !isset($revoked["items"][0]["url"]),
        "disabled role permissions disappear on next request",
    );
    accessResponse(
        $root->call(
            "/admin/api/roles/" . $role["id"],
            array_replace($roleInput, ["status" => 0]),
        ),
        200,
        "disable last active role",
    );
    accessResponse(
        $operator->call("/admin/api/resources"),
        403,
        "removing all active grants immediately denies existing sessions",
    );
    accessResponse(
        $operator->call("/admin/api/me"),
        200,
        "unassigned administrator retains only its own login profile",
    );
    accessResponse(
        $root->call("/admin/api/roles/" . $role["id"], $roleInput),
        200,
        "restore test role",
    );
    accessResponse(
        $root->call(
            "/admin/api/admins/" . $userId,
            $adminInput + ["is_super" => 1],
        ),
        422,
        "super flag cannot be mass assigned",
    );
    accessResponse(
        $root->call("/admin/api/admins/" . $rootId, [
            "username" => $tag . "_root",
            "display_name" => $tag,
            "password" => "",
            "status" => 0,
            "role_ids" => [],
        ]),
        422,
        "super administrator cannot be disabled",
    );
    accessResponse(
        $operator->call("/admin/api/logout", []),
        200,
        "administrator logout",
    );
    accessResponse(
        $operator->call("/user/api/me"),
        200,
        "administrator logout preserves same-ID ordinary user session",
    );
    accessResponse(
        $operator->call("/admin/api/login", [
            "username" => $tag,
            "password" => $password,
        ]),
        200,
        "administrator re-login",
    );
    accessResponse(
        $root->call(
            "/admin/api/admins/" . $userId,
            array_replace($adminInput, [
                "password" => bin2hex(random_bytes(16)),
            ]),
        ),
        200,
        "reset administrator password",
    );
    accessResponse(
        $operator->call("/admin/api/me"),
        401,
        "password reset revokes administrator sessions",
    );
    accessResponse(
        $operator->call("/user/api/me"),
        200,
        "password reset preserves ordinary user session",
    );
    accessResponse(
        $root->call(
            "/admin/api/admins/" . $userId,
            array_replace($adminInput, ["status" => 0]),
        ),
        200,
        "disable independent administrator",
    );
    verifyAccess(
        (int) Db::name("users")->where("id", $userId)->value("status") === 1,
        "disabling admin does not disable same-ID user",
    );
    $accountRows = accessResponse(
        $root->call("/admin/api/admins"),
        200,
        "administrator list",
    );
    verifyAccess(
        !array_filter($accountRows, fn($row) => isset($row["password_hash"])),
        "administrator list never returns password hashes",
    );
    echo "ALL $checks ACCESS CHECKS PASSED\n";
} finally {
    if ($resourceId) {
        Db::name("resources")->where("id", $resourceId)->delete();
    }
    if ($userId) {
        Db::name("sessions")->where("user_id", $userId)->delete();
        Db::name("users")
            ->where("id", $userId)
            ->where("username", $tag)
            ->delete();
    }
    Db::name("audit_logs")->whereIn("actor_id", $adminIds)->delete();
    Db::name("admins")->whereIn("id", $adminIds)->delete();
    if ($roleIds) {
        Db::name("roles")->whereIn("id", $roleIds)->delete();
    }
    echo "Access test fixtures removed.\n";
}
