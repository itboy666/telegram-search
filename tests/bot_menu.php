<?php
// All database changes are rolled back. No Telegram requests or real messages.
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();
set_exception_handler(static function (Throwable $e): never {
    fwrite(STDERR, $e->getMessage() . PHP_EOL);
    exit(1);
});
use think\facade\Db;
use app\common\telegram\{Bot, Icons, Transport, EmojiDelivery};
use app\common\service\BotMenu;
use app\common\exception\DomainError;

final class MenuTransport implements Transport
{
    public array $calls = [];
    public function call(string $method, array $params = []): mixed
    {
        $this->calls[] = [$method, $params];
        return $method === "sendMessage"
            ? ["message_id" => count($this->calls)]
            : true;
    }
    public function sent(): array
    {
        return array_values(
            array_filter($this->calls, fn($c) => $c[0] === "sendMessage"),
        );
    }
    public function last(): array
    {
        return $this->calls[array_key_last($this->calls)][1];
    }
}
$checks = 0;
function checkMenu(bool $ok, string $name): void
{
    if (!$ok) {
        throw new RuntimeException("FAIL: " . $name);
    }
    global $checks;
    $checks++;
    echo "PASS " . $name . PHP_EOL;
}
function rejectsMenu(array $menu, string $name): void
{
    try {
        BotMenu::validate($menu);
    } catch (DomainError) {
        checkMenu(true, $name);
        return;
    }
    checkMenu(false, $name);
}
$probe = [
    "text" => '<tg-emoji emoji-id="6032850693348399258">🔎</tg-emoji>',
    "reply_markup" => [
        "inline_keyboard" => [
            [
                [
                    "text" => "搜索",
                    "icon_custom_emoji_id" => "6032850693348399258",
                ],
            ],
        ],
    ],
];
$retained = EmojiDelivery::inspect($probe, [
    "entities" => [
        ["type" => "custom_emoji", "custom_emoji_id" => "6032850693348399258"],
    ],
    "reply_markup" => $probe["reply_markup"],
]);
checkMenu(
    $retained["text"]["status"] === "retained" &&
        $retained["buttons"]["status"] === "retained",
    "Telegram retained icons are detected",
);
$stripped = EmojiDelivery::inspect($probe, [
    "text" => "🔎",
    "reply_markup" => ["inline_keyboard" => [[["text" => "搜索"]]]],
]);
checkMenu(
    $stripped["text"]["retained"] === 0 &&
        $stripped["buttons"]["status"] === "not_retained",
    "Telegram stripped icons are detected",
);
checkMenu(
    EmojiDelivery::inspect(["text" => "hello"], ["text" => "hello"]) === [],
    "plain messages do not erase icon diagnostics",
);
checkMenu(
    !str_contains(json_encode($stripped), "6032850693348399258"),
    "diagnostics contain no message or emoji identifiers",
);
Db::startTrans();
try {
    Db::name("settings")
        ->where("name", "submissions_enabled")
        ->update(["value" => "1"]);
    $uid = random_int(81000000000, 81999999999);
    $tag = "menu_" . bin2hex(random_bytes(4));
    $menu = BotMenu::defaults();
    $menu["welcome"] =
        "你好 {{nickname}}（{{username}}），ID {{user_id}} <script> & 欢迎\n{{datetime}}";
    $menu["hint"] =
        "语言 {{language_code}} / Premium {{is_premium}} / {{date}}";
    $menu["rows"][0][0]["text"] = "热门搜索";
    $newIcon = array_key_first(
        array_filter(
            Icons::catalog(),
            fn($name) => str_starts_with($name, "ios_"),
            ARRAY_FILTER_USE_KEY,
        ),
    );
    checkMenu(
        is_string($newIcon),
        "full-pack icons are available beyond legacy aliases",
    );
    $menu["welcome_icon"] = $newIcon;
    $menu["rows"][] = [
        [
            "id" => "custom",
            "text" => "自定义说明",
            "icon" => $newIcon,
            "action" => "message",
            "value" => "<b>原样文字</b>",
            "style" => "primary",
            "enabled" => true,
        ],
        [
            "id" => "website",
            "text" => "网站",
            "icon" => "link",
            "action" => "url",
            "value" => "https://t.me/telegram",
            "style" => "default",
            "enabled" => true,
        ],
        [
            "id" => "disabled",
            "text" => "隐藏按钮",
            "icon" => "star",
            "action" => "help",
            "value" => "",
            "style" => "default",
            "enabled" => false,
        ],
    ];
    $saved = BotMenu::save($menu, 0)["config"];
    checkMenu(
        BotMenu::get() === $saved,
        "menu settings survive database round trip",
    );
    $fake = new MenuTransport();
    $bot = new Bot($fake);
    $message = [
        "text" => "/start",
        "chat" => ["id" => $uid, "type" => "private"],
        "from" => [
            "id" => $uid,
            "first_name" => $tag . "<b>&",
            "last_name" => "同学",
            "username" => "menu_tester",
            "language_code" => "zh-hans",
            "is_premium" => true,
        ],
    ];
    $bot->handle(["message" => $message]);
    $start = $fake->last();
    checkMenu(
        str_contains($start["text"], "&lt;script&gt; &amp;"),
        "welcome text is HTML escaped",
    );
    checkMenu(
        str_contains(
            $start["text"],
            $tag . "&lt;b&gt;&amp; 同学（@menu_tester）",
        ) &&
            str_contains($start["text"], "ID " . $uid) &&
            str_contains($start["text"], "语言 zh-hans / Premium 是") &&
            preg_match(
                "/\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}/",
                $start["text"],
            ) &&
            !str_contains($start["text"], "{{"),
        "start resolves fresh user data and time while escaping names",
    );
    checkMenu(
        str_contains($start["text"], Icons::catalog()[$newIcon]["id"]),
        "welcome uses newly added original custom emoji ID",
    );
    checkMenu(
        $start["reply_markup"]["inline_keyboard"][0][0]["text"] === "热门搜索",
        "configured button label is used",
    );
    $lastRow = $start["reply_markup"]["inline_keyboard"][3];
    checkMenu(count($lastRow) === 2, "disabled button is omitted");
    checkMenu(
        $lastRow[0]["style"] === "primary" &&
            $lastRow[0]["icon_custom_emoji_id"] ===
                Icons::catalog()[$newIcon]["id"],
        "button color and newly added custom emoji emitted",
    );
    checkMenu(
        isset($lastRow[1]["url"]) && !isset($lastRow[1]["callback_data"]),
        "URL button has only URL action",
    );
    $cb = [
        "id" => "menu-test",
        "from" => $message["from"],
        "message" => ["chat" => $message["chat"], "message_id" => 1],
        "data" => $lastRow[0]["callback_data"],
    ];
    $bot->handle(["callback_query" => $cb]);
    checkMenu(
        str_contains($fake->last()["text"], "&lt;b&gt;原样文字&lt;/b&gt;"),
        "custom message executes and escapes HTML",
    );
    $before = count($fake->sent());
    $wrong = $cb;
    $wrong["from"]["id"]++;
    $bot->handle(["callback_query" => $wrong]);
    checkMenu(
        count($fake->sent()) === $before,
        "another user cannot activate private menu",
    );
    $wrong = $cb;
    $wrong["message"]["chat"]["type"] = "group";
    $bot->handle(["callback_query" => $wrong]);
    checkMenu(
        count($fake->sent()) === $before,
        "group copy cannot activate private menu",
    );
    $wrong = $cb;
    $wrong["data"] = "m:" . BotMenu::version() . ":disabled";
    $bot->handle(["callback_query" => $wrong]);
    checkMenu(
        count($fake->sent()) === $before,
        "disabled callbacks are rejected",
    );
    $wrong["data"] = "m:" . BotMenu::version() . ":website";
    $bot->handle(["callback_query" => $wrong]);
    checkMenu(
        count($fake->sent()) === $before,
        "URL action cannot be forged into callback",
    );
    foreach (["hot", "rank", "mine", "help", "submit", "discover"] as $action) {
        $cb["data"] = "m:" . BotMenu::version() . ":" . $action;
        $before = count($fake->sent());
        $bot->handle(["callback_query" => $cb]);
        checkMenu(
            count($fake->sent()) === $before + 1,
            $action . " menu action produces a response",
        );
        if (in_array($action, ["help", "submit"], true)) {
            checkMenu(
                str_contains($fake->last()["text"], "/add ") &&
                    str_contains($fake->last()["text"], "自动获取标题") &&
                    !str_contains($fake->last()["text"], "/submit") &&
                    !str_contains($fake->last()["text"], "链接 |") &&
                    !str_contains($fake->last()["text"], "名称必填"),
                $action . " menu explains link-only /add with automatic titles",
            );
        }
    }
    $sent = $fake->sent();
    $search = $sent[array_key_last($sent)][1];
    checkMenu(
        !isset($search["reply_parameters"]),
        "menu discovery has no user keyword message to quote",
    );
    checkMenu(
        isset(
            $search["reply_markup"]["inline_keyboard"][0][0][
                "icon_custom_emoji_id"
            ],
        ),
        "search filters use the icon pack",
    );
    $cb["data"] = "m:" . BotMenu::version() . ":home";
    $cb["from"]["first_name"] = "新昵称{{date}}<b>";
    unset($cb["from"]["username"]);
    $bot->handle(["callback_query" => $cb]);
    checkMenu(
        str_contains(
            $fake->last()["text"],
            "新昵称{{date}}&lt;b&gt; 同学（未设置）",
        ) && !str_contains($fake->last()["text"], "@menu_tester"),
        "return to menu uses callback user data and does not re-expand names",
    );
    $secondMessage = [
        "text" => "/start",
        "chat" => ["id" => $uid + 1, "type" => "private"],
        "from" => ["id" => $uid + 1, "first_name" => "另一位用户"],
    ];
    $bot->handle(["message" => $secondMessage]);
    checkMenu(
        str_contains($fake->last()["text"], "另一位用户（未设置）") &&
            str_contains($fake->last()["text"], "ID " . ($uid + 1)) &&
            !str_contains($fake->last()["text"], $tag),
        "welcome values are isolated between users",
    );
    $oldVersion = BotMenu::version();
    $menu["welcome"] = "欢迎语已更新";
    BotMenu::save($menu, 0);
    $before = count($fake->sent());
    $cb["data"] = "m:" . $oldVersion . ":help";
    $bot->handle(["callback_query" => $cb]);
    checkMenu(
        count($fake->sent()) === $before &&
            str_contains($fake->last()["text"], "菜单已更新"),
        "old menu version does not execute changed actions",
    );
    $invalid = $menu;
    $invalid["rows"][3][1]["value"] = "javascript:alert(1)";
    rejectsMenu($invalid, "unsafe URL rejected");
    $invalid["rows"][3][1]["value"] = "https://user:password@example.com/";
    rejectsMenu($invalid, "URL credentials rejected");
    $invalid = $menu;
    $invalid["rows"][0][0]["icon"] = "../../secret";
    rejectsMenu($invalid, "icons restricted to the verified pack");
    $invalid = $menu;
    $invalid["rows"][0][0]["action"] = "run_shell";
    rejectsMenu($invalid, "unknown callback action rejected");
    $invalid = $menu;
    $invalid["rows"][0][0]["id"] = "home";
    rejectsMenu($invalid, "reserved callback ID rejected");
    $invalid = $menu;
    $invalid["rows"][0][1]["id"] = $invalid["rows"][0][0]["id"];
    rejectsMenu($invalid, "duplicate callback ID rejected");
    $invalid = $menu;
    $invalid["welcome"] = str_repeat("字", 1801);
    rejectsMenu($invalid, "oversized welcome rejected");
    $invalid["welcome"] = "{{nicknme}}";
    rejectsMenu($invalid, "unknown welcome tag rejected");
    $invalid = $menu;
    $invalid["hint"] = "{{datetime";
    rejectsMenu($invalid, "unclosed hint tag rejected");
    $invalid = $menu;
    $invalid["rows"][0] = array_map(
        fn($i) => array_replace($menu["rows"][0][0], ["id" => "row_" . $i]),
        range(1, 6),
    );
    rejectsMenu($invalid, "oversized keyboard row rejected");
    array_pop($invalid["rows"][0]);
    BotMenu::save($invalid, 0);
    $bot->handle(["message" => $message]);
    checkMenu(
        count($fake->last()["reply_markup"]["inline_keyboard"][0]) === 5,
        "five unique buttons can be saved and rendered in one row",
    );
    $menu["rows"] = [];
    BotMenu::save($menu, 0);
    $bot->handle(["message" => $message]);
    checkMenu(
        $fake->last()["reply_markup"]["inline_keyboard"] === [],
        "text-only start is supported",
    );
    echo "ALL $checks BOT MENU CHECKS PASSED" . PHP_EOL;
} finally {
    Db::rollback();
    echo "All menu test changes rolled back." . PHP_EOL;
}
