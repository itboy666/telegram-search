<?php
namespace app\common\service;

/** Uses observed message links and actual HTML media elements, never guessed IDs. */
class TelegramPreviewDataset
{
    public const SOURCE = "telegram_public_20261116";

    public static function parse(
        string $html,
        string $username,
        string $topic = "",
    ): array {
        $previous = libxml_use_internal_errors(true);
        $document = new \DOMDocument();
        try {
            $document->loadHTML(
                '<?xml encoding="utf-8" ?>' . $html,
                LIBXML_NONET | LIBXML_NOERROR | LIBXML_NOWARNING,
            );
        } finally {
            libxml_clear_errors();
            libxml_use_internal_errors($previous);
        }
        $xpath = new \DOMXPath($document);
        $has = static fn(
            string $class,
        ): string => "contains(concat(' ',normalize-space(@class),' '),' $class ')";
        $nodes = $xpath->query(
            "//*[" . $has("tgme_widget_message") . "][@data-post]",
        );
        $rows = [];
        $counts = ["observed" => 0, "unsupported" => 0, "excluded" => 0];
        foreach ($nodes as $node) {
            $counts["observed"]++;
            $post = $node->getAttribute("data-post");
            if (
                !preg_match(
                    "#^" . preg_quote($username, "#") . '/([1-9][0-9]*)$#iD',
                    $post,
                    $match,
                )
            ) {
                $counts["unsupported"]++;
                continue;
            }
            $find = static fn(string $class) => $xpath->query(
                ".//*[" . $has($class) . "]",
                $node,
            );
            $textNode = $find("tgme_widget_message_text")->item(0);
            $text = $textNode
                ? CurlieDataset::text($textNode->textContent, 1000)
                : "";
            if (CurlieDataset::excluded($text)) {
                $counts["excluded"]++;
                continue;
            }
            $type = match (true) {
                $find("tgme_widget_message_audio")->length > 0 => "audio",
                $find("tgme_widget_message_voice")->length > 0 => "other",
                $find("tgme_widget_message_video_player")->length > 0
                    => "video",
                $find("tgme_widget_message_document")->length > 0 => "file",
                $find("tgme_widget_message_photo_wrap")->length > 0
                    => "article",
                $find("tgme_widget_message_sticker")->length > 0 => "other",
                $find("tgme_widget_message_poll")->length > 0 => "other",
                $find("message_media_not_supported")->length > 0 => null,
                $text !== "" => "article",
                default => null,
            };
            if (!$type) {
                $counts["unsupported"]++;
                continue;
            }
            $fileNode =
                $find("tgme_widget_message_document_title")->item(0) ??
                $find("tgme_widget_message_audio_title")->item(0);
            $title = $fileNode
                ? CurlieDataset::text($fileNode->textContent, 100)
                : mb_substr($text, 0, 80);
            if ($title === "") {
                $title =
                    "@" .
                    $username .
                    " · " .
                    ResourceService::TYPES[$type] .
                    " #" .
                    $match[1];
            }
            $time = $xpath->query(".//time[@datetime]", $node)->item(0);
            $rows[] = [
                "url" => TelegramLink::normalize("https://t.me/" . $post),
                "title" => $title,
                "type" => $type,
                "description" => mb_substr($text, 0, 160),
                "tags" => mb_substr(
                    $topic . "," . CurlieDataset::topics($text . " " . $topic),
                    0,
                    255,
                ),
                "source_file" => "https://t.me/s/" . $username,
                "source_row" => (int) $match[1],
                "observed_media_type" => $type,
                "post_date" => $time?->getAttribute("datetime"),
            ];
        }
        $before = null;
        foreach ($xpath->query("//a[@data-before][@href]") as $anchor) {
            if (
                preg_match(
                    "#^/s/" .
                        preg_quote($username, "#") .
                        '\?before=([1-9][0-9]*)$#iD',
                    $anchor->getAttribute("href"),
                    $match,
                )
            ) {
                $before = (int) $match[1];
                break;
            }
        }
        return ["rows" => $rows, "before" => $before, "counts" => $counts];
    }
}
