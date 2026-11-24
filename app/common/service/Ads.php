<?php
namespace app\common\service;

use app\common\exception\DomainError;
use app\common\telegram\{Client, Transport, TelegramError};
use think\facade\Db;

class Ads
{
    public const KINDS = [
        "keyword" => "关键词排名",
        "brand" => "品牌展示",
        "search_top" => "搜索顶部轮播",
        "search_bottom" => "搜索底部按钮",
        "group_pin" => "群组置顶",
        "channel" => "频道广告",
    ];
    public const DESCRIPTIONS = [
        "keyword" => "精确匹配关键词，在自然搜索结果上方展示推广链接。",
        "brand" => "按品牌词展示品牌介绍与官网链接，明确标注广告。",
        "search_top" => "搜索结果顶部展示一条广告，多条在每次搜索时轮换。",
        "search_bottom" => "在搜索结果底部展示推广按钮，保留原有分页。",
        "group_pin" => "审核后由机器人发送并置顶，需机器人具有群管理权限。",
        "channel" => "审核后在指定频道发布一条广告，到期不重复发布。",
    ];

    public static function saveSlot(?int $id, array $input, int $actor): array
    {
        return CatalogTransaction::run(function () use ($id, $input, $actor) {
            $old = $id
                ? Db::name("ad_slots")->where("id", $id)->lock(true)->find()
                : null;
            if ($id && !$old) {
                throw new DomainError("广告位不存在。", 404);
            }
            $data = array_replace(
                $old ?? [
                    "name" => "",
                    "kind" => "keyword",
                    "description" => "",
                    "price" => 10000000,
                    "duration_days" => 7,
                    "capacity" => 1,
                    "target_chat" => "",
                    "enabled" => 0,
                ],
                array_intersect_key(
                    $input,
                    array_flip([
                        "name",
                        "kind",
                        "description",
                        "duration_days",
                        "capacity",
                        "target_chat",
                        "enabled",
                    ]),
                ),
            );
            if (isset($input["price"])) {
                $data["price"] = Money::parse($input["price"]);
            }
            $data["name"] = trim((string) $data["name"]);
            if (
                $data["name"] === "" ||
                mb_strlen($data["name"]) > 80 ||
                !isset(self::KINDS[$data["kind"]])
            ) {
                throw new DomainError("请填写广告位名称并选择有效类型。");
            }
            if (mb_strlen((string) $data["description"]) > 500) {
                throw new DomainError("广告位说明最多 500 字。");
            }
            foreach (
                ["duration_days" => 365, "capacity" => 50]
                as $key => $max
            ) {
                $n = filter_var($data[$key], FILTER_VALIDATE_INT);
                if (!$n || $n < 1 || $n > $max) {
                    throw new DomainError("$key 超出范围（1–$max）。");
                }
                $data[$key] = $n;
            }
            if ($data["kind"] === "group_pin" && $data["capacity"] !== 1) {
                throw new DomainError("群置顶首版为独占投放，容量请设置为 1。");
            }
            $data["target_chat"] = trim((string) $data["target_chat"]);
            $data["enabled"] = (int) filter_var(
                $data["enabled"],
                FILTER_VALIDATE_BOOL,
            );
            if (
                in_array($data["kind"], ["group_pin", "channel"], true) &&
                $data["enabled"] &&
                !preg_match(
                    '/^(-[1-9]\d{4,19}|@[a-zA-Z0-9_]{5,32})$/D',
                    $data["target_chat"],
                )
            ) {
                throw new DomainError(
                    "群组或频道广告位需填写群 ID 或 @频道名。",
                );
            }
            if (
                $old &&
                ($data["kind"] !== $old["kind"] ||
                    $data["target_chat"] !== $old["target_chat"]) &&
                Db::name("ad_orders")
                    ->where("slot_id", $id)
                    ->whereIn("status", [
                        "pending",
                        "dispatching",
                        "active",
                        "delivery_issue",
                    ])
                    ->count()
            ) {
                throw new DomainError(
                    "广告位仍有订单，不能修改类型或目标聊天。请新建广告位。",
                    409,
                );
            }
            unset($data["id"]);
            $data["updated_at"] = date("Y-m-d H:i:s");
            if ($id) {
                Db::name("ad_slots")->where("id", $id)->update($data);
            } else {
                $data["created_at"] = date("Y-m-d H:i:s");
                $id = Db::name("ad_slots")->insertGetId($data);
            }
            Settings::audit("保存广告位", "广告位 #$id", $actor);
            return Money::row(Db::name("ad_slots")->where("id", $id)->find());
        });
    }

    public static function deleteSlot(int $id, int $actor): array
    {
        return CatalogTransaction::run(function () use ($id, $actor) {
            $row = Db::name("ad_slots")->where("id", $id)->lock(true)->find();
            if (!$row) {
                throw new DomainError("广告位不存在。", 404);
            }
            if (Db::name("ad_orders")->where("slot_id", $id)->count()) {
                throw new DomainError(
                    "广告位已有订单，请停用以保留历史。",
                    409,
                );
            }
            Db::name("ad_slots")->where("id", $id)->delete();
            Settings::audit("删除空广告位", "广告位 #$id", $actor);
            return ["deleted" => true];
        });
    }

    public static function buy(int $user, array $input): array
    {
        $key = Payments::key($input["request_key"] ?? "");
        $title = trim((string) ($input["title"] ?? ""));
        $url = trim((string) ($input["url"] ?? ""));
        $keyword = mb_strtolower(trim((string) ($input["keyword"] ?? "")));
        $content = trim((string) ($input["content"] ?? ""));
        if (
            $title === "" ||
            mb_strlen($title) > 60 ||
            preg_match('/[\r\n\p{Cc}]/u', $title)
        ) {
            throw new DomainError("广告标题须为 1–60 字单行文本。");
        }
        if (
            !filter_var($url, FILTER_VALIDATE_URL) ||
            parse_url($url, PHP_URL_SCHEME) !== "https" ||
            parse_url($url, PHP_URL_USER) ||
            strlen($url) > 500 ||
            preg_match("/[\s\p{Cc}]/u", $url)
        ) {
            throw new DomainError("请输入完整的 HTTPS 广告链接。");
        }
        if (mb_strlen($content) > 500 || mb_strlen($keyword) > 80) {
            throw new DomainError("广告正文最多 500 字，关键词最多 80 字。");
        }
        return CatalogTransaction::run(function () use (
            $user,
            $key,
            $input,
            $title,
            $url,
            $keyword,
            $content,
        ) {
            $slot = Db::name("ad_slots")
                ->where("id", (int) ($input["slot_id"] ?? 0))
                ->lock(true)
                ->find();
            Wallet::activeUser($user);
            $old = Db::name("ad_orders")
                ->where("user_id", $user)
                ->where("request_key", $key)
                ->find();
            if ($old) {
                return Money::row($old);
            }
            if (!$slot || !$slot["enabled"]) {
                throw new DomainError("广告位暂未开放购买。", 409);
            }
            if (
                isset($input["quoted_price"]) &&
                Money::parse($input["quoted_price"]) !== (int) $slot["price"]
            ) {
                throw new DomainError(
                    "广告位价格已调整，请刷新后重新确认。",
                    409,
                );
            }
            if (
                isset($input["quoted_days"]) &&
                (int) $input["quoted_days"] !== (int) $slot["duration_days"]
            ) {
                throw new DomainError(
                    "投放时长已调整，请刷新后重新确认。",
                    409,
                );
            }
            if (
                in_array($slot["kind"], ["keyword", "brand"], true) &&
                $keyword === ""
            ) {
                throw new DomainError("请填写需要精确匹配的关键词或品牌词。");
            }
            $busy = Db::name("ad_orders")
                ->where("slot_id", $slot["id"])
                ->where(function ($q) {
                    $q->whereIn("status", [
                        "pending",
                        "dispatching",
                        "delivery_issue",
                    ])->whereOr(function ($q) {
                        $q->where("status", "active")->where(
                            "ends_at",
                            ">",
                            date("Y-m-d H:i:s"),
                        );
                    });
                });
            if (in_array($slot["kind"], ["keyword", "brand"], true)) {
                $busy->where("keyword", $keyword);
            }
            if ($busy->count() >= (int) $slot["capacity"]) {
                throw new DomainError("此广告位的名额已满，请稍后购买。", 409);
            }
            $row = [
                "user_id" => $user,
                "slot_id" => $slot["id"],
                "request_key" => $key,
                "slot_name" => $slot["name"],
                "kind" => $slot["kind"],
                "price" => $slot["price"],
                "duration_days" => $slot["duration_days"],
                "target_chat" => $slot["target_chat"],
                "title" => $title,
                "url" => $url,
                "content" => $content,
                "keyword" => $keyword,
                "status" => "pending",
                "created_at" => date("Y-m-d H:i:s"),
            ];
            $id = Db::name("ad_orders")->insertGetId($row);
            Wallet::post(
                $user,
                -(int) $row["price"],
                "ad_purchase",
                "ad-buy:$id",
                "购买 " . $row["slot_name"] . " #$id",
            );
            return Money::row($row + ["id" => $id]);
        });
    }

    public static function review(
        int $id,
        string $decision,
        string $note,
        int $actor,
        ?Transport $client = null,
    ): array {
        if (
            !in_array($decision, ["approve", "reject", "refund"], true) ||
            trim($note) === ""
        ) {
            throw new DomainError("请选择审核结果并填写说明。");
        }
        $row = Db::name("ad_orders")->where("id", $id)->find();
        if (!$row) {
            throw new DomainError("广告订单不存在。", 404);
        }
        $external = in_array($row["kind"], ["group_pin", "channel"], true);
        $botId = 0;
        if ($decision === "approve" && $external) {
            $client ??= Client::configured();
            $me = $client->call("getMe");
            $botId = (int) $me["id"];
            $chat = $client->call("getChat", [
                "chat_id" => $row["target_chat"],
            ]);
            $member = $client->call("getChatMember", [
                "chat_id" => $row["target_chat"],
                "user_id" => $botId,
            ]);
            $allowed =
                $row["kind"] === "channel"
                    ? $chat["type"] === "channel" &&
                        !empty($member["can_post_messages"])
                    : in_array($chat["type"], ["group", "supergroup"], true) &&
                        !empty($member["can_pin_messages"]);
            if (!$allowed) {
                throw new DomainError(
                    "机器人缺少投放目标的发帖或置顶权限，请先配置。",
                    409,
                );
            }
        }
        return CatalogTransaction::run(function () use (
            $id,
            $decision,
            $note,
            $actor,
            $external,
            $botId,
        ) {
            $row = Db::name("ad_orders")->where("id", $id)->lock(true)->find();
            if ($decision !== "refund" && $row["status"] !== "pending") {
                throw new DomainError("该订单已审核，请刷新列表。", 409);
            }
            if (
                $decision === "refund" &&
                !in_array(
                    $row["status"],
                    ["pending", "active", "delivery_issue"],
                    true,
                )
            ) {
                throw new DomainError(
                    "当前状态不支持退款，请先核实投放结果。",
                    409,
                );
            }
            $change = [
                "review_note" => mb_substr($note, 0, 300),
                "reviewer_id" => $actor,
            ];
            if ($decision === "approve") {
                $change["status"] = $external ? "dispatching" : "active";
                if ($external) {
                    Db::name("ad_deliveries")->insert([
                        "order_id" => $id,
                        "bot_id" => $botId,
                        "state" => "queued",
                        "updated_at" => date("Y-m-d H:i:s"),
                    ]);
                } else {
                    $change["starts_at"] = date("Y-m-d H:i:s");
                    $change["ends_at"] = date(
                        "Y-m-d H:i:s",
                        time() + $row["duration_days"] * 86400,
                    );
                }
            } else {
                $change["status"] =
                    $decision === "reject" ? "rejected" : "refunded";
                Wallet::post(
                    (int) $row["user_id"],
                    (int) $row["price"],
                    "ad_refund",
                    "ad-refund:$id",
                    "广告订单 #$id 退款",
                );
            }
            Db::name("ad_orders")->where("id", $id)->update($change);
            Settings::audit(
                "审核广告订单",
                "订单 #{$id}，{$decision}；" . mb_substr($note, 0, 150),
                $actor,
            );
            return Money::row(Db::name("ad_orders")->where("id", $id)->find());
        });
    }

    public static function search(string $keyword, string $seed): array
    {
        $now = date("Y-m-d H:i:s");
        $rows = Db::name("ad_orders")
            ->where("status", "active")
            ->where("starts_at", "<=", $now)
            ->where("ends_at", ">", $now)
            ->whereIn("kind", [
                "keyword",
                "brand",
                "search_top",
                "search_bottom",
            ])
            ->where(function ($q) use ($keyword) {
                $q->whereIn("kind", ["search_top", "search_bottom"])->whereOr(
                    "keyword",
                    mb_strtolower(trim($keyword)),
                );
            })
            ->order("id")
            ->limit(200)
            ->select()
            ->toArray();
        $out = [];
        foreach (["keyword", "brand", "search_top", "search_bottom"] as $kind) {
            $items = array_values(
                array_filter($rows, fn($r) => $r["kind"] === $kind),
            );
            if (!$items) {
                continue;
            }
            $r =
                $items[
                    (int) hexdec(substr(hash("sha256", "$seed:$kind"), 0, 7)) %
                        count($items)
                ];
            $out[$kind] = array_intersect_key(
                $r,
                array_flip(["id", "title", "url", "content", "kind"]),
            );
        }
        return $out;
    }

    public static function html(array $ad): string
    {
        $e = fn($s) => htmlspecialchars(
            $s,
            ENT_QUOTES | ENT_SUBSTITUTE,
            "UTF-8",
        );
        return '广告 · <a href="' .
            $e($ad["url"]) .
            '">' .
            $e($ad["title"]) .
            "</a>";
    }
}
