<?php
/** Real MySQL search with mock Telegram transport; all test writes are rolled back. */
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();
set_exception_handler(static function (Throwable $e): never {
    fwrite(STDERR, $e->getMessage() . PHP_EOL);
    exit(1);
});

use app\common\exception\DomainError;
use app\common\service\{
    BotSearchMigration,
    BotSearchSettings,
    ResourceService,
    SearchService,
};
use app\common\telegram\{Bot, Icons, TelegramError, Transport};
use think\facade\Db;

final class SearchTransport implements Transport
{
    public array $calls = [];
    public int $messageId = 7000;
    public bool $failEdit = false;
    public bool $missingReply = false;

    public function call(string $method, array $params = []): mixed
    {
        $this->calls[] = [$method, $params];
        if ($this->missingReply && $method === "sendMessage") {
            $this->missingReply = false;
            throw TelegramError::fromResponse(
                400,
                "Bad Request: message to be replied not found",
            );
        }
        if ($this->failEdit && $method === "editMessageText") {
            $this->failEdit = false;
            throw new TelegramError(429, 1);
        }
        return $method === "sendMessage"
            ? ["message_id" => ++$this->messageId]
            : true;
    }
    public function last(string $method): array
    {
        $calls = array_values(
            array_filter($this->calls, fn($call) => $call[0] === $method),
        );
        return $calls ? $calls[array_key_last($calls)][1] : [];
    }
    public function edits(): int
    {
        return count(
            array_filter(
                $this->calls,
                fn($call) => $call[0] === "editMessageText",
            ),
        );
    }
}

$checks = 0;
function searchCheck(bool $passed, string $label): void
{
    global $checks;
    if (!$passed) {
        throw new RuntimeException("FAIL " . $label);
    }
    $checks++;
    echo "PASS " . $label . PHP_EOL;
}
function searchLinks(array $message): array
{
    preg_match_all('/<a href="([^"]+)">/', $message["text"], $links);
    return $links[1];
}

searchCheck(
    BotSearchMigration::run()["already_applied"],
    "migration is repeatable",
);
Db::startTrans();
try {
    Db::name("settings")->where("name", BotSearchSettings::SETTING)->delete();
    searchCheck(
        BotSearchSettings::pageSize() === 12,
        "new installations default to twelve results",
    );
    foreach (
        [0, 31, -1, 1.5, true, [], null, "", "12.5", "1e1", "12x"]
        as $value
    ) {
        $rejected = false;
        try {
            BotSearchSettings::save($value, 0);
        } catch (DomainError $e) {
            $rejected = $e->status === 422 && isset($e->fields["page_size"]);
        }
        searchCheck(
            $rejected,
            "invalid page size rejected: " . json_encode($value),
        );
    }
    foreach ([1, "12", 30] as $value) {
        BotSearchSettings::save($value, 0);
        searchCheck(
            BotSearchSettings::pageSize() === (int) $value,
            "valid page size persists: " . $value,
        );
    }
    BotSearchSettings::save(12, 0);
    $tag = "search_" . bin2hex(random_bytes(4));
    $uid = random_int(82000000000, 82999999999);
    for ($i = 0; $i < 31; $i++) {
        ResourceService::save([
            "title" =>
                $i === 30
                    ? "紧凑\n标题 & <测试> " . str_repeat("👩‍💻", 30)
                    : str_repeat("标题<&>😀 ", 15) . $i,
            "url" => "https://t.me/" . $tag . "_" . $i,
            "tags" => $tag,
            "description" => "采集时在线人数：10。 " . $tag,
            "type" => "group",
            "members" => 12345,
        ]);
    }
    $fake = new SearchTransport();
    $bot = new Bot($fake);
    $message = [
        "message_id" => 101,
        "text" => $tag,
        "from" => ["id" => $uid, "first_name" => "Search test"],
        "chat" => ["id" => $uid, "type" => "private"],
    ];
    $bot->handle(["message" => $message]);
    $sent = $fake->last("sendMessage");
    searchCheck(
        ($sent["reply_parameters"]["message_id"] ?? 0) === 101 &&
            $sent["reply_parameters"]["allow_sending_without_reply"] === true,
        "private keyword search replies to the exact triggering message",
    );
    $session = Db::name("bot_sessions")
        ->where(["telegram_user_id" => $uid, "message_id" => $fake->messageId])
        ->find();
    searchCheck(
        count(searchLinks($sent)) === 12,
        "first page actually renders twelve links",
    );
    searchCheck(
        (int) $session["page_size"] === 12 &&
            !(int) $session["filters_expanded"],
        "new session snapshots size and collapsed state",
    );
    searchCheck(
        !str_contains($sent["text"], "采集时在线人数") &&
            !str_contains($sent["text"], "description"),
        "collection descriptions omitted from Bot message",
    );
    searchCheck(
        str_contains($sent["text"], "紧凑 标题 &amp; &lt;测试&gt;"),
        "titles collapse whitespace and escape HTML",
    );
    searchCheck(
        str_contains($sent["text"], "12.3 K") &&
            str_contains($sent["text"], "共 31 条结果，当前第 1 页，共 3 页"),
        "member counts and full page summary render",
    );
    $lines = array_filter(
        explode("\n", $sent["text"]),
        fn($line) => str_contains($line, '<a href="'),
    );
    searchCheck(
        count($lines) === 12 && !str_contains(implode("\n", $lines), "\n\n"),
        "one logical line per result without description gaps",
    );
    $rows = $sent["reply_markup"]["inline_keyboard"];
    searchCheck(
        array_column($rows[0], "text") === ["群组", "频道", "视频", "更多筛选"],
        "collapsed first row has four buttons in requested order",
    );
    searchCheck(
        count($rows) === 2 &&
            array_column($rows[1], "text") === ["返回主菜单", "下一页"],
        "compact navigation follows the filters",
    );
    searchCheck(
        $sent["link_preview_options"]["is_disabled"] &&
            $sent["parse_mode"] === "HTML",
        "compact results keep disabled previews and linked HTML",
    );
    $firstLinks = searchLinks($sent);
    $callback = function (string $action, ?array $override = null) use (
        $uid,
        &$session,
        $bot,
    ): void {
        $cb = [
            "id" => "search-test",
            "from" => ["id" => $uid],
            "message" => [
                "chat" => ["id" => $uid],
                "message_id" => $session["message_id"],
            ],
            "data" =>
                "s:" .
                $session["id"] .
                ":" .
                $session["revision"] .
                ":" .
                $action,
        ];
        $bot->handle([
            "callback_query" => array_replace_recursive($cb, $override ?? []),
        ]);
        $session = Db::name("bot_sessions")
            ->where("id", $session["id"])
            ->find();
    };
    $callback("f1", ["from" => ["id" => $uid + 1]]);
    searchCheck($fake->edits() === 0, "filter expansion verifies owner");
    $callback("f1", ["message" => ["chat" => ["id" => $uid + 1]]]);
    searchCheck($fake->edits() === 0, "filter expansion verifies chat");
    $callback("f1", ["message" => ["message_id" => 123]]);
    searchCheck($fake->edits() === 0, "filter expansion verifies message");
    $callback("f1");
    $edited = $fake->last("editMessageText");
    $rows = $edited["reply_markup"]["inline_keyboard"];
    searchCheck(
        (int) $session["filters_expanded"] === 1 &&
            (int) $session["page"] === 1,
        "expand preserves current page",
    );
    searchCheck(
        searchLinks($edited) === $firstLinks,
        "expand preserves result list",
    );
    searchCheck(
        count($rows[0]) === 4 &&
            array_column($rows[1], "text") === [
                "全部",
                "音乐",
                "图文",
                "其他",
                "店铺",
            ],
        "expanded second row has five buttons",
    );
    searchCheck(
        array_column($rows[2], "text") === ["机器人", "文件", "网站"],
        "remaining resource types stay reachable",
    );
    $allButtons = array_merge(...$rows);
    $typeActions = array_filter(
        array_column($allButtons, "callback_data"),
        fn($data) => preg_match('/:t[a-z]*$/', $data),
    );
    searchCheck(
        count($typeActions) === count(ResourceService::TYPES) + 1,
        "all categories and all-types filter are present",
    );
    $knownIcons = array_column(Icons::catalog(), "id");
    searchCheck(
        count(
            array_filter(
                $allButtons,
                fn($b) => in_array(
                    $b["icon_custom_emoji_id"] ?? "",
                    $knownIcons,
                    true,
                ),
            ),
        ) === count($allButtons),
        "every control uses the Telegram iOS icon pack",
    );
    $before = $fake->edits();
    $callback("f0", ["data" => "s:" . $session["id"] . ":1:f0"]);
    searchCheck(
        $fake->edits() === $before,
        "stale toggle cannot edit the message",
    );
    BotSearchSettings::save(5, 0);
    $callback("p2");
    $secondLinks = searchLinks($fake->last("editMessageText"));
    searchCheck(
        (int) $session["page_size"] === 12 &&
            count($secondLinks) === 12 &&
            !array_intersect($firstLinks, $secondLinks),
        "settings changes do not skip or duplicate existing session pages",
    );
    searchCheck(
        (int) $session["page"] === 2 &&
            (int) $session["filters_expanded"] === 1,
        "paging preserves expanded filters",
    );
    searchCheck(
        (int) $fake->last("editMessageText")["message_id"] ===
            (int) $session["message_id"] &&
            !isset($fake->last("editMessageText")["reply_parameters"]),
        "paging edits the original result without replacing its reply context",
    );
    $callback("f0");
    searchCheck(
        (int) $session["page"] === 2 &&
            searchLinks($fake->last("editMessageText")) === $secondLinks,
        "collapse preserves page and results",
    );
    $callback("p1");
    searchCheck(
        searchLinks($fake->last("editMessageText")) === $firstLinks,
        "previous page returns original links",
    );
    $callback("p3");
    searchCheck(
        count(searchLinks($fake->last("editMessageText"))) === 7,
        "last page displays remaining results",
    );
    searchCheck(
        !in_array(
            "下一页",
            array_column(
                array_merge(
                    ...$fake->last("editMessageText")["reply_markup"][
                        "inline_keyboard"
                    ],
                ),
                "text",
            ),
            true,
        ),
        "last page has no next button",
    );
    $callback("tgroup");
    searchCheck(
        (int) $session["page"] === 1 &&
            $session["type"] === "group" &&
            (int) $session["page_size"] === 12,
        "type switch resets only the page",
    );
    $callback("tchannel");
    searchCheck(
        str_contains($fake->last("editMessageText")["text"], "暂时没有") &&
            count(searchLinks($fake->last("editMessageText"))) === 0,
        "empty category remains navigable",
    );
    $callback("t");
    $fake->failEdit = true;
    $revision = $session["revision"];
    try {
        $callback("f1");
    } catch (TelegramError $e) {
        searchCheck(
            $e->apiCode === 429,
            "Telegram edit failure propagates for retry",
        );
    }
    $session = Db::name("bot_sessions")->where("id", $session["id"])->find();
    searchCheck(
        $session["revision"] === $revision &&
            !(int) $session["filters_expanded"],
        "failed edit does not advance session or filter state",
    );
    $callback("f1");
    searchCheck(
        (int) $session["filters_expanded"] === 1,
        "failed toggle can be retried",
    );
    Db::name("bot_sessions")
        ->where("id", $session["id"])
        ->update(["expires_at" => "2000-01-01 00:00:00"]);
    $before = $fake->edits();
    $callback("f0");
    searchCheck(
        $fake->edits() === $before,
        "expired filters cannot update results",
    );
    $bot->handle(["message" => $message]);
    searchCheck(
        count(searchLinks($fake->last("sendMessage"))) === 5,
        "new search picks up changed admin setting",
    );
    searchCheck(
        new SearchService()->search(["q" => $tag], "web", false, false)[
            "page_size"
        ] === 12,
        "Bot setting does not change website or API page size",
    );

    // Insert exactly like the previous worker: database defaults preserve its five-row pages.
    $legacyId = bin2hex(random_bytes(12));
    Db::name("bot_sessions")->insert([
        "id" => $legacyId,
        "telegram_user_id" => $uid,
        "chat_id" => $uid,
        "message_id" => 8000,
        "query" => $tag,
        "expires_at" => date("Y-m-d H:i:s", time() + 600),
    ]);
    $session = Db::name("bot_sessions")->where("id", $legacyId)->find();
    searchCheck(
        (int) $session["page_size"] === 5 &&
            (int) $session["filters_expanded"] === 1,
        "legacy sessions retain original defaults",
    );
    $callback("p2");
    searchCheck(
        (int) $session["page"] === 2 &&
            count(searchLinks($fake->last("editMessageText"))) === 5,
        "legacy next-page callbacks remain usable",
    );

    BotSearchSettings::save(30, 0);
    $bot->handle(["message" => $message]);
    $large = $fake->last("sendMessage");
    $visible = html_entity_decode(
        strip_tags($large["text"]),
        ENT_QUOTES | ENT_HTML5,
        "UTF-8",
    );
    searchCheck(
        count(searchLinks($large)) === 30,
        "maximum setting displays thirty actual results",
    );
    searchCheck(
        strlen(mb_convert_encoding($visible, "UTF-16LE", "UTF-8")) / 2 <= 4096,
        "thirty long emoji and HTML titles fit Telegram message limit",
    );
    searchCheck(
        mb_check_encoding($large["text"], "UTF-8") &&
            !str_contains($large["text"], "\u{FFFD}"),
        "truncation preserves valid Unicode",
    );
    searchCheck(
        str_contains(
            new SearchService()->search(["q" => $tag], "web", false, false)[
                "items"
            ][0]["description"],
            "采集时在线人数",
        ),
        "original stored descriptions remain available outside Bot rendering",
    );

    $render = new ReflectionMethod(Bot::class, "render");
    $layoutItems = [
        ["title" => str_repeat("中文标题", 12), "members" => 0],
        ["title" => str_repeat("中文标题", 12), "members" => 12345],
        ["title" => "سینما توییتر فارسی ⚫", "members" => 12345],
        ["title" => "\u{202E}Safe\u{2069}\n\tTitle\u{200F}", "members" => 0],
        ["title" => str_repeat("👩‍💻🇨🇳e\u{0301}", 20), "members" => 0],
        ["title" => "Bot News", "members" => 0],
        ["title" => "می\u{200C}روم", "members" => 0],
    ];
    foreach ($layoutItems as $index => &$item) {
        $item += [
            "type" => "channel",
            "url" => "https://t.me/layout_fixture_" . $index,
        ];
    }
    unset($item);
    $layoutMessage = $render->invoke($bot, $session, [
        "items" => $layoutItems,
        "total" => count($layoutItems),
        "total_pages" => 1,
        "has_more" => false,
    ]);
    preg_match_all(
        '/<a href="[^"]+">([^<]*)<\/a>/u',
        $layoutMessage["text"],
        $layoutTitles,
    );
    $layoutTitles = array_map(
        fn($title) => html_entity_decode($title, ENT_QUOTES | ENT_HTML5, "UTF-8"),
        $layoutTitles[1],
    );
    searchCheck(
        str_ends_with($layoutTitles[0], "…") &&
            mb_strwidth($layoutTitles[0], "UTF-8") > 60 &&
            mb_strwidth($layoutTitles[0], "UTF-8") <= 80 &&
            mb_strwidth($layoutTitles[1] . "  12.3 K", "UTF-8") <= 80 &&
            mb_strlen($layoutTitles[1]) < mb_strlen($layoutTitles[0]),
        "long CJK titles show more context with bounded width and room for member counts",
    );
    $layoutLines = array_values(
        array_filter(
            explode("\n", $layoutMessage["text"]),
            fn($line) => str_contains($line, '<a href="'),
        ),
    );
    searchCheck(
        count($layoutLines) === count($layoutItems) &&
            count(
                array_filter(
                    $layoutLines,
                    fn($line) => str_starts_with($line, "\u{200E}<tg-emoji") &&
                        str_contains($line, "\u{2068}<a ") &&
                        str_contains($line, "</a>\u{2069}\u{200E}"),
                ),
            ) === count($layoutItems),
        "every result anchors its icon on an LTR row and isolates the title direction",
    );
    searchCheck(
        $layoutTitles[2] === $layoutItems[2]["title"] &&
            str_ends_with($layoutLines[2], "\u{2069}\u{200E}  12.3 K"),
        "Persian title retains native text while the member count stays outside its direction",
    );
    searchCheck(
        $layoutTitles[3] === "Safe Title",
        "imported bidi overrides and line breaks cannot disrupt other results",
    );
    searchCheck(
        (bool) preg_match(
            '/^(?:👩‍💻🇨🇳e\x{0301})+(?:👩‍💻(?:🇨🇳)?)?…$/u',
            $layoutTitles[4],
        ),
        "joined emoji, flags and combining accents remain whole when truncated",
    );
    searchCheck(
        $layoutTitles[5] === "Bot News" &&
            $layoutTitles[6] === $layoutItems[6]["title"] &&
            searchLinks($layoutMessage) === array_column($layoutItems, "url"),
        "short titles, Persian joining and all destination links remain intact",
    );
    searchCheck(
        str_contains(
            new SearchService()->search(["q" => $tag], "web", false, false)[
                "items"
            ][0]["title"],
            str_repeat("👩‍💻", 30),
        ),
        "list-only truncation leaves the full stored title intact",
    );
    $limitMessage = $render->invoke(
        $bot,
        array_replace($session, ["page" => 100]),
        [
            "items" => [],
            "total" => 3010,
            "total_pages" => 602,
            "has_more" => true,
        ],
    );
    searchCheck(
        str_contains($limitMessage["text"], "共 602 页") &&
            str_contains($limitMessage["text"], "可浏览前 100 页"),
        "page limit is explicit without understating total results",
    );
    searchCheck(
        !in_array(
            "下一页",
            array_column(
                array_merge(
                    ...$limitMessage["reply_markup"]["inline_keyboard"],
                ),
                "text",
            ),
            true,
        ),
        "no next-page control beyond the existing search limit",
    );
    foreach (["/search " . $tag, "/discover"] as $offset => $text) {
        $trigger = array_replace($message, [
            "message_id" => 201 + $offset,
            "text" => $text,
            "reply_to_message" => ["message_id" => 99],
        ]);
        $bot->handle(["message" => $trigger]);
        searchCheck(
            $fake->last("sendMessage")["reply_parameters"]["message_id"] ===
                $trigger["message_id"],
            "command replies to the trigger, not the message it quotes: " .
                strtok($text, " "),
        );
    }
    $withoutId = $message;
    unset($withoutId["message_id"]);
    $bot->handle(["message" => $withoutId]);
    searchCheck(
        !isset($fake->last("sendMessage")["reply_parameters"]) &&
            str_contains($fake->last("sendMessage")["text"], "搜索："),
        "missing incoming message ID sends a normal search result",
    );

    BotSearchSettings::save(12, 0);
    $before = count($fake->calls);
    $sessionsBefore = Db::name("bot_sessions")->count();
    $logsBefore = Db::name("search_logs")->count();
    $hitsBefore = (int) Db::name("rate_limits")->sum("hits");
    $fake->missingReply = true;
    $bot->handle(["message" => array_replace($message, ["message_id" => 303])]);
    $attempts = array_slice($fake->calls, $before);
    $plain = $attempts[0][1] ?? [];
    unset($plain["reply_parameters"]);
    searchCheck(
        count($attempts) === 2 &&
            $attempts[0][0] === "sendMessage" &&
            $attempts[0][1]["reply_parameters"]["message_id"] === 303 &&
            $attempts[1] === ["sendMessage", $plain],
        "deleted keyword falls back once with identical search content and controls",
    );
    $session = Db::name("bot_sessions")
        ->where(["telegram_user_id" => $uid, "message_id" => $fake->messageId])
        ->find();
    searchCheck(
        $session &&
            Db::name("bot_sessions")->count() === $sessionsBefore + 1 &&
            Db::name("search_logs")->count() === $logsBefore + 1 &&
            (int) Db::name("rate_limits")->sum("hits") === $hitsBefore + 2,
        "fallback records one search, one result message and one set of quota hits",
    );
    $deliveredId = $fake->messageId;
    $callback("p2");
    searchCheck(
        (int) $session["page"] === 2 &&
            (int) $fake->last("editMessageText")["message_id"] === $deliveredId &&
            $fake->messageId === $deliveredId,
        "fallback result remains pageable without sending another result",
    );
    echo "ALL $checks BOT SEARCH CHECKS PASSED" . PHP_EOL;
} finally {
    Db::rollback();
    echo "All search fixtures and settings rolled back." . PHP_EOL;
}
