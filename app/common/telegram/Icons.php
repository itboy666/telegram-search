<?php
namespace app\common\telegram;

class Icons
{
    public static function catalog(): array
    {
        return config("tgios_icons", []);
    }
    public static function html(string $name): string
    {
        $icon =
            self::catalog()[
                $name === "website" ? "ios_5776233299424843260" : $name
            ] ?? null;
        return $icon
            ? '<tg-emoji emoji-id="' .
                    $icon["id"] .
                    '">' .
                    $icon["emoji"] .
                    "</tg-emoji>"
            : "";
    }
    public static function button(string $name, array $button): array
    {
        $icon =
            self::catalog()[
                $name === "website" ? "ios_5776233299424843260" : $name
            ] ?? null;
        if ($icon) {
            $button["icon_custom_emoji_id"] = $icon["id"];
        }
        return $button;
    }
}
