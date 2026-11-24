<?php
/** Anonymous-preview fixtures + transactional submission checks; no network requests. */
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();

use app\common\exception\DomainError;
use app\common\service\{
    PublicTelegramPage,
    ResourceService,
    SubmissionService,
    TelegramMetadata,
};
use think\facade\Db;

set_exception_handler(static function (Throwable $e): never {
    fwrite(STDERR, $e->getMessage() . PHP_EOL);
    exit(1);
});
$checks = 0;
function metadataCheck(bool $ok, string $label): void
{
    global $checks;
    if (!$ok) {
        throw new RuntimeException("FAIL " . $label);
    }
    $checks++;
    echo "PASS " . $label . PHP_EOL;
}
function metadataReject(callable $call, string $text, int $status = 422): void
{
    try {
        $call();
    } catch (DomainError $e) {
        metadataCheck(
            $e->status === $status && str_contains($e->getMessage(), $text),
            "rejection explains: " . $text,
        );
        return;
    }
    throw new RuntimeException("Expected metadata/submission rejection: " . $text);
}
$profile = <<<'HTML'
<meta property="og:title" content="开发 &amp; 设计 &lt;新闻&gt;">
<div class="tgme_page_title"><span>开发 &amp; 设计 &lt;新闻&gt;</span><i>✔</i></div>
<div class="tgme_page_extra">1 234 subscribers</div>
<div class="tgme_page_description">最新 <b>开发</b>
  教程</div>
HTML;
$url = "https://t.me/metadatatest";
$data = TelegramMetadata::parse($profile, $url);
metadataCheck(
    $data === [
        "title" => "开发 & 设计 <新闻>",
        "description" => "最新 开发 教程",
        "type" => "channel",
    ],
    "channel title decodes entities, preserves Unicode and excludes verification badges",
);
metadataCheck(
    TelegramMetadata::parse(str_replace("subscribers", "members", $profile), $url)["type"] === "group",
    "member counts identify group previews",
);
metadataCheck(
    TelegramMetadata::parse(
        $profile . '<a class="tgme_action_button_new shine" href="#">Start Bot</a>',
        $url,
    )["type"] === "bot",
    "observed Start Bot action identifies a bot",
);
metadataCheck(
    TelegramMetadata::parse(str_replace("1 234 subscribers", "@metadatatest", $profile), $url)["type"] === "other",
    "personal profiles are not guessed to be channels",
);
metadataCheck(
    TelegramMetadata::parse(
        '<div class="tgme_page_title"><span>没有 OG 的标题</span><i>✔</i></div>',
        $url,
    )["title"] === "没有 OG 的标题",
    "visible title is available when the OG field is missing",
);
metadataCheck(
    TelegramMetadata::parse(
        '<meta property="og:title" content="   "><div class="tgme_page_title"><span>实际标题</span></div>',
        $url,
    )["title"] === "实际标题",
    "empty OG title falls back to the visible title",
);
$long = str_repeat("测试😀", 100);
$bounded = TelegramMetadata::parse(
    '<div class="tgme_page_title"><span>' . $long . '</span></div>',
    $url,
);
metadataCheck(
    mb_strlen($bounded["title"]) === 180 && mb_check_encoding($bounded["title"], "UTF-8"),
    "long fetched titles fit the database validation without invalid Unicode",
);
$post = static fn(string $content, string $id = "MetaDataTest/123"): string =>
    '<div class="tgme_widget_message" data-post="' . $id . '">' .
    '<a class="tgme_widget_message_owner_name">开发频道</a>' . $content . '</div>';
$postUrl = $url . "/123";
$article = TelegramMetadata::parse(
    $post('<div class="tgme_widget_message_text">PHP &amp; Go <b>教程</b></div>'),
    $postUrl,
);
metadataCheck(
    $article["title"] === "PHP & Go 教程" && $article["type"] === "article",
    "exact post content supplies the title with case-insensitive username matching",
);
metadataCheck(
    TelegramMetadata::parse(
        $post('<div class="tgme_widget_message_text">0</div>'),
        $postUrl,
    )["title"] === "0",
    "a literal zero is a valid fetched title",
);
foreach (
    [
        "video" => '<a class="tgme_widget_message_video_player"></a>',
        "article" => '<a class="tgme_widget_message_photo_wrap"></a>',
        "other" => '<div class="tgme_widget_message_sticker"></div>',
    ] as $type => $media
) {
    $data = TelegramMetadata::parse($post($media), $postUrl);
    metadataCheck(
        $data["type"] === $type && $data["title"] === "开发频道",
        "captionless " . $type . " uses its observed owner title",
    );
}
foreach (
    [
        "file" => ['tgme_widget_message_document', 'tgme_widget_message_document_title', '教程.zip'],
        "audio" => ['tgme_widget_message_audio', 'tgme_widget_message_audio_title', '音乐示例'],
    ] as $type => [$container, $titleClass, $title]
) {
    $data = TelegramMetadata::parse(
        $post('<div class="' . $container . '"><span class="' . $titleClass . '">' .
            $title . '</span></div><div class="tgme_widget_message_text">附加说明</div>'),
        $postUrl,
    );
    metadataCheck(
        $data["type"] === $type && $data["title"] === $title && $data["description"] === "附加说明",
        $type . " uses the actual file or track title",
    );
}
foreach (
    [
        ["", $url],
        ['<title>Telegram</title><meta property="og:title" content="Telegram: Contact @missing">', $url],
        [$post('<div class="tgme_widget_message_error">Post not found</div>'), $postUrl],
        [$post('<div class="tgme_widget_message_text">Wrong post</div>', "metadatatest/124"), $postUrl],
        [$post('<div class="tgme_widget_message_text">Wrong channel</div>', "anotherchannel/123"), $postUrl],
        [$post('<div class="message_media_not_supported">Unavailable</div>'), $postUrl],
        [$post(""), $postUrl],
        [$profile, $postUrl],
    ] as [$html, $target]
) {
    metadataReject(fn() => TelegramMetadata::parse($html, $target), "公开标题");
}
$paths = [];
$lookup = new TelegramMetadata(static function (string $path) use (&$paths, $profile, $post): string {
    $paths[] = $path;
    return str_contains($path, "?embed=")
        ? $post('<div class="tgme_widget_message_text">匹配的帖子</div>')
        : $profile;
});
$lookup->fetch("http://telegram.me/MetaDataTest?start=ignored");
$lookup->fetch("https://t.me/s/MetaDataTest/000123?single");
metadataCheck(
    $paths === ["/metadatatest", "/metadatatest/123?embed=1&mode=tme"],
    "fetches only canonical Telegram paths and the specific requested post",
);
foreach (
    [
        "http://127.0.0.1/private",
        "https://t.me.evil.test/metadatatest",
        "https://user:pass@t.me/metadatatest",
        "https://t.me/+privateInvite",
        "file:///etc/passwd",
    ] as $target
) {
    metadataReject(fn() => $lookup->fetch($target), "公开的 Telegram 链接");
}
metadataCheck(count($paths) === 2, "invalid URLs never reach the fetch callback");
foreach (
    ["/../private", "//evil.test/page", "/metadatatest?redirect=http://127.0.0.1", "/metadatatest/123?embed=1&url=evil", "/s/metadatatest/123?embed=1&mode=tme"]
    as $path
) {
    $rejected = false;
    try {
        PublicTelegramPage::get($path);
    } catch (RuntimeException $e) {
        $rejected = $e->getMessage() === "无效的公开预览路径。";
    }
    metadataCheck($rejected, "preview fetcher refuses an unapproved path");
}
$timeout = new TelegramMetadata(static function (): never {
    throw new RuntimeException("Simulated connection timeout with private diagnostics");
});
metadataReject(fn() => $timeout->fetch($url), "稍后重新发送 /add 链接", 503);

Db::startTrans();
try {
    Db::name("settings")->where("name", "submissions_enabled")->update(["value" => "1"]);
    $tag = "metadata_" . bin2hex(random_bytes(4));
    $user = static fn(string $suffix): int => Db::name("users")->insertGetId([
        "username" => $tag . "_" . $suffix,
        "display_name" => "Metadata fixture",
        "created_at" => date("Y-m-d H:i:s"),
    ]);
    $owner = $user("owner");
    $good = new TelegramMetadata(static fn(): string => $profile);
    $link = "https://t.me/" . $tag . "_new";
    $beforeHits = (int) Db::name("rate_limits")->sum("hits");
    $saved = SubmissionService::createFromTelegram($owner, $link, $good);
    $record = Db::name("submissions")->where("id", $saved["id"])->find();
    metadataCheck(
        $record["url"] === $link &&
            $record["title"] === "开发 & 设计 <新闻>" &&
            $record["status"] === "pending" &&
            $saved["title"] === $record["title"] &&
            (int) $record["user_id"] === $owner &&
            !Db::name("resources")->where("url_hash", $record["url_hash"])->find(),
        "auto titles are saved to the submitting user's pending application",
    );
    metadataCheck(
        (int) Db::name("rate_limits")->sum("hits") === $beforeHits + 1,
        "auto-title submission consumes the submission budget once",
    );
    $count = Db::name("submissions")->count();
    metadataReject(
        fn() => SubmissionService::createFromTelegram($owner, "https://t.me/" . $tag . "_timeout", $timeout),
        "稍后重新发送",
        503,
    );
    metadataCheck(Db::name("submissions")->count() === $count, "failed fetching leaves no application");

    $raceLink = "https://t.me/" . $tag . "_race";
    $race = new TelegramMetadata(static function () use ($raceLink, $profile): string {
        ResourceService::save([
            "url" => $raceLink,
            "title" => "Added during preview request",
            "status" => "blocked",
            "status_reason" => "抓取期间已被封禁",
        ]);
        return $profile;
    });
    metadataReject(
        fn() => SubmissionService::createFromTelegram($owner, $raceLink, $race),
        "抓取期间已被封禁",
    );
    metadataCheck(Db::name("submissions")->count() === $count, "late resource conflicts cannot create a duplicate application");

    $other = $user("other");
    $pendingLink = "https://t.me/" . $tag . "_pending";
    $pending = new TelegramMetadata(static function () use ($other, $pendingLink, $profile): string {
        SubmissionService::create($other, ["url" => $pendingLink, "title" => "Another user's request"]);
        return $profile;
    });
    metadataReject(
        fn() => SubmissionService::createFromTelegram($owner, $pendingLink, $pending),
        "正在审核",
    );
    metadataCheck(
        Db::name("submissions")->where("url", $pendingLink)->count() === 1 &&
            (int) Db::name("submissions")->where("url", $pendingLink)->value("user_id") === $other,
        "an application created during fetching remains the only owner of its link",
    );

    $revoked = $user("revoked");
    $revoking = new TelegramMetadata(static function () use ($revoked, $profile): string {
        Db::name("users")->where("id", $revoked)->update([
            "submission_allowed" => 0,
            "submission_note" => "抓取期间禁投",
        ]);
        return $profile;
    });
    metadataReject(
        fn() => SubmissionService::createFromTelegram($revoked, "https://t.me/" . $tag . "_revoked", $revoking),
        "抓取期间禁投",
        403,
    );
    metadataCheck(
        Db::name("submissions")->where("user_id", $revoked)->count() === 0,
        "permission revoked during fetching prevents the write",
    );

    $closing = new TelegramMetadata(static function () use ($profile): string {
        Db::name("settings")->where("name", "submissions_enabled")->update(["value" => "0"]);
        return $profile;
    });
    metadataReject(
        fn() => SubmissionService::createFromTelegram($owner, "https://t.me/" . $tag . "_closed", $closing),
        "暂未开放",
        403,
    );
    metadataCheck(
        !Db::name("submissions")->where("url", "https://t.me/" . $tag . "_closed")->find(),
        "disabling submissions during fetching prevents the write",
    );
} finally {
    Db::rollback();
}
echo "ALL $checks TELEGRAM METADATA CHECKS PASSED (fixtures rolled back)\n";
