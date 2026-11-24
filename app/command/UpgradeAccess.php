<?php
namespace app\command;

use app\common\service\AccessMigration;
use think\console\{Command, Input, Output};

class UpgradeAccess extends Command
{
    protected function configure(): void
    {
        $this->setName("app:upgrade-access")->setDescription(
            "迁移独立管理员、会话与 RBAC 表（可重复运行；先停止开发服务）",
        );
    }
    protected function execute(Input $input, Output $output): int
    {
        $result = AccessMigration::run();
        $output->writeln(
            $result["already_applied"]
                ? "权限数据库已是当前版本。"
                : "权限迁移完成，已迁移管理员：" . $result["migrated"],
        );
        if (!empty($result["backup"])) {
            $output->writeln("迁移前账号及会话备份：" . $result["backup"]);
        }
        return 0;
    }
}
