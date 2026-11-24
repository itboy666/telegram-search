<?php
namespace app\user\controller;
use app\common\http\Response;
use app\common\service\{Auth, SubmissionService, CatalogSource};
use app\common\exception\DomainError;
use think\facade\Db;

class Account
{
    public function botLogin(): \think\Response
    {
        return Response::run(
            fn() => \app\common\service\BotLogin::login(request()),
        );
    }
    private function run(callable $fn, bool $write = false): \think\Response
    {
        return Response::run(
            fn() => $fn(Auth::session(request(), "user", $write)),
        );
    }
    public function login(): \think\Response
    {
        return Response::run(fn() => Auth::login(request(), "user"));
    }
    public function register(): \think\Response
    {
        return Response::run(fn() => Auth::register(request()));
    }
    public function logout(): \think\Response
    {
        return Response::run(fn() => Auth::logout(request(), "user"));
    }
    public function me(): \think\Response
    {
        return $this->run(fn($s) => $s);
    }
    public function favorites(): \think\Response
    {
        return $this->run(function ($s) {
            $q = Db::name("favorites")
                ->alias("f")
                ->join(["ts_resources" => "r"], "f.resource_id=r.id")
                ->where("f.user_id", $s["user"]["id"])
                ->where("r.status", "published");
            $total = (clone $q)->count();
            $items = $q
                ->field(
                    "r.id,r.title,r.url,r.type,r.description,r.tags,r.members,r.created_at,r.source",
                )
                ->order("f.created_at", "desc")
                ->page(max(1, (int) request()->param("page", 1)), 12)
                ->select()
                ->toArray();
            foreach ($items as &$item) {
                $item["source_attribution"] = CatalogSource::attribution(
                    $item["source"],
                );
            }
            unset($item);
            return [
                "items" => $items,
                "total" => $total,
            ];
        });
    }
    public function favorite(): \think\Response
    {
        return $this->run(function ($s) {
            return \app\common\service\CatalogTransaction::run(function () use (
                $s,
            ) {
                $id = (int) request()->param("id");
                $save = (bool) request()->param("saved", true);
                $user = Db::name("users")
                    ->where("id", $s["user"]["id"])
                    ->lock(true)
                    ->find();
                if (!$user || !$user["status"]) {
                    throw new DomainError("当前账号没有访问权限。", 403);
                }
                if (
                    !Db::name("resources")
                        ->where("id", $id)
                        ->where("status", "published")
                        ->lock(true)
                        ->find()
                ) {
                    throw new DomainError("资源不存在或已下架。", 404);
                }
                if ($save) {
                    Db::execute(
                        "INSERT IGNORE INTO ts_favorites (user_id,resource_id,created_at) VALUES (?,?,?)",
                        [$s["user"]["id"], $id, date("Y-m-d H:i:s")],
                    );
                } else {
                    Db::name("favorites")
                        ->where("user_id", $s["user"]["id"])
                        ->where("resource_id", $id)
                        ->delete();
                }
                return ["saved" => $save];
            });
        }, true);
    }
    public function favoriteIds(): \think\Response
    {
        return $this->run(function ($s) {
            $ids = array_slice(
                array_filter(
                    array_map(
                        "intval",
                        explode(",", (string) request()->param("ids", "")),
                    ),
                ),
                0,
                50,
            );
            return $ids
                ? array_map(
                    "intval",
                    Db::name("favorites")
                        ->where("user_id", $s["user"]["id"])
                        ->whereIn("resource_id", $ids)
                        ->column("resource_id"),
                )
                : [];
        });
    }
    public function submissions(): \think\Response
    {
        return $this->run(function ($s) {
            $q = Db::name("submissions")->where("user_id", $s["user"]["id"]);
            return [
                "total" => (clone $q)->count(),
                "items" => $q
                    ->order("id", "desc")
                    ->page(max(1, (int) request()->param("page", 1)), 12)
                    ->select()
                    ->toArray(),
            ];
        });
    }
    public function submit(): \think\Response
    {
        return $this->run(
            fn($s) => SubmissionService::create(
                (int) $s["user"]["id"],
                request()->post(),
            ),
            true,
        );
    }
}
