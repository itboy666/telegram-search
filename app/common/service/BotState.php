<?php
namespace app\common\service;
class BotState
{
    public static function read(): array
    {
        try {
            return self::load(BotCredentials::snapshot());
        } catch (\app\common\exception\DomainError $e) {
            // Keep the configuration form and public site usable if a key needs recovery.
            return [
                "configured" => true,
                "running" => false,
                "state" => "error",
                "last_error" => $e->getMessage(),
                "token" => [
                    "configured" => true,
                    "source" => "database",
                    "updated_at" => null,
                ],
            ];
        }
    }
    private static function load(array $credentials): array
    {
        $file = root_path() . "runtime/bot.json";
        $state = is_file($file)
            ? json_decode(file_get_contents($file), true)
            : [];
        $state = is_array($state) ? $state : [];
        $tokenId = explode(":", $credentials["token"])[0];
        if (isset($state["id"]) && (string) $state["id"] !== $tokenId) {
            $state = [];
        }
        if (
            ($state["credential_version"] ??
                hash("sha256", trim((string) config("telegram.token")))) !==
            $credentials["version"]
        ) {
            // The same bot keeps its cursor; old errors/emoji diagnostics do not.
            $state = array_intersect_key(
                $state,
                array_flip(["id", "offset", "last_update_at"]),
            );
        }
        $state = array_replace($credentials["bot"], $state);
        $state["checked_at"] ??= $credentials["updated_at"];
        $state["configured"] = $credentials["token"] !== "";
        $state["token"] = [
            "configured" => $state["configured"],
            "source" => $credentials["source"],
            "updated_at" => $credentials["updated_at"],
        ];
        $state["running"] =
            ($state["state"] ?? "") === "running" &&
            (int) ($state["heartbeat"] ?? 0) > time() - 70;
        return $state;
    }
    public static function write(array $state, ?string $version = null): bool
    {
        $dir = root_path() . "runtime/";
        if (!is_dir($dir)) {
            mkdir($dir, 0755, true);
        }
        $lock = fopen($dir . "bot-state.lock", "c+");
        try {
            if (!flock($lock, LOCK_EX)) {
                throw new \RuntimeException("无法锁定机器人状态。");
            }
            $credentials = BotCredentials::snapshot();
            if ($version !== null && $credentials["version"] !== $version) {
                return false;
            }
            $saved = array_merge(self::load($credentials), $state);
            $saved["credential_version"] = $credentials["version"];
            unset($saved["token"]);
            $file = $dir . "bot.json";
            $temp = $file . "." . getmypid() . ".tmp";
            if (
                file_put_contents(
                    $temp,
                    json_encode(
                        $saved,
                        JSON_UNESCAPED_UNICODE | JSON_PRETTY_PRINT,
                    ),
                    LOCK_EX,
                ) === false ||
                !rename($temp, $file)
            ) {
                throw new \RuntimeException("无法保存机器人状态。");
            }
            return true;
        } finally {
            flock($lock, LOCK_UN);
            fclose($lock);
        }
    }
}
