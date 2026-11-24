<?php
/** Real MySQL, simulated Telegram group events. All fixture writes roll back. */
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();
set_exception_handler(static function (Throwable $e): never {
    fwrite(STDERR, $e->getMessage() . PHP_EOL);
    exit(1);
});

use app\common\service\{BotSearchSettings, ResourceService};
use app\common\telegram\{Bot, Transport};
use think\facade\Db;

final class GroupTransport implements Transport
{
    public array $calls = [];
    public int $messageId = 17000;
    public function call(string $method, array $params = []): mixed
    {
        $this->calls[] = [$method, $params];
        return $method === "sendMessage"
            ? ["message_id" => ++$this->messageId]
            : true;
    }
    public function last(string $method): array
    {
        $calls = array_values(
            array_filter($this->calls, fn($c) => $c[0] === $method),
        );
        return $calls ? $calls[array_key_last($calls)][1] : [];
    }
}

$checks = 0;
function groupCheck(bool $ok, string $name): void
{
    global $checks;
    if (!$ok) {
        throw new RuntimeException("FAIL " . $name);
    }
    $checks++;
    echo "PASS " . $name . PHP_EOL;
}
function groupSession(string $id): array
{
    return Db::name("bot_sessions")->where("id", $id)->find();
}
function groupCallback(array $session, int $userId, string $action): array
{
    return [
        "callback_query" => [
            "id" => "group-test-" . bin2hex(random_bytes(4)),
            "from" => ["id" => $userId, "first_name" => "Group test"],
            "message" => [
                "chat" => ["id" => $session["chat_id"], "type" => "supergroup"],
                "message_id" => $session["message_id"],
            ],
            "data" =>
                "s:" .
                $session["id"] .
                ":" .
                $session["revision"] .
                ":" .
                $action,
        ],
    ];
}

Db::startTrans();
try {
    BotSearchSettings::save(12, 0);
    $tag = "group_" . bin2hex(random_bytes(4));
    $owner = random_int(84000000000, 84999999900);
    $other = $owner + 1;
    $chat = -$owner;
    for ($i = 0; $i < 25; $i++) {
        ResourceService::save([
            "title" => $tag . " " . $i,
            "url" => "https://t.me/" . $tag . "_" . $i,
            "type" => $i % 2 ? "channel" : "group",
        ]);
    }
    $fake = new GroupTransport();
    $bot = new Bot($fake, "GroupSearchBot");
    $message = [
        "message_id" => 30,
        "chat" => ["id" => $chat, "type" => "group"],
        "from" => ["id" => $owner, "first_name" => "Search owner"],
        "text" => $tag,
    ];
    $bot->handle(["message" => $message]);
    $sent = $fake->last("sendMessage");
    $session = Db::name("bot_sessions")
        ->where("message_id", $fake->messageId)
        ->where("chat_id", $chat)
        ->find();
    groupCheck(
        (int) $session["telegram_user_id"] === $owner &&
            (int) $session["chat_id"] === $chat,
        "group search binds a real user and a signed group ID",
    );
    groupCheck(
        $sent["reply_parameters"]["message_id"] === 30 &&
            $sent["reply_parameters"]["allow_sending_without_reply"],
        "result replies to its keyword message",
    );
    groupCheck(
        !isset($sent["message_thread_id"]),
        "ordinary group omits forum topic",
    );
    groupCheck(
        substr_count($sent["text"], '<a href="') === 12,
        "group uses configured twelve-row pagination",
    );
    groupCheck(
        str_contains($sent["text"], "仅搜索者本人可操作"),
        "group result explains control ownership",
    );
    $buttons = array_merge(...$sent["reply_markup"]["inline_keyboard"]);
    groupCheck(
        count(
            array_filter(
                $buttons,
                fn($b) => str_starts_with(
                    $b["callback_data"],
                    "s:" . $session["id"] . ":",
                ),
            ),
        ) === count($buttons),
        "every result control is session-bound including help",
    );
    groupCheck(
        in_array("搜索帮助", array_column($buttons, "text"), true) &&
            !in_array("返回主菜单", array_column($buttons, "text"), true),
        "group has safe help instead of private account menu",
    );

    // A second searcher in the same chat gets a completely independent session.
    $second = $message;
    $second["from"]["id"] = $other;
    $second["message_id"] = 31;
    $second["chat"]["type"] = "supergroup";
    $bot->handle(["message" => $second]);
    $otherSession = Db::name("bot_sessions")
        ->where("message_id", $fake->messageId)
        ->where("chat_id", $chat)
        ->find();
    groupCheck(
        $session["id"] !== $otherSession["id"] &&
            (int) $otherSession["telegram_user_id"] === $other,
        "same-keyword searches remain separate for two members",
    );

    $beforeLogs = Db::name("search_logs")->where("query", $tag)->count();
    $beforeLimits = Db::name("rate_limits")->sum("hits");
    foreach (
        ["p2", "p1", "tgroup", "tchannel", "t", "f1", "f0", "h"]
        as $action
    ) {
        $beforeCalls = count($fake->calls);
        $bot->handle(groupCallback($session, $other, $action));
        $answer = $fake->last("answerCallbackQuery");
        groupCheck(
            count($fake->calls) === $beforeCalls + 1 &&
                $fake->calls[array_key_last($fake->calls)][0] ===
                    "answerCallbackQuery" &&
                $answer["show_alert"] === true &&
                $answer["cache_time"] === 0 &&
                str_contains($answer["text"], "不能操作别人的搜索结果"),
            "foreign " . $action . " shows a private rejection alert only",
        );
    }
    groupCheck(
        groupSession($session["id"]) === $session &&
            groupSession($otherSession["id"]) === $otherSession,
        "unauthorized controls cannot mutate either member's session",
    );
    groupCheck(
        Db::name("search_logs")->where("query", $tag)->count() ===
            $beforeLogs &&
            Db::name("rate_limits")->sum("hits") === $beforeLimits,
        "denied clicks neither run a search nor consume the owner's quota",
    );
    $bot->handle(groupCallback($session, $owner, "p2"));
    $session = groupSession($session["id"]);
    groupCheck(
        (int) $session["page"] === 2 && (int) $session["revision"] === 2,
        "owner can advance to page two",
    );
    $edited = $fake->last("editMessageText");
    groupCheck(
        (int) $edited["chat_id"] === $chat &&
            (int) $edited["message_id"] === (int) $session["message_id"],
        "pagination edits only the owner's original result",
    );
    $bot->handle(groupCallback($session, $owner, "f1"));
    $session = groupSession($session["id"]);
    groupCheck(
        (int) $session["filters_expanded"] === 1 &&
            (int) $session["page"] === 2,
        "owner expands categories without changing page",
    );
    foreach (
        array_merge(
            ...$fake->last("editMessageText")["reply_markup"][
                "inline_keyboard"
            ],
        )
        as $button
    ) {
        $click = groupCallback($session, $other, "h");
        $click["callback_query"]["data"] = $button["callback_data"];
        $before = count($fake->calls);
        $bot->handle($click);
        groupCheck(
            count($fake->calls) === $before + 1 &&
                $fake->last("answerCallbackQuery")["show_alert"],
            "expanded control rejects another member: " . $button["text"],
        );
    }
    $bot->handle(groupCallback($session, $owner, "p1"));
    $session = groupSession($session["id"]);
    groupCheck(
        (int) $session["page"] === 1,
        "owner can return to the previous page",
    );
    $bot->handle(groupCallback($session, $owner, "tchannel"));
    $session = groupSession($session["id"]);
    groupCheck(
        $session["type"] === "channel" && (int) $session["page"] === 1,
        "owner switches category",
    );
    $bot->handle(groupCallback($session, $owner, "f0"));
    $session = groupSession($session["id"]);
    groupCheck(
        (int) $session["filters_expanded"] === 0,
        "owner collapses categories",
    );
    $before = count($fake->calls);
    $bot->handle(groupCallback($session, $owner, "h"));
    $help = $fake->last("answerCallbackQuery");
    groupCheck(
        count($fake->calls) === $before + 1 &&
            $help["show_alert"] &&
            str_contains($help["text"], "/search@GroupSearchBot") &&
            mb_strlen($help["text"]) <= 200 &&
            groupSession($session["id"]) === $session,
        "group help stays private and does not replace the result",
    );
    groupCheck(
        groupSession($otherSession["id"]) === $otherSession,
        "owner operations never touch another person's search",
    );

    foreach (["chat", "message", "revision"] as $field) {
        $click = groupCallback($session, $owner, "p2");
        if ($field === "chat") {
            $click["callback_query"]["message"]["chat"]["id"]--;
        } elseif ($field === "message") {
            $click["callback_query"]["message"]["message_id"]++;
        } else {
            $click["callback_query"]["data"] = "s:" . $session["id"] . ":1:p2";
        }
        $before = count($fake->calls);
        $bot->handle($click);
        groupCheck(
            count($fake->calls) === $before + 1 &&
                groupSession($session["id"]) === $session,
            "rejects mismatched " . $field . " without an edit",
        );
    }
    Db::name("bot_sessions")
        ->where("id", $session["id"])
        ->update(["expires_at" => "2000-01-01 00:00:00"]);
    $bot->handle(groupCallback($session, $owner, "p2"));
    groupCheck(
        str_contains($fake->last("answerCallbackQuery")["text"], "已过期") &&
            $fake->last("answerCallbackQuery")["show_alert"],
        "expired owner search gets a recovery alert",
    );
    $bot->handle(groupCallback($session, $other, "p2"));
    groupCheck(
        str_contains(
            $fake->last("answerCallbackQuery")["text"],
            "不能操作别人的",
        ),
        "foreign owner is rejected even for expired results",
    );

    $topic = $message;
    $topic["chat"]["type"] = "supergroup";
    $topic["message_id"] = 40;
    $topic["message_thread_id"] = 17;
    $topic["text"] = "/search@groupsearchbot " . $tag;
    $bot->handle(["message" => $topic]);
    $sent = $fake->last("sendMessage");
    groupCheck(
        $sent["reply_parameters"]["message_id"] === 40 &&
            $sent["message_thread_id"] === 17 &&
            str_contains($sent["text"], "搜索：" . $tag),
        "addressed command is case-insensitive and replies in the same forum topic",
    );
    $topic["text"] = "/search " . $tag;
    $before = $fake->messageId;
    $bot->handle(["message" => $topic]);
    groupCheck(
        $fake->messageId === $before + 1,
        "unaddressed search command works when delivered",
    );
    foreach (
        [
            "/search@AnotherBot " . $tag,
            "/start@AnotherBot",
            "/help@AnotherBot",
            "/discover@AnotherBot",
            "/add@AnotherBot https://t.me/example",
            "/submit@AnotherBot https://t.me/example",
            "/unknown",
        ]
        as $text
    ) {
        $topic["text"] = $text;
        $before = count($fake->calls);
        $bot->handle(["message" => $topic]);
        groupCheck(
            count($fake->calls) === $before,
            "ignores unrelated group command: " . explode(" ", $text)[0],
        );
    }
    $topic["text"] = "/search@GroupSearchBot";
    $bot->handle(["message" => $topic]);
    groupCheck(
        str_contains($fake->last("sendMessage")["text"], "指令后输入关键词") &&
            $fake->last("sendMessage")["message_thread_id"] === 17,
        "empty search command gives topic-aware usage",
    );
    $topic["text"] = str_repeat("长", 121);
    $bot->handle(["message" => $topic]);
    groupCheck(
        str_contains($fake->last("sendMessage")["text"], "最多 120 字") &&
            $fake->last("sendMessage")["reply_parameters"]["message_id"] === 40,
        "validation errors reply to the originating message",
    );
    foreach (["start", "help", "add", "submit"] as $command) {
        $topic["text"] = "/" . $command . "@GroupSearchBot";
        $bot->handle(["message" => $topic]);
        $sent = $fake->last("sendMessage");
        groupCheck(
            !isset($sent["reply_markup"]) &&
                str_contains($sent["text"], "私聊"),
            "group " . $command . " does not expose private menu actions",
        );
    }
    $beforeSubmissions = Db::name("submissions")->count();
    $beforeSessions = Db::name("bot_sessions")->count();
    $topic["text"] =
        "/add@GroupSearchBot https://t.me/" . $tag . "_add | 群内投稿";
    $bot->handle(["message" => $topic]);
    $sent = $fake->last("sendMessage");
    groupCheck(
        str_contains($sent["text"], "私聊机器人发送 /add") &&
            $sent["reply_parameters"]["message_id"] === 40 &&
            $sent["message_thread_id"] === 17 &&
            Db::name("submissions")->count() === $beforeSubmissions &&
            Db::name("bot_sessions")->count() === $beforeSessions,
        "group /add with arguments replies in its topic and creates no application or search",
    );
    $topic["text"] = "/discover@GroupSearchBot";
    $bot->handle(["message" => $topic]);
    groupCheck(
        str_contains($fake->last("sendMessage")["text"], "发现资源"),
        "group discover command is supported",
    );

    $anonymous = $topic;
    $anonymous["text"] = "/search@GroupSearchBot " . $tag;
    $anonymous["sender_chat"] = $anonymous["chat"];
    $anonymous["from"] = ["id" => 1087968824, "is_bot" => true];
    $before = Db::name("bot_sessions")->count();
    $bot->handle(["message" => $anonymous]);
    groupCheck(
        Db::name("bot_sessions")->count() === $before &&
            str_contains($fake->last("sendMessage")["text"], "个人身份"),
        "anonymous admins cannot own a search through the synthetic sender",
    );
    foreach (
        [
            "bot",
            "channel",
            "automatic_forward",
            "anonymous_text",
            "service",
            "missing_user",
        ]
        as $case
    ) {
        $ignored = $message;
        if ($case === "bot") {
            $ignored["from"]["is_bot"] = true;
        } elseif ($case === "channel") {
            $ignored["chat"]["type"] = "channel";
        } elseif ($case === "automatic_forward") {
            $ignored["is_automatic_forward"] = true;
        } elseif ($case === "anonymous_text") {
            $ignored["sender_chat"] = $message["chat"];
        } elseif ($case === "service") {
            unset($ignored["text"]);
            $ignored["new_chat_members"] = [
                ["id" => 8974065497, "is_bot" => true],
            ];
        } else {
            unset($ignored["from"]);
        }
        $before = count($fake->calls);
        $bot->handle(["message" => $ignored]);
        groupCheck(
            count($fake->calls) === $before,
            "does not create searches for " . $case,
        );
    }

    $private = $message;
    $private["chat"] = ["id" => $owner, "type" => "private"];
    $private["text"] = "/search@GroupSearchBot " . $tag;
    $bot->handle(["message" => $private]);
    $sent = $fake->last("sendMessage");
    $privateSession = Db::name("bot_sessions")
        ->where("chat_id", $owner)
        ->where("message_id", $fake->messageId)
        ->find();
    groupCheck(
        ($sent["reply_parameters"]["message_id"] ?? 0) ===
            $private["message_id"] &&
            $sent["reply_parameters"]["allow_sending_without_reply"] === true &&
            !str_contains($sent["text"], "仅搜索者本人"),
        "private command search replies to the user's original message",
    );
    $before = count($fake->calls);
    $bot->handle(groupCallback($privateSession, $other, "h"));
    groupCheck(
        count($fake->calls) === $before + 1 &&
            $fake->last("answerCallbackQuery")["show_alert"],
        "private return-home is also owner-checked",
    );
    $before = $fake->messageId;
    $click = groupCallback($privateSession, $owner, "h");
    $click["callback_query"]["message"]["chat"]["type"] = "private";
    $bot->handle($click);
    groupCheck(
        $fake->messageId === $before + 1 &&
            str_starts_with(
                $fake->last("sendMessage")["reply_markup"][
                    "inline_keyboard"
                ][0][0]["callback_data"],
                "m:",
            ),
        "private owner can return to the configured main menu",
    );

    Db::name("users")
        ->where("telegram_id", $other)
        ->update(["status" => 0]);
    $before = count($fake->calls);
    $bot->handle(groupCallback($otherSession, $other, "p2"));
    groupCheck(
        count($fake->calls) === $before + 1 &&
            str_contains(
                $fake->last("answerCallbackQuery")["text"],
                "已停用",
            ) &&
            groupSession($otherSession["id"]) === $otherSession,
        "disabled users cannot operate existing group sessions",
    );
    $before = Db::name("bot_sessions")->count();
    $bot->handle(["message" => $second]);
    groupCheck(
        Db::name("bot_sessions")->count() === $before &&
            str_contains($fake->last("sendMessage")["text"], "已停用"),
        "disabled users cannot start new group searches",
    );
    echo "\n" .
        $checks .
        " group checks passed; all test writes rolled back.\n";
} finally {
    Db::rollback();
}
