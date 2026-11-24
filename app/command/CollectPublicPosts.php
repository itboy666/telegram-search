<?php
namespace app\command;

use app\common\service\{
    CuratedBotsDataset,
    CurlieDataset,
    PublicTelegramPage,
    ResourceImport,
    TelegramPreviewDataset,
};
use think\console\{Command, Input, Output};
use think\console\input\{Argument, Option};
use think\facade\Db;

class CollectPublicPosts extends Command
{
    protected function configure(): void
    {
        $this->setName("resources:collect-public")
            ->setDescription(
                "采集公开频道预览中的真实帖子；按实际媒体类型分类，保留缓存与来源",
            )
            ->addArgument(
                "directory",
                Argument::REQUIRED,
                "含 tgrpg-entries.json 的来源目录",
            )
            ->addOption(
                "pages",
                null,
                Option::VALUE_REQUIRED,
                "每个频道最多预览页数",
                "5",
            )
            ->addOption(
                "target",
                null,
                Option::VALUE_REQUIRED,
                "每类目标唯一记录数",
                "50000",
            )
            ->addOption(
                "apply",
                null,
                Option::VALUE_NONE,
                "写入数据库；默认只预检",
            );
    }

    protected function execute(Input $input, Output $output): int
    {
        ini_set("memory_limit", "768M");
        $reportDir = null;
        try {
            $directory = realpath((string) $input->getArgument("directory"));
            $pages = filter_var(
                $input->getOption("pages"),
                FILTER_VALIDATE_INT,
            );
            $target = filter_var(
                $input->getOption("target"),
                FILTER_VALIDATE_INT,
            );
            if (
                !$directory ||
                !$pages ||
                $pages < 1 ||
                $pages > 200 ||
                !$target ||
                $target < 1 ||
                $target > 100000
            ) {
                throw new \RuntimeException(
                    "目录、页数（1–200）或目标数量（1–100000）无效。",
                );
            }
            $data = json_decode(
                file_get_contents("$directory/tgrpg-entries.json"),
                true,
                512,
                JSON_THROW_ON_ERROR,
            );
            $seeds = [
                "telegram" => "Telegram,软件",
                "durov" => "Telegram,技术",
                "contest" => "开发,设计",
            ];
            $topics = [
                "software" => "软件",
                "tech" => "开发,编程",
                "ai" => "人工智能,AI",
                "learning" => "教育,学习",
                "wallpaper" => "摄影,设计",
                "video" => "影音,音乐",
                "stickers" => "贴纸",
            ];
            foreach ($data["entries"] as $entry) {
                if (
                    ($entry["kind"] ?? "") === "channel" &&
                    isset($topics[$entry["category"]]) &&
                    preg_match('/^[a-zA-Z0-9_]{4,32}$/D', $entry["username"]) &&
                    !CurlieDataset::excluded(
                        $entry["title"] . " " . ($entry["description"] ?? ""),
                    )
                ) {
                    $seeds[strtolower($entry["username"])] =
                        $topics[$entry["category"]];
                }
            }
            // Existing channel metadata is only used to discover audio candidates.
            // Classification still comes exclusively from the observed post markup.
            $musicSeeds = Db::name("resources")
                ->where("type", "channel")
                ->where("status", "published")
                ->whereLike("tags", "%音乐%")
                ->order("id", "asc")
                ->limit(30)
                ->field("title,url")
                ->select()
                ->toArray();
            foreach ($musicSeeds as $seed) {
                $name = ltrim(
                    (string) parse_url($seed["url"], PHP_URL_PATH),
                    "/",
                );
                if (
                    preg_match('/^[a-zA-Z0-9_]{4,32}$/D', $name) &&
                    !preg_match(
                        "/NS Music|IronMensch|Right Music Crew|TEAM OF WAR/i",
                        $seed["title"],
                    ) &&
                    !CurlieDataset::excluded($seed["title"])
                ) {
                    $seeds[$name] = "音乐";
                }
            }
            $cacheDir = "$directory/public-previews";
            if (!is_dir($cacheDir)) {
                mkdir($cacheDir, 0700, true);
            }
            $reportDir =
                runtime_path() .
                "import-reports/public-" .
                date("Ymd-His") .
                "-" .
                bin2hex(random_bytes(3));
            mkdir($reportDir, 0700, true);
            $writer = new CollectCatalog();
            $summary = [
                "mode" => $input->getOption("apply") ? "apply" : "dry_run",
                "created_at" => date(DATE_ATOM),
                "target_per_type" => $target,
                "pages_per_channel" => $pages,
                "seed_count" => count($seeds),
                "seed_source" =>
                    CuratedBotsDataset::SOURCES["tgrpg_bots_20261116"],
                "music_seed_source" =>
                    "已收录的频道元数据（仅用于发现入口，逐帖按页面类型核验）",
                "selected_by_type" => array_fill_keys(
                    ["video", "article", "audio", "file", "other"],
                    0,
                ),
                "counts" => [],
                "channels" => [],
                "complete" => false,
                "changes" =>
                    "按可见媒体元素分类；标题/摘要最多80/160字；不下载媒体正文、不猜测帖子编号、不重分类频道为店铺。",
            ];
            $seen = $pending = [];
            $batch = 0;
            $flush = function () use (
                &$pending,
                &$summary,
                &$batch,
                $writer,
                $reportDir,
                $input,
                $output,
            ): void {
                if (!$pending) {
                    return;
                }
                $file = "$reportDir/batch-" . ++$batch . ".json";
                $writer->save($file, ["rows" => $pending, "report" => null]);
                $report = ResourceImport::run(
                    $pending,
                    TelegramPreviewDataset::SOURCE,
                    (bool) $input->getOption("apply"),
                );
                $writer->save($file, ["rows" => $pending, "report" => $report]);
                foreach ($report["counts"] as $key => $n) {
                    $summary["counts"][$key] =
                        ($summary["counts"][$key] ?? 0) + $n;
                }
                $pending = [];
                $writer->save("$reportDir/summary.json", $summary);
                $output->writeln(
                    "帖子批次 {$batch}：" . json_encode($report["counts"]),
                );
            };
            foreach (array_chunk($seeds, 4, true) as $seedGroup) {
                $active = array_fill_keys(array_keys($seedGroup), null);
                for ($page = 0; $page < $pages && $active; $page++) {
                    $paths = $bodies = [];
                    foreach ($active as $name => $before) {
                        $file =
                            "$cacheDir/$name-" .
                            ($before ?? "latest") .
                            ".html";
                        if (is_file($file)) {
                            $bodies[$name] = file_get_contents($file);
                        } else {
                            $paths[$name] =
                                "/s/$name" . ($before ? "?before=$before" : "");
                        }
                    }
                    if ($paths) {
                        $fetched = PublicTelegramPage::getMany($paths);
                        foreach ($fetched as $name => $html) {
                            if ($html !== null) {
                                file_put_contents(
                                    "$cacheDir/$name-" .
                                        ($active[$name] ?? "latest") .
                                        ".html",
                                    $html,
                                    LOCK_EX,
                                );
                            }
                            $bodies[$name] = $html;
                        }
                        usleep(1000000);
                    }
                    foreach ($active as $name => $before) {
                        $state = $summary["channels"][$name] ?? [
                            "pages" => 0,
                            "selected" => 0,
                            "status" => "page_limit",
                        ];
                        if (!is_string($bodies[$name] ?? null)) {
                            $state["status"] = "unavailable";
                            $summary["channels"][$name] = $state;
                            unset($active[$name]);
                            continue;
                        }
                        $result = TelegramPreviewDataset::parse(
                            $bodies[$name],
                            $name,
                            $seedGroup[$name],
                        );
                        $state["pages"]++;
                        foreach ($result["rows"] as $row) {
                            if (
                                isset($seen[$row["url"]]) ||
                                $summary["selected_by_type"][$row["type"]] >=
                                    $target
                            ) {
                                continue;
                            }
                            $seen[$row["url"]] = true;
                            $summary["selected_by_type"][$row["type"]]++;
                            $pending[] = $row;
                            $state["selected"]++;
                        }
                        if (
                            !$result["before"] ||
                            ($before && $result["before"] >= $before)
                        ) {
                            $state["status"] = $result["counts"]["observed"]
                                ? "exhausted"
                                : "no_public_posts";
                            unset($active[$name]);
                        } else {
                            $active[$name] = $result["before"];
                        }
                        $summary["channels"][$name] = $state;
                    }
                    if (count($pending) >= 1000) {
                        $flush();
                    }
                }
                $flush();
                $writer->save("$reportDir/summary.json", $summary);
                $output->writeln(
                    "已处理频道 " .
                        count($summary["channels"]) .
                        "/" .
                        count($seeds) .
                        "；分类：" .
                        json_encode($summary["selected_by_type"]),
                );
            }
            $summary["complete"] = true;
            $summary["targets_reached_by_type"] = array_map(
                static fn($count) => $count >= $target,
                $summary["selected_by_type"],
            );
            $writer->save("$reportDir/summary.json", $summary);
            $output->writeln("报告：" . $reportDir . "/summary.json");
            return 0;
        } catch (\Throwable $e) {
            $output->writeln(
                "采集未完成：" .
                    ($e instanceof \PDOException
                        ? "数据库处理失败。"
                        : $e->getMessage()),
            );
            if ($reportDir) {
                $output->writeln(
                    "已提交批次保留，缓存可复用。报告：" . $reportDir,
                );
            }
            return 1;
        }
    }
}
