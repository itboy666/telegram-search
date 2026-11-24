<?php
namespace app\common\telegram;

class Client implements Transport
{
    public function __construct(
        #[\SensitiveParameter] private string $token,
        private ?string $credentialVersion = null,
        private int $requestTimeout = 20,
    ) {
        if (!preg_match('/^\d+:[A-Za-z0-9_-]+$/', $token)) {
            throw new \RuntimeException(
                "请先在后台「机器人连接」配置有效的 Bot Token。",
            );
        }
    }
    public static function configured(): self
    {
        $credentials = \app\common\service\BotCredentials::snapshot();
        return new self($credentials["token"], $credentials["version"]);
    }
    public function call(string $method, array $params = []): mixed
    {
        $multipart = false;
        foreach ($params as $value) {
            if (
                $value instanceof \CURLStringFile ||
                $value instanceof \CURLFile
            ) {
                $multipart = true;
            }
        }
        $body = $multipart
            ? array_map(
                fn($value) => is_array($value)
                    ? json_encode($value, JSON_UNESCAPED_UNICODE)
                    : $value,
                $params,
            )
            : json_encode($params ?: new \stdClass(), JSON_UNESCAPED_UNICODE);
        $curl = curl_init(
            "https://api.telegram.org/bot" . $this->token . "/" . $method,
        );
        curl_setopt_array($curl, [
            CURLOPT_POST => true,
            CURLOPT_POSTFIELDS => $body,
            CURLOPT_HTTPHEADER => $multipart
                ? []
                : ["Content-Type: application/json"],
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_CONNECTTIMEOUT => 10,
            CURLOPT_TIMEOUT => max(
                $this->requestTimeout,
                (int) ($params["timeout"] ?? 0) + 10,
            ),
            CURLOPT_SSL_VERIFYPEER => true,
            CURLOPT_SSL_VERIFYHOST => 2,
        ]);
        $raw = curl_exec($curl);
        $code = (int) curl_getinfo($curl, CURLINFO_HTTP_CODE);
        $errno = curl_errno($curl);
        curl_close($curl);
        // Neither curl errors nor request URLs are logged: both can contain credentials.
        if ($raw === false || $errno) {
            throw new TelegramError(0);
        }
        $json = json_decode($raw, true);
        if (!is_array($json) || empty($json["ok"])) {
            throw TelegramError::fromResponse(
                (int) ($json["error_code"] ?? $code),
                (string) ($json["description"] ?? ""),
                (int) ($json["parameters"]["retry_after"] ?? 0),
            );
        }
        $result = $json["result"];
        if (
            in_array($method, ["sendMessage", "editMessageText"], true) &&
            is_array($result)
        ) {
            $checks = EmojiDelivery::inspect($params, $result);
            if ($checks) {
                try {
                    $previous =
                        \app\common\service\BotState::read()["custom_emoji"] ??
                        [];
                    \app\common\service\BotState::write(
                        [
                            "custom_emoji" => array_merge($previous, $checks),
                        ],
                        $this->credentialVersion,
                    );
                } catch (\Throwable) {
                    // A diagnostic write must never retry an already delivered message.
                }
            }
        }
        return $result;
    }
    public function download(string $path): string
    {
        if (
            !preg_match('#^[a-zA-Z0-9_/-]+\.[a-zA-Z0-9]+$#', $path) ||
            str_contains($path, "..")
        ) {
            throw new \RuntimeException("Telegram 文件路径无效。");
        }
        $curl = curl_init(
            "https://api.telegram.org/file/bot" . $this->token . "/" . $path,
        );
        curl_setopt_array($curl, [
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_CONNECTTIMEOUT => 10,
            CURLOPT_TIMEOUT => 30,
            CURLOPT_SSL_VERIFYPEER => true,
            CURLOPT_SSL_VERIFYHOST => 2,
        ]);
        $bytes = curl_exec($curl);
        $code = (int) curl_getinfo($curl, CURLINFO_HTTP_CODE);
        curl_close($curl);
        if ($bytes === false || $code !== 200) {
            throw new \RuntimeException("Telegram 图标下载失败。");
        }
        return $bytes;
    }
}
