<?php
namespace app\common\service;

use app\common\exception\DomainError;
use app\common\telegram\{Client, TelegramError, Transport};
use think\facade\Db;

/** The only source of Bot credentials. Never return snapshot() from an API. */
class BotCredentials
{
    public const SETTING = "telegram_bot_credentials";

    public static function snapshot(): array
    {
        $stored = Db::name("settings")
            ->where("name", self::SETTING)
            ->value("value");
        if ($stored === null) {
            $token = trim((string) config("telegram.token"));
            return [
                "token" => $token,
                "version" => hash("sha256", $token),
                "source" => $token === "" ? "none" : "environment",
                "bot" => [],
                "updated_at" => null,
            ];
        }
        try {
            $record = json_decode($stored, true, 512, JSON_THROW_ON_ERROR);
            if (
                !is_array($record) ||
                !is_string($record["encrypted"] ?? null) ||
                !is_string($record["version"] ?? null) ||
                !is_array($record["bot"] ?? null) ||
                !is_string($record["updated_at"] ?? null)
            ) {
                throw new \RuntimeException();
            }
            $bytes = base64_decode($record["encrypted"], true);
            if ($bytes === false || strlen($bytes) < 29) {
                throw new \RuntimeException();
            }
            $token = openssl_decrypt(
                substr($bytes, 28),
                "aes-256-gcm",
                self::key(false),
                OPENSSL_RAW_DATA,
                substr($bytes, 0, 12),
                substr($bytes, 12, 16),
                self::SETTING . ":" . $record["version"],
            );
            if ($token === false || $token === "") {
                throw new \RuntimeException();
            }
            return [
                "token" => $token,
                "version" => $record["version"],
                "source" => "database",
                "bot" => $record["bot"],
                "updated_at" => $record["updated_at"],
            ];
        } catch (\Throwable) {
            // Never silently switch back to an old .env token on key/data loss.
            throw new DomainError(
                "机器人凭据无法读取，请重新保存 Token，或恢复对应的 .bot-token.key。",
                503,
            );
        }
    }

    public static function current(string $version): bool
    {
        return hash_equals(self::snapshot()["version"], $version);
    }

    public static function verify(
        #[\SensitiveParameter] string $input,
        ?Transport $client = null,
    ): array {
        $token = trim($input);
        if (!preg_match('/^[1-9]\d{4,19}:[A-Za-z0-9_-]{20,128}$/D', $token)) {
            throw new DomainError(
                "请输入 BotFather 提供的完整 Bot Token。",
                422,
                [
                    "token" => "Token 格式不正确，请粘贴完整内容。",
                ],
            );
        }
        try {
            $me = ($client ?? new Client($token, null, 12))->call("getMe");
        } catch (TelegramError $e) {
            $message = match ($e->apiCode) {
                401, 404 => "Token 无效或已被撤销，请从 BotFather 获取后重试。",
                429 => "Telegram 验证请求过于频繁，请稍后重试。",
                default
                    => "暂时无法连接 Telegram，请检查网络后重试。原配置未修改。",
            };
            throw new DomainError($message, 422, ["token" => $message]);
        }
        if (
            !is_array($me) ||
            empty($me["is_bot"]) ||
            (string) ($me["id"] ?? "") !== explode(":", $token, 2)[0] ||
            empty($me["username"])
        ) {
            throw new DomainError(
                "Telegram 返回的机器人信息不匹配，原配置未修改。",
                422,
                [
                    "token" => "无法验证此机器人的身份。",
                ],
            );
        }
        return [
            "id" => (int) $me["id"],
            "username" => (string) $me["username"],
            "name" => (string) ($me["first_name"] ?? $me["username"]),
        ];
    }

    public static function save(
        #[\SensitiveParameter] string $input,
        int $actor,
        ?Transport $client = null,
    ): void {
        // Verify before any stored configuration changes.
        $bot = self::verify($input, $client);
        $version = bin2hex(random_bytes(16));
        $iv = random_bytes(12);
        $tag = "";
        $encrypted = openssl_encrypt(
            trim($input),
            "aes-256-gcm",
            self::key(true),
            OPENSSL_RAW_DATA,
            $iv,
            $tag,
            self::SETTING . ":" . $version,
        );
        if ($encrypted === false) {
            throw new DomainError("凭据加密失败，原配置未修改。", 503);
        }
        $value = json_encode(
            [
                "encrypted" => base64_encode($iv . $tag . $encrypted),
                "version" => $version,
                "bot" => $bot,
                "updated_at" => date("Y-m-d H:i:s"),
            ],
            JSON_UNESCAPED_UNICODE | JSON_THROW_ON_ERROR,
        );
        Db::transaction(function () use ($value, $actor, $bot) {
            Db::execute(
                "INSERT INTO ts_settings (name,value) VALUES (?,?) ON DUPLICATE KEY UPDATE value=?",
                [self::SETTING, $value, $value],
            );
            Settings::audit(
                "更新机器人 Token",
                "已验证并保存 @" .
                    $bot["username"] .
                    "（Bot ID " .
                    $bot["id"] .
                    "）",
                $actor,
            );
        });
    }

    private static function key(bool $create): string
    {
        $file = root_path() . ".bot-token.key";
        if (!$create && !is_file($file)) {
            throw new \RuntimeException("机器人加密密钥缺失。");
        }
        $mask = umask(0077);
        try {
            $handle = fopen($file, $create ? "c+b" : "rb");
        } finally {
            umask($mask);
        }
        if ($handle === false) {
            throw new DomainError("无法访问机器人加密密钥文件。", 503);
        }
        try {
            if (!flock($handle, $create ? LOCK_EX : LOCK_SH)) {
                throw new DomainError("无法锁定机器人加密密钥文件。", 503);
            }
            $key = stream_get_contents($handle);
            if ($key === "" && $create) {
                $key = random_bytes(32);
                if (fwrite($handle, $key) !== 32 || !fflush($handle)) {
                    throw new DomainError("无法保存机器人加密密钥。", 503);
                }
            }
            if (strlen($key) !== 32) {
                throw new DomainError("机器人加密密钥格式不正确。", 503);
            }
            return $key;
        } finally {
            flock($handle, LOCK_UN);
            fclose($handle);
        }
    }
}
