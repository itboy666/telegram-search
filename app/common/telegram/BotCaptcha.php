<?php
namespace app\common\telegram;

use app\common\service\{BotSecuritySettings, RateLimit};
use app\common\exception\DomainError;
use think\facade\Db;

class BotCaptcha
{
    public function __construct(
        private Transport $client,
        private int $botId,
    ) {}

    public function trusted(int $chat, int $uid): bool
    {
        $p = BotSecuritySettings::policy($this->botId, $chat);
        if ($p["mode"] === "off") {
            return true;
        }
        $row = Db::name("bot_verifications")
            ->where($this->key($chat, $uid))
            ->find();
        return $row &&
            $row["policy_hash"] === $p["hash"] &&
            (int) $row["verified_until"] > time();
    }
    public function throttle(int $chat, int $uid): void
    {
        $p = BotSecuritySettings::get();
        RateLimit::hit(
            "guard-search:{$this->botId}:$uid",
            $p["search_limit"],
            60,
        );
        if ($chat < 0) {
            RateLimit::hit(
                "guard-chat:{$this->botId}:$chat",
                $p["group_limit"],
                60,
            );
        }
    }
    public function gate(
        int $chat,
        int $uid,
        string $query,
        array $context,
        bool $resuming = false,
    ): bool {
        if (mb_strlen($query) > 120) {
            throw new DomainError("搜索词最多 120 字。");
        }
        if ($this->trusted($chat, $uid)) {
            if (!$resuming) {
                $this->throttle($chat, $uid);
            }
            return true;
        }
        $p = BotSecuritySettings::policy($this->botId, $chat);
        $issued = Db::transaction(function () use (
            $chat,
            $uid,
            $query,
            $context,
            $p,
        ) {
            Db::execute(
                "INSERT IGNORE INTO ts_bot_verifications (bot_id,chat_id,user_id,answer_hashes,payload,reply_context,expires_at) VALUES (?,?,?,'[]','{}','{}',?)",
                [
                    $this->botId,
                    $chat,
                    $uid,
                    date("Y-m-d H:i:s", time() + 86400),
                ],
            );
            $r = Db::name("bot_verifications")
                ->where($this->key($chat, $uid))
                ->lock(true)
                ->find();
            if ((int) $r["blocked_until"] > time()) {
                throw new DomainError(
                    "验证失败次数过多，请在 " .
                        ((int) $r["blocked_until"] - time()) .
                        " 秒后重新发送关键词。",
                    429,
                );
            }
            if (
                $r["policy_hash"] === $p["hash"] &&
                $r["state"] === "pending" &&
                $r["challenge_until"] > time() &&
                $r["message_id"]
            ) {
                // Repeated keywords never create additional prompts or reset attempts.
                return null;
            }
            RateLimit::hit("guard-issue:{$this->botId}:$chat:$uid", 3, 60);
            $this->throttle($chat, $uid);
            $puzzle = CaptchaPuzzle::make($p["mode"]);
            $data = [
                "challenge_id" => bin2hex(random_bytes(16)),
                "policy_hash" => $p["hash"],
                "state" => "pending",
                "method" => $puzzle["method"],
                "answer_hashes" => json_encode(
                    array_map(
                        fn($answer) => hash("sha256", $answer),
                        $puzzle["answers"],
                    ),
                ),
                "payload" => json_encode(
                    [
                        "prompt" => $puzzle["prompt"],
                        "choices" => $puzzle["choices"],
                    ],
                    JSON_UNESCAPED_UNICODE,
                ),
                "pending_query" => $query,
                "reply_context" => json_encode($context),
                "thread_id" => (int) ($context["message_thread_id"] ?? 0),
                "message_id" => 0,
                "attempts" => 0,
                "step" => 0,
                "issued_at" => time(),
                "challenge_until" => time() + $p["challenge_seconds"],
                "verified_until" => 0,
                "blocked_until" => 0,
                "expires_at" => date(
                    "Y-m-d H:i:s",
                    time() + max(86400, $p["trust_minutes"] * 60 + 600),
                ),
            ];
            Db::name("bot_verifications")->where("id", $r["id"])->update($data);
            return ["row" => array_replace($r, $data), "puzzle" => $puzzle];
        });
        if (!$issued) {
            return false;
        }
        $r = $issued["row"];
        $puzzle = $issued["puzzle"];
        $text =
            "搜索前请完成验证 · 仅本人可操作\n\n" .
            $puzzle["prompt"] .
            "\n\n" .
            $p["challenge_seconds"] .
            " 秒内有效；最多答错 " .
            $p["max_attempts"] .
            " 次。通过后自动继续本次搜索。";
        $params = ["chat_id" => $chat] + $context;
        if ($puzzle["choices"]) {
            $buttons = [];
            foreach ($puzzle["choices"] as $key => $label) {
                $buttons[] = [
                    "text" => $label,
                    "callback_data" => "v:" . $r["challenge_id"] . ":" . $key,
                ];
            }
            $params["reply_markup"] = [
                "inline_keyboard" => array_chunk($buttons, 3),
            ];
        } else {
            $params["reply_markup"] = [
                "force_reply" => true,
                "selective" => true,
                "input_field_placeholder" => "输入验证答案",
            ];
        }
        if (isset($puzzle["photo"])) {
            $params["photo"] = new \CURLStringFile(
                $puzzle["photo"],
                "captcha.png",
                "image/png",
            );
            $params["caption"] = $text;
            $sent = $this->client->call("sendPhoto", $params);
        } else {
            $params["text"] = $text;
            $sent = $this->client->call("sendMessage", $params);
        }
        Db::name("bot_verifications")
            ->where("id", $r["id"])
            ->where("challenge_id", $r["challenge_id"])
            ->update(["message_id" => $sent["message_id"]]);
        return false;
    }
    public function callback(array $cb, callable $answer): ?array
    {
        if (
            !preg_match(
                '/^v:([a-f0-9]{32}):([a-f0-9]{12})$/D',
                (string) ($cb["data"] ?? ""),
                $m,
            )
        ) {
            $answer("验证按钮无效，请重新发送关键词。", true);
            return null;
        }
        $result = $this->verify(
            $m[1],
            (int) ($cb["from"]["id"] ?? 0),
            (int) ($cb["message"]["chat"]["id"] ?? 0),
            (int) ($cb["message"]["message_id"] ?? 0),
            (int) ($cb["message"]["message_thread_id"] ?? 0),
            $m[2],
            true,
        );
        $answer($result["text"], $result["status"] !== "passed");
        if ($result["status"] === "passed") {
            $this->finish($result["row"]);
            return $result["row"];
        }
        if ($result["status"] === "failed") {
            $this->finish($result["row"], $result["text"]);
        }
        return null;
    }
    public function text(array $m): array
    {
        $chat = (int) $m["chat"]["id"];
        $uid = (int) $m["from"]["id"];
        $r = Db::name("bot_verifications")
            ->where($this->key($chat, $uid))
            ->where("state", "pending")
            ->find();
        if (!$r || !in_array($r["method"], ["text", "math"], true)) {
            return ["handled" => false];
        }
        $replyId = (int) ($m["reply_to_message"]["message_id"] ?? 0);
        if (
            ($chat < 0 && $replyId !== (int) $r["message_id"]) ||
            ($replyId && $replyId !== (int) $r["message_id"])
        ) {
            return ["handled" => false];
        }
        if (str_starts_with(trim($m["text"]), "/")) {
            return ["handled" => false];
        }
        $result = $this->verify(
            $r["challenge_id"],
            $uid,
            $chat,
            (int) $r["message_id"],
            (int) ($m["message_thread_id"] ?? 0),
            strtoupper(trim($m["text"])),
            false,
        );
        if ($result["status"] === "passed" || $result["status"] === "failed") {
            $this->finish(
                $result["row"],
                $result["status"] === "passed" ? "" : $result["text"],
            );
        } else {
            try {
                RateLimit::hit(
                    "guard-answer-notice:{$this->botId}:$chat:$uid",
                    1,
                    5,
                );
                $this->client->call(
                    "sendMessage",
                    [
                        "chat_id" => $chat,
                        "text" => $result["text"],
                        "reply_parameters" => [
                            "message_id" => $m["message_id"],
                            "allow_sending_without_reply" => true,
                        ],
                    ] +
                        (!empty($m["message_thread_id"])
                            ? ["message_thread_id" => $m["message_thread_id"]]
                            : []),
                );
            } catch (DomainError) {
            }
        }
        return [
            "handled" => true,
            "resume" => $result["status"] === "passed" ? $result["row"] : null,
        ];
    }
    private function verify(
        string $id,
        int $uid,
        int $chat,
        int $message,
        int $thread,
        string $value,
        bool $button,
    ): array {
        return Db::transaction(function () use (
            $id,
            $uid,
            $chat,
            $message,
            $thread,
            $value,
            $button,
        ) {
            $r = Db::name("bot_verifications")
                ->where(["bot_id" => $this->botId, "challenge_id" => $id])
                ->lock(true)
                ->find();
            $error = fn($text) => ["status" => "error", "text" => $text];
            if (!$r) {
                return $error("验证已失效，请重新发送关键词。");
            }
            if ((int) $r["user_id"] !== $uid) {
                return $error("不能操作别人的验证，请发送自己的搜索关键词。");
            }
            if (
                (int) $r["chat_id"] !== $chat ||
                (int) $r["message_id"] !== $message ||
                (int) $r["thread_id"] !== $thread ||
                !$message
            ) {
                return $error("此验证不属于当前消息或话题。");
            }
            $p = BotSecuritySettings::policy($this->botId, $chat);
            if ($r["state"] !== "pending") {
                return $error(
                    $r["state"] === "verified"
                        ? "此验证已完成，请使用搜索结果。"
                        : "验证次数已用完，请稍后重新搜索。",
                );
            }
            if (
                $r["challenge_until"] <= time() ||
                $r["policy_hash"] !== $p["hash"]
            ) {
                Db::name("bot_verifications")
                    ->where("id", $r["id"])
                    ->update(["state" => "expired"]);
                return $error("验证已过期或规则已更新，请重新发送关键词。");
            }
            try {
                RateLimit::hit(
                    "guard-answer:{$this->botId}:$chat:$uid",
                    12,
                    60,
                );
            } catch (DomainError) {
                return $error("验证操作过于频繁，请稍后重试。");
            }
            $payload = json_decode($r["payload"], true);
            if (
                $button !== !in_array($r["method"], ["text", "math"], true) ||
                ($button && !array_key_exists($value, $payload["choices"]))
            ) {
                return $error("请按本题提示完成验证。");
            }
            $hashes = json_decode($r["answer_hashes"], true);
            if (
                !hash_equals($hashes[(int) $r["step"]], hash("sha256", $value))
            ) {
                $attempts = (int) $r["attempts"] + 1;
                $failed = $attempts >= $p["max_attempts"];
                Db::name("bot_verifications")
                    ->where("id", $r["id"])
                    ->update([
                        "attempts" => $attempts,
                        "step" => 0,
                        "state" => $failed ? "failed" : "pending",
                        "blocked_until" => $failed
                            ? time() + $p["cooldown_seconds"]
                            : 0,
                    ]);
                return [
                    "status" => $failed ? "failed" : "wrong",
                    "row" => $r,
                    "text" => $failed
                        ? "答错次数已用完，请在 " .
                            $p["cooldown_seconds"] .
                            " 秒后重新搜索。"
                        : "答案不正确，还可尝试 " .
                            ($p["max_attempts"] - $attempts) .
                            " 次。" .
                            ($r["method"] === "sequence"
                                ? "请从第一个 emoji 重新开始。"
                                : ""),
                ];
            }
            if ((int) $r["step"] + 1 < count($hashes)) {
                Db::name("bot_verifications")
                    ->where("id", $r["id"])
                    ->update(["step" => (int) $r["step"] + 1]);
                return [
                    "status" => "progress",
                    "text" =>
                        "已完成 " .
                        ((int) $r["step"] + 1) .
                        "/" .
                        count($hashes) .
                        "，继续点击下一个。",
                ];
            }
            Db::name("bot_verifications")
                ->where("id", $r["id"])
                ->update([
                    "state" => "verified",
                    "verified_until" => time() + $p["trust_minutes"] * 60,
                ]);
            return [
                "status" => "passed",
                "text" => "验证通过，正在继续搜索。",
                "row" => $r,
            ];
        });
    }
    private function finish(array $r, string $text = ""): void
    {
        $text = $text ?: "验证通过，正在继续搜索。";
        $photo = $r["method"] === "text";
        try {
            $this->client->call(
                $photo ? "editMessageCaption" : "editMessageText",
                [
                    "chat_id" => $r["chat_id"],
                    "message_id" => $r["message_id"],
                    $photo ? "caption" : "text" => $text,
                    "reply_markup" => ["inline_keyboard" => []],
                ],
            );
        } catch (TelegramError $e) {
            if (!in_array($e->apiCode, [400, 403], true)) {
                throw $e;
            }
        }
    }
    private function key(int $chat, int $uid): array
    {
        return [
            "bot_id" => $this->botId,
            "chat_id" => $chat,
            "user_id" => $uid,
        ];
    }
}
