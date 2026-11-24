<?php
namespace app\common\service;

use app\common\exception\DomainError;
use app\common\telegram\{Client, Transport};
use think\facade\Db;

/** An ambiguous send is never automatically retried: Telegram has no send idempotency key. */
class AdDelivery
{
    public static function run(int $id, ?Transport $client = null): void
    {
        $job = CatalogTransaction::run(function () use ($id) {
            $job = Db::name("ad_deliveries")
                ->where("id", $id)
                ->lock(true)
                ->find();
            if (!$job || $job["state"] !== "queued") {
                return null;
            }
            Db::name("ad_deliveries")
                ->where("id", $id)
                ->update([
                    "state" => "sending",
                    "updated_at" => date("Y-m-d H:i:s"),
                ]);
            return $job;
        });
        if (!$job) {
            return;
        }
        $order = Db::name("ad_orders")->where("id", $job["order_id"])->find();
        $messageId = null;
        try {
            $client ??= Client::configured();
            $me = $client->call("getMe");
            if ((int) $me["id"] !== (int) $job["bot_id"]) {
                throw new DomainError("机器人身份已更换，请人工核对投放。");
            }
            $message = $client->call("sendMessage", [
                "chat_id" => $order["target_chat"],
                "text" =>
                    Ads::html($order) .
                    "\n\n" .
                    htmlspecialchars(
                        $order["content"],
                        ENT_QUOTES | ENT_SUBSTITUTE,
                        "UTF-8",
                    ),
                "parse_mode" => "HTML",
                "link_preview_options" => ["is_disabled" => true],
            ]);
            $messageId = (int) $message["message_id"];
            Db::name("ad_deliveries")
                ->where("id", $id)
                ->update(["message_id" => $messageId]);
            if ($order["kind"] === "group_pin") {
                $client->call("pinChatMessage", [
                    "chat_id" => $order["target_chat"],
                    "message_id" => $messageId,
                    "disable_notification" => true,
                ]);
            }
            self::activate($job, $order, $messageId);
        } catch (\Throwable) {
            CatalogTransaction::run(function () use ($id, $job, $messageId) {
                Db::name("ad_deliveries")
                    ->where("id", $id)
                    ->update([
                        "state" => "needs_review",
                        "message_id" => $messageId,
                        "error" => $messageId
                            ? "消息已发送，置顶未确认；请核实权限后处理。"
                            : "发送结果未确认，禁止自动重发；请检查聊天记录后处理。",
                        "updated_at" => date("Y-m-d H:i:s"),
                    ]);
                Db::name("ad_orders")
                    ->where("id", $job["order_id"])
                    ->update(["status" => "delivery_issue"]);
            });
        }
    }

    private static function activate(
        array $job,
        array $order,
        int $message,
    ): void {
        CatalogTransaction::run(function () use ($job, $order, $message) {
            Db::name("ad_deliveries")
                ->where("id", $job["id"])
                ->update([
                    "state" => "sent",
                    "message_id" => $message,
                    "error" => "",
                    "updated_at" => date("Y-m-d H:i:s"),
                ]);
            Db::name("ad_orders")
                ->where("id", $order["id"])
                ->update([
                    "status" => "active",
                    "starts_at" => date("Y-m-d H:i:s"),
                    "ends_at" => date(
                        "Y-m-d H:i:s",
                        time() + $order["duration_days"] * 86400,
                    ),
                ]);
        });
    }

    public static function resolve(int $id, int $message, int $actor): array
    {
        if ($message < 1) {
            throw new DomainError("请填写实际投放消息 ID。");
        }
        $order = Db::name("ad_orders")->where("id", $id)->find();
        $job = Db::name("ad_deliveries")->where("order_id", $id)->find();
        if (!$job || !$order || $order["status"] !== "delivery_issue") {
            throw new DomainError("没有待处理的投放异常。", 409);
        }
        $client = Client::configured();
        if ((int) $client->call("getMe")["id"] !== (int) $job["bot_id"]) {
            throw new DomainError("请切回原机器人后处理此消息。", 409);
        }
        CatalogTransaction::run(function () use ($id, $job, $message) {
            $current = Db::name("ad_orders")
                ->where("id", $id)
                ->lock(true)
                ->find();
            if ($current["status"] !== "delivery_issue") {
                throw new DomainError("订单状态已变化，请刷新。", 409);
            }
            Db::name("ad_orders")
                ->where("id", $id)
                ->update(["status" => "dispatching"]);
            Db::name("ad_deliveries")
                ->where("id", $job["id"])
                ->update([
                    "state" => "sending",
                    "message_id" => $message,
                    "updated_at" => date("Y-m-d H:i:s"),
                ]);
        });
        try {
            if ($order["kind"] === "group_pin") {
                $client->call("pinChatMessage", [
                    "chat_id" => $order["target_chat"],
                    "message_id" => $message,
                    "disable_notification" => true,
                ]);
            }
            return CatalogTransaction::run(function () use (
                $order,
                $job,
                $message,
                $actor,
                $id,
            ) {
                self::activate($job, $order, $message);
                Settings::audit(
                    "人工确认广告投放",
                    "订单 #{$id}，消息 #{$message}",
                    $actor,
                );
                return ["resolved" => true];
            });
        } catch (\Throwable) {
            CatalogTransaction::run(function () use ($id, $job) {
                Db::name("ad_orders")
                    ->where("id", $id)
                    ->update(["status" => "delivery_issue"]);
                Db::name("ad_deliveries")
                    ->where("id", $job["id"])
                    ->update([
                        "state" => "needs_review",
                        "error" => "未能确认置顶，请检查目标消息与权限。",
                    ]);
            });
            throw new DomainError(
                "无法确认投放，请检查消息 ID 与机器人权限。",
                409,
            );
        }
    }

    public static function maintain(): void
    {
        // A process interrupted during send must be investigated, not sent twice.
        foreach (
            Db::name("ad_deliveries")
                ->where("state", "sending")
                ->where("updated_at", "<", date("Y-m-d H:i:s", time() - 300))
                ->select()
            as $job
        ) {
            CatalogTransaction::run(function () use ($job) {
                Db::name("ad_deliveries")
                    ->where("id", $job["id"])
                    ->where("state", "sending")
                    ->update([
                        "state" => "needs_review",
                        "error" => "进程中断，投放结果需人工核对。",
                    ]);
                Db::name("ad_orders")
                    ->where("id", $job["order_id"])
                    ->where("status", "dispatching")
                    ->update(["status" => "delivery_issue"]);
            });
        }
        Db::name("ad_orders")
            ->where("status", "active")
            ->where("ends_at", "<=", date("Y-m-d H:i:s"))
            ->update(["status" => "completed"]);
        foreach (
            Db::name("ad_deliveries")
                ->alias("d")
                ->join(["ts_ad_orders" => "o"], "o.id=d.order_id")
                ->where("o.kind", "group_pin")
                ->whereIn("o.status", ["completed", "refunded"])
                ->whereIn("d.state", ["sent", "needs_review"])
                ->whereNotNull("d.message_id")
                ->field("d.*,o.target_chat")
                ->limit(20)
                ->select()
                ->toArray()
            as $job
        ) {
            try {
                $client = Client::configured();
                if (
                    (int) $client->call("getMe")["id"] !== (int) $job["bot_id"]
                ) {
                    continue;
                }
                $client->call("unpinChatMessage", [
                    "chat_id" => $job["target_chat"],
                    "message_id" => (int) $job["message_id"],
                ]);
                Db::name("ad_deliveries")
                    ->where("id", $job["id"])
                    ->update(["state" => "finished", "error" => ""]);
            } catch (\Throwable) {
                Db::name("ad_deliveries")
                    ->where("id", $job["id"])
                    ->update([
                        "error" =>
                            "结束置顶失败，将自动重试；请核对机器人权限。",
                    ]);
            }
        }
    }
}
