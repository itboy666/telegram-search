<?php
namespace app\command;

use app\common\service\CatalogMigration;
use think\console\{Command, Input, Output};

class UpgradeCatalog extends Command
{
    protected function configure(): void
    {
        $this->setName("app:upgrade-catalog")->setDescription(
            "升级链接唯一性、资源状态和用户投稿权限",
        );
    }
    protected function execute(Input $input, Output $output): int
    {
        try {
            $result = CatalogMigration::run();
            $output->writeln(
                $result["already_applied"]
                    ? "资源库数据库已是当前版本。"
                    : "链接去重与投稿权限升级完成。",
            );
            return 0;
        } catch (\Throwable $e) {
            $output->writeln("升级未完成：" . $e->getMessage());
            return 1;
        }
    }
}
