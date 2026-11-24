<?php
namespace app\admin\controller;

use app\common\exception\DomainError;
use app\common\http\Response;
use app\common\service\{
    Auth,
    Rbac,
    Money,
    Payments,
    Ads,
    AdDelivery,
    CommerceConfig,
    Tronscan,
    RateLimit,
};
use think\facade\Db;

class Commerce
{
    private function run(
        string $module,
        string $action,
        callable $fn,
        bool $write = false,
    ): \think\Response {
        return Response::run(function () use ($module, $action, $fn, $write) {
            $s = Auth::session(request(), "admin", $write);
            Rbac::require($s["access"], "$module.$action");
            if ($write) {
                Rbac::checkWrite($s["access"], $module, request()->post());
            }
            return $fn($s);
        });
    }

    public function listing(): \think\Response
    {
        $module = (string) request()->param("section");
        if (
            !in_array(
                $module,
                [
                    "wallets",
                    "wallet_entries",
                    "recharges",
                    "ad_slots",
                    "ad_orders",
                ],
                true,
            )
        ) {
            return json(["message" => "接口不存在"], 404);
        }
        return $this->run($module, "read", function ($s) use ($module) {
            $q = Db::name($module);
            foreach (["id", "user_id", "status", "kind"] as $field) {
                $v = trim((string) request()->get($field, ""));
                if ($v === "") {
                    continue;
                }
                if (
                    $field !== "id" &&
                    (!isset(Rbac::catalog()[$module]["fields"][$field]) ||
                        !Rbac::readable($s["access"], $module, $field))
                ) {
                    throw new DomainError("无权使用此筛选字段。", 403);
                }
                $q->where($field, $v);
            }
            $total = (clone $q)->count();
            $rows = $q
                ->order("id", "desc")
                ->page(max(1, (int) request()->get("page", 1)), 20)
                ->select()
                ->toArray();
            foreach ($rows as &$row) {
                if ($module === "ad_orders") {
                    $job = Db::name("ad_deliveries")
                        ->where("order_id", $row["id"])
                        ->find();
                    $row += [
                        "message_id" => $job["message_id"] ?? null,
                        "delivery_state" => $job["state"] ?? "",
                        "delivery_error" => $job["error"] ?? "",
                    ];
                }
                $row = Money::row($row);
            }
            unset($row);
            return [
                "total" => $total,
                "items" => Rbac::rows($s["access"], $module, $rows),
            ];
        });
    }

    public function settings(): \think\Response
    {
        return $this->run(
            "commerce_settings",
            "read",
            fn($s) => Rbac::project(
                $s["access"],
                "commerce_settings",
                CommerceConfig::read(),
            ),
        );
    }
    public function saveSettings(): \think\Response
    {
        return $this->run(
            "commerce_settings",
            "update",
            fn($s) => Rbac::project(
                $s["access"],
                "commerce_settings",
                CommerceConfig::save(request()->post(), (int) $s["user"]["id"]),
            ),
            true,
        );
    }
    public function checkConnection(): \think\Response
    {
        return $this->run(
            "commerce_settings",
            "update",
            function () {
                $s = CommerceConfig::read();
                if (!$s["receive_address"]) {
                    throw new DomainError("请先配置收款地址。");
                }
                new Tronscan()->transfers($s["receive_address"], time() - 60);
                return [
                    "connected" => true,
                    "message" => "Tronscan 查询成功（未进行转账）",
                ];
            },
            true,
        );
    }
    public function saveSlot(): \think\Response
    {
        $id = (int) request()->param("id", 0);
        return $this->run(
            "ad_slots",
            $id ? "update" : "create",
            fn($s) => Rbac::project(
                $s["access"],
                "ad_slots",
                Ads::saveSlot(
                    $id ?: null,
                    request()->post(),
                    (int) $s["user"]["id"],
                ),
            ),
            true,
        );
    }
    public function deleteSlot(): \think\Response
    {
        return $this->run(
            "ad_slots",
            "delete",
            fn($s) => Ads::deleteSlot(
                (int) request()->param("id"),
                (int) $s["user"]["id"],
            ),
            true,
        );
    }
    public function reviewRecharge(): \think\Response
    {
        return $this->run(
            "recharges",
            "review",
            fn($s) => Rbac::project(
                $s["access"],
                "recharges",
                Payments::review(
                    (int) request()->param("id"),
                    (string) request()->post("decision", ""),
                    (string) request()->post("review_note", ""),
                    (int) $s["user"]["id"],
                ),
            ),
            true,
        );
    }
    public function checkRecharge(): \think\Response
    {
        return $this->run(
            "recharges",
            "check",
            function ($s) {
                RateLimit::hit(
                    "admin-payment-check:" . $s["user"]["id"],
                    10,
                    60,
                );
                return Rbac::project(
                    $s["access"],
                    "recharges",
                    Payments::check(
                        (int) request()->param("id"),
                        null,
                        (string) request()->post("txid", ""),
                    ),
                );
            },
            true,
        );
    }
    public function reviewAd(): \think\Response
    {
        return $this->run(
            "ad_orders",
            "review",
            fn($s) => Rbac::project(
                $s["access"],
                "ad_orders",
                Ads::review(
                    (int) request()->param("id"),
                    (string) request()->post("decision", ""),
                    (string) request()->post("review_note", ""),
                    (int) $s["user"]["id"],
                ),
            ),
            true,
        );
    }
    public function resolveAd(): \think\Response
    {
        return $this->run(
            "ad_orders",
            "resolve",
            fn($s) => AdDelivery::resolve(
                (int) request()->param("id"),
                (int) request()->post("message_id", 0),
                (int) $s["user"]["id"],
            ),
            true,
        );
    }
}
