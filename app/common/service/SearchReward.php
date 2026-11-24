<?php
namespace app\common\service;

use think\facade\Db;

class SearchReward
{
    public static function grant(
        int $user,
        string $keyword,
        array $result,
        string $source,
    ): ?array {
        if (
            !in_array($source, ["bot", "web"], true) ||
            trim($keyword) === "" ||
            empty($result["items"]) ||
            (int) ($result["page"] ?? 1) !== 1
        ) {
            return null;
        }
        if (!CommerceConfig::read()["reward_enabled"]) {
            return null;
        }
        return CatalogTransaction::run(function () use ($user, $source) {
            Db::name("commerce_config")->where("id", 1)->lock(true)->find();
            $s = CommerceConfig::read();
            if (!$s["reward_enabled"]) {
                return null;
            }
            $day = date("Y-m-d");
            Wallet::activeUser($user);
            if (
                Db::name("reward_claims")
                    ->where("user_id", $user)
                    ->where("day", $day)
                    ->count()
            ) {
                return null;
            }
            $amount = Money::parse($s["reward_amount"]);
            Db::execute(
                "INSERT IGNORE INTO ts_reward_days (day,amount) VALUES (?,0)",
                [$day],
            );
            $spent = Db::name("reward_days")
                ->where("day", $day)
                ->lock(true)
                ->value("amount");
            $lifetime = (int) Db::name("reward_claims")
                ->where("user_id", $user)
                ->sum("amount");
            if (
                $spent + $amount > Money::parse($s["reward_daily_cap"]) ||
                $lifetime + $amount > Money::parse($s["reward_user_cap"])
            ) {
                return null;
            }
            Db::name("reward_claims")->insert([
                "user_id" => $user,
                "day" => $day,
                "amount" => $amount,
                "source" => $source,
                "created_at" => date("Y-m-d H:i:s"),
            ]);
            Db::name("reward_days")
                ->where("day", $day)
                ->inc("amount", $amount)
                ->update();
            return Money::row(
                Wallet::post(
                    $user,
                    $amount,
                    "search_reward",
                    "reward:$user:$day",
                    "每日首次有效搜索奖励",
                ),
            );
        });
    }
}
