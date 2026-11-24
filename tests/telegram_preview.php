<?php
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();
set_exception_handler(static function (Throwable $e): never {
    fwrite(STDERR, $e->getMessage() . "\n");
    exit(1);
});

use app\common\service\TelegramPreviewDataset;

$html = <<<'HTML'
<a data-before="120" href="/s/examplechannel?before=120"></a>
<div class="tgme_widget_message_wrap">
  <div class="tgme_widget_message" data-post="examplechannel/123">
    <a class="tgme_widget_message_video_player"></a>
    <a class="tgme_widget_message_photo_wrap"></a>
    <div class="tgme_widget_message_text">真实视频说明</div>
  </div>
</div>
<div class="tgme_widget_message" data-post="examplechannel/124">
  <div class="tgme_widget_message_document"><div class="tgme_widget_message_document_title">source.zip</div></div>
</div>
<div class="tgme_widget_message" data-post="examplechannel/125">
  <div class="tgme_widget_message_audio"><div class="tgme_widget_message_audio_title">Demo audio</div></div>
</div>
<div class="tgme_widget_message" data-post="examplechannel/126"><a class="tgme_widget_message_photo_wrap"></a></div>
<div class="tgme_widget_message" data-post="examplechannel/127"><div class="tgme_widget_message_text">开发教程</div></div>
<div class="tgme_widget_message" data-post="examplechannel/128"><div class="tgme_widget_message_sticker"></div></div>
<div class="tgme_widget_message" data-post="otherchannel/129"><div class="tgme_widget_message_text">其他频道</div></div>
<div class="tgme_widget_message" data-post="examplechannel/130">Unsupported media</div>
<div class="tgme_widget_message" data-post="examplechannel/131">
  <div class="message_media_not_supported">This media is not supported in your browser</div>
  <div class="tgme_widget_message_text">A caption does not prove that this is an article</div>
</div>
HTML;
$result = TelegramPreviewDataset::parse($html, "examplechannel", "开发");
if (
    array_column($result["rows"], "type") !== [
        "video",
        "file",
        "audio",
        "article",
        "article",
        "other",
    ]
) {
    throw new RuntimeException("Actual media types must map correctly.");
}
if ($result["before"] !== 120 || $result["counts"]["unsupported"] !== 3) {
    throw new RuntimeException(
        "Observed pagination and unsupported records must be preserved.",
    );
}
if (
    count(array_unique(array_column($result["rows"], "url"))) !== 6 ||
    $result["rows"][1]["url"] !== "https://t.me/examplechannel/124" ||
    $result["rows"][1]["title"] !== "source.zip"
) {
    throw new RuntimeException("Use only observed links and file names.");
}
$empty = TelegramPreviewDataset::parse(
    "<html>Telegram contact page</html>",
    "examplechannel",
);
if ($empty["rows"] || $empty["before"] !== null) {
    throw new RuntimeException("Contact pages cannot become fabricated posts.");
}
echo "PASS media classification, observed IDs, pagination, foreign-channel and unsupported-media rejection\n";
