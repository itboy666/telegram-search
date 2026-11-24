<?php
namespace app\common\service;

use app\common\exception\DomainError;
use think\facade\{Db, Cookie};

class BotLogin
{
    public static function issue(int $user): string
    {
        return CatalogTransaction::run(function () use ($user) {
            Wallet::activeUser($user);
            $code = strtoupper(bin2hex(random_bytes(10)));
            Db::name("bot_login_codes")
                ->where("user_id", $user)
                ->whereNull("used_at")
                ->delete();
            Db::name("bot_login_codes")->insert([
                "user_id" => $user,
                "code_hash" => hash("sha256", $code),
                "expires_at" => date("Y-m-d H:i:s", time() + 300),
            ]);
            return $code;
        });
    }

    public static function login(\think\Request $request): array
    {
        RateLimit::hit("bot-login:" . $request->ip(), 8, 300);
        $code = strtoupper(trim((string) $request->post("code", "")));
        if (!preg_match('/^[A-F0-9]{20}$/D', $code)) {
            throw new DomainError("请输入机器人 /account 提供的完整登录码。");
        }
        $token = bin2hex(random_bytes(32));
        $csrf = bin2hex(random_bytes(24));
        $user = CatalogTransaction::run(function () use ($code, $token, $csrf) {
            $row = Db::name("bot_login_codes")
                ->where("code_hash", hash("sha256", $code))
                ->lock(true)
                ->find();
            if (
                !$row ||
                $row["used_at"] ||
                strtotime($row["expires_at"]) <= time()
            ) {
                throw new DomainError(
                    "登录码已失效，请在机器人私聊中重新发送 /account。",
                    401,
                );
            }
            Wallet::activeUser((int) $row["user_id"]);
            Db::name("bot_login_codes")
                ->where("id", $row["id"])
                ->update(["used_at" => date("Y-m-d H:i:s")]);
            Db::name("sessions")->insert([
                "user_id" => $row["user_id"],
                "scope" => "user",
                "token_hash" => hash("sha256", $token),
                "csrf_token" => $csrf,
                "created_at" => date("Y-m-d H:i:s"),
                "expires_at" => date("Y-m-d H:i:s", time() + 43200),
            ]);
            return Db::name("users")
                ->where("id", $row["user_id"])
                ->field("id,username,display_name,telegram_id,created_at")
                ->find();
        });
        Cookie::set("starindex_user", $token, [
            "expire" => 43200,
            "path" => "/",
            "httponly" => true,
            "secure" => $request->isSsl(),
            "samesite" => "Lax",
        ]);
        return ["user" => $user, "csrf_token" => $csrf];
    }
}
