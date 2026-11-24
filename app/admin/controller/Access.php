<?php
namespace app\admin\controller;

use app\common\http\Response;
use app\common\service\{Auth, Rbac, AccessManagement};

class Access
{
    private function run(callable $fn, bool $write = false): \think\Response
    {
        return Response::run(function () use ($fn, $write) {
            $session = Auth::session(request(), "admin", $write);
            Rbac::require($session["access"], "access.manage");
            return $fn($session);
        });
    }
    public function catalog(): \think\Response
    {
        return $this->run(fn() => Rbac::catalog());
    }
    public function admins(): \think\Response
    {
        return $this->run(fn() => AccessManagement::admins());
    }
    public function roles(): \think\Response
    {
        return $this->run(fn() => AccessManagement::roles());
    }
    public function saveAdmin(): \think\Response
    {
        return $this->run(
            fn($s) => AccessManagement::saveAdmin(
                request()->post(),
                (int) $s["user"]["id"],
                (int) request()->route("id", 0) ?: null,
            ),
            true,
        );
    }
    public function saveRole(): \think\Response
    {
        return $this->run(
            fn($s) => AccessManagement::saveRole(
                request()->post(),
                (int) $s["user"]["id"],
                (int) request()->route("id", 0) ?: null,
            ),
            true,
        );
    }
    public function deleteAdmin(): \think\Response
    {
        return $this->run(
            fn($s) => AccessManagement::deleteAdmin(
                (int) request()->route("id"),
                (int) $s["user"]["id"],
            ),
            true,
        );
    }
    public function deleteRole(): \think\Response
    {
        return $this->run(
            fn($s) => AccessManagement::deleteRole(
                (int) request()->route("id"),
                (int) $s["user"]["id"],
            ),
            true,
        );
    }
}
