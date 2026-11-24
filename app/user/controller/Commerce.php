<?php
namespace app\user\controller;

use app\common\http\Response;
use app\common\service\{
    Auth,
    Wallet,
    Money,
    Payments,
    Ads,
    CommerceConfig,
    SearchReward,
    SearchService,
    RateLimit,
};
use think\facade\Db;

class Commerce
{
    private function run(callable $fn, bool $write = false): \think\Response
    {
        return Response::run(
            fn() => $fn(
                (int) Auth::session(request(), "user", $write)["user"]["id"],
            ),
        );
    }
    public function summary(): \think\Response
    {
        return $this->run(function ($user) {
            $s = CommerceConfig::read();
            return [
                "balance" => Wallet::balance($user),
                "currency" => "USDT",
                "reward_today" => Money::format(
                    (int) Db::name("reward_claims")
                        ->where("user_id", $user)
                        ->where("day", date("Y-m-d"))
                        ->value("amount", 0),
                ),
                "reward_total" => Money::format(
                    (int) Db::name("reward_claims")
                        ->where("user_id", $user)
                        ->sum("amount"),
                ),
                "active_ads" => Db::name("ad_orders")
                    ->where("user_id", $user)
                    ->where("status", "active")
                    ->where("ends_at", ">", date("Y-m-d H:i:s"))
                    ->count(),
                "config" => array_intersect_key(
                    $s,
                    array_flip([
                        "trc20_enabled",
                        "offline_enabled",
                        "offline_instructions",
                        "reward_enabled",
                        "reward_amount",
                        "reward_daily_cap",
                        "reward_user_cap",
                    ]),
                ),
            ];
        });
    }
    public function slots(): \think\Response
    {
        return $this->run(
            fn() => [
                "kinds" => Ads::KINDS,
                "items" => array_map(
                    fn($r) => Money::row($r),
                    Db::name("ad_slots")
                        ->field(
                            "id,name,kind,description,price,duration_days,capacity,enabled",
                        )
                        ->order("id")
                        ->select()
                        ->toArray(),
                ),
            ],
        );
    }
    public function records(): \think\Response
    {
        return $this->run(function ($user) {
            $table = match ((string) request()->param("section")) {
                "ledger" => "wallet_entries",
                "recharges" => "recharges",
                default => "ad_orders",
            };
            $q = Db::name($table)->where("user_id", $user);
            return [
                "total" => (clone $q)->count(),
                "items" => array_map(
                    fn($r) => Money::row($r),
                    $q
                        ->order("id", "desc")
                        ->page(max(1, (int) request()->get("page", 1)), 20)
                        ->select()
                        ->toArray(),
                ),
            ];
        });
    }
    public function recharge(): \think\Response
    {
        return $this->run(function ($user) {
            RateLimit::hit("recharge-create:$user", 6, 3600);
            return Payments::create($user, request()->post());
        }, true);
    }
    public function proof(): \think\Response
    {
        return $this->run(
            fn($user) => Payments::proof(
                $user,
                (int) request()->param("id"),
                (string) request()->post("proof", ""),
            ),
            true,
        );
    }
    public function check(): \think\Response
    {
        return $this->run(function ($user) {
            RateLimit::hit("recharge-check:$user", 6, 60);
            return Payments::check(
                (int) request()->param("id"),
                $user,
                (string) request()->post("txid", ""),
            );
        }, true);
    }
    public function buy(): \think\Response
    {
        return $this->run(function ($user) {
            RateLimit::hit("ad-buy:$user", 20, 60);
            return Ads::buy($user, request()->post());
        }, true);
    }
    public function search(): \think\Response
    {
        return $this->run(function ($user) {
            RateLimit::hit("user-search:$user", 60, 60);
            $input = request()->post();
            $result = new SearchService()->search($input);
            $result["reward"] = SearchReward::grant(
                $user,
                (string) ($input["q"] ?? ""),
                $result,
                "web",
            );
            $result["ads"] = Ads::search(
                (string) ($input["q"] ?? ""),
                bin2hex(random_bytes(4)),
            );
            return $result;
        }, true);
    }
}
