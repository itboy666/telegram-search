<?php
namespace app\common\service;

/** Only explicitly declared bots; channel/group entries are never relabelled. */
class CuratedBotsDataset
{
    public const SOURCES = [
        "tgrpg_bots_20261116" => [
            "title" => "Awesome Telegram",
            "author" => "TGrpg / TGBox contributors",
            "url" => "https://github.com/TGrpg/awesome-telegram",
            "license" => "CC BY 4.0",
            "license_url" => "https://creativecommons.org/licenses/by/4.0/",
        ],
        "dany_bots_20261116" => [
            "title" => "TelegramBotsList",
            "author" => "danyspin97 and contributors",
            "url" => "https://github.com/danyspin97/TelegramBotsList",
            "license" => "Unlicense",
        ],
        "erkcet_bots_20261116" => [
            "title" => "Awesome Telegram Bots",
            "author" => "erkcet and contributors",
            "url" => "https://github.com/erkcet/awesome-telegram-bots",
            "license" => "CC0",
        ],
    ];

    public static function read(string $directory): array
    {
        $data = json_decode(
            file_get_contents("$directory/tgrpg-entries.json"),
            true,
            512,
            JSON_THROW_ON_ERROR,
        );
        if (!isset($data["entries"]) || !is_array($data["entries"])) {
            throw new \RuntimeException("TGBox 数据格式不匹配。");
        }
        $groups = array_fill_keys(array_keys(self::SOURCES), []);
        $categories = [];
        foreach ($data["categories"] as $category) {
            if ($category["kind"] === "bot") {
                $categories[$category["slug"]] = $category["nameZh"];
            }
        }
        foreach ($data["entries"] as $index => $entry) {
            if (($entry["kind"] ?? "") !== "bot") {
                continue;
            }
            $description =
                (string) ($entry["descriptionZh"] ?? "" ?:
                $entry["description"] ?? "");
            $groups["tgrpg_bots_20261116"][] = [
                "title" => CurlieDataset::text($entry["title"], 180),
                "url" => "https://t.me/" . $entry["username"],
                "type" => "bot",
                "description" => CurlieDataset::text($description, 5000),
                "tags" =>
                    ($categories[$entry["category"]] ?? $entry["category"]) .
                    "," .
                    implode(",", $entry["tags"] ?? []),
                // Bot monthly users are not group members.
                "members" => null,
                "source_file" => "tgrpg-entries.json",
                "source_row" => $index + 1,
            ];
        }
        foreach (
            ["dany" => "danyspin-bots.md", "erkcet" => "erkcet-bots.md"]
            as $key => $file
        ) {
            foreach (
                file("$directory/$file", FILE_IGNORE_NEW_LINES)
                as $index => $line
            ) {
                if (
                    !preg_match(
                        '/^\s*-\s+\[([^\]]+)\]\((https:\/\/t\.me\/[A-Za-z0-9_]+)\)(.*)$/',
                        $line,
                        $m,
                    )
                ) {
                    continue;
                }
                // A direct entry in a bot list alone is insufficient; the live check verifies the page.
                $groups[$key . "_bots_20261116"][] = [
                    "title" => CurlieDataset::text(ltrim($m[1], "@"), 180),
                    "url" => $m[2],
                    "type" => "bot",
                    "description" =>
                        $key === "dany"
                            ? "开源 Telegram 机器人，来源：TelegramBotsList。"
                            : CurlieDataset::text(ltrim($m[3], " -"), 5000),
                    "tags" =>
                        $key === "dany" ? "开源,开发,机器人" : "工具,机器人",
                    "source_file" => $file,
                    "source_row" => $index + 1,
                ];
            }
        }
        return $groups;
    }
}
