<?php
namespace app\common\telegram;
class TelegramError extends \RuntimeException
{
    public function __construct(
        public int $apiCode,
        public int $retryAfter = 0,
        public bool $notModified = false,
        public bool $replyNotFound = false,
    ) {
        parent::__construct(
            "Telegram 请求未完成（" . $apiCode . "）",
            $apiCode,
        );
    }
    public static function fromResponse(
        int $apiCode,
        #[\SensitiveParameter] string $description,
        int $retryAfter = 0,
    ): self {
        $description = strtolower(trim($description));
        // Keep only classifications, never the raw response or a credential URL.
        return new self(
            $apiCode,
            $retryAfter,
            str_contains($description, "message is not modified"),
            $apiCode === 400 &&
                $description ===
                    "bad request: message to be replied not found",
        );
    }
}
