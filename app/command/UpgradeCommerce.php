<?php
namespace app\command;
use think\console\{Command, Input, Output};
class UpgradeCommerce extends Command
{
    protected function configure(): void
    {
        $this->setName("app:upgrade-commerce")->setDescription(
            "升级钱包、支付、奖励和广告表",
        );
    }
    protected function execute(Input $input, Output $output): int
    {
        \app\common\service\CommerceMigration::run();
        $output->writeln("财务与营销数据库升级完成。");
        return 0;
    }
}
