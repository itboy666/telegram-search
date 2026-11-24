<?php
namespace app\common\service;

use app\common\exception\DomainError;
use think\facade\Db;

class Wallet
{
    public static function balance(int $user): string
    {
        return Money::format(
            (int) Db::name("wallets")
                ->where("user_id", $user)
                ->value("balance", 0),
        );
    }

    /** Caller owns a transaction. A ledger entry and its balance must commit together. */
    public static function post(
        int $user,
        int $amount,
        string $kind,
        string $key,
        string $note,
    ): array {
        if (!Db::connect()->getPdo()->inTransaction()) {
            throw new \LogicException(
                "Wallet writes require an enclosing transaction.",
            );
        }
        $u = Db::name("users")->where("id", $user)->lock(true)->find();
        if (!$u) {
            throw new DomainError("用户不存在。", 404);
        }
        Db::execute(
            "INSERT IGNORE INTO ts_wallets (user_id,balance,updated_at) VALUES (?,0,NOW())",
            [$user],
        );
        $wallet = Db::name("wallets")
            ->where("user_id", $user)
            ->lock(true)
            ->find();
        $existing = Db::name("wallet_entries")
            ->where("business_key", $key)
            ->find();
        if ($existing) {
            if (
                (int) $existing["user_id"] !== $user ||
                (int) $existing["amount"] !== $amount ||
                $existing["kind"] !== $kind
            ) {
                throw new DomainError("资金业务编号冲突，请联系管理员。", 409);
            }
            return $existing;
        }
        $balance = (int) $wallet["balance"];
        if (
            $amount === 0 ||
            abs($amount) > Money::MAX ||
            $balance > PHP_INT_MAX - max(0, $amount)
        ) {
            throw new DomainError("资金金额超出允许范围。");
        }
        if ($balance + $amount < 0) {
            throw new DomainError("余额不足，请先充值。", 409);
        }
        Db::name("wallets")
            ->where("id", $wallet["id"])
            ->update([
                "balance" => $balance + $amount,
                "updated_at" => date("Y-m-d H:i:s"),
            ]);
        $entry = [
            "user_id" => $user,
            "amount" => $amount,
            "balance_after" => $balance + $amount,
            "kind" => $kind,
            "business_key" => $key,
            "note" => mb_substr($note, 0, 300),
            "created_at" => date("Y-m-d H:i:s"),
        ];
        $entry["id"] = Db::name("wallet_entries")->insertGetId($entry);
        return $entry;
    }

    public static function activeUser(int $id): void
    {
        $u = Db::name("users")->where("id", $id)->lock(true)->find();
        if (!$u || !$u["status"]) {
            throw new DomainError("当前账号已停用。", 403);
        }
    }
}
