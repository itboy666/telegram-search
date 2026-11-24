<?php
namespace app\admin\controller;
use app\common\http\Response;
use app\common\service\{
    Auth,
    ResourceService,
    SearchService,
    Settings,
    BotState,
    BotCredentials,
    BotSearchSettings,
    Rbac,
    TelegramLink,
    CatalogTransaction,
    ResourceLink,
    AdminRecords,
};
use app\common\exception\DomainError;
use think\facade\Db;
class Panel
{
    private function run(callable $fn, bool $write = false): \think\Response
    {
        return Response::run(function () use ($fn, $write) {
            $s = Auth::session(request(), "admin", $write);
            $action = strtolower(request()->action());
            if ($action === "me") {
                return $fn($s);
            }
            [$permission, $module, $shape] = Rbac::policy(
                $action,
                (bool) request()->route("id", 0),
            );
            Rbac::require($s["access"], $permission);
            if ($write) {
                $body = request()->post();
                if (
                    in_array(
                        $action,
                        [
                            "archiveresource",
                            "revokekey",
                            "userstatus",
                            "review",
                        ],
                        true,
                    )
                ) {
                    $body["status"] ??= null;
                }
                Rbac::checkWrite($s["access"], $module, $body);
                if (
                    $action === "createkey" &&
                    !Rbac::readable($s["access"], "keys", "token")
                ) {
                    throw new DomainError(
                        "创建密钥需要「创建时查看完整密钥」权限。",
                        403,
                    );
                }
            }
            return Rbac::output($s["access"], $module, $shape, $fn($s));
        });
    }
    public function login(): \think\Response
    {
        return Response::run(fn() => Auth::login(request(), "admin"));
    }
    public function logout(): \think\Response
    {
        return Response::run(fn() => Auth::logout(request(), "admin"));
    }
    public function me(): \think\Response
    {
        return $this->run(fn($s) => $s);
    }
    public function dashboard(): \think\Response
    {
        return $this->run(function () {
            $since = date("Y-m-d 00:00:00", strtotime("-6 days"));
            $rows = Db::name("search_logs")
                ->where("created_at", ">=", $since)
                ->fieldRaw("DATE(created_at) AS day, COUNT(*) AS total")
                ->group("day")
                ->select()
                ->toArray();
            $map = array_column($rows, "total", "day");
            $trend = [];
            for ($i = 6; $i >= 0; $i--) {
                $day = date("Y-m-d", strtotime("-" . $i . " days"));
                $trend[] = ["date" => $day, "count" => (int) ($map[$day] ?? 0)];
            }
            return [
                "stats" => [
                    "resources" => Db::name("resources")
                        ->where("status", "published")
                        ->count(),
                    "searches_today" => Db::name("search_logs")
                        ->where("created_at", ">=", date("Y-m-d 00:00:00"))
                        ->count(),
                    "pending" => Db::name("submissions")
                        ->where("status", "pending")
                        ->count(),
                    "users" => Db::name("users")->count(),
                ],
                "trend" => $trend,
                "recent_resources" => Db::name("resources")
                    ->where("status", "published")
                    ->order("id", "desc")
                    ->limit(5)
                    ->select()
                    ->toArray(),
                "popular" => Db::name("search_logs")
                    ->where("created_at", ">=", $since)
                    ->fieldRaw("query,COUNT(*) AS searches")
                    ->group("query")
                    ->order("searches", "desc")
                    ->limit(6)
                    ->select()
                    ->toArray(),
                "activity" => Db::name("audit_logs")
                    ->order("id", "desc")
                    ->limit(6)
                    ->select()
                    ->toArray(),
                "bot" => BotState::read(),
                "updated_at" => date("Y-m-d H:i:s"),
            ];
        });
    }
    public function resources(): \think\Response
    {
        return $this->run(
            fn($s) => new SearchService()->search(
                request()->param(),
                "admin",
                true,
                false,
                $s["access"]["fields"]["resources"]["read"] ?? [],
            ),
        );
    }
    public function saveResource(): \think\Response
    {
        return $this->run(function ($s) {
            return CatalogTransaction::run(function () use ($s) {
                $id = (int) request()->route("id", 0);
                $old = $id
                    ? Db::name("resources")->where("id", $id)->find()
                    : [];
                if ($id && !$old) {
                    throw new DomainError("资源不存在。", 404);
                }
                $url = ResourceLink::normalize(
                    (string) request()->post("url", $old["url"] ?? ""),
                    (string) request()->post("type", $old["type"] ?? "channel"),
                );
                TelegramLink::lock([
                    hash("sha256", $url),
                    ...$old ? [$old["url_hash"]] : [],
                ]);
                if ($id) {
                    $current = Db::name("resources")
                        ->where("id", $id)
                        ->lock(true)
                        ->find();
                    if (
                        !$current ||
                        $current["url_hash"] !== $old["url_hash"]
                    ) {
                        throw new DomainError(
                            "资源已更新，请刷新后重试。",
                            409,
                        );
                    }
                    $old = $current;
                }
                $input = array_replace($old, request()->post());
                if (
                    !$id &&
                    !Rbac::writable($s["access"], "resources", "status")
                ) {
                    $input["status"] = "hidden";
                }
                $r = ResourceService::save($input, $id ?: null);
                Settings::audit(
                    $id ? "编辑资源" : "新增资源",
                    $r["title"],
                    (int) $s["user"]["id"],
                );
                return $r;
            });
        }, true);
    }
    public function archiveResource(): \think\Response
    {
        return $this->run(function ($s) {
            return CatalogTransaction::run(function () use ($s) {
                $id = (int) request()->route("id");
                $r = AdminRecords::record("resources", $id, true);
                Db::name("resources")
                    ->where("id", $id)
                    ->update([
                        "status" => "archived",
                        "updated_at" => date("Y-m-d H:i:s"),
                    ]);
                Settings::audit(
                    "归档资源",
                    $r["title"],
                    (int) $s["user"]["id"],
                );
                return ["archived" => true];
            });
        }, true);
    }
    public function categories(): \think\Response
    {
        return $this->run(function ($s) {
            $query = Db::name("categories")
                ->alias("c")
                ->leftJoin(
                    ["ts_resources" => "r"],
                    'r.category_id=c.id AND r.status <> \'archived\'',
                )
                ->field("c.id,c.name,c.description,c.sort_order,c.created_at")
                ->fieldRaw("COUNT(r.id) AS resource_count")
                ->group("c.id,c.name,c.description,c.sort_order,c.created_at")
                ->order("c.sort_order,c.id");
            AdminRecords::filter(
                $query,
                request()->param(),
                $s["access"],
                "categories",
                "c",
            );
            return $query->select()->toArray();
        });
    }
    public function categoryOptions(): \think\Response
    {
        return Response::run(function () {
            $s = Auth::session(request(), "admin");
            if (
                !(
                    Rbac::can($s["access"], "resources.read") &&
                    Rbac::readable($s["access"], "resources", "category_id")
                ) &&
                !(
                    Rbac::can($s["access"], "submissions.read") &&
                    Rbac::readable($s["access"], "submissions", "category_id")
                )
            ) {
                throw new DomainError("没有读取资源分类选项的权限。", 403);
            }
            return Db::name("categories")
                ->field("id,name")
                ->order("sort_order,id")
                ->select()
                ->toArray();
        });
    }
    public function saveCategory(): \think\Response
    {
        return $this->run(function ($s) {
            return CatalogTransaction::run(function () use ($s) {
                $id = (int) request()->route("id", 0);
                $old = $id ? AdminRecords::record("categories", $id, true) : [];
                $input = array_replace($old, request()->post());
                $name = trim((string) ($input["name"] ?? ""));
                if ($name === "" || mb_strlen($name) > 80) {
                    throw new DomainError("分类名称须为 1–80 字。");
                }
                if (
                    Db::name("categories")
                        ->where("name", $name)
                        ->when($id, fn($q) => $q->where("id", "<>", $id))
                        ->count()
                ) {
                    throw new DomainError("分类名称已存在。");
                }
                $data = [
                    "name" => $name,
                    "description" => mb_substr(
                        (string) ($input["description"] ?? ""),
                        0,
                        255,
                    ),
                    "sort_order" => (int) ($input["sort_order"] ?? 0),
                ];
                if ($id) {
                    Db::name("categories")->where("id", $id)->update($data);
                } else {
                    $id = Db::name("categories")->insertGetId(
                        $data + ["created_at" => date("Y-m-d H:i:s")],
                    );
                }
                Settings::audit("保存分类", $name, (int) $s["user"]["id"]);
                return ["id" => $id];
            });
        }, true);
    }
    public function deleteCategory(): \think\Response
    {
        return $this->run(function ($s) {
            return CatalogTransaction::run(function () use ($s) {
                $id = (int) request()->route("id");
                AdminRecords::record("categories", $id, true);
                if (
                    Db::name("resources")->where("category_id", $id)->count() ||
                    Db::name("submissions")->where("category_id", $id)->count()
                ) {
                    throw new DomainError(
                        "这个分类仍有关联资源或申请，请先调整所属分类。",
                    );
                }
                Db::name("categories")->where("id", $id)->delete();
                Settings::audit(
                    "删除分类",
                    "分类 #" . $id,
                    (int) $s["user"]["id"],
                );
                return ["deleted" => true];
            });
        }, true);
    }
    public function submissions(): \think\Response
    {
        return $this->run(function ($s) {
            $q = Db::name("submissions")
                ->alias("s")
                ->leftJoin(["ts_users" => "u"], "u.id=s.user_id");
            AdminRecords::filter(
                $q,
                request()->param(),
                $s["access"],
                "submissions",
                "s",
            );
            if (request()->param("status")) {
                if (!Rbac::readable($s["access"], "submissions", "status")) {
                    throw new DomainError("无权按审核状态筛选。", 403);
                }
                $q->where("s.status", (string) request()->param("status"));
            }
            $total = (clone $q)->count();
            return [
                "items" => $q
                    ->field("s.*,u.display_name as submitter")
                    ->order("s.id", "desc")
                    ->page(max(1, (int) request()->param("page", 1)), 20)
                    ->select()
                    ->toArray(),
                "total" => $total,
            ];
        });
    }
    public function review(): \think\Response
    {
        return $this->run(function ($s) {
            return CatalogTransaction::run(function () use ($s) {
                $id = (int) request()->route("id");
                $row = Db::name("submissions")->where("id", $id)->find();
                if (!$row) {
                    throw new DomainError("申请不存在。", 404);
                }
                TelegramLink::lock([$row["url_hash"]]);
                $row = Db::name("submissions")
                    ->where("id", $id)
                    ->lock(true)
                    ->find();
                if (!$row) {
                    throw new DomainError("申请不存在。", 404);
                }
                if ($row["status"] !== "pending") {
                    throw new DomainError(
                        "这条申请已经审核，请刷新列表。",
                        409,
                    );
                }
                $status = (string) request()->post("status");
                if (!in_array($status, ["approved", "rejected"], true)) {
                    throw new DomainError("无效的审核操作。");
                }
                $data = [
                    "status" => $status,
                    "review_note" => mb_substr(
                        (string) request()->post(
                            "review_note",
                            $row["review_note"],
                        ),
                        0,
                        500,
                    ),
                    "reviewer_id" => $s["user"]["id"],
                    "reviewed_at" => date("Y-m-d H:i:s"),
                ];
                if ($status === "approved") {
                    $resource = ResourceService::save(
                        [
                            "title" => $row["title"],
                            "url" => $row["url"],
                            "type" => $row["type"],
                            "description" => $row["description"],
                            "category_id" => $row["category_id"],
                            "owner_id" => $row["user_id"],
                            "source" => "submission",
                        ],
                        null,
                        (int) $row["id"],
                    );
                    $data["resource_id"] = $resource["id"];
                }
                Db::name("submissions")->where("id", $id)->update($data);
                Settings::audit(
                    $status === "approved" ? "通过收录" : "拒绝收录",
                    $row["title"],
                    (int) $s["user"]["id"],
                );
                return $data;
            });
        }, true);
    }
    public function users(): \think\Response
    {
        return $this->run(function ($s) {
            $q = Db::name("users")->field(
                "id,username,display_name,telegram_id,status,submission_allowed,submission_note,created_at,last_login_at",
            );
            AdminRecords::filter($q, request()->param(), $s["access"], "users");
            $total = (clone $q)->count();
            return [
                "items" => $q
                    ->order("id", "desc")
                    ->page(max(1, (int) request()->param("page", 1)), 20)
                    ->select()
                    ->toArray(),
                "total" => $total,
            ];
        });
    }
    public function userStatus(): \think\Response
    {
        return $this->run(function ($s) {
            $id = (int) request()->route("id");
            $u = Db::name("users")->where("id", $id)->find();
            if (!$u) {
                throw new DomainError("用户不存在。", 404);
            }
            if (!in_array(request()->post("status"), [0, 1, "0", "1"], true)) {
                throw new DomainError("状态值无效。", 422);
            }
            $status = (int) request()->post("status");
            Db::name("users")
                ->where("id", $id)
                ->update(["status" => $status]);
            if (!$status) {
                Db::name("sessions")->where("user_id", $id)->delete();
            }
            Settings::audit(
                $status ? "启用用户" : "停用用户",
                $u["username"],
                (int) $s["user"]["id"],
            );
            return ["status" => $status];
        }, true);
    }
    public function userSubmissionPolicy(): \think\Response
    {
        return $this->run(function ($s) {
            $id = (int) request()->route("id");
            $input = request()->post();
            $data = [];
            if (array_key_exists("submission_allowed", $input)) {
                if (
                    !in_array(
                        $input["submission_allowed"],
                        [0, 1, "0", "1"],
                        true,
                    )
                ) {
                    throw new DomainError("请选择是否允许提交收录。");
                }
                $data["submission_allowed"] =
                    (int) $input["submission_allowed"];
            }
            if (array_key_exists("submission_note", $input)) {
                if (
                    !is_string($input["submission_note"]) ||
                    mb_strlen($input["submission_note"]) > 500
                ) {
                    throw new DomainError("投稿权限说明最多 500 字。", 422, [
                        "submission_note" => "请填写 500 字以内的说明",
                    ]);
                }
                $data["submission_note"] = trim($input["submission_note"]);
            }
            if (!$data) {
                throw new DomainError("请提供需要修改的投稿权限。");
            }
            return Db::transaction(function () use ($s, $id, $data) {
                $user = Db::name("users")->where("id", $id)->lock(true)->find();
                if (!$user) {
                    throw new DomainError("用户不存在。", 404);
                }
                Db::name("users")->where("id", $id)->update($data);
                Settings::audit(
                    "修改投稿权限",
                    "用户 #" .
                        $id .
                        " · " .
                        json_encode($data, JSON_UNESCAPED_UNICODE),
                    (int) $s["user"]["id"],
                );
                return array_replace($user, $data);
            });
        }, true);
    }
    public function settings(): \think\Response
    {
        return $this->run(fn() => Settings::all());
    }
    public function saveSettings(): \think\Response
    {
        return $this->run(function ($s) {
            $input = request()->post();
            $limits = [
                "site_name" => 40,
                "site_description" => 160,
                "public_api_enabled" => 1,
                "submissions_enabled" => 1,
            ];
            Db::transaction(function () use ($input, $limits) {
                foreach ($limits as $key => $limit) {
                    if (!isset($input[$key])) {
                        continue;
                    }
                    $value = trim((string) $input[$key]);
                    if (
                        mb_strlen($value) > $limit ||
                        ($key === "site_name" && $value === "")
                    ) {
                        throw new DomainError("请检查设置内容的长度。");
                    }
                    if (
                        str_ends_with($key, "_enabled") &&
                        !in_array($value, ["0", "1"], true)
                    ) {
                        throw new DomainError("开关值无效。");
                    }
                    Db::execute(
                        "INSERT INTO ts_settings (name,value) VALUES (?,?) ON DUPLICATE KEY UPDATE value=?",
                        [$key, $value, $value],
                    );
                }
            });
            Settings::audit(
                "更新系统设置",
                "站点与访问设置已保存",
                (int) $s["user"]["id"],
            );
            return Settings::all();
        }, true);
    }
    public function bot(): \think\Response
    {
        return $this->run(
            fn() => BotState::read() + [
                "mode" => "long_polling",
                "commands" => [
                    "/start",
                    "/discover",
                    "/search",
                    "/add",
                    "/account",
                    "/wallet",
                    "/help",
                    "/groupsettings",
                ],
                "page_size" => BotSearchSettings::pageSize(),
            ],
        );
    }
    public function botMenu(): \think\Response
    {
        return $this->run(fn() => \app\common\service\BotMenu::payload());
    }
    public function botSecurity(): \think\Response
    {
        return $this->run(
            fn() => \app\common\service\BotSecuritySettings::get(),
        );
    }
    public function saveBotSecurity(): \think\Response
    {
        return $this->run(
            fn($s) => \app\common\service\BotSecuritySettings::save(
                request()->post(),
                (int) $s["user"]["id"],
            ),
            true,
        );
    }
    public function botGroups(): \think\Response
    {
        return $this->run(function ($s) {
            $query = Db::name("bot_groups");
            $id = (string) request()->get("id", "");
            if ($id !== "") {
                $query->where("id", $id);
            }
            $q = trim((string) request()->get("q", ""));
            $fields = array_intersect(
                ["title", "chat_id"],
                $s["access"]["fields"]["bot_groups"]["read"] ?? [],
            );
            if ($q !== "" && $fields) {
                $query->where(function ($query) use ($fields, $q) {
                    foreach ($fields as $field) {
                        $query->whereOr(
                            $field,
                            "like",
                            "%" .
                                addcslashes(mb_substr($q, 0, 100), "%_\\") .
                                "%",
                        );
                    }
                });
            }
            $total = (clone $query)->count();
            $page = max(1, (int) request()->get("page", 1));
            return [
                "items" => $query
                    ->order("id", "desc")
                    ->page($page, 20)
                    ->select()
                    ->toArray(),
                "total" => $total,
                "page" => $page,
                "page_size" => 20,
            ];
        });
    }
    public function saveBotGroup(): \think\Response
    {
        return $this->run(
            fn($s) => \app\common\telegram\GroupGuard::save(
                (int) request()->route("id"),
                request()->post(),
                (int) $s["user"]["id"],
            ),
            true,
        );
    }
    public function saveBot(): \think\Response
    {
        return $this->run(function ($s) {
            $input = request()->post();
            $hasToken = array_key_exists("token", $input);
            $hasSize = array_key_exists("page_size", $input);
            if (!$hasToken && !$hasSize) {
                throw new DomainError("请填写需要保存的机器人配置。", 422);
            }
            if ($hasToken && !is_string($input["token"])) {
                throw new DomainError("请填写需要保存的 Bot Token。", 422, [
                    "token" => "请输入完整的 Bot Token。",
                ]);
            }
            if ($hasSize) {
                BotSearchSettings::validate($input["page_size"]);
            }
            Db::transaction(function () use ($input, $hasToken, $hasSize, $s) {
                $actor = (int) $s["user"]["id"];
                if ($hasToken) {
                    BotCredentials::save($input["token"], $actor);
                }
                if ($hasSize) {
                    BotSearchSettings::save($input["page_size"], $actor);
                }
            });
            return BotState::read() + [
                "page_size" => BotSearchSettings::pageSize(),
            ];
        }, true);
    }
    public function saveBotMenu(): \think\Response
    {
        return $this->run(
            fn($s) => \app\common\service\BotMenu::save(
                array_replace(
                    \app\common\service\BotMenu::get(),
                    request()->post(),
                ),
                (int) $s["user"]["id"],
            ),
            true,
        );
    }
    public function logs(): \think\Response
    {
        return $this->run(function ($s) {
            $query = Db::name("audit_logs");
            AdminRecords::filter(
                $query,
                request()->param(),
                $s["access"],
                "logs",
            );
            $total = (clone $query)->count();
            return [
                "total" => $total,
                "items" => $query
                    ->order("id", "desc")
                    ->page(max(1, (int) request()->param("page", 1)), 20)
                    ->select()
                    ->toArray(),
            ];
        });
    }
    public function keys(): \think\Response
    {
        return $this->run(function ($s) {
            $query = Db::name("api_keys")
                ->field(
                    "id,name,prefix,status,daily_limit,created_at,last_used_at",
                )
                ->order("id", "desc");
            AdminRecords::filter(
                $query,
                request()->param(),
                $s["access"],
                "keys",
            );
            return $query->select()->toArray();
        });
    }
    public function createKey(): \think\Response
    {
        return $this->run(function ($s) {
            $name = trim((string) request()->post("name", ""));
            if ($name === "" || mb_strlen($name) > 80) {
                throw new DomainError("请输入 1–80 字的密钥名称。");
            }
            $token = "si_" . bin2hex(random_bytes(24));
            $id = Db::name("api_keys")->insertGetId([
                "name" => $name,
                "token_hash" => hash("sha256", $token),
                "prefix" => substr($token, 0, 11),
                "daily_limit" => max(
                    1,
                    min(100000, (int) request()->post("daily_limit", 1000)),
                ),
                "created_at" => date("Y-m-d H:i:s"),
            ]);
            Settings::audit("创建 API 密钥", $name, (int) $s["user"]["id"]);
            return ["id" => $id, "token" => $token];
        }, true);
    }
    public function revokeKey(): \think\Response
    {
        return $this->run(function ($s) {
            $id = (int) request()->route("id");
            AdminRecords::record("api_keys", $id);
            Db::name("api_keys")
                ->where("id", $id)
                ->update(["status" => 0]);
            Settings::audit(
                "撤销 API 密钥",
                "密钥 #" . $id,
                (int) $s["user"]["id"],
            );
            return ["revoked" => true];
        }, true);
    }
    public function resource(): \think\Response
    {
        return $this->run(
            fn() => AdminRecords::record(
                "resources",
                (int) request()->route("id"),
            ),
        );
    }
    public function deleteResource(): \think\Response
    {
        return $this->run(
            fn($s) => AdminRecords::deleteResource(
                (int) request()->route("id"),
                (int) $s["user"]["id"],
            ),
            true,
        );
    }
    public function saveUser(): \think\Response
    {
        return $this->run(function ($s) {
            $id = (int) request()->route("id", 0) ?: null;
            $input = request()->post();
            if (!$id) {
                foreach (["status", "submission_allowed"] as $field) {
                    if (!Rbac::writable($s["access"], "users", $field)) {
                        $input[$field] = 0;
                    }
                }
            }
            return AdminRecords::saveUser($input, $id, (int) $s["user"]["id"]);
        }, true);
    }
    public function deleteUser(): \think\Response
    {
        return $this->run(
            fn($s) => AdminRecords::deleteUser(
                (int) request()->route("id"),
                (int) $s["user"]["id"],
            ),
            true,
        );
    }
    public function saveSubmission(): \think\Response
    {
        return $this->run(
            fn($s) => AdminRecords::saveSubmission(
                request()->post(),
                (int) request()->route("id", 0) ?: null,
                (int) $s["user"]["id"],
            ),
            true,
        );
    }
    public function deleteSubmission(): \think\Response
    {
        return $this->run(
            fn($s) => AdminRecords::deleteSubmission(
                (int) request()->route("id"),
                (int) $s["user"]["id"],
            ),
            true,
        );
    }
    public function updateKey(): \think\Response
    {
        return $this->run(
            fn($s) => AdminRecords::updateKey(
                request()->post(),
                (int) request()->route("id"),
                (int) $s["user"]["id"],
            ),
            true,
        );
    }
    public function deleteKey(): \think\Response
    {
        return $this->run(
            fn($s) => AdminRecords::deleteKey(
                (int) request()->route("id"),
                (int) $s["user"]["id"],
            ),
            true,
        );
    }
}
