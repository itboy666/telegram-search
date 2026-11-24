<?php
namespace app\api\controller;
use app\common\http\Response;
use app\common\service\{
    SearchService,
    ResourceService,
    Settings,
    RateLimit,
    BotState,
};
use app\common\exception\DomainError;
use think\facade\Db;
class Catalog
{
    public function config(): \think\Response
    {
        return Response::run(function () {
            $s = Settings::all();
            $bot = BotState::read();
            return [
                "site_name" => $s["site_name"],
                "site_description" => $s["site_description"],
                "submissions_enabled" => $s["submissions_enabled"],
                "bot_username" => $bot["username"] ?? null,
                "types" => ResourceService::TYPES,
                "resource_count" => Db::name("resources")
                    ->where("status", "published")
                    ->count(),
            ];
        });
    }
    public function categories(): \think\Response
    {
        return Response::run(
            fn() => Db::name("categories")
                ->order("sort_order,id")
                ->select()
                ->toArray(),
        );
    }
    public function search(): \think\Response
    {
        return Response::run(function () {
            RateLimit::hit("web:" . request()->ip(), 120, 60);
            $result = new SearchService()->search(request()->param());
            $result["ads"] = \app\common\service\Ads::search(
                (string) request()->param("q", ""),
                bin2hex(random_bytes(4)),
            );
            return $result;
        });
    }
    public function detail(): \think\Response
    {
        return Response::run(function () {
            $row = Db::name("resources")
                ->where("id", (int) request()->param("id"))
                ->where("status", "published")
                ->find();
            if (!$row) {
                throw new DomainError("资源不存在或已下架。", 404);
            }
            unset($row["url_hash"]);
            return $row;
        });
    }
    public function external(): \think\Response
    {
        return Response::run(function () {
            if (Settings::get("public_api_enabled") !== "1") {
                throw new DomainError("开放 API 暂未启用。", 403);
            }
            $header = (string) request()->header("authorization", "");
            $token = str_starts_with($header, "Bearer ")
                ? substr($header, 7)
                : "";
            $key = Db::name("api_keys")
                ->where("token_hash", hash("sha256", $token))
                ->where("status", 1)
                ->find();
            if (!$key) {
                throw new DomainError("API 密钥无效。", 401);
            }
            RateLimit::hit(
                "key:" . $key["id"] . ":" . date("Y-m-d"),
                (int) $key["daily_limit"],
                86400,
            );
            RateLimit::hit("key-burst:" . $key["id"], 60, 60);
            Db::name("api_keys")
                ->where("id", $key["id"])
                ->update(["last_used_at" => date("Y-m-d H:i:s")]);
            return new SearchService()->search(request()->param(), "api");
        });
    }
}
