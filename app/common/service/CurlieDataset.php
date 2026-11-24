<?php
namespace app\common\service;

use app\common\exception\DomainError;

/** Reads publisher TSV exports; preserves source category and row in every record. */
class CurlieDataset
{
    public const SOURCE = "curlie_20261116";
    public const ATTRIBUTION = [
        "title" => "Curlie Directory",
        "author" => "Curlie.org community",
        "url" => "https://curlie.org/docs/en/rdf.html",
        "license" => "CC BY 3.0",
        "license_url" => "https://creativecommons.org/licenses/by/3.0/",
        "changes" =>
            "优先中文目录，其次英文行业目录；规范化网址、移除 HTML、截短超长字段；保留分类标签。未逐条验证网站当前可用性。",
    ];

    public static function read(string $directory, int $limit): array
    {
        $rows = $seen = [];
        $counts = [
            "scanned" => 0,
            "invalid" => 0,
            "duplicate" => 0,
            "excluded" => 0,
            "chinese" => 0,
        ];
        foreach (["World", "Business", "Top"] as $part) {
            $categories = [];
            foreach (self::lines("$directory/rdf-$part-s.tsv") as [, $fields]) {
                if (count($fields) >= 2) {
                    $categories[$fields[0]] = $fields[1];
                }
            }
            foreach (
                self::lines("$directory/rdf-$part-c.tsv")
                as [$line, $fields]
            ) {
                $counts["scanned"]++;
                if (count($fields) !== 4) {
                    $counts["invalid"]++;
                    continue;
                }
                [$url, $title, $description, $categoryId] = $fields;
                $category = $categories[$categoryId] ?? "";
                if (
                    $part === "World" &&
                    !preg_match(
                        '#^World/Chinese_(?:Simplified|Traditional)(?:/|$)#D',
                        $category,
                    )
                ) {
                    continue;
                }
                if (self::excluded($title . " " . $category)) {
                    $counts["excluded"]++;
                    continue;
                }
                try {
                    $url = ResourceLink::normalize($url, "website");
                } catch (DomainError) {
                    $counts["invalid"]++;
                    continue;
                }
                if (isset($seen[$url])) {
                    $counts["duplicate"]++;
                    continue;
                }
                $title = self::text($title, 180);
                if ($title === "") {
                    $counts["invalid"]++;
                    continue;
                }
                $seen[$url] = true;
                $counts["chinese"] += $part === "World" ? 1 : 0;
                $rows[] = [
                    "title" => $title,
                    "url" => $url,
                    "type" => "website",
                    "description" => self::text($description, 5000),
                    "tags" => mb_substr(
                        str_replace("/", ",", $category) .
                            "," .
                            self::topics($category),
                        0,
                        255,
                    ),
                    "source_file" => "rdf-$part-c.tsv",
                    "source_row" => $line,
                    "source_sheet" => $category,
                ];
                if (count($rows) >= $limit) {
                    break 2;
                }
            }
        }
        $counts["selected"] = count($rows);
        return ["rows" => $rows, "selection" => $counts];
    }

    private static function lines(string $file): \Generator
    {
        $handle = @fopen($file, "rb");
        if (!$handle) {
            throw new \RuntimeException("无法读取数据文件：" . basename($file));
        }
        try {
            $line = 0;
            while (($text = fgets($handle)) !== false) {
                yield [++$line, explode("\t", rtrim($text, "\r\n"))];
            }
        } finally {
            fclose($handle);
        }
    }

    public static function text(string $value, int $length): string
    {
        return mb_substr(
            trim(
                preg_replace(
                    "/[\s\p{Z}\p{Cc}]+/u",
                    " ",
                    html_entity_decode(
                        strip_tags($value),
                        ENT_QUOTES | ENT_HTML5,
                        "UTF-8",
                    ),
                ) ?? "",
            ),
            0,
            $length,
        );
    }

    public static function excluded(string $value): bool
    {
        return (bool) preg_match(
            "/porn|xxx|nsfw|hentai|erotic|onlyfans|pedo|loli|gore|\bsex\b|порно|эрот|секс|наркот|色情|成人视频|博彩|赌场|网赌|赌博|洗钱|贩毒|冰毒|可卡因/iu",
            $value,
        );
    }

    public static function topics(string $text): string
    {
        $labels = [];
        foreach (ZenodoDataset::TOPICS as $label => $pattern) {
            if (preg_match($pattern, str_replace("_", " ", $text))) {
                $labels[] = $label;
            }
        }
        foreach (
            [
                "Business" => "商业,企业",
                "Industrial" => "工业,制造",
                "Textiles" => "纺织",
                "Agriculture" => "农业",
                "Software" => "软件",
                "Construction" => "建筑",
                "Chemicals" => "化工",
                "Electronics" => "电子",
                "Transportation" => "物流,运输",
                "Retail" => "零售",
                "Computers" => "计算机",
            ]
            as $word => $label
        ) {
            if (stripos($text, $word) !== false) {
                $labels[] = $label;
            }
        }
        return implode(",", $labels);
    }
}
