<?php
namespace app\common\service;

class CatalogSource
{
    public static function attribution(string $source): ?array
    {
        $data = match ($source) {
            CurlieDataset::SOURCE => CurlieDataset::ATTRIBUTION,
            ZenodoDataset::SOURCE => ZenodoDataset::ATTRIBUTION,
            TelegramPreviewDataset::SOURCE => [
                "title" => "Telegram 公开帖子预览",
                "author" => "各链接对应的频道作者",
                "url" => "https://telegram.org/",
                "license" => "原作者保留权利",
                "changes" =>
                    "仅索引公开帖子链接和短摘要；类型来自页面实际媒体元素，未下载媒体文件。",
            ],
            default => CuratedBotsDataset::SOURCES[$source] ?? null,
        };
        if (!$data) {
            return null;
        }
        return ["key" => $source] +
            $data + [
                "changes" =>
                    "规范化链接、截短过长字段；来源快照不代表当前内容和可用性。",
            ];
    }
}
