<?php
namespace app\command;

use app\common\service\BotSearchMigration;
use think\console\{Command, Input, Output};

class UpgradeBotSearch extends Command
{
    protected function configure(): void
    {
        $this->setName("app:upgrade-bot-search")->setDescription(
            "新增机器人搜索分页与筛选状态字段（可重复运行）",
        );
    }
    protected function execute(Input $input, Output $output): int
    {
        $result = BotSearchMigration::run();
        $output->writeln(
            $result["already_applied"]
                ? "机器人搜索数据库已是当前版本。"
                : "机器人搜索迁移完成，已有搜索保留原分页条数。",
        );
        return 0;
    }
}
