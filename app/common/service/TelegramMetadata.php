<?php
namespace app\common\service;

use app\common\exception\DomainError;

/** Resolve titles from anonymous Telegram previews, without visiting submitted hosts. */
class TelegramMetadata
{
    public function __construct(private ?\Closure $fetchPage = null) {}

    public function fetch(string $url): array
    {
        $url = TelegramLink::normalize($url);
        $path = parse_url($url, PHP_URL_PATH);
        if (substr_count($path, "/") === 2) {
            $path .= "?embed=1&mode=tme";
        }
        $fetch =
            $this->fetchPage ??
            static fn(string $path): string => PublicTelegramPage::get(
                $path,
                8,
            );
        try {
            $html = $fetch($path);
        } catch (\RuntimeException) {
            throw new DomainError(
                "暂时无法获取链接标题，请稍后重新发送 /add 链接。",
                503,
            );
        }
        return self::parse($html, $url);
    }

    public static function parse(string $html, string $url): array
    {
        $url = TelegramLink::normalize($url);
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
        $path = ltrim(parse_url($url, PHP_URL_PATH), "/");
        if (str_contains($path, "/")) {
            $node = null;
            foreach (
                $xpath->query(
                    "//*[" . $has("tgme_widget_message") . "][@data-post]",
                )
                as $candidate
            ) {
                if (
                    strcasecmp($candidate->getAttribute("data-post"), $path) === 0
                ) {
                    $node = $candidate;
                    break;
                }
            }
            if (!$node) {
                self::unavailable();
            }
            $find = static fn(string $class) => $xpath
                ->query(".//*[" . $has($class) . "]", $node)
                ->item(0);
            if ($find("tgme_widget_message_error")) {
                self::unavailable();
            }
            $text = self::text(
                $find("tgme_widget_message_text")?->textContent ?? "",
            );
            $type = match (true) {
                (bool) $find("tgme_widget_message_video_player") => "video",
                (bool) $find("tgme_widget_message_audio") => "audio",
                (bool) $find("tgme_widget_message_document") => "file",
                (bool) $find("tgme_widget_message_photo_wrap") => "article",
                (bool) $find("tgme_widget_message_voice"),
                (bool) $find("tgme_widget_message_sticker"),
                (bool) $find("tgme_widget_message_poll") => "other",
                (bool) $find("message_media_not_supported") => null,
                $text !== "" => "article",
                default => null,
            };
            if ($type === null) {
                self::unavailable();
            }
            $title = self::text(
                ($find("tgme_widget_message_document_title") ??
                    $find("tgme_widget_message_audio_title"))?->textContent ??
                    "",
                180,
            );
            if ($title === "") {
                $title = self::text($text, 180);
            }
            if ($title === "") {
                $title = self::text(
                    $find("tgme_widget_message_owner_name")?->textContent ?? "",
                    180,
                );
            }
            $description = $text;
        } else {
            $node = $xpath
                ->query("//*[" . $has("tgme_page_title") . "]")
                ->item(0);
            // Missing/private usernames render a generic Contact page with no title node.
            if (!$node) {
                self::unavailable();
            }
            $title = self::text(
                $xpath->query("//meta[@property='og:title']/@content")
                    ->item(0)?->nodeValue ?? "",
                180,
            );
            if ($title === "") {
                $title = self::text(
                    $xpath->query(".//span", $node)->item(0)?->textContent ??
                        $node->textContent,
                    180,
                );
            }
            $extra = "";
            foreach (
                $xpath->query("//*[" . $has("tgme_page_extra") . "]") as $item
            ) {
                $extra .= " " . $item->textContent;
            }
            $type = match (true) {
                PublicTelegramPage::isBot($html) => "bot",
                (bool) preg_match('/\bsubscribers?\b/i', $extra) => "channel",
                (bool) preg_match('/\bmembers?\b/i', $extra) => "group",
                default => "other",
            };
            $description = self::text(
                $xpath->query("//*[" . $has("tgme_page_description") . "]")
                    ->item(0)?->textContent ?? "",
            );
        }
        if ($title === "") {
            self::unavailable();
        }
        return [
            "title" => $title,
            "description" => $description,
            "type" => $type,
        ];
    }

    private static function text(string $value, int $limit = 5000): string
    {
        $value = preg_replace('/[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]/u', "", $value);
        return mb_substr(
            trim(preg_replace('/[\s\p{Z}]+/u', " ", $value)),
            0,
            $limit,
        );
    }

    private static function unavailable(): never
    {
        throw new DomainError(
            "无法获取该链接的公开标题，请确认链接有效且内容可公开访问后重试。",
            422,
            ["url" => "未找到可收录的公开页面或帖子"],
        );
    }
}
