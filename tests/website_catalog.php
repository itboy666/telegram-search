<?php
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();
set_exception_handler(static function (Throwable $e): never {
    fwrite(STDERR, $e->getMessage() . "\n");
    exit(1);
});

use app\common\exception\DomainError;
use app\common\service\{
    ResourceLink,
    ResourceService,
    ResourceImport,
    SubmissionService,
    SearchService,
    PublicTelegramPage,
    BotSearchSettings,
};
use app\common\telegram\{Icons, Bot, Transport};
use think\facade\Db;

$checks = 0;
function websiteCheck(bool $ok, string $label): void
{
    global $checks;
    if (!$ok) {
        throw new RuntimeException("FAIL " . $label);
    }
    $checks++;
    echo "PASS " . $label . "\n";
}
function websiteReject(callable $fn, string $notice = ""): void
{
    try {
        $fn();
    } catch (DomainError $e) {
        websiteCheck(
            $e->status === 422 &&
                ($notice === "" || str_contains($e->getMessage(), $notice)),
            "rejects invalid/duplicate website: " . $notice,
        );
        return;
    }
    throw new RuntimeException("Expected rejection: " . $notice);
}

foreach (
    [
        "HTTPS://EXAMPLE.COM:443#intro" => "https://example.com/",
        "http://Example.com:80/a?b=1&a=2#section" =>
            "http://example.com/a?b=1&a=2",
        "https://example.com/A%2FB?key=Value" =>
            "https://example.com/A%2FB?key=Value",
    ]
    as $input => $expected
) {
    websiteCheck(
        ResourceLink::normalize($input, "website") === $expected,
        "conservative canonical identity",
    );
}
foreach (
    [
        "javascript:alert(1)",
        "file:///etc/passwd",
        "https://user:pass@example.com/",
        "https://localhost/",
        "http://127.0.0.1/",
        "http://10.0.0.1/",
        "http://2130706433/",
        "https://example.com\\@evil.com/",
        "https://example.com/%GG",
        "https://example.com/a b",
        "https://example.com/" . str_repeat("a", 501),
        "https://t.me/telegram",
        "https://example.com/\xFF",
    ]
    as $input
) {
    websiteReject(fn() => ResourceLink::normalize($input, "website"));
}
websiteReject(fn() => ResourceLink::normalize("https://example.com/", "video"));
websiteCheck(
    ResourceLink::normalize("http://telegram.me/ExampleBot?start=x", "bot") ===
        "https://t.me/examplebot",
    "Telegram aliases stay canonical",
);
websiteCheck(
    ResourceLink::normalize("https://example.com/?a=1&b=2", "website") !==
        ResourceLink::normalize("https://example.com/?b=2&a=1", "website"),
    "meaningful query order is preserved",
);
websiteCheck(
    Icons::button("website", ["text" => "网站"])["icon_custom_emoji_id"] ===
        "5776233299424843260",
    "website uses original Telegram iOS globe",
);
websiteCheck(
    PublicTelegramPage::isBot(
        '<a class="tgme_action_button_new shine" href="tg://resolve?domain=spambot">Start Bot</a>',
    ),
    "bot identity requires live Start Bot action",
);
websiteCheck(
    !PublicTelegramPage::isBot(
        '<a class="tgme_action_button_new" href="#">View in Telegram</a>',
    ),
    "a channel is not treated as a bot",
);

Db::startTrans();
try {
    Db::name("settings")
        ->where("name", "submissions_enabled")
        ->update(["value" => "1"]);
    $tag = "website_" . bin2hex(random_bytes(6));
    $user = Db::name("users")->insertGetId([
        "username" => $tag,
        "display_name" => "Website test",
        "created_at" => date("Y-m-d H:i:s"),
    ]);
    $row = [
        "title" => $tag,
        "url" => "https://example.com/$tag",
        "type" => "website",
        "source" => "curlie_20261116",
    ];
    $before = Db::name("resources")->count();
    $dry = ResourceImport::run(
        [$row, array_replace($row, ["url" => $row["url"] . "#test"])],
        $tag,
    );
    websiteCheck(
        $dry["counts"]["planned"] === 1 &&
            $dry["counts"]["duplicate_input"] === 1,
        "website aliases deduplicate before import",
    );
    websiteCheck(
        Db::name("resources")->count() === $before,
        "dry run is read-only",
    );
    $saved = ResourceService::save($row);
    websiteReject(fn() => SubmissionService::create($user, $row), "已存在");
    $again = ResourceImport::run([$row], $tag, true);
    websiteCheck(
        $again["counts"]["inserted"] === 0 &&
            $again["counts"]["duplicate_existing"] === 1,
        "repeat import preserves existing website",
    );
    ResourceService::save(
        $row + ["status" => "blocked", "status_reason" => "测试封禁原因"],
        $saved["id"],
    );
    websiteReject(
        fn() => SubmissionService::create($user, $row),
        "测试封禁原因",
    );
    $submitted = array_replace($row, ["url" => $row["url"] . "/pending"]);
    SubmissionService::create($user, $submitted);
    websiteReject(
        fn() => SubmissionService::create($user, $submitted),
        "正在审核",
    );
    $import = ResourceImport::run([$submitted], $tag, true);
    websiteCheck(
        $import["counts"]["duplicate_submission"] === 1,
        "import respects pending submissions",
    );
    ResourceService::save($row, $saved["id"]);
    $search = new SearchService()->search(
        ["q" => $tag, "type" => "website"],
        "test",
        false,
        false,
    );
    websiteCheck(
        $search["total"] === 1 && $search["items"][0]["type_label"] === "网站",
        "website type is searchable",
    );
    websiteCheck(
        $search["items"][0]["source_attribution"]["license"] === "CC BY 3.0",
        "public API supplies source attribution",
    );
    $sources = [
        "curlie_20261116",
        "zenodo_14749767",
        "tgrpg_bots_20261116",
        "dany_bots_20261116",
        "erkcet_bots_20261116",
        "telegram_public_20261116",
    ];
    for ($i = 1; $i < 30; $i++) {
        ResourceService::save([
            "title" => str_repeat("标题<&>👩‍💻", 15),
            "tags" => $tag,
            "url" => $row["url"] . "/" . $i,
            "type" => "website",
            "source" => $sources[$i % count($sources)],
        ]);
    }
    BotSearchSettings::save(30, 0);
    $transport = new class implements Transport {
        public array $sent = [];
        public function call(string $method, array $params = []): mixed
        {
            if ($method === "sendMessage") {
                $this->sent = $params;
                return ["message_id" => 9876];
            }
            return true;
        }
    };
    $uid = random_int(86000000000, 86999999999);
    new Bot($transport)->handle([
        "message" => [
            "text" => $tag,
            "from" => ["id" => $uid, "first_name" => "Website test"],
            "chat" => ["id" => $uid, "type" => "private"],
        ],
    ]);
    $body = $transport->sent["text"] ?? "";
    $visible = html_entity_decode(
        strip_tags($body),
        ENT_QUOTES | ENT_HTML5,
        "UTF-8",
    );
    websiteCheck(
        substr_count($body, '<a href="https://example.com/') === 30,
        "Bot renders thirty actual website results",
    );
    websiteCheck(
        strlen(mb_convert_encoding($visible, "UTF-16LE", "UTF-8")) / 2 <= 4096,
        "thirty long titles fit Telegram limit",
    );
    websiteCheck(
        !str_contains($body, "来源：") &&
            !str_contains($body, "With content from") &&
            !str_contains($body, "Curlie.org") &&
            !str_contains($body, "curlie.org/docs/") &&
            !str_contains($body, "creativecommons.org") &&
            !str_contains($body, "CC BY") &&
            !str_contains($body, "原作者保留权利") &&
            !str_contains($body, "Telegram 公开帖子预览"),
        "Bot omits source credits and license footer for every catalog source",
    );
    websiteCheck(
        str_contains($body, 'emoji-id="5776233299424843260"'),
        "Bot keeps the original Telegram iOS globe icon",
    );
} finally {
    Db::rollback();
}
echo "ALL $checks WEBSITE CHECKS PASSED (rolled back)\n";
