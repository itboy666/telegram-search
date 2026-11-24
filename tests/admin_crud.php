<?php
/** Real HTTP CRUD, authorization and identity invariants. Only tagged fixtures are removed. */
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();

use think\facade\Db;

set_exception_handler(static function (Throwable $e): never {
    fwrite(STDERR, $e->getMessage() . "\n");
    exit(1);
});

class CrudClient
{
    public string $cookie = "";
    public string $csrf = "";
    public function call(
        string $path,
        ?array $body = null,
        ?string $method = null,
    ): array {
        $base = rtrim(getenv("STARINDEX_TEST_URL") ?: "http://127.0.0.1:8088", "/");
        $curl = curl_init($base . $path);
        curl_setopt_array($curl, [
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_PROXY => "",
            CURLOPT_TIMEOUT => 25,
            CURLOPT_COOKIE => $this->cookie,
            CURLOPT_HTTPHEADER => [
                "Content-Type: application/json",
                "Origin: " . $base,
                "X-CSRF-Token: " . $this->csrf,
            ],
            CURLOPT_CUSTOMREQUEST =>
                $method ?? ($body === null ? "GET" : "POST"),
        ]);
        if ($body !== null) {
            curl_setopt($curl, CURLOPT_POSTFIELDS, json_encode($body));
        }
        $raw = curl_exec($curl);
        $status = (int) curl_getinfo($curl, CURLINFO_HTTP_CODE);
        curl_close($curl);
        return [$status, json_decode((string) $raw, true) ?? []];
    }
}
$checks = 0;
function crudCheck(bool $ok, string $message): void
{
    global $checks;
    if (!$ok) {
        throw new RuntimeException("FAIL " . $message);
    }
    echo "PASS " . $message . "\n";
    $checks++;
}
function crudResponse(array $response, int $status, string $message): array
{
    crudCheck(
        $response[0] === $status,
        $message . " (HTTP " . $response[0] . ")",
    );
    return $response[1]["data"] ?? $response[1];
}
function crudSession(int $id): CrudClient
{
    $token = bin2hex(random_bytes(32));
    $csrf = bin2hex(random_bytes(24));
    Db::name("admin_sessions")->insert([
        "admin_id" => $id,
        "token_hash" => hash("sha256", $token),
        "csrf_token" => $csrf,
        "created_at" => date("Y-m-d H:i:s"),
        "expires_at" => date("Y-m-d H:i:s", time() + 900),
    ]);
    $client = new CrudClient();
    $client->cookie = "starindex_admin=" . $token;
    $client->csrf = $csrf;
    return $client;
}
$tag = "crud_" . bin2hex(random_bytes(4));
$ids = [
    "admins" => [],
    "roles" => [],
    "users" => [],
    "categories" => [],
    "resources" => [],
    "submissions" => [],
    "api_keys" => [],
];
$hashes = [];
$setting = Db::name("settings")
    ->where("name", "submissions_enabled")
    ->value("value");
$remember = function (string $table, array $row) use (&$ids): int {
    $id = (int) $row["id"];
    $ids[$table][] = $id;
    return $id;
};
$resourceBody = function (string $suffix) use ($tag, &$hashes): array {
    $url = "https://t.me/" . $tag . $suffix;
    $hashes[] = hash("sha256", $url);
    return [
        "title" => $tag . $suffix,
        "url" => $url,
        "type" => "channel",
        "description" => "Temporary CRUD fixture",
    ];
};
try {
    Db::name("settings")
        ->where("name", "submissions_enabled")
        ->update(["value" => "1"]);
    foreach ([1, 0] as $super) {
        $ids["admins"][] = (int) Db::name("admins")->insertGetId([
            "username" => $tag . "_" . $super,
            "display_name" => "CRUD fixture",
            "is_super" => $super,
            "created_at" => date("Y-m-d H:i:s"),
        ]);
    }
    $root = crudSession($ids["admins"][0]);
    $limited = crudSession($ids["admins"][1]);
    $anonymous = new CrudClient();
    crudResponse(
        $anonymous->call("/admin/api/resources"),
        401,
        "unauthenticated list denied",
    );
    $noCsrf = clone $root;
    $noCsrf->csrf = "";
    foreach (
        ["resources", "users", "submissions", "keys", "admins", "roles"]
        as $module
    ) {
        crudResponse(
            $noCsrf->call("/admin/api/$module/999999999", [], "DELETE"),
            403,
            "$module deletion requires CSRF",
        );
        crudResponse(
            $limited->call("/admin/api/$module/999999999", [], "DELETE"),
            403,
            "$module deletion requires permission",
        );
    }

    $role = $remember(
        "roles",
        crudResponse(
            $root->call("/admin/api/roles", [
                "name" => $tag,
                "description" => "CRUD scope",
                "status" => 1,
                "permissions" => [
                    "resources.read",
                    "resources.update",
                    "users.read",
                    "users.update",
                    "submissions.read",
                    "submissions.update",
                ],
                "fields" => [
                    "resources" => ["title" => "write"],
                    "users" => ["display_name" => "write"],
                    "submissions" => [
                        "title" => "write",
                        "category_id" => "read",
                    ],
                ],
            ]),
            200,
            "create role with field grants",
        ),
    );
    Db::name("admin_roles")->insert([
        "admin_id" => $ids["admins"][1],
        "role_id" => $role,
    ]);
    crudResponse(
        $limited->call("/admin/api/lookups/categories"),
        200,
        "submission category lookup does not require resource category access",
    );
    crudResponse(
        $root->call("/admin/api/roles/$role", [], "DELETE"),
        409,
        "assigned role cannot be deleted",
    );
    crudResponse(
        $root->call("/admin/api/admins/" . $ids["admins"][0], [], "DELETE"),
        409,
        "super/current admin cannot be deleted",
    );

    $category = $remember(
        "categories",
        crudResponse(
            $root->call("/admin/api/categories", [
                "name" => $tag,
                "description" => "Before",
                "sort_order" => 99,
            ]),
            200,
            "create category",
        ),
    );
    crudResponse(
        $root->call("/admin/api/categories/$category", [
            "description" => "After",
        ]),
        200,
        "partial category update",
    );
    $categories = crudResponse(
        $root->call("/admin/api/categories?id=$category"),
        200,
        "category ID filter",
    );
    crudCheck(
        count($categories) === 1 &&
            (int) $categories[0]["id"] === $category &&
            $categories[0]["description"] === "After",
        "category ID and edit persist",
    );

    $password = bin2hex(random_bytes(16));
    $user = $remember(
        "users",
        crudResponse(
            $root->call("/admin/api/users", [
                "username" => $tag,
                "display_name" => "测试用户",
                "password" => $password,
            ]),
            200,
            "create website user",
        ),
    );
    crudCheck(
        password_verify(
            $password,
            Db::name("users")->where("id", $user)->value("password_hash"),
        ),
        "user password stored as a hash",
    );
    crudResponse(
        $root->call("/admin/api/users", [
            "username" => $tag,
            "display_name" => "Duplicate",
            "password" => $password,
        ]),
        422,
        "duplicate user rejected",
    );
    crudResponse(
        $root->call("/admin/api/users", [
            "username" => $tag . "x",
            "display_name" => "Invalid",
            "password" => "short",
        ]),
        422,
        "short password rejected",
    );
    crudResponse(
        $root->call("/admin/api/users/$user", ["display_name" => "已更新昵称"]),
        200,
        "edit user nickname",
    );
    $users = crudResponse(
        $root->call("/admin/api/users?id=$user"),
        200,
        "user ID filter",
    );
    crudCheck(
        $users["total"] === 1 &&
            $users["items"][0]["display_name"] === "已更新昵称" &&
            !isset($users["items"][0]["password_hash"]) &&
            !isset($users["items"][0]["password"]),
        "user response contains ID and no credentials",
    );
    $userSearch = crudResponse(
        $root->call("/admin/api/users?q=" . rawurlencode("已更新昵称")),
        200,
        "nickname search supported",
    );
    crudCheck(
        in_array($user, array_column($userSearch["items"], "id")),
        "nickname search finds updated user",
    );
    crudResponse(
        $limited->call("/admin/api/users/$user", ["status" => 0]),
        403,
        "user field authorization enforced",
    );
    $maskedUser = crudResponse(
        $limited->call("/admin/api/users?id=$user"),
        200,
        "limited user list",
    );
    $visibleUserKeys = array_keys($maskedUser["items"][0]);
    sort($visibleUserKeys, SORT_STRING); // JSON object member order is not significant.
    crudCheck(
        $visibleUserKeys === ["display_name", "id"],
        "user hidden fields are removed",
    );
    crudResponse(
        $limited->call("/admin/api/users?q=$tag"),
        200,
        "search limited to granted fields",
    );
    Db::name("sessions")->insert([
        "user_id" => $user,
        "token_hash" => hash("sha256", random_bytes(32)),
        "csrf_token" => bin2hex(random_bytes(24)),
        "scope" => "user",
        "expires_at" => date("Y-m-d H:i:s", time() + 600),
        "created_at" => date("Y-m-d H:i:s"),
    ]);
    $resetPassword = bin2hex(random_bytes(16));
    crudResponse(
        $root->call("/admin/api/users/$user", ["password" => $resetPassword]),
        200,
        "reset user password",
    );
    crudCheck(
        !Db::name("sessions")->where("user_id", $user)->count() &&
            password_verify(
                $resetPassword,
                Db::name("users")->where("id", $user)->value("password_hash"),
            ),
        "password reset revokes old sessions",
    );
    crudResponse(
        $root->call("/admin/api/users/$user", [
            "submission_allowed" => 0,
            "submission_note" => "fixture restriction",
        ]),
        200,
        "edit submission restrictions",
    );
    crudResponse(
        $root->call("/admin/api/users/$user", [], "DELETE"),
        409,
        "restricted users retained for enforcement",
    );
    crudResponse(
        $root->call("/admin/api/users/$user", ["submission_allowed" => 1]),
        200,
        "restore submission permission",
    );
    $telegram = (int) Db::name("users")->insertGetId([
        "username" => "tg_" . $tag,
        "display_name" => "Telegram fixture",
        "telegram_id" => random_int(8000000000000, 9000000000000),
        "created_at" => date("Y-m-d H:i:s"),
    ]);
    $ids["users"][] = $telegram;
    crudResponse(
        $root->call("/admin/api/users/$telegram", ["username" => $tag . "new"]),
        422,
        "Telegram identity cannot be renamed",
    );
    crudResponse(
        $root->call("/admin/api/users/$telegram", ["password" => $password]),
        422,
        "Telegram identity cannot gain a local password",
    );
    crudResponse(
        $root->call("/admin/api/users/$telegram", [
            "display_name" => "昵称可管理",
        ]),
        200,
        "Telegram nickname remains editable",
    );

    $body = $resourceBody("_resource") + ["category_id" => $category];
    $resource = $remember(
        "resources",
        crudResponse(
            $root->call("/admin/api/resources", $body),
            200,
            "create resource",
        ),
    );
    crudResponse(
        $root->call("/admin/api/resources", $body),
        422,
        "duplicate link rejected",
    );
    crudResponse(
        $root->call("/admin/api/categories/$category", [], "DELETE"),
        422,
        "referenced category cannot be deleted",
    );
    $detail = crudResponse(
        $root->call("/admin/api/resources/$resource"),
        200,
        "resource detail",
    );
    crudCheck(
        (int) $detail["id"] === $resource && $detail["url"] === $body["url"],
        "detail uses database ID",
    );
    $masked = crudResponse(
        $limited->call("/admin/api/resources/$resource"),
        200,
        "limited resource detail",
    );
    $visibleResourceKeys = array_keys($masked);
    sort($visibleResourceKeys, SORT_STRING);
    crudCheck(
        $visibleResourceKeys === ["id", "title"],
        "detail field mask excludes hidden URL",
    );
    crudResponse(
        $limited->call("/admin/api/resources/$resource", [
            "url" => $body["url"],
        ]),
        403,
        "resource hidden field write denied",
    );
    crudResponse(
        $limited->call("/admin/api/resources/$resource", [
            "title" => $tag . " updated",
        ]),
        200,
        "authorized partial resource update",
    );
    $list = crudResponse(
        $root->call("/admin/api/resources?id=$resource"),
        200,
        "resource ID filter",
    );
    crudCheck(
        $list["total"] === 1 && (int) $list["items"][0]["id"] === $resource,
        "resource ID lookup is exact",
    );
    crudResponse(
        $root->call("/admin/api/resources?id=-1"),
        422,
        "invalid database ID rejected",
    );
    crudResponse(
        $root->call("/admin/api/users?id=abc"),
        422,
        "invalid user ID rejected",
    );
    $deep = crudResponse(
        $root->call("/admin/api/resources?page=101&page_size=12"),
        200,
        "resource pagination beyond first 1200",
    );
    crudCheck($deep["page"] === 101, "admin page is no longer clamped to 100");

    $websiteUrl = "https://example.com/" . $tag;
    $hashes[] = hash("sha256", $websiteUrl);
    $website = $remember(
        "resources",
        crudResponse(
            $root->call("/admin/api/resources", [
                "title" => $tag . " website",
                "type" => "website",
                "url" => $websiteUrl,
            ]),
            200,
            "website resource accepted by admin controller",
        ),
    );
    crudResponse(
        $root->call("/admin/api/resources/$website", [
            "description" => "Website edited",
        ]),
        200,
        "website partial edit retains URL type",
    );
    $urlSearch = crudResponse(
        $root->call("/admin/api/resources?q=" . rawurlencode($websiteUrl)),
        200,
        "admin URL search supported",
    );
    crudCheck(
        $urlSearch["total"] === 1 &&
            (int) $urlSearch["items"][0]["id"] === $website,
        "URL search finds website",
    );
    foreach (["blocked", "invalid"] as $status) {
        crudResponse(
            $root->call("/admin/api/resources/$website", ["status" => $status]),
            200,
            "$status resource state set",
        );
        crudResponse(
            $root->call("/admin/api/resources/$website", [], "DELETE"),
            409,
            "$status resource cannot bypass link restrictions by deletion",
        );
    }
    crudResponse(
        $root->call("/admin/api/resources/$website", ["status" => "hidden"]),
        200,
        "restore resource state",
    );
    Db::name("favorites")->insert([
        "user_id" => $user,
        "resource_id" => $website,
        "created_at" => date("Y-m-d H:i:s"),
    ]);
    crudResponse(
        $root->call("/admin/api/resources/$website", [], "DELETE"),
        200,
        "delete website resource",
    );
    crudCheck(
        !Db::name("favorites")->where("resource_id", $website)->count(),
        "resource deletion clears favorites",
    );
    crudResponse(
        $root->call("/admin/api/resources/$website"),
        404,
        "deleted resource detail returns 404",
    );

    $submissionBody = $resourceBody("_submission") + [
        "user_id" => $user,
        "category_id" => $category,
    ];
    $submission = $remember(
        "submissions",
        crudResponse(
            $root->call("/admin/api/submissions", $submissionBody),
            200,
            "create pending submission",
        ),
    );
    crudResponse(
        $root->call("/admin/api/users/$user", [], "DELETE"),
        409,
        "user with submission history cannot be deleted",
    );
    crudResponse(
        $root->call("/admin/api/submissions", $submissionBody),
        422,
        "duplicate submission rejected",
    );
    crudResponse(
        $root->call("/admin/api/submissions/$submission", [
            "title" => "待审标题已修改",
        ]),
        200,
        "edit pending submission",
    );
    crudResponse(
        $root->call("/admin/api/submissions/$submission", [
            "url" => $body["url"],
        ]),
        422,
        "submission edit cannot duplicate existing resource",
    );
    crudResponse(
        $root->call("/admin/api/submissions/$submission", [
            "user_id" => $telegram,
        ]),
        422,
        "submission author cannot be changed",
    );
    crudResponse(
        $root->call("/admin/api/submissions/$submission", [
            "status" => "approved",
        ]),
        422,
        "edit endpoint cannot bypass review workflow",
    );
    crudResponse(
        $limited->call("/admin/api/submissions/$submission/review", [
            "status" => "approved",
        ]),
        403,
        "submission update grant does not grant review",
    );
    $pendingList = crudResponse(
        $root->call("/admin/api/submissions?id=$submission"),
        200,
        "submission ID filter",
    );
    crudCheck(
        $pendingList["total"] === 1 &&
            $pendingList["items"][0]["title"] === "待审标题已修改",
        "submission edits retain database identity",
    );
    crudResponse(
        $root->call("/admin/api/submissions/$submission", [], "DELETE"),
        200,
        "delete pending submission",
    );
    crudResponse(
        $root->call("/admin/api/submissions/$submission", [], "DELETE"),
        404,
        "missing submission returns 404",
    );

    $reviewed = $remember(
        "submissions",
        crudResponse(
            $root->call("/admin/api/submissions", $submissionBody),
            200,
            "resubmit deleted pending link",
        ),
    );
    $approval = crudResponse(
        $root->call("/admin/api/submissions/$reviewed/review", [
            "status" => "approved",
            "review_note" => "fixture approval",
        ]),
        200,
        "approve submission via review endpoint",
    );
    $ids["resources"][] = (int) $approval["resource_id"];
    crudResponse(
        $root->call("/admin/api/submissions/$reviewed", [], "DELETE"),
        409,
        "approved review history cannot be deleted",
    );
    crudResponse(
        $root->call("/admin/api/submissions/$reviewed", [
            "title" => "not allowed",
        ]),
        409,
        "approved review history cannot be edited",
    );
    crudResponse(
        $root->call(
            "/admin/api/resources/" . $approval["resource_id"],
            [],
            "DELETE",
        ),
        409,
        "resource with review history cannot be deleted",
    );
    crudResponse(
        $root->call("/admin/api/resources/$resource", [], "DELETE"),
        200,
        "delete unreferenced resource",
    );
    crudResponse(
        $root->call("/admin/api/resources/$resource", [], "DELETE"),
        404,
        "missing resource deletion returns 404",
    );

    $key = $remember(
        "api_keys",
        crudResponse(
            $root->call("/admin/api/keys", [
                "name" => $tag,
                "daily_limit" => 100,
            ]),
            200,
            "create API key",
        ),
    );
    $updatedKey = crudResponse(
        $root->call("/admin/api/keys/$key", [
            "name" => $tag . " edited",
            "daily_limit" => 123,
        ]),
        200,
        "edit API key",
    );
    crudCheck(
        $updatedKey["daily_limit"] === 123 &&
            !isset($updatedKey["token"]) &&
            !isset($updatedKey["token_hash"]),
        "key metadata edit never returns credentials",
    );
    crudResponse(
        $root->call("/admin/api/keys/$key", ["daily_limit" => 0]),
        422,
        "key quota validated",
    );
    $keyList = crudResponse(
        $root->call("/admin/api/keys?id=$key"),
        200,
        "key ID filter",
    );
    crudCheck(
        count($keyList) === 1 && (int) $keyList[0]["id"] === $key,
        "key ID is persistent",
    );
    crudResponse(
        $root->call("/admin/api/keys/$key", [], "DELETE"),
        409,
        "active API key requires revocation",
    );
    crudResponse(
        $root->call("/admin/api/keys/$key/revoke", []),
        200,
        "revoke API key",
    );
    crudResponse(
        $root->call("/admin/api/keys/$key", [], "DELETE"),
        200,
        "delete revoked API key",
    );

    $newAdmin = $remember(
        "admins",
        crudResponse(
            $root->call("/admin/api/admins", [
                "username" => $tag . "_managed",
                "display_name" => "Managed fixture",
                "password" => $password,
                "status" => 1,
                "role_ids" => [$role],
            ]),
            200,
            "create administrator",
        ),
    );
    $managed = crudSession($newAdmin);
    crudResponse(
        $root->call("/admin/api/admins/$newAdmin", [
            "username" => $tag . "_managed",
            "display_name" => "Edited fixture",
            "password" => "",
            "status" => 1,
            "role_ids" => [$role],
        ]),
        200,
        "edit administrator",
    );
    $admins = crudResponse(
        $root->call("/admin/api/admins"),
        200,
        "administrator IDs listed",
    );
    crudCheck(
        in_array($newAdmin, array_column($admins, "id")),
        "administrator list includes real ID",
    );
    crudResponse(
        $root->call("/admin/api/admins/$newAdmin", [], "DELETE"),
        200,
        "delete administrator",
    );
    crudResponse(
        $managed->call("/admin/api/resources"),
        401,
        "deleted administrator sessions revoked",
    );
    crudCheck(
        !Db::name("admin_roles")->where("admin_id", $newAdmin)->count(),
        "deleted administrator roles cleared",
    );
    Db::name("admin_roles")->where("admin_id", $ids["admins"][1])->delete();
    crudResponse(
        $root->call("/admin/api/roles/$role", [
            "name" => $tag,
            "description" => "Updated role",
            "status" => 1,
            "permissions" => ["resources.read"],
            "fields" => ["resources" => ["title" => "read"]],
        ]),
        200,
        "edit role permissions",
    );
    crudResponse(
        $root->call("/admin/api/roles/$role", [], "DELETE"),
        200,
        "delete unassigned role",
    );
    crudCheck(
        !Db::name("role_fields")->where("role_id", $role)->count() &&
            !Db::name("role_permissions")->where("role_id", $role)->count(),
        "role grants cascade after deletion",
    );

    $logs = crudResponse(
        $root->call("/admin/api/logs"),
        200,
        "audit logs paginated",
    );
    crudCheck(
        count($logs["items"]) <= 20 && $logs["total"] > 20,
        "audit list uses total and page size",
    );
    $logId = (int) Db::name("audit_logs")
        ->where("actor_id", $ids["admins"][0])
        ->max("id");
    $log = crudResponse(
        $root->call("/admin/api/logs?id=$logId"),
        200,
        "audit ID filter",
    );
    crudCheck(
        $log["total"] === 1 && (int) $log["items"][0]["id"] === $logId,
        "audit ID is real database key",
    );
    crudCheck(
        $root->call("/admin/api/logs/$logId", [], "DELETE")[0] >= 400,
        "audit records have no deletion endpoint",
    );

    // Remove only this run's reviewed history to exercise the remaining final deletions.
    Db::name("submissions")->whereIn("id", $ids["submissions"])->delete();
    Db::name("resources")->whereIn("id", $ids["resources"])->delete();
    crudResponse(
        $root->call("/admin/api/categories/$category", [], "DELETE"),
        200,
        "delete unreferenced category",
    );
    crudResponse(
        $root->call("/admin/api/categories/$category", [], "DELETE"),
        404,
        "missing category deletion returns 404",
    );
    crudResponse(
        $root->call("/admin/api/users/$user", [], "DELETE"),
        200,
        "delete unreferenced user",
    );
    crudResponse(
        $root->call("/admin/api/users/$user", [], "DELETE"),
        404,
        "missing user deletion returns 404",
    );
    crudResponse(
        $root->call("/admin/api/users/$telegram", [], "DELETE"),
        200,
        "delete unreferenced Telegram visitor",
    );
} finally {
    Db::name("settings")
        ->where("name", "submissions_enabled")
        ->update(["value" => $setting]);
    foreach (
        ["resources" => "resource_id", "users" => "user_id"]
        as $table => $field
    ) {
        if ($ids[$table]) {
            Db::name("favorites")->whereIn($field, $ids[$table])->delete();
        }
    }
    if ($ids["users"]) {
        Db::name("sessions")->whereIn("user_id", $ids["users"])->delete();
        Db::name("rate_limits")
            ->whereIn(
                "bucket",
                array_map(fn($id) => "submission:" . $id, $ids["users"]),
            )
            ->delete();
    }
    if ($ids["admins"]) {
        Db::name("audit_logs")->whereIn("actor_id", $ids["admins"])->delete();
        Db::name("admin_sessions")
            ->whereIn("admin_id", $ids["admins"])
            ->delete();
    }
    foreach (
        [
            "submissions",
            "resources",
            "categories",
            "users",
            "api_keys",
            "admins",
            "roles",
        ]
        as $table
    ) {
        if ($ids[$table]) {
            Db::name($table)
                ->whereIn("id", array_unique($ids[$table]))
                ->delete();
        }
    }
    if ($hashes) {
        Db::name("link_locks")->whereIn("url_hash", $hashes)->delete();
    }
}
echo "Admin CRUD: {$checks} checks passed; fixtures removed.\n";
