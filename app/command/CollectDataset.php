<?php
namespace app\command;

use app\common\service\{ResourceImport, ZenodoDataset};
use think\console\{Command, Input, Output};
use think\console\input\{Argument, Option};
use think\facade\Db;

class CollectDataset extends Command
{
    protected function configure(): void
    {
        $this->setName("resources:collect-dataset")
            ->setDescription(
                "读取已下载的公开频道数据集，校验、筛选、去重并分批导入",
            )
            ->addArgument(
                "file",
                Argument::REQUIRED,
                "Zenodo 14749767 原始 ZIP",
            )
            ->addOption(
                "limit",
                null,
                Option::VALUE_REQUIRED,
                "选取的唯一频道数量",
                "105000",
            )
            ->addOption(
                "apply",
                null,
                Option::VALUE_NONE,
                "写入数据库，默认仅预检",
            );
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
            if ($limit === false) {
                throw new \RuntimeException("数量必须为整数。");
            }
            $file = realpath((string) $input->getArgument("file"));
            $apply = (bool) $input->getOption("apply");
            $dataset = ZenodoDataset::read($file ?: "", $limit);
            $reportDir =
                root_path() .
                "runtime/import-reports/dataset-" .
                date("Ymd-His") .
                "-" .
                bin2hex(random_bytes(3));
            if (!mkdir($reportDir, 0700, true)) {
                throw new \RuntimeException("无法创建采集报告目录。");
            }
            $summary = [
                "mode" => $apply ? "apply" : "dry_run",
                "created_at" => date(DATE_ATOM),
                "source" => ZenodoDataset::ATTRIBUTION,
                "source_key" => ZenodoDataset::SOURCE,
                "archive_sha256" => hash_file("sha256", $file),
                "selection" => $dataset["counts"],
                "counts" => [],
                "completed_batches" => 0,
                "complete" => false,
                "changes" =>
                    "公开用户名规范化；重复用户名保留较新快照；排除明显成人/违法目录；标题最多180字；按标题/用户名规则标注主题；不将浏览量当作成员数。",
            ];
            $this->save($reportDir . "/summary.json", $summary);
            $output->writeln(
                json_encode($dataset["counts"], JSON_UNESCAPED_UNICODE),
            );
            foreach (
                array_chunk($dataset["records"], 2500)
                as $number => $records
            ) {
                $batchPath = $reportDir . "/batch-" . ($number + 1) . ".json";
                // Save exact source mapping before committing the corresponding import.
                $this->save($batchPath, [
                    "source_records" => $records,
                    "report" => null,
                ]);
                $report = ResourceImport::run(
                    array_map([ZenodoDataset::class, "resource"], $records),
                    ZenodoDataset::SOURCE,
                    $apply,
                );
                $this->save($batchPath, [
                    "source_records" => $records,
                    "report" => $report,
                ]);
                foreach ($report["counts"] as $key => $value) {
                    $summary["counts"][$key] =
                        ($summary["counts"][$key] ?? 0) + $value;
                }
                $summary["completed_batches"]++;
                $this->save($reportDir . "/summary.json", $summary);
                $output->writeln(
                    "批次 " .
                        ($number + 1) .
                        "：" .
                        json_encode($report["counts"], JSON_UNESCAPED_UNICODE),
                );
            }
            $summary["complete"] = true;
            $summary["database_total"] = Db::name("resources")->count();
            $summary["database_unique_urls"] = Db::name("resources")
                ->distinct(true)
                ->count("url_hash");
            $summary["source_total"] = Db::name("resources")
                ->where("source", ZenodoDataset::SOURCE)
                ->count();
            $this->save($reportDir . "/summary.json", $summary);
            $output->writeln($apply ? "导入完成。" : "预检完成，未写入资源。");
            $output->writeln(
                "数据库资源：" .
                    $summary["database_total"] .
                    "；唯一链接：" .
                    $summary["database_unique_urls"],
            );
            $output->writeln("报告：" . $reportDir . "/summary.json");
            return 0;
        } catch (\Throwable $e) {
            $output->writeln("采集导入未完成：" . $e->getMessage());
            if ($reportDir) {
                $output->writeln(
                    "已提交批次保留，重跑会跳过已有链接。检查报告：" .
                        $reportDir,
                );
            }
            return 1;
        }
    }
    private function save(string $file, array $data): void
    {
        $temp = $file . ".tmp";
        $json = json_encode(
            $data,
            JSON_UNESCAPED_UNICODE | JSON_PRETTY_PRINT | JSON_THROW_ON_ERROR,
        );
        if (
            file_put_contents($temp, $json, LOCK_EX) !== strlen($json) ||
            !rename($temp, $file)
        ) {
            throw new \RuntimeException("报告保存失败。");
        }
        chmod($file, 0600);
    }
}
