<?php
namespace app\command;
use think\console\{Command, Input, Output};
use app\common\service\BotSecuritySettings;
class UpgradeBotSecurity extends Command
{
    protected function configure(): void
    {
        $this->setName("app:upgrade-bot-security")->setDescription(
            "创建群安全策略和搜索验证状态表",
        );
    }
    protected function execute(Input $input, Output $output): int
    {
        BotSecuritySettings::migrate();
        $output->writeln("机器人安全策略数据库已就绪。");
        return 0;
    }
}
