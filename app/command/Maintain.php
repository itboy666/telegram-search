<?php
namespace app\command;
use think\console\{Command, Input, Output};
use think\facade\Db;
class Maintain extends Command
{
    protected function configure(): void
    {
        $this->setName("app:maintain")->setDescription(
            "清理过期会话、限流桶与 90 天前搜索记录",
        );
    }
    protected function execute(Input $input, Output $output): int
    {
        $now = date("Y-m-d H:i:s");
        foreach (
            [
                "sessions",
                "admin_sessions",
                "bot_sessions",
                "bot_verifications",
                "rate_limits",
            ]
            as $table
        ) {
            $count = Db::name($table)->where("expires_at", "<", $now)->delete();
            $output->writeln($table . "：清理 " . $count . " 条过期记录");
        }
        Db::name("bot_updates")
            ->whereIn("state", ["done", "ignored"])
            ->where("created_at", "<", date("Y-m-d H:i:s", time() - 7 * 86400))
            ->delete();
        Db::name("search_logs")
            ->where("created_at", "<", date("Y-m-d H:i:s", time() - 90 * 86400))
            ->delete();
        return 0;
    }
}
