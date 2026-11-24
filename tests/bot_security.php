<?php
/** Real MySQL, simulated Telegram. No messages or deletion requests leave this process. */
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();
use app\common\service\{BotSecuritySettings, ResourceService};
use app\common\telegram\{
    Bot,
    CaptchaPuzzle,
    GroupGuard,
    TelegramError,
    Transport,
};
use app\common\exception\DomainError;
use think\facade\Db;

set_exception_handler(static function (Throwable $e): never {
    fwrite(STDERR, $e->getMessage() . PHP_EOL);
    exit(1);
});
final class SecurityTransport implements Transport
{
    public array $calls = [];
    public array $admins = [];
    public int $message = 90000;
    public bool $lookupFails = false;
    public bool $deleteFails = false;
    public bool $canDelete = true;
    public array $missingMessages = [];
    public int $botId;
    public function call(string $method, array $params = []): mixed
    {
        $this->calls[] = [$method, $params];
        if (
            in_array($method, ["sendMessage", "sendPhoto"], true) &&
            in_array(
                $params["reply_parameters"]["message_id"] ?? null,
                $this->missingMessages,
                true,
            )
        ) {
            throw TelegramError::fromResponse(
                400,
                "Bad Request: message to be replied not found",
            );
        }
        if ($method === "getChatMember") {
            if ($this->lookupFails) {
                throw new TelegramError(400);
            }
            return [
                "status" =>
                    in_array($params["user_id"], $this->admins, true) ||
                    $params["user_id"] === $this->botId
                        ? "administrator"
                        : "member",
                "can_delete_messages" => $this->canDelete,
            ];
        }
        if ($method === "deleteMessage" && $this->deleteFails) {
            throw new TelegramError(403);
        }
        return in_array($method, ["sendMessage", "sendPhoto"], true)
            ? ["message_id" => ++$this->message]
            : true;
    }
    public function last(string $method = "sendMessage"): array
    {
        $items = array_values(
            array_filter($this->calls, fn($c) => $c[0] === $method),
        );
        return $items ? $items[array_key_last($items)][1] : [];
    }
    public function count(string $method): int
    {
        return count(array_filter($this->calls, fn($c) => $c[0] === $method));
    }
}
$checks = 0;
function guardCheck(bool $ok, string $message): void
{
    global $checks;
    if (!$ok) {
        throw new RuntimeException("FAIL " . $message);
    }
    $checks++;
    echo "PASS $message\n";
}
guardCheck(
    BotSecuritySettings::migrate()["already_applied"],
    "security migration is repeatable",
);
foreach (["emoji", "button", "sequence", "math", "text", "random"] as $method) {
    $p = CaptchaPuzzle::make($method);
    guardCheck(
        !empty($p["answers"]) && !empty($p["prompt"]),
        "$method puzzle has an answer and instructions",
    );
    if ($method === "text") {
        $size = getimagesizefromstring($p["photo"]);
        guardCheck(
            $size[0] === 300 &&
                $size[1] === 96 &&
                preg_match("/[A-Z]/", $p["answers"][0]) &&
                preg_match("/[2-9]/", $p["answers"][0]) &&
                !str_contains($p["prompt"], $p["answers"][0]),
            "text CAPTCHA contains a PNG and a mixed code that is absent from the caption",
        );
    }
}
$ops = [];
for ($i = 0; $i < 128; $i++) {
    $p = CaptchaPuzzle::make("math");
    preg_match("/(\d+) ([+−×÷]) (\d+)/u", $p["prompt"], $parts);
    $ops[$parts[2]] = true;
    $a = (int) $parts[1];
    $b = (int) $parts[3];
    $expected = match ($parts[2]) {
        "+" => $a + $b,
        "−" => $a - $b,
        "×" => $a * $b,
        "÷" => intdiv($a, $b),
    };
    if (
        $p["answers"][0] !== (string) $expected ||
        $expected < 0 ||
        ($parts[2] === "÷" && $a % $b)
    ) {
        throw new RuntimeException("Invalid arithmetic puzzle");
    }
}
guardCheck(
    count($ops) === 4,
    "all four arithmetic operators produce exact non-negative integer answers",
);
Db::startTrans();
try {
    $base = random_int(87000000000, 87999999000);
    $botId = $base + 100;
    $chat = -$base;
    $tag = "guard_" . bin2hex(random_bytes(4));
    $fake = new SecurityTransport();
    $fake->botId = $botId;
    $fake->admins = [$base + 1];
    $bot = new Bot($fake, "GuardBot", $botId);
    $msgId = 1;
    $send = function (
        int $uid,
        int $chatId,
        string $text,
        array $extra = [],
    ) use ($bot, &$msgId): void {
        $bot->handle([
            "message" => array_replace_recursive(
                [
                    "message_id" => ++$msgId,
                    "chat" => [
                        "id" => $chatId,
                        "type" => $chatId < 0 ? "supergroup" : "private",
                        "title" => "Security fixture",
                    ],
                    "from" => ["id" => $uid, "first_name" => "Fixture"],
                    "text" => $text,
                ],
                $extra,
            ),
        ]);
    };
    $row = fn($uid, $chatId) => Db::name("bot_verifications")
        ->where(["bot_id" => $botId, "chat_id" => $chatId, "user_id" => $uid])
        ->find();
    $click = function (
        array $r,
        string $choice,
        ?int $uid = null,
        array $extra = [],
    ) use ($bot): void {
        $bot->handle([
            "callback_query" => array_replace_recursive(
                [
                    "id" => bin2hex(random_bytes(8)),
                    "from" => ["id" => $uid ?? (int) $r["user_id"]],
                    "message" => [
                        "chat" => [
                            "id" => (int) $r["chat_id"],
                            "type" =>
                                $r["chat_id"] < 0 ? "supergroup" : "private",
                        ],
                        "message_id" => (int) $r["message_id"],
                        "message_thread_id" => (int) $r["thread_id"],
                    ],
                    "data" => "v:" . $r["challenge_id"] . ":" . $choice,
                ],
                $extra,
            ),
        ]);
    };
    $choice = function (array $r, int $step = 0): string {
        $hash = json_decode($r["answer_hashes"], true)[$step];
        foreach (json_decode($r["payload"], true)["choices"] as $key => $_) {
            if (hash_equals($hash, hash("sha256", $key))) {
                return $key;
            }
        }
        throw new RuntimeException("Missing challenge choice");
    };
    ResourceService::save(["title" => $tag, "url" => "https://t.me/" . $tag]);
    BotSecuritySettings::save(
        [
            "private_mode" => "emoji",
            "group_mode" => "emoji",
            "allow_group_override" => 1,
            "group_limit" => 20,
            "search_limit" => 60,
        ],
        0,
    );
    $uid = $base + 2;
    $before = Db::name("bot_sessions")->count();
    $send($uid, $uid, "/search " . $tag);
    $originalMessageId = $msgId;
    $r = $row($uid, $uid);
    guardCheck(
        $fake->last()["reply_parameters"]["message_id"] === $originalMessageId &&
            json_decode($r["reply_context"], true)["reply_parameters"][
                "message_id"
            ] === $originalMessageId,
        "private verification prompt stores the original keyword reply context",
    );
    guardCheck(
        $r &&
            $r["state"] === "pending" &&
            Db::name("bot_sessions")->count() === $before,
        "unverified search only sends a challenge and runs no search",
    );
    $sent = $fake->count("sendMessage");
    for ($i = 0; $i < 5; $i++) {
        $send($uid, $uid, $tag);
    }
    guardCheck(
        $fake->count("sendMessage") === $sent &&
            $row($uid, $uid)["challenge_id"] === $r["challenge_id"],
        "repeated keywords do not flood prompts or reset the active challenge",
    );
    $right = $choice($r);
    $click($r, $right, $uid + 10);
    guardCheck(
        str_contains(
            $fake->last("answerCallbackQuery")["text"],
            "别人的验证",
        ) && $row($uid, $uid)["attempts"] === 0,
        "other users cannot consume attempts or verify the owner",
    );
    $click($r, $right, null, ["message" => ["chat" => ["id" => $chat]]]);
    guardCheck(
        $row($uid, $uid)["state"] === "pending",
        "challenge cannot be replayed in another chat",
    );
    $click($r, $right, null, [
        "message" => ["message_id" => $r["message_id"] + 1],
    ]);
    guardCheck(
        $row($uid, $uid)["state"] === "pending",
        "challenge cannot be replayed from another message",
    );
    $click($r, $right);
    guardCheck(
        $row($uid, $uid)["state"] === "verified" &&
            Db::name("bot_sessions")->count() === $before + 1 &&
            str_contains($fake->last()["text"], $tag) &&
            $fake->last()["reply_parameters"]["message_id"] === $originalMessageId,
        "correct emoji grants scoped trust and resumes the original search",
    );
    $before = Db::name("bot_sessions")->count();
    $click($r, $right);
    guardCheck(
        Db::name("bot_sessions")->count() === $before,
        "completed challenge cannot replay the pending search",
    );
    $send($uid, $uid, $tag);
    guardCheck(
        Db::name("bot_sessions")->count() === $before + 1,
        "trusted search proceeds without another challenge",
    );
    $send($uid, $chat, $tag);
    guardCheck(
        $row($uid, $chat)["state"] === "pending",
        "private trust does not transfer into a group",
    );

    $uid = $base + 3;
    $send($uid, $uid, $tag);
    $r = $row($uid, $uid);
    $wrong = array_values(
        array_diff(array_keys(json_decode($r["payload"], true)["choices"]), [
            $choice($r),
        ]),
    )[0];
    for ($i = 0; $i < 3; $i++) {
        $click($r, $wrong);
    }
    $failed = $row($uid, $uid);
    guardCheck(
        $failed["state"] === "failed" && $failed["blocked_until"] > time(),
        "three wrong answers trigger a cooldown",
    );
    $send($uid, $uid, $tag);
    guardCheck(
        $row($uid, $uid)["challenge_id"] === $r["challenge_id"] &&
            str_contains($fake->last()["text"], "失败次数过多"),
        "a new search cannot reset a failed challenge during cooldown",
    );
    $click($r, $choice($r));
    guardCheck(
        $row($uid, $uid)["state"] === "failed",
        "a correct answer after lockout does not grant trust",
    );

    $uid = $base + 20;
    $originalMessageId = $msgId + 1;
    $fake->missingMessages = [$originalMessageId];
    $before = $fake->count("sendMessage");
    $sessionCount = Db::name("bot_sessions")->count();
    $send($uid, $uid, $tag);
    $r = $row($uid, $uid);
    guardCheck(
        $r["state"] === "pending" &&
            (int) $r["message_id"] === $fake->message &&
            !isset($fake->last()["reply_parameters"]) &&
            $fake->count("sendMessage") === $before + 2,
        "deleted keyword does not prevent delivering or saving its verification prompt",
    );
    $click($r, $choice($r));
    guardCheck(
        $row($uid, $uid)["state"] === "verified" &&
            $fake->count("sendMessage") === $before + 4 &&
            !isset($fake->last()["reply_parameters"]) &&
            str_contains($fake->last()["text"], $tag) &&
            Db::name("bot_sessions")->count() === $sessionCount + 1,
        "verification resumes one usable search when the keyword was already deleted",
    );
    $fake->missingMessages = [];

    BotSecuritySettings::save(["private_mode" => "sequence"], 0);
    $uid = $base + 4;
    $send($uid, $uid, $tag);
    $r = $row($uid, $uid);
    $click($r, $choice($r, 0));
    guardCheck(
        $row($uid, $uid)["step"] === 1 &&
            $row($uid, $uid)["state"] === "pending",
        "sequence requires all steps",
    );
    $click($r, $choice($r, 2));
    guardCheck(
        $row($uid, $uid)["step"] === 0 && $row($uid, $uid)["attempts"] === 1,
        "out-of-order click resets progress and consumes an attempt",
    );
    for ($i = 0; $i < 3; $i++) {
        $click($r, $choice($r, $i));
    }
    guardCheck(
        $row($uid, $uid)["state"] === "verified",
        "ordered emoji sequence completes verification",
    );
    BotSecuritySettings::save(["private_mode" => "math"], 0);
    $uid = $base + 5;
    $send($uid, $uid, $tag);
    $originalMessageId = $msgId;
    $r = $row($uid, $uid);
    preg_match("/(\d+) ([+−×÷]) (\d+)/u", $fake->last()["text"], $parts);
    $a = (int) $parts[1];
    $b = (int) $parts[3];
    $answer = match ($parts[2]) {
        "+" => $a + $b,
        "−" => $a - $b,
        "×" => $a * $b,
        "÷" => intdiv($a, $b),
    };
    $send($uid, $uid, (string) $answer);
    guardCheck(
        $row($uid, $uid)["state"] === "verified" &&
            $fake->last()["reply_parameters"]["message_id"] === $originalMessageId,
        "private arithmetic resumes a reply to the keyword, not the answer",
    );
    BotSecuritySettings::save(["private_mode" => "text"], 0);
    $uid = $base + 6;
    $send($uid, $uid, $tag);
    $r = $row($uid, $uid);
    guardCheck(
        $fake->last("sendPhoto")["photo"] instanceof CURLStringFile &&
            $r["method"] === "text",
        "image challenge is sent as an in-memory multipart PNG",
    );
    Db::name("bot_verifications")
        ->where("id", $r["id"])
        ->update(["answer_hashes" => json_encode([hash("sha256", "AB23CD")])]);
    $send($uid, $uid, "ab23cd");
    guardCheck(
        $row($uid, $uid)["state"] === "verified" &&
            $fake->count("editMessageCaption") > 0,
        "image code accepts case-insensitive input and updates the photo caption",
    );

    BotSecuritySettings::save(["group_mode" => "math"], 0);
    $uid = $base + 7;
    $send($uid, $chat, $tag, ["message_thread_id" => 17]);
    $originalMessageId = $msgId;
    $r = $row($uid, $chat);
    Db::name("bot_verifications")
        ->where("id", $r["id"])
        ->update(["answer_hashes" => json_encode([hash("sha256", "12")])]);
    $send($uid, $chat, "12", ["message_thread_id" => 17]);
    guardCheck(
        $row($uid, $chat)["state"] === "pending",
        "unrelated group numbers are not treated as CAPTCHA answers",
    );
    $send($uid, $chat, "12", [
        "message_thread_id" => 18,
        "reply_to_message" => ["message_id" => $r["message_id"]],
    ]);
    guardCheck(
        $row($uid, $chat)["state"] === "pending",
        "typed answers cannot cross forum topics",
    );
    $send($uid, $chat, "12", [
        "message_thread_id" => 17,
        "reply_to_message" => ["message_id" => $r["message_id"]],
    ]);
    guardCheck(
        $row($uid, $chat)["state"] === "verified" &&
            $fake->last()["message_thread_id"] === 17 &&
            $fake->last()["reply_parameters"]["message_id"] === $originalMessageId,
        "forum answer resumes the search replying to the original keyword in its topic",
    );
    BotSecuritySettings::save(["private_mode" => "button"], 0);
    $uid = $base + 8;
    $send($uid, $uid, $tag);
    $r = $row($uid, $uid);
    Db::name("bot_verifications")
        ->where("id", $r["id"])
        ->update(["challenge_until" => time() - 1]);
    $click($r, $choice($r));
    guardCheck(
        $row($uid, $uid)["state"] === "expired",
        "expired challenge cannot grant trust",
    );
    $send($uid, $uid, $tag);
    $r = $row($uid, $uid);
    BotSecuritySettings::save(["private_mode" => "emoji"], 0);
    $click($r, $choice($r));
    guardCheck(
        $row($uid, $uid)["state"] === "expired",
        "policy changes invalidate outstanding challenges",
    );
    $send($uid, $uid, $tag);
    $r = $row($uid, $uid);
    $foreign = new Bot($fake, "OtherBot", $botId + 1);
    $foreign->handle([
        "callback_query" => [
            "id" => "different-bot",
            "from" => ["id" => $uid],
            "message" => [
                "chat" => ["id" => $uid, "type" => "private"],
                "message_id" => $r["message_id"],
            ],
            "data" => "v:" . $r["challenge_id"] . ":" . $choice($r),
        ],
    ]);
    guardCheck(
        $row($uid, $uid)["state"] === "pending",
        "one bot cannot accept another bot's challenge",
    );

    $oldSession = Db::name("bot_sessions")
        ->where("telegram_user_id", $base + 2)
        ->where("chat_id", $base + 2)
        ->order("message_id", "desc")
        ->find();
    $beforeEdits = $fake->count("editMessageText");
    $bot->handle([
        "callback_query" => [
            "id" => "old-search",
            "from" => ["id" => $base + 2],
            "message" => [
                "chat" => ["id" => $base + 2, "type" => "private"],
                "message_id" => $oldSession["message_id"],
            ],
            "data" =>
                "s:" .
                $oldSession["id"] .
                ":" .
                $oldSession["revision"] .
                ":p1",
        ],
    ]);
    guardCheck(
        $fake->count("editMessageText") === $beforeEdits &&
            str_contains(
                $fake->last("answerCallbackQuery")["text"],
                "重新验证",
            ),
        "old search buttons cannot bypass a newly enabled verification policy",
    );

    $bot->handle([
        "my_chat_member" => [
            "chat" => [
                "id" => $chat,
                "type" => "supergroup",
                "title" => "Security fixture",
            ],
            "from" => ["id" => $base + 1],
            "old_chat_member" => ["status" => "left"],
            "new_chat_member" => [
                "user" => ["id" => $botId],
                "status" => "administrator",
                "can_delete_messages" => true,
            ],
        ],
    ]);
    $group = fn() => Db::name("bot_groups")
        ->where(["bot_id" => $botId, "chat_id" => $chat])
        ->find();
    guardCheck(
        (int) $group()["inviter_id"] === $base + 1 &&
            $group()["can_delete"] === 1,
        "join event records inviter and actual deletion rights",
    );
    $send($base + 9, $chat, "/captcha off");
    guardCheck(
        $group()["verify_mode"] === "inherit",
        "ordinary members cannot change group settings",
    );
    $send($base + 1, $chat, "/captcha sequence");
    guardCheck(
        $group()["verify_mode"] === "sequence" &&
            BotSecuritySettings::policy($botId, $chat)["mode"] === "sequence",
        "current administrator can override group verification",
    );
    BotSecuritySettings::save(
        ["allow_group_override" => 0, "group_mode" => "emoji"],
        0,
    );
    $send($base + 1, $chat, "/captcha off");
    guardCheck(
        BotSecuritySettings::policy($botId, $chat)["mode"] === "emoji" &&
            $group()["verify_mode"] === "sequence",
        "global enforcement cannot be disabled by a group command",
    );
    $fake->admins = [];
    $send($base + 1, $chat, "/ads on");
    guardCheck(
        $group()["ad_mode"] === "off",
        "inviter loses configuration authority immediately after demotion",
    );
    $fake->admins = [$base + 1];
    $fake->canDelete = false;
    $send($base + 1, $chat, "/ads on");
    guardCheck(
        $group()["ad_mode"] === "off",
        "enabling moderation requires real bot deletion rights",
    );
    $fake->canDelete = true;
    // A fresh administrator avoids the intentional six-command limit.
    $fake->admins[] = $base + 20;
    $send($base + 20, $chat, "/ads on");
    guardCheck(
        $group()["ad_mode"] === "links",
        "administrator enables explicit link-blocking rules",
    );
    $before = $fake->count("deleteMessage");
    $send($base + 9, $chat, "广告 https://ads.example/test");
    guardCheck(
        $fake->count("deleteMessage") === $before + 1 &&
            $group()["blocked_count"] === 1,
        "member advertisement is deleted and counted",
    );
    $send($base + 20, $chat, "公告 https://ads.example/test");
    guardCheck(
        $fake->count("deleteMessage") === $before + 1,
        "current administrators are exempt from link deletion",
    );
    $send($base + 20, $chat, "/allowdomain allowed.example");
    $send($base + 9, $chat, "https://docs.allowed.example/path");
    guardCheck(
        $fake->count("deleteMessage") === $before + 1,
        "allowed domain includes real subdomains",
    );
    $send($base + 9, $chat, "https://allowed.example.evil.test/path");
    guardCheck(
        $fake->count("deleteMessage") === $before + 2,
        "lookalike host cannot bypass the domain allowlist",
    );
    $send($base + 9, $chat, "点这里", [
        "entities" => [
            [
                "type" => "text_link",
                "offset" => 0,
                "length" => 3,
                "url" => "https://ads.example",
            ],
        ],
    ]);
    guardCheck(
        $fake->count("deleteMessage") === $before + 3,
        "hidden text links are inspected",
    );
    $bot->handle([
        "edited_message" => [
            "message_id" => 500,
            "edit_date" => time(),
            "chat" => ["id" => $chat, "type" => "supergroup"],
            "from" => ["id" => $base + 9],
            "caption" => "https://ads.example",
        ],
    ]);
    guardCheck(
        $fake->count("deleteMessage") === $before + 4,
        "edited media captions are moderated without triggering a search",
    );
    $fake->lookupFails = true;
    $send($base + 9, $chat, "https://ads.example");
    guardCheck(
        $fake->count("deleteMessage") === $before + 4 &&
            str_contains($group()["last_error"], "身份"),
        "uncertain administrator lookup skips deletion and records the reason",
    );
    $fake->lookupFails = false;
    $fake->deleteFails = true;
    $send($base + 9, $chat, "https://ads.example");
    guardCheck(
        $group()["blocked_count"] === 4 &&
            str_contains($group()["last_error"], "删除权限"),
        "failed deletion is not counted as successful",
    );
    $fake->deleteFails = false;
    $send($base + 20, $chat, "/ads keywords");
    $send($base + 20, $chat, "/adwords 违规推广|垃圾广告");
    $send($base + 9, $chat, "这是垃圾广告内容");
    guardCheck(
        $group()["blocked_count"] === 5,
        "literal administrator keywords are enforced",
    );
    $send($base + 20, $chat, "/flood on");
    $before = $group()["blocked_count"];
    for ($i = 0; $i < 4; $i++) {
        $send($base + 21, $chat, "同一段内容");
    }
    guardCheck(
        $group()["blocked_count"] === $before + 1,
        "fourth repeated message is deleted without warning-message spam",
    );

    guardCheck(
        BotSecuritySettings::policy($botId + 1, $chat)["mode"] === "emoji",
        "group rules are scoped to a bot identity",
    );
    BotSecuritySettings::save(
        ["private_mode" => "off", "search_limit" => 1],
        0,
    );
    $uid = $base + 40;
    $send($uid, $uid, $tag);
    $before = Db::name("bot_sessions")->count();
    $send($uid, $uid, $tag);
    $messages = $fake->count("sendMessage");
    $send($uid, $uid, $tag);
    guardCheck(
        Db::name("bot_sessions")->count() === $before &&
            $fake->count("sendMessage") === $messages,
        "rate limits apply with verification off and suppress repeated limit notices",
    );
    BotSecuritySettings::save(
        ["private_mode" => "button", "search_limit" => 1],
        0,
    );
    $uid = $base + 41;
    $send($uid, $uid, $tag);
    $r = $row($uid, $uid);
    $before = Db::name("bot_sessions")->count();
    $click($r, $choice($r));
    guardCheck(
        Db::name("bot_sessions")->count() === $before + 1,
        "passing verification resumes a reserved search even with a one-search budget",
    );
    $menu = \app\common\service\BotMenu::defaults();
    \app\common\service\BotMenu::save($menu, 0);
    $bot->handle([
        "callback_query" => [
            "id" => "menu-limited",
            "from" => ["id" => $uid],
            "message" => [
                "chat" => ["id" => $uid, "type" => "private"],
                "message_id" => 900,
            ],
            "data" =>
                "m:" . \app\common\service\BotMenu::version() . ":discover",
        ],
    ]);
    guardCheck(
        str_contains($fake->last("answerCallbackQuery")["text"], "过于频繁") &&
            $fake->last("answerCallbackQuery")["show_alert"],
        "menu searches handle rate rejection without failing the update worker",
    );
    foreach (
        [
            ["private_mode" => "invalid"],
            ["group_limit" => 21],
            ["trust_minutes" => 0],
            ["max_attempts" => 6],
        ]
        as $bad
    ) {
        $rejected = false;
        try {
            BotSecuritySettings::save($bad, 0);
        } catch (DomainError) {
            $rejected = true;
        }
        guardCheck($rejected, "invalid global security setting is rejected");
    }
    echo "\n$checks security checks passed; fixtures rolled back.\n";
} finally {
    Db::rollback();
}
