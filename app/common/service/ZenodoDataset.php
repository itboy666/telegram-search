<?php
namespace app\common\service;

/** Reproducible adapter for the publisher's CC BY 4.0 channel snapshot. */
class ZenodoDataset
{
    public const SOURCE = "zenodo_14749767";
    public const MD5 = "d0f1e673261bb58b95f156ef0524848e";
    public const ATTRIBUTION = [
        "title" => "Telegram channels graph dataset",
        "author" => "Svitlana Shapovalova; Anton Sofiienko",
        "published_at" => "2025-01-27",
        "url" => "https://zenodo.org/records/14749767",
        "license" => "CC BY 4.0",
        "license_url" => "https://creativecommons.org/licenses/by/4.0/",
    ];
    public const TOPICS = [
        "开发,编程" =>
            "/编程|开发|程序员|\b(?:python|php|golang|javascript|typescript|programming|coding|developer|devops|linux|kubernetes)\b|программирован/iu",
        "人工智能,AI" =>
            "/人工智能|机器学习|\b(?:ai|chatgpt|machine learning|artificial intelligence|deep learning)\b/iu",
        "云计算,服务器" =>
            "/服务器|云计算|\b(?:cloud|vps|hosting|aws|azure|server|docker)\b/iu",
        "教育,学习" =>
            "/教育|学习|课程|\b(?:education|learning|learn|tutorial|course|university|science)\b|образован|университет/iu",
        "设计" => "/设计|\b(?:design|figma|photoshop|ux|ui)\b|дизайн/iu",
        "招聘,求职" =>
            "/招聘|求职|\b(?:jobs|career|vacanc|recruitment)\w*\b|ваканс/iu",
        "旅游" =>
            "/旅游|旅行|\b(?:travel|tourism|tourist)\w*\b|туризм|путешеств/iu",
        "新闻,资讯" => "/新闻|资讯|\b(?:news|journalism)\b|новост/iu",
        "摄影" => "/摄影|\b(?:photography|photographer)\b|фотограф/iu",
        "电商,营销" =>
            "/电商|营销|\b(?:ecommerce|e-commerce|marketing|shopify)\b|маркетинг/iu",
        "音乐" => "/音乐|\b(?:music|musician)\b|музык/iu",
        "体育" =>
            "/体育|足球|篮球|\b(?:sport|football|basketball|fitness)\w*\b|футбол|спорт/iu",
    ];

    public static function read(string $file, int $limit): array
    {
        if ($limit < 1 || $limit > 250000) {
            throw new \InvalidArgumentException("数量须在 1–250000 之间。");
        }
        if (!is_file($file) || hash_file("md5", $file) !== self::MD5) {
            throw new \RuntimeException(
                "数据文件与发布者的校验和不一致，未导入。",
            );
        }
        $zip = new \ZipArchive();
        if (
            $zip->open($file) !== true ||
            !($stream = $zip->getStream("Channels.csv"))
        ) {
            throw new \RuntimeException("无法读取 Channels.csv。");
        }
        $counts = [
            "source_rows" => 0,
            "invalid_public_link" => 0,
            "excluded_content" => 0,
            "duplicate_username" => 0,
        ];
        $channels = [];
        try {
            $header = fgetcsv($stream, null, "\t", '"', "");
            if (
                $header !== [
                    "Id",
                    "Name",
                    "Title",
                    "CreatedAt",
                    "SnapshotAt",
                    "TopLang",
                    "P90Views",
                    "P90Forwards",
                ]
            ) {
                throw new \RuntimeException("数据字段已变化，需要先检查映射。");
            }
            while (
                ($values = fgetcsv($stream, null, "\t", '"', "")) !== false
            ) {
                $counts["source_rows"]++;
                if (count($values) !== count($header)) {
                    throw new \RuntimeException(
                        "来源第 " .
                            ($counts["source_rows"] + 1) .
                            " 条记录字段不完整。",
                    );
                }
                $row = array_combine($header, $values);
                $name = trim((string) $row["Name"]);
                $title = trim(
                    preg_replace(
                        "/[\s\p{Z}\p{Cc}]+/u",
                        " ",
                        (string) $row["Title"],
                    ) ?? "",
                );
                if (
                    !preg_match('/^[A-Za-z0-9_]{4,32}$/D', $name) ||
                    $title === "" ||
                    !mb_check_encoding($title, "UTF-8")
                ) {
                    $counts["invalid_public_link"]++;
                    continue;
                }
                $haystack = $title . " " . $name;
                // Keep this general demo corpus away from explicit/adult or illicit directories.
                if (
                    preg_match(
                        "/porn|xxx|nsfw|hentai|erotic|onlyfans|pedo|loli|gore|\bsex\b|порно|эрот|секс|наркот|幼女|萝莉|儿童色情|未成年|小学生|初中生|幼齿|幼交|兽交|强奸|迷奸|裸聊|色情|成人视频|成人片|约炮|成人视频|博彩|赌场|网赌|赌博|洗钱|贩毒|冰毒|可卡因/iu",
                        $haystack,
                    )
                ) {
                    $counts["excluded_content"]++;
                    continue;
                }
                try {
                    $url = TelegramLink::normalize("https://t.me/" . $name);
                } catch (\app\common\exception\DomainError) {
                    $counts["invalid_public_link"]++;
                    continue;
                }
                $snapshot =
                    $row["SnapshotAt"] === "1970-01-01 00:00:00"
                        ? null
                        : $row["SnapshotAt"];
                $topics = [];
                foreach (self::TOPICS as $label => $pattern) {
                    if (preg_match($pattern, $haystack)) {
                        $topics[] = $label;
                    }
                }
                $han = (bool) preg_match("/[\x{4e00}-\x{9fff}]/u", $title);
                $record = [
                    "url" => $url,
                    "title" => $title,
                    "username" => $name,
                    "source_row" => $counts["source_rows"] + 1,
                    "source_id" => $row["Id"],
                    "snapshot_at" => $snapshot,
                    "language" => $row["TopLang"] ?: null,
                    "topics" => array_values(
                        array_filter(
                            array_unique(explode(",", implode(",", $topics))),
                            fn($value) => $value !== "",
                        ),
                    ),
                    "contains_han" => $han,
                    "priority" =>
                        ($han ? 100000 : 0) +
                        ($topics ? 10000 : 0) +
                        ($row["TopLang"] === "en" ? 1000 : 0) +
                        ($snapshot ? 100 : 0),
                ];
                if (isset($channels[$url])) {
                    $counts["duplicate_username"]++;
                    if (
                        ($channels[$url]["snapshot_at"] ?? "") >=
                        ($snapshot ?? "")
                    ) {
                        continue;
                    }
                }
                $channels[$url] = $record;
            }
        } finally {
            fclose($stream);
            $zip->close();
        }
        $counts["eligible_unique"] = count($channels);
        uasort(
            $channels,
            fn($a, $b) => $b["priority"] <=> $a["priority"] ?:
            strcmp(hash("sha256", $a["url"]), hash("sha256", $b["url"])),
        );
        $selected = array_values(array_slice($channels, 0, $limit, true));
        $counts["selected"] = count($selected);
        $counts["selected_han_titles"] = count(
            array_filter($selected, fn($r) => $r["contains_han"]),
        );
        $counts["selected_topic_matches"] = count(
            array_filter($selected, fn($r) => $r["topics"]),
        );
        return ["records" => $selected, "counts" => $counts];
    }

    public static function resource(array $record): array
    {
        return [
            "title" => mb_substr($record["title"], 0, 180),
            "url" => $record["url"],
            "type" => "channel",
            "members" => null,
            "description" =>
                "公开频道历史快照，当前可访问性尚未核验。" .
                ($record["snapshot_at"]
                    ? "来源记录时间：" . $record["snapshot_at"] . "。"
                    : "来源未提供该频道的快照时间。") .
                (mb_strlen($record["title"]) > 180
                    ? "\n原始名称：" . mb_substr($record["title"], 0, 1000)
                    : ""),
            "tags" => implode(
                ",",
                array_filter([$record["username"], ...$record["topics"]]),
            ),
            "source_file" => "Channels.csv",
            "source_row" => $record["source_row"],
        ];
    }
}
