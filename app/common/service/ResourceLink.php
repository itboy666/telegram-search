<?php
namespace app\common\service;

use app\common\exception\DomainError;

/** Website identities are conservative: paths, query order and schemes stay distinct. */
class ResourceLink
{
    public static function normalize(string $input, string $type): string
    {
        if ($type !== "website") {
            return TelegramLink::normalize($input);
        }
        $url = trim($input);
        $parts = parse_url($url);
        if (
            !$parts ||
            !mb_check_encoding($url, "UTF-8") ||
            preg_match("/[\s\p{Z}\p{C}\\\\]/u", $url) ||
            !in_array(
                strtolower($parts["scheme"] ?? ""),
                ["http", "https"],
                true,
            ) ||
            isset($parts["user"]) ||
            isset($parts["pass"])
        ) {
            self::invalid();
        }
        $scheme = strtolower($parts["scheme"]);
        $host = strtolower(rtrim($parts["host"] ?? "", "."));
        if (
            in_array(
                $host,
                ["t.me", "www.t.me", "telegram.me", "www.telegram.me"],
                true,
            )
        ) {
            throw new DomainError(
                "Telegram 链接请选择对应的 Telegram 资源类型。",
                422,
                [
                    "url" => "「网站」仅收录普通网站，请调整资源类型",
                ],
            );
        }
        if (preg_match('/[^\x00-\x7f]/', $host)) {
            $host = function_exists("idn_to_ascii")
                ? idn_to_ascii($host, IDNA_DEFAULT, INTL_IDNA_VARIANT_UTS46)
                : false;
        }
        $ip = trim((string) $host, "[]");
        $isIp = filter_var($ip, FILTER_VALIDATE_IP);
        if (
            !$host ||
            ($isIp &&
                !filter_var(
                    $ip,
                    FILTER_VALIDATE_IP,
                    FILTER_FLAG_NO_PRIV_RANGE | FILTER_FLAG_NO_RES_RANGE,
                )) ||
            (!$isIp &&
                (!filter_var(
                    $host,
                    FILTER_VALIDATE_DOMAIN,
                    FILTER_FLAG_HOSTNAME,
                ) ||
                    !str_contains($host, ".") ||
                    preg_match(
                        '/\.(?:local|localhost|internal|test|invalid)$/iD',
                        $host,
                    ) ||
                    preg_match('/^[\d.]+$/D', $host)))
        ) {
            self::invalid();
        }
        $port = $parts["port"] ?? null;
        if ($port !== null && ($port < 1 || $port > 65535)) {
            self::invalid();
        }
        $url =
            $scheme .
            "://" .
            $host .
            ($port &&
            !(
                ($scheme === "http" && $port === 80) ||
                ($scheme === "https" && $port === 443)
            )
                ? ":" . $port
                : "") .
            ($parts["path"] ?? "" ?: "/") .
            (isset($parts["query"]) ? "?" . $parts["query"] : "");
        if (
            !mb_check_encoding($url, "UTF-8") ||
            mb_strlen($url) > 500 ||
            preg_match("/%(?![0-9a-f]{2})/i", $url)
        ) {
            self::invalid();
        }
        return $url;
    }

    private static function invalid(): never
    {
        throw new DomainError(
            "请填写有效的公开网站地址（http/https，最多 500 字）。",
            422,
            [
                "url" => "不支持本地地址、账号密码或非网页协议",
            ],
        );
    }
}
