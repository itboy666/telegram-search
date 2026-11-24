<?php
namespace app\common\telegram;

/** Inspect only icon IDs; never persist message bodies or chat identifiers. */
class EmojiDelivery
{
    public static function inspect(array $request, array $message): array
    {
        $checks = [];
        preg_match_all('/<tg-emoji emoji-id="(\d+)">/', $request["text"] ?? "", $matches);
        $expected = $matches[1];
        if ($expected) {
            $actual = array_column(
                array_filter($message["entities"] ?? [], fn($entity) => ($entity["type"] ?? "") === "custom_emoji"),
                "custom_emoji_id",
            );
            $checks["text"] = self::result($expected, $actual);
        }
        $expected = $actual = [];
        foreach ($request["reply_markup"]["inline_keyboard"] ?? [] as $row) {
            foreach ($row as $button) {
                if (!empty($button["icon_custom_emoji_id"])) {
                    $expected[] = (string) $button["icon_custom_emoji_id"];
                }
            }
        }
        if ($expected) {
            foreach ($message["reply_markup"]["inline_keyboard"] ?? [] as $row) {
                foreach ($row as $button) {
                    if (!empty($button["icon_custom_emoji_id"])) {
                        $actual[] = (string) $button["icon_custom_emoji_id"];
                    }
                }
            }
            $checks["buttons"] = self::result($expected, $actual);
        }
        return $checks;
    }
    private static function result(array $expected, array $actual): array
    {
        sort($expected);
        sort($actual);
        return [
            "status" => $expected === $actual ? "retained" : "not_retained",
            "requested" => count($expected),
            "retained" => count($actual),
            "checked_at" => date(DATE_ATOM),
        ];
    }
}
