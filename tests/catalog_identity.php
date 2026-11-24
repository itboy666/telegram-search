<?php
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();
set_exception_handler(static function (Throwable $e): never {
    fwrite(STDERR, $e->getMessage() . "\n");
    exit(1);
});

use app\common\exception\DomainError;
use app\common\service\{
    CatalogMigration,
    ResourceImport,
    ResourceService,
    SearchService,
    SubmissionService,
    TelegramLink,
    TelegramMetadata,
};
use app\common\telegram\{Bot, Transport};
use think\facade\Db;

$checks = 0;
function catalogCheck(bool $ok, string $message): void
{
    global $checks;
    if (!$ok) {
        throw new RuntimeException("FAIL " . $message);
    }
    echo "PASS " . $message . "\n";
    $checks++;
}
function catalogReject(callable $fn, string $contains, int $status = 422): void
{
    $caught = false;
    try {
        $fn();
    } catch (DomainError $e) {
        $caught =
            $e->status === $status && str_contains($e->getMessage(), $contains);
    }
    catalogCheck($caught, "rejection explains: " . $contains);
}

catalogCheck(
    CatalogMigration::run()["already_applied"],
    "catalog migration is repeatable",
);
foreach (
    [
        "@ExampleBot",
        "t.me/ExampleBot/",
        "http://telegram.me/ExampleBot?start=hello",
        "HTTPS://WWW.T.ME/ExampleBot/#anchor",
        "https://t.me/s/ExampleBot",
    ]
    as $url
) {
    catalogCheck(
        TelegramLink::normalize($url) === "https://t.me/examplebot",
        "canonical alias: " . $url,
    );
}
catalogCheck(
    TelegramLink::normalize("https://t.me/s/ExampleBot/000123?single") ===
        "https://t.me/examplebot/123",
    "post preview and leading zeros canonicalize",
);
catalogCheck(
    TelegramLink::normalize("https://t.me/examplebot/123") !==
        TelegramLink::normalize("https://t.me/examplebot/124"),
    "distinct real posts stay distinct",
);
foreach (
    [
        "https://t.me/+invite",
        "https://t.me/joinchat/hash",
        "https://t.me/share/url",
        "https://t.me/proxy",
        "https://user:pass@t.me/example",
        "https://t.me.evil.test/example",
        "https://t.me/example/0",
        "https://t.me/%65xample",
        "javascript:alert(1)",
        "https://t.me/example:123",
    ]
    as $url
) {
    $rejected = false;
    try {
        TelegramLink::normalize($url);
    } catch (DomainError) {
        $rejected = true;
    }
    catalogCheck($rejected, "rejects unsupported or ambiguous link");
}
Db::startTrans();
try {
    Db::name("settings")
        ->where("name", "submissions_enabled")
        ->update(["value" => "1"]);
    $tag = "identity_" . bin2hex(random_bytes(4));
    $tg = random_int(85000000000, 85999999900);
    $user = function (int $suffix) use ($tag, $tg): int {
        return Db::name("users")->insertGetId([
            "username" => $tag . "_" . $suffix,
            "display_name" => "Identity test",
            "telegram_id" => $tg + $suffix,
            "created_at" => date("Y-m-d H:i:s"),
        ]);
    };
    $a = $user(1);
    $b = $user(2);
    foreach (
        [
            "published" => "正常收录",
            "hidden" => "已下架",
            "archived" => "已归档",
            "blocked" => "已被封禁",
            "invalid" => "已标记为失效",
        ]
        as $status => $notice
    ) {
        $r = ResourceService::save([
            "title" => $tag . " " . $status,
            "url" => "https://t.me/" . $tag . "_" . $status,
            "status" => $status,
            "status_reason" => "管理员填写的明确原因",
        ]);
        catalogReject(
            fn() => SubmissionService::create($a, [
                "url" =>
                    "https://telegram.me/" .
                    strtoupper($tag . "_" . $status) .
                    "/?x=1",
            ]),
            $notice,
        );
        if ($status !== "published") {
            catalogReject(
                fn() => SubmissionService::create($b, [
                    "title" => "duplicate",
                    "url" => $r["url"],
                ]),
                "管理员填写的明确原因",
            );
            catalogCheck(
                new SearchService()->search(
                    ["q" => $tag . " " . $status],
                    "web",
                    false,
                    false,
                )["total"] === 0,
                "abnormal resource is absent from public search: " . $status,
            );
        }
        $report = ResourceImport::run(
            [["title" => "must not replace", "url" => $r["url"]]],
            $tag,
            true,
        );
        catalogCheck(
            $report["counts"]["duplicate_existing"] === 1 &&
                Db::name("resources")->where("id", $r["id"])->find() === $r,
            "imports preserve existing " . $status . " resources and reasons",
        );
    }
    $pendingInput = [
        "title" => "Review test",
        "url" => "https://t.me/" . $tag . "_pending",
    ];
    $pending = SubmissionService::create($a, $pendingInput);
    catalogReject(
        fn() => SubmissionService::create($b, [
            "title" => "other user",
            "url" => "@" . strtoupper($tag . "_pending"),
        ]),
        "正在审核",
    );
    catalogReject(fn() => ResourceService::save($pendingInput), "正在审核");
    $report = ResourceImport::run([$pendingInput], $tag, true);
    catalogCheck(
        $report["counts"]["duplicate_submission"] === 1 &&
            !$report["counts"]["inserted"],
        "import cannot bypass a pending submission",
    );
    $approved = ResourceService::save(
        $pendingInput,
        null,
        (int) $pending["id"],
    );
    Db::name("submissions")
        ->where("id", $pending["id"])
        ->update(["status" => "approved", "resource_id" => $approved["id"]]);
    $edited = ResourceService::save(
        array_replace($approved, ["title" => "Reviewed title"]),
        (int) $approved["id"],
    );
    catalogCheck(
        $edited["title"] === "Reviewed title",
        "review approval and editing its resource remain available",
    );
    $rejected = SubmissionService::create($b, [
        "title" => "Rejected test",
        "url" => "https://t.me/" . $tag . "_reject",
    ]);
    Db::name("submissions")
        ->where("id", $rejected["id"])
        ->update([
            "status" => "rejected",
            "review_note" => "来源不符合收录规则",
        ]);
    catalogReject(
        fn() => SubmissionService::create($a, [
            "url" => "https://t.me/" . $tag . "_reject",
        ]),
        "来源不符合收录规则",
    );
    $report = ResourceImport::run(
        [
            [
                "title" => "cannot bypass",
                "url" => "https://t.me/" . $tag . "_reject",
            ],
        ],
        $tag,
        true,
    );
    catalogCheck(
        $report["counts"]["duplicate_submission"] === 1,
        "import preserves rejected review decisions",
    );
    Db::name("users")
        ->where("id", $b)
        ->update([
            "submission_allowed" => 0,
            "submission_note" => "重复发送无关链接",
        ]);
    catalogReject(
        fn() => SubmissionService::create($b, ["url" => "bad"]),
        "你已被禁止提交收录",
        403,
    );
    catalogReject(
        fn() => SubmissionService::create($b, [
            "url" => "https://t.me/" . $tag . "_published",
        ]),
        "重复发送无关链接",
        403,
    );
    catalogCheck(
        (int) Db::name("users")->where("id", $b)->value("status") === 1,
        "submission ban does not disable account access",
    );
    Db::name("users")
        ->where("id", $b)
        ->update(["submission_allowed" => 1]);
    $restored = SubmissionService::create($b, [
        "title" => "Restored permission",
        "url" => "https://t.me/" . $tag . "_restore",
    ]);
    catalogCheck(
        $restored["status"] === "pending",
        "restoring permission allows a new submission",
    );
    Db::name("users")
        ->where("id", $b)
        ->update(["status" => 0]);
    catalogReject(
        fn() => SubmissionService::create($b, ["url" => "bad"]),
        "账号已停用",
        403,
    );

    $fake = new class implements Transport {
        public array $calls = [];
        public function call(string $method, array $params = []): mixed
        {
            $this->calls[] = [$method, $params];
            return ["message_id" => 1];
        }
    };
    $metadataRequests = [];
    $metadata = new TelegramMetadata(
        static function (string $path) use (&$metadataRequests): string {
            $metadataRequests[] = $path;
            if (str_ends_with($path, "_missing_name")) {
                return '<meta property="og:title" content="Telegram: Contact @missing">';
            }
            return '<meta property="og:title" content="自动抓取的当前标题">' .
                '<div class="tgme_page_title"><span>自动抓取的当前标题</span></div>' .
                '<div class="tgme_page_extra">120 members</div>' .
                '<div class="tgme_page_description">来自公开页面的简介</div>';
        },
    );
    $bot = new Bot($fake, "IdentityBot", 0, $metadata);
    $bot->handle([
        "message" => [
            "chat" => ["id" => $tg + 1, "type" => "private"],
            "from" => ["id" => $tg + 1],
            "text" => "/add https://t.me/" . $tag . "_blocked",
        ],
    ]);
    catalogCheck(
        str_contains($fake->calls[0][1]["text"], "已被封禁"),
        "Bot /add link-only submission reports the existing blocked status",
    );
    Db::name("users")
        ->where("id", $a)
        ->update([
            "submission_allowed" => 0,
            "submission_note" => "测试禁止原因",
        ]);
    $bot->handle([
        "message" => [
            "chat" => ["id" => $tg + 1, "type" => "private"],
            "from" => ["id" => $tg + 1],
            "text" => "/add https://t.me/" . $tag . "_new | test",
        ],
    ]);
    catalogCheck(
        str_contains($fake->calls[1][1]["text"], "测试禁止原因"),
        "Bot /add submission ban returns the configured reason",
    );
    catalogCheck(
        $metadataRequests === [],
        "known blocked links and banned submitters trigger no remote fetch",
    );
    $before = count($fake->calls);
    $bot->handle([
        "message" => [
            "chat" => ["id" => $tg + 1, "type" => "private"],
            "from" => ["id" => $tg + 1],
            "text" => $tag,
        ],
    ]);
    catalogCheck(
        count($fake->calls) === $before + 1 &&
            str_contains($fake->calls[$before][1]["text"], "搜索"),
        "banned submitter can still search via Bot",
    );

    $c = $user(3);
    $addMessage = function (string $text) use ($bot, $fake, $tg): string {
        $bot->handle([
            "message" => [
                "chat" => ["id" => $tg + 3, "type" => "private"],
                "from" => ["id" => $tg + 3],
                "text" => $text,
            ],
        ]);
        return $fake->calls[array_key_last($fake->calls)][1]["text"];
    };
    $beforeSubmissions = Db::name("submissions")->count();
    $beforeSessions = Db::name("bot_sessions")->count();
    $notice = $addMessage("/add");
    catalogCheck(
        str_contains($notice, "/add https://t.me/username") &&
            str_contains($notice, "自动获取标题") &&
            !str_contains($notice, "|") &&
            Db::name("submissions")->count() === $beforeSubmissions &&
            Db::name("bot_sessions")->count() === $beforeSessions,
        "bare /add explains the format without creating a submission or search",
    );
    $notice = $addMessage(
        "/add@identitybot https://telegram.me/" .
            strtoupper($tag) .
            "_add/",
    );
    $added = Db::name("submissions")->where("user_id", $c)->find();
    catalogCheck(
        $added !== null &&
            str_contains($notice, "#" . $added["id"]) &&
            $added["url"] === "https://t.me/" . $tag . "_add" &&
            $added["title"] === "自动抓取的当前标题" &&
            $added["description"] === "来自公开页面的简介" &&
            $added["type"] === "group" &&
            str_contains($notice, $added["title"]) &&
            $metadataRequests === ["/" . $tag . "_add"] &&
            $added["status"] === "pending" &&
            !Db::name("resources")
                ->where("url_hash", $added["url_hash"])
                ->find(),
        "link-only addressed /add fetches its current title and saves a normalized pending application",
    );
    catalogCheck(
        str_contains(
            $addMessage("/add @" . strtoupper($tag . "_add")),
            "正在审核",
        ) && Db::name("submissions")->where("user_id", $c)->count() === 1,
        "/add rejects a pending duplicate even when the name is omitted",
    );
    catalogCheck(
        str_contains(
            $addMessage("/add https://t.me/" . $tag . "_published"),
            "正常收录",
        ),
        "/add explains that a published link already exists",
    );
    catalogCheck(
        str_contains(
            $addMessage("/add https://t.me/" . $tag . "_missing_name"),
            "无法获取该链接的公开标题",
        ) && Db::name("submissions")->where("user_id", $c)->count() === 1,
        "/add rejects unavailable previews without storing a placeholder title",
    );
    $notice = $addMessage("/submit https://t.me/" . $tag . "_legacy | 旧命令");
    $legacy = Db::name("submissions")
        ->where("url", "https://t.me/" . $tag . "_legacy")
        ->find();
    catalogCheck(
        $legacy !== null &&
            str_contains($notice, "#" . $legacy["id"]) &&
            $legacy["title"] === "自动抓取的当前标题" &&
            $legacy["description"] === "来自公开页面的简介" &&
            $legacy["status"] === "pending",
        "legacy /submit still fetches the current title instead of trusting its old manual fields",
    );
    catalogCheck(
        str_contains($addMessage("/submit"), "/add https://t.me/username"),
        "bare legacy command directs users to the new /add syntax",
    );
    Db::name("settings")
        ->where("name", "submissions_enabled")
        ->update(["value" => "0"]);
    catalogCheck(
        str_contains($addMessage("/add"), "暂未开放"),
        "bare /add respects the disabled submission setting",
    );
    catalogCheck(
        str_contains(
            $addMessage("/add https://t.me/" . $tag . "_closed | 暂停期间"),
            "暂未开放",
        ) && Db::name("submissions")->where("user_id", $c)->count() === 2,
        "/add cannot submit when collection is disabled",
    );
    catalogCheck(
        $metadataRequests === [
            "/" . $tag . "_add",
            "/" . $tag . "_missing_name",
            "/" . $tag . "_legacy",
        ],
        "pending duplicates, published links and disabled collection never fetch remotely",
    );
    Db::name("settings")
        ->where("name", "submissions_enabled")
        ->update(["value" => "1"]);

    $batch = [];
    for ($i = 0; $i < 501; $i++) {
        $batch[] = [
            "title" => "Bulk " . $i,
            "url" => "https://t.me/" . $tag . "_bulk" . $i,
        ];
    }
    $batch[] = [
        "title" => "duplicate across batch boundary",
        "url" => "@" . strtoupper($tag . "_bulk0"),
    ];
    $report = ResourceImport::run($batch, $tag, true);
    catalogCheck(
        $report["counts"]["inserted"] === 501 &&
            $report["counts"]["duplicate_input"] === 1,
        "batched import counts unique links across a batch boundary",
    );
    $report = ResourceImport::run($batch, $tag, true);
    catalogCheck(
        $report["counts"]["inserted"] === 0 &&
            $report["counts"]["duplicate_existing"] === 501,
        "re-import is idempotent across multiple batches",
    );
    echo "\n$checks catalog checks passed; all fixture writes rolled back.\n";
} finally {
    Db::rollback();
}
