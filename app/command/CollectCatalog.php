<?php
namespace app\command;

use app\common\service\{
    CuratedBotsDataset,
    CurlieDataset,
    PublicTelegramPage,
    ResourceImport,
    TelegramLink,
};
use think\console\{Command, Input, Output};
use think\console\input\{Argument, Option};
use think\facade\Db;

class CollectCatalog extends Command
{
    protected function configure(): void
    {
        $this->setName("resources:collect-catalog")
            ->setDescription("导入 Curlie 网站或核验公开机器人目录；默认预检")
            ->addArgument("dataset", Argument::REQUIRED, "curlie 或 bots")
            ->addArgument("directory", Argument::REQUIRED, "原始数据目录")
            ->addOption(
                "limit",
                null,
                Option::VALUE_REQUIRED,
                "最多选取数量",
                "50000",
            )
            ->addOption("apply", null, Option::VALUE_NONE, "实际入库");
    }

    protected function execute(Input $input, Output $output): int
    {
        ini_set("memory_limit", "768M");
        $reportDir = null;
        try {
            $limit = filter_var(
                $input->getOption("limit"),
                FILTER_VALIDATE_INT,
            );
            $directory = realpath((string) $input->getArgument("directory"));
            if (
                !$directory ||
                !is_dir($directory) ||
                !$limit ||
                $limit > 100000 ||
                $limit < 1
            ) {
                throw new \RuntimeException("目录或数量无效（1–100000）。");
            }
            $apply = (bool) $input->getOption("apply");
            $dataset = $input->getArgument("dataset");
            if (!in_array($dataset, ["curlie", "bots"], true)) {
                throw new \RuntimeException("未知数据集。");
            }
            $reportDir =
                runtime_path() .
                "import-reports/catalog-" .
                $dataset .
                "-" .
                date("Ymd-His") .
                "-" .
                bin2hex(random_bytes(3));
            mkdir($reportDir, 0700, true);
            $summary = [
                "created_at" => date(DATE_ATOM),
                "mode" => $apply ? "apply" : "dry_run",
                "target" => $limit,
                "complete" => false,
                "sources" => [],
                "counts" => [],
                "selection" => [],
                "files" => [],
            ];
            if ($dataset === "curlie") {
                $result = CurlieDataset::read($directory, $limit);
                $groups = [CurlieDataset::SOURCE => $result["rows"]];
                $summary["selection"] = $result["selection"];
                $summary["sources"] = [
                    CurlieDataset::SOURCE => CurlieDataset::ATTRIBUTION,
                ];
                foreach (glob($directory . "/*.tsv") as $file) {
                    $summary["files"][basename($file)] = hash_file(
                        "sha256",
                        $file,
                    );
                }
            } else {
                $groups = CuratedBotsDataset::read($directory);
                $summary["sources"] = CuratedBotsDataset::SOURCES;
                $groups = $this->verifyBots(
                    $groups,
                    $directory,
                    $summary,
                    $output,
                    $limit,
                );
                foreach (
                    ["tgrpg-entries.json", "danyspin-bots.md", "erkcet-bots.md"]
                    as $file
                ) {
                    $summary["files"][$file] = hash_file(
                        "sha256",
                        "$directory/$file",
                    );
                }
            }
            $this->save("$reportDir/summary.json", $summary);
            foreach ($groups as $source => $rows) {
                foreach (array_chunk($rows, 2500) as $batch => $chunk) {
                    $path = "$reportDir/$source-" . ($batch + 1) . ".json";
                    $this->save($path, ["rows" => $chunk, "report" => null]);
                    $report = ResourceImport::run($chunk, $source, $apply);
                    $this->save($path, ["rows" => $chunk, "report" => $report]);
                    foreach ($report["counts"] as $key => $count) {
                        $summary["counts"][$key] =
                            ($summary["counts"][$key] ?? 0) + $count;
                    }
                    $this->save("$reportDir/summary.json", $summary);
                    $output->writeln(
                        $source .
                            " 批次 " .
                            ($batch + 1) .
                            "：" .
                            json_encode($report["counts"]),
                    );
                }
            }
            $summary["complete"] = true;
            $summary["target_reached"] =
                ($summary["selection"]["selected"] ??
                    ($summary["selection"]["verified"] ?? 0)) >=
                $limit;
            $summary["database_by_type"] = Db::name("resources")
                ->field("type,COUNT(*) as total")
                ->group("type")
                ->select()
                ->toArray();
            $this->save("$reportDir/summary.json", $summary);
            $output->writeln(
                json_encode($summary["selection"], JSON_UNESCAPED_UNICODE),
            );
            $output->writeln("报告：" . $reportDir . "/summary.json");
            return 0;
        } catch (\Throwable $e) {
            $output->writeln(
                "未完成：" .
                    ($e instanceof \PDOException
                        ? "数据库处理失败，请检查报告。"
                        : $e->getMessage()),
            );
            if ($reportDir) {
                $output->writeln(
                    "已提交批次保留；重跑跳过重复链接。报告：" . $reportDir,
                );
            }
            return 1;
        }
    }

    private function verifyBots(
        array $groups,
        string $directory,
        array &$summary,
        Output $output,
        int $limit,
    ): array {
        $cacheFile = "$directory/bot-preview-checks.json";
        $cache = is_file($cacheFile)
            ? json_decode(
                file_get_contents($cacheFile),
                true,
                512,
                JSON_THROW_ON_ERROR,
            )
            : [];
        $seen = $verified = [];
        $counts = [
            "candidates" => 0,
            "duplicate" => 0,
            "excluded" => 0,
            "not_bot_or_unavailable" => 0,
            "verified" => 0,
        ];
        foreach ($groups as $source => $rows) {
            foreach ($rows as $row) {
                $counts["candidates"]++;
                if (
                    CurlieDataset::excluded(
                        $row["title"] . " " . $row["description"],
                    )
                ) {
                    $counts["excluded"]++;
                    continue;
                }
                try {
                    $url = TelegramLink::normalize($row["url"]);
                } catch (\Throwable) {
                    $counts["not_bot_or_unavailable"]++;
                    continue;
                }
                if (isset($seen[$url])) {
                    $counts["duplicate"]++;
                    continue;
                }
                $seen[$url] = true;
                if (
                    !isset($cache[$url]) ||
                    ($cache[$url]["checked_at"] ?? 0) < time() - 86400
                ) {
                    try {
                        $html = PublicTelegramPage::get(
                            parse_url($url, PHP_URL_PATH),
                        );
                        $cache[$url] = [
                            "is_bot" => PublicTelegramPage::isBot($html),
                            "checked_at" => time(),
                        ];
                    } catch (\Throwable $e) {
                        // Transient failures are not cached, so rerunning retries them.
                        $counts["not_bot_or_unavailable"]++;
                        usleep(500000);
                        continue;
                    }
                    $this->save($cacheFile, $cache);
                    usleep(300000);
                }
                if (!$cache[$url]["is_bot"]) {
                    $counts["not_bot_or_unavailable"]++;
                    continue;
                }
                $row["url"] = $url;
                $row["verified_at"] = date(
                    DATE_ATOM,
                    $cache[$url]["checked_at"],
                );
                $verified[$source][] = $row;
                $counts["verified"]++;
                if ($counts["verified"] % 50 === 0) {
                    $output->writeln(
                        "公开页面已核验机器人：" . $counts["verified"],
                    );
                }
                if ($counts["verified"] >= $limit) {
                    break 2;
                }
            }
        }
        $summary["selection"] = $counts;
        return $verified;
    }

    public function save(string $path, array $data): void
    {
        $json = json_encode(
            $data,
            JSON_UNESCAPED_UNICODE | JSON_PRETTY_PRINT | JSON_THROW_ON_ERROR,
        );
        if (
            file_put_contents($path . ".tmp", $json, LOCK_EX) !==
                strlen($json) ||
            !rename($path . ".tmp", $path)
        ) {
            throw new \RuntimeException("无法保存采集报告。");
        }
        chmod($path, 0600);
    }
}
