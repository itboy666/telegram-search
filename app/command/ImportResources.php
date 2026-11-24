<?php
namespace app\command;

use app\common\service\ResourceImport;
use think\console\{Command, Input, Output};
use think\console\input\{Argument, Option};

class ImportResources extends Command
{
    protected function configure(): void
    {
        $this->setName("resources:import")
            ->setDescription("导入整理后的资源 JSON，默认只预检，不修改已有资源")
            ->addArgument("file", Argument::REQUIRED, "JSON 文件（含 rows 数组）")
            ->addOption("apply", null, Option::VALUE_NONE, "确认写入本地资源库")
            ->addOption("source", null, Option::VALUE_REQUIRED, "批次标识");
    }

    protected function execute(Input $input, Output $output): int
    {
        try {
            $file = realpath((string) $input->getArgument("file"));
            if (!$file || !is_file($file) || filesize($file) > 50 * 1024 * 1024) {
                throw new \RuntimeException("请输入存在且小于 50 MB 的本地 JSON 文件。");
            }
            $data = json_decode(file_get_contents($file), true, 512, JSON_THROW_ON_ERROR);
            if (!is_array($data) || !isset($data["rows"]) || !is_array($data["rows"])) {
                throw new \RuntimeException("JSON 须包含 rows 数组。");
            }
            $source = $input->getOption("source") ?: "import_" . substr(hash_file("sha256", $file), 0, 16);
            $directory = root_path() . "runtime/import-reports/";
            if (!is_dir($directory) && !mkdir($directory, 0700, true)) {
                throw new \RuntimeException("无法创建导入报告目录。");
            }
            $reportPath = $directory . date("Ymd-His") . "-" . bin2hex(random_bytes(4)) . ".json";
            $handle = fopen($reportPath, "x");
            if (!$handle) {
                throw new \RuntimeException("无法创建导入报告。");
            }
            chmod($reportPath, 0600);
            try {
                $report = ResourceImport::run($data["rows"], (string) $source, (bool) $input->getOption("apply"));
                $report["input_sha256"] = hash_file("sha256", $file);
                $report["manifest"] = $data["manifest"] ?? [];
                $encoded = json_encode($report, JSON_UNESCAPED_UNICODE | JSON_PRETTY_PRINT | JSON_THROW_ON_ERROR);
                if (fwrite($handle, $encoded) !== strlen($encoded)) {
                    throw new \RuntimeException("数据库处理已完成，但报告写入不完整；重新预检可核对现有记录。");
                }
            } finally {
                fclose($handle);
            }
            $output->writeln($report["mode"] === "apply" ? "导入完成。" : "预检完成，未写入数据库。");
            $output->writeln(json_encode($report["counts"], JSON_UNESCAPED_UNICODE));
            $output->writeln("批次：" . $source);
            $output->writeln("报告：" . $reportPath);
            return 0;
        } catch (\Throwable $e) {
            $output->writeln("导入未完成：" . ($e instanceof \PDOException ? "数据库操作失败，事务已回滚。" : $e->getMessage()));
            return 1;
        }
    }
}
