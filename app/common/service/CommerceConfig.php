<?php
namespace app\common\service;

use app\common\exception\DomainError;
use think\facade\Db;

class CommerceConfig
{
    public const DEFAULTS = [
        "trc20_enabled" => false,
        "receive_address" => "",
        "offline_enabled" => false,
        "offline_instructions" => "",
        "reward_enabled" => false,
        "reward_amount" => "0.010000",
        "reward_daily_cap" => "10.000000",
        "reward_user_cap" => "1.000000",
        "public_url" => "http://127.0.0.1:8088",
    ];

    public static function read(): array
    {
        $row = Db::name("commerce_config")->where("id", 1)->find();
        return array_replace(
            self::DEFAULTS,
            json_decode($row["config"] ?? "{}", true),
        ) + ["key_configured" => !empty($row["secret"])];
    }

    public static function save(array $input, ?int $actor = null): array
    {
        return CatalogTransaction::run(function () use ($input, $actor) {
            Db::name("commerce_config")->where("id", 1)->lock(true)->find();
            $old = self::read();
            $data = array_replace(
                $old,
                array_intersect_key($input, self::DEFAULTS),
            );
            foreach (
                ["trc20_enabled", "offline_enabled", "reward_enabled"]
                as $key
            ) {
                $data[$key] = filter_var($data[$key], FILTER_VALIDATE_BOOL);
            }
            foreach (
                ["reward_amount", "reward_daily_cap", "reward_user_cap"]
                as $key
            ) {
                $data[$key] = Money::format(Money::parse($data[$key]));
            }
            $data["receive_address"] = trim((string) $data["receive_address"]);
            if (
                $data["receive_address"] !== "" &&
                !Tronscan::validAddress($data["receive_address"])
            ) {
                throw new DomainError("TRC20 收款地址校验失败。");
            }
            $data["offline_instructions"] = trim(
                (string) $data["offline_instructions"],
            );
            if (mb_strlen($data["offline_instructions"]) > 2000) {
                throw new DomainError("收款说明最多 2000 字。");
            }
            $url = rtrim(trim((string) $data["public_url"]), "/");
            if (
                !filter_var($url, FILTER_VALIDATE_URL) ||
                !in_array(
                    parse_url($url, PHP_URL_SCHEME),
                    ["http", "https"],
                    true,
                ) ||
                parse_url($url, PHP_URL_USER) ||
                parse_url($url, PHP_URL_QUERY) ||
                parse_url($url, PHP_URL_FRAGMENT)
            ) {
                throw new DomainError(
                    "请输入网站根地址，例如 https://example.com。",
                );
            }
            $data["public_url"] = $url;
            $change = [];
            if (trim((string) ($input["api_key"] ?? "")) !== "") {
                $key = trim((string) $input["api_key"]);
                if (!preg_match('/^[A-Za-z0-9_-]{16,128}$/D', $key)) {
                    throw new DomainError("Tronscan API Key 格式不正确。");
                }
                $iv = random_bytes(12);
                $tag = "";
                $cipher = openssl_encrypt(
                    $key,
                    "aes-256-gcm",
                    self::key(),
                    OPENSSL_RAW_DATA,
                    $iv,
                    $tag,
                    "starindex-commerce",
                );
                if ($cipher === false) {
                    throw new DomainError("支付密钥加密失败。", 503);
                }
                $change["secret"] = base64_encode($iv . $tag . $cipher);
                $data["key_configured"] = true;
            }
            if (
                $data["trc20_enabled"] &&
                (!$data["receive_address"] || !$data["key_configured"])
            ) {
                throw new DomainError(
                    "启用链上充值前，请填写收款地址及 API Key。",
                );
            }
            if ($data["offline_enabled"] && !$data["offline_instructions"]) {
                throw new DomainError("请先填写线下转账收款说明。");
            }
            unset($data["key_configured"]);
            Db::name("commerce_config")
                ->where("id", 1)
                ->update(
                    $change + [
                        "config" => json_encode(
                            $data,
                            JSON_UNESCAPED_UNICODE | JSON_THROW_ON_ERROR,
                        ),
                        "updated_at" => date("Y-m-d H:i:s"),
                    ],
                );
            Settings::audit(
                "更新支付与奖励设置",
                "密钥仅加密保存；配置已更新",
                $actor,
            );
            return self::read();
        });
    }

    public static function secret(): string
    {
        $raw = Db::name("commerce_config")->where("id", 1)->value("secret");
        if (!$raw) {
            throw new DomainError("尚未配置 Tronscan API Key。", 503);
        }
        $raw = base64_decode($raw, true);
        if (
            $raw === false ||
            strlen($raw) < 29 ||
            !is_file(root_path() . ".commerce.key")
        ) {
            throw new DomainError(
                "支付密钥无法读取，请恢复 .commerce.key 或重新保存 API Key。",
                503,
            );
        }
        $value = openssl_decrypt(
            substr($raw, 28),
            "aes-256-gcm",
            self::key(),
            OPENSSL_RAW_DATA,
            substr($raw, 0, 12),
            substr($raw, 12, 16),
            "starindex-commerce",
        );
        if (!$value) {
            throw new DomainError("支付密钥无法解密，请重新保存。", 503);
        }
        return $value;
    }

    private static function key(): string
    {
        $mask = umask(0077);
        try {
            $fp = fopen(root_path() . ".commerce.key", "c+b");
        } finally {
            umask($mask);
        }
        if (!$fp) {
            throw new DomainError("无法读取支付加密密钥。", 503);
        }
        try {
            if (!flock($fp, LOCK_EX)) {
                throw new DomainError("无法锁定支付密钥。", 503);
            }
            $key = stream_get_contents($fp);
            if ($key === "") {
                $key = random_bytes(32);
                if (fwrite($fp, $key) !== 32 || !fflush($fp)) {
                    throw new DomainError("无法保存支付密钥。", 503);
                }
            }
            if (strlen($key) !== 32) {
                throw new DomainError("支付密钥文件损坏。", 503);
            }
            return $key;
        } finally {
            flock($fp, LOCK_UN);
            fclose($fp);
        }
    }
}
