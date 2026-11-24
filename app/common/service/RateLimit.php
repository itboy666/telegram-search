<?php
namespace app\common\service;
use think\facade\Db;
use app\common\exception\DomainError;
class RateLimit
{
    public static function hit(string $identity, int $limit, int $seconds): void
    {
        $window = (int) floor(time() / $seconds);
        $key = hash("sha256", $identity) . ":" . $window;
        $count = Db::transaction(function () use ($key, $seconds) {
            Db::execute(
                "INSERT INTO ts_rate_limits (bucket,hits,expires_at) VALUES (?,1,?) ON DUPLICATE KEY UPDATE hits=hits+1",
                [$key, date("Y-m-d H:i:s", time() + $seconds * 2)],
            );
            return (int) Db::name("rate_limits")
                ->where("bucket", $key)
                ->value("hits");
        });
        if ($count > $limit) {
            throw new DomainError("操作过于频繁，请稍后再试。", 429);
        }
    }
}
