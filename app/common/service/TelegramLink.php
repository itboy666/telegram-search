<?php
namespace app\common\service;

use app\common\exception\DomainError;
use think\facade\Db;

/** Shared canonical identity and transaction-scoped locks for every catalog writer. */
class TelegramLink
{
    public static function normalize(string $input): string
    {
        $url = trim($input);
        if (preg_match('/^@[A-Za-z0-9_]{4,32}$/D', $url)) {
            $url = "https://t.me/" . substr($url, 1);
        }
        if (preg_match("#^(?:www\.)?(?:t\.me|telegram\.me)/#i", $url)) {
            $url = "https://" . $url;
        }
        $parts = parse_url($url);
        if (
            !$parts ||
            !in_array(
                strtolower($parts["scheme"] ?? ""),
                ["http", "https"],
                true,
            ) ||
            !in_array(
                strtolower($parts["host"] ?? ""),
                ["t.me", "www.t.me", "telegram.me", "www.telegram.me"],
                true,
            ) ||
            isset($parts["user"]) ||
            isset($parts["pass"]) ||
            isset($parts["port"]) ||
            !preg_match(
                '#^/(?:s/)?([A-Za-z0-9_]{4,32})(?:/([0-9]+))?/?$#iD',
                $parts["path"] ?? "",
                $match,
            ) ||
            in_array(
                strtolower($match[1]),
                [
                    "joinchat",
                    "addstickers",
                    "addemoji",
                    "share",
                    "proxy",
                    "socks",
                    "login",
                    "invoice",
                    "setlanguage",
                    "addtheme",
                    "contact",
                    "boost",
                    "giftcode",
                    "confirmphone",
                ],
                true,
            )
        ) {
            throw new DomainError(
                "请填写公开的 Telegram 链接，例如 https://t.me/telegram。",
                422,
                [
                    "url" => "仅支持公开 Telegram 用户名或帖子链接",
                ],
            );
        }
        $post = isset($match[2]) ? ltrim($match[2], "0") : null;
        if ($post !== null && ($post === "" || strlen($post) > 20)) {
            throw new DomainError("帖子编号无效。", 422, [
                "url" => "请填写有效的帖子链接",
            ]);
        }
        return "https://t.me/" .
            strtolower($match[1]) .
            ($post !== null ? "/" . $post : "");
    }

    /** Must be called inside the caller's transaction. Locks survive nested commits. */
    public static function lock(array $hashes): void
    {
        $hashes = array_values(array_unique($hashes));
        sort($hashes, SORT_STRING);
        foreach (array_chunk($hashes, 500) as $chunk) {
            if (!$chunk) {
                continue;
            }
            Db::execute(
                "INSERT INTO ts_link_locks (url_hash) VALUES " .
                    implode(",", array_fill(0, count($chunk), "(?)")) .
                    " ON DUPLICATE KEY UPDATE url_hash=ts_link_locks.url_hash",
                $chunk,
            );
        }
    }

    public static function resourceMessage(array $resource): string
    {
        $message = match ($resource["status"]) {
            "published" => "该链接已存在，已正常收录，请勿重复提交。",
            "hidden" => "该链接已存在，目前已下架，不能重复提交。",
            "archived" => "该链接已存在，目前已归档，不能重复提交。",
            "blocked" => "该链接已被封禁，不能提交收录。",
            "invalid" => "该链接已标记为失效，不能提交收录。",
            default => "该链接已存在，当前状态不允许重复提交。",
        };
        $reason = trim((string) ($resource["status_reason"] ?? ""));
        return $message .
            ($resource["status"] !== "published" && $reason !== ""
                ? "\n原因：" . $reason
                : "");
    }

    public static function submissionMessage(array $submission): string
    {
        return match ($submission["status"]) {
            "pending" => "该链接已存在收录申请，正在审核，请勿重复提交。",
            "rejected" => "该链接的收录申请已被拒绝，不能重复提交。" .
                (trim($submission["review_note"] ?? "") !== ""
                    ? "\n原因：" . $submission["review_note"]
                    : ""),
            default => "该链接已有收录记录，请勿重复提交。",
        };
    }
}
