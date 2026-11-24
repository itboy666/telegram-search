<?php
namespace app\common\service;

use app\common\exception\DomainError;
use think\facade\Db;

class Payments
{
    public static function key(mixed $key): string
    {
        if (
            !is_string($key) ||
            !preg_match('/^[a-zA-Z0-9_-]{16,64}$/D', $key)
        ) {
            throw new DomainError("请求编号无效，请刷新页面。");
        }
        return $key;
    }

    public static function create(int $user, array $input): array
    {
        $key = self::key($input["request_key"] ?? "");
        $method = (string) ($input["method"] ?? "");
        if (!in_array($method, ["trc20", "offline"], true)) {
            throw new DomainError("请选择充值方式。");
        }
        $amount = Money::parse($input["amount"] ?? "");
        if (
            $amount < 1000000 ||
            $amount > 100000000000 ||
            ($method === "trc20" && $amount % 1000000 !== 0)
        ) {
            throw new DomainError(
                "链上充值请输入 1–100000 的整数金额；线下充值最低 1 USDT。",
            );
        }
        return CatalogTransaction::run(function () use (
            $user,
            $key,
            $method,
            $amount,
        ) {
            Db::name("commerce_config")->where("id", 1)->lock(true)->find();
            Wallet::activeUser($user);
            $old = Db::name("recharges")
                ->where("user_id", $user)
                ->where("request_key", $key)
                ->find();
            if ($old) {
                return Money::row($old);
            }
            $config = CommerceConfig::read();
            if (
                !$config[
                    $method === "trc20" ? "trc20_enabled" : "offline_enabled"
                ]
            ) {
                throw new DomainError("此充值方式暂未启用。", 409);
            }
            if (
                Db::name("recharges")
                    ->where("user_id", $user)
                    ->whereIn("status", ["pending", "review"])
                    ->where("expires_at", ">", date("Y-m-d H:i:s"))
                    ->count() >= 3
            ) {
                throw new DomainError(
                    "请先完成已有充值订单，最多同时保留三笔。",
                    409,
                );
            }
            $address = $method === "trc20" ? $config["receive_address"] : null;
            if ($address) {
                $base = $amount;
                $allocated = false;
                for ($i = 0; $i < 100; $i++) {
                    $amount = $base + random_int(1, 999999);
                    if (
                        !Db::name("recharges")
                            ->where("receive_address", $address)
                            ->where("amount", $amount)
                            ->count()
                    ) {
                        $allocated = true;
                        break;
                    }
                }
                if (!$allocated) {
                    throw new DomainError(
                        "当前金额的付款订单较多，请更换充值金额。",
                        409,
                    );
                }
            }
            $row = [
                "user_id" => $user,
                "request_key" => $key,
                "method" => $method,
                "amount" => $amount,
                "receive_address" => $address,
                "status" => "pending",
                "created_at" => date("Y-m-d H:i:s"),
                "expires_at" => date(
                    "Y-m-d H:i:s",
                    time() + ($address ? 1800 : 86400),
                ),
            ];
            $row["id"] = Db::name("recharges")->insertGetId($row);
            return Money::row($row);
        });
    }

    public static function proof(int $user, int $id, string $proof): array
    {
        $proof = trim($proof);
        if (mb_strlen($proof) < 5 || mb_strlen($proof) > 500) {
            throw new DomainError(
                "请填写 5–500 字转账凭证说明，包括付款人和转账编号。",
            );
        }
        return CatalogTransaction::run(function () use ($user, $id, $proof) {
            $row = self::owned($id, $user, true);
            if (
                $row["method"] !== "offline" ||
                !in_array($row["status"], ["pending", "review"], true)
            ) {
                throw new DomainError("此订单不能提交线下凭证。", 409);
            }
            Db::name("recharges")
                ->where("id", $id)
                ->update(["proof" => $proof, "status" => "review"]);
            return Money::row(self::owned($id, $user));
        });
    }

    public static function owned(
        int $id,
        ?int $user = null,
        bool $lock = false,
    ): array {
        $q = Db::name("recharges")->where("id", $id);
        if ($user !== null) {
            $q->where("user_id", $user);
        }
        $row = $q->lock($lock)->find();
        if (!$row) {
            throw new DomainError("充值订单不存在。", 404);
        }
        return $row;
    }

    public static function review(
        int $id,
        string $decision,
        string $note,
        int $actor,
    ): array {
        if (
            !in_array($decision, ["approve", "reject"], true) ||
            trim($note) === ""
        ) {
            throw new DomainError("请选择审核结果并填写核账说明。");
        }
        return CatalogTransaction::run(function () use (
            $id,
            $decision,
            $note,
            $actor,
        ) {
            $row = self::owned($id, null, true);
            if ($row["method"] !== "offline" || $row["status"] !== "review") {
                throw new DomainError("仅可审核已提交凭证的线下充值。", 409);
            }
            if ($decision === "approve") {
                Wallet::post(
                    (int) $row["user_id"],
                    (int) $row["amount"],
                    "recharge",
                    "recharge:$id",
                    "线下充值 #$id",
                );
            }
            Db::name("recharges")
                ->where("id", $id)
                ->update([
                    "status" => $decision === "approve" ? "paid" : "rejected",
                    "review_note" => mb_substr($note, 0, 300),
                    "reviewer_id" => $actor,
                    "paid_at" =>
                        $decision === "approve" ? date("Y-m-d H:i:s") : null,
                ]);
            Settings::audit("审核线下充值", "订单 #{$id}，{$decision}", $actor);
            return Money::row(self::owned($id));
        });
    }

    public static function check(
        int $id,
        ?int $user = null,
        string $hash = "",
        ?Tronscan $client = null,
    ): array {
        $order = self::owned($id, $user);
        if ($order["status"] === "paid") {
            return Money::row($order);
        }
        if ($order["method"] !== "trc20") {
            throw new DomainError("线下充值请提交凭证后等待审核。");
        }
        $client ??= new Tronscan();
        if ($hash === "") {
            $deadline = microtime(true) + 7;
            // Pages are bounded; a payer can submit their hash to find older transfers.
            for ($start = 0; $start < 1000; $start += 50) {
                $items = $client->transfers(
                    $order["receive_address"],
                    strtotime($order["created_at"]),
                    $start,
                );
                foreach ($items as $item) {
                    if (
                        ($item["to_address"] ?? "") ===
                            $order["receive_address"] &&
                        (string) ($item["quant"] ?? "") ===
                            (string) $order["amount"] &&
                        ($item["contract_address"] ?? "") === Tronscan::USDT &&
                        (int) ($item["block_ts"] ?? 0) >=
                            strtotime($order["created_at"]) * 1000
                    ) {
                        $hash = (string) ($item["transaction_id"] ?? "");
                        break 2;
                    }
                }
                if (count($items) < 50 || microtime(true) >= $deadline) {
                    break;
                }
            }
            if ($hash === "") {
                throw new DomainError(
                    "尚未找到匹配的转账，请按订单完整金额付款后再核查。",
                    409,
                );
            }
        }
        $hash = strtolower(trim($hash));
        $tx = $client->transaction($hash); // Network calls stay outside database transactions.
        return self::settle($id, $hash, $tx);
    }

    private static function settle(int $id, string $hash, array $tx): array
    {
        return CatalogTransaction::run(function () use ($id, $hash, $tx) {
            $row = self::owned($id, null, true);
            if ($row["status"] === "paid") {
                return Money::row($row);
            }
            if (
                $row["method"] !== "trc20" ||
                !in_array($row["status"], ["pending", "expired"], true)
            ) {
                throw new DomainError("此订单不能自动入账。", 409);
            }
            Tronscan::verify($tx, $row, $hash);
            if (Db::name("recharges")->where("txid", $hash)->count()) {
                throw new DomainError("该交易已入账，请勿重复提交。", 409);
            }
            Db::name("recharges")
                ->where("id", $id)
                ->update([
                    "status" => "paid",
                    "txid" => $hash,
                    "paid_at" => date("Y-m-d H:i:s"),
                ]);
            Wallet::post(
                (int) $row["user_id"],
                (int) $row["amount"],
                "recharge",
                "recharge:$id",
                "USDT-TRC20 充值 #$id",
            );
            return Money::row(self::owned($id));
        });
    }
}
