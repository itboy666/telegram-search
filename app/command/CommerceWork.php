<?php
namespace app\command;
use think\console\{Command, Input, Output};
use think\facade\Db;
use app\common\service\{Payments, AdDelivery};
use app\common\exception\DomainError;

class CommerceWork extends Command
{
    protected function configure(): void
    {
        $this->setName("commerce:work")
            ->setDescription("自动核查链上充值与广告投放")
            ->addOption("once");
    }
    protected function execute(Input $input, Output $output): int
    {
        // Local singleton; deployment across hosts is also protected by a MySQL advisory lock.
        $lock = fopen(runtime_path() . "commerce-worker.lock", "c+");
        if (!$lock || !flock($lock, LOCK_EX | LOCK_NB)) {
            $output->writeln("财务任务已在运行。");
            return 1;
        }
        $stop = false;
        if (function_exists("pcntl_async_signals")) {
            pcntl_async_signals(true);
            foreach ([SIGTERM, SIGINT] as $sig) {
                pcntl_signal($sig, function () use (&$stop) {
                    $stop = true;
                });
            }
        }
        do {
            $acquired = false;
            try {
                $acquired =
                    (bool) (Db::query(
                        "SELECT GET_LOCK('starindex_commerce_worker',0) AS n",
                    )[0]["n"] ?? 0);
                if ($acquired) {
                    Db::name("recharges")
                        ->where("method", "trc20")
                        ->where("status", "pending")
                        ->where("expires_at", "<", date("Y-m-d H:i:s"))
                        ->update(["status" => "expired"]);
                    // Rotate through orders, including recently expired orders whose transfer may confirm late.
                    $orders = Db::name("recharges")
                        ->where("method", "trc20")
                        ->whereIn("status", ["pending", "expired"])
                        ->where(
                            "expires_at",
                            ">",
                            date("Y-m-d H:i:s", time() - 86400),
                        )
                        ->order("id")
                        ->column("id");
                    foreach ($orders as $id) {
                        if ($stop) {
                            break;
                        }
                        try {
                            Payments::check((int) $id);
                        } catch (DomainError $e) {
                            if ($e->status !== 409) {
                                $output->writeln(
                                    "链上查询暂不可用，下轮重试。",
                                );
                                break;
                            }
                        }
                        usleep(250000);
                    }
                    AdDelivery::maintain();
                    foreach (
                        Db::name("ad_deliveries")
                            ->where("state", "queued")
                            ->order("id")
                            ->limit(20)
                            ->column("id")
                        as $id
                    ) {
                        if ($stop) {
                            break;
                        }
                        AdDelivery::run((int) $id);
                    }
                    Db::name("bot_login_codes")
                        ->where(
                            "expires_at",
                            "<",
                            date("Y-m-d H:i:s", time() - 86400),
                        )
                        ->delete();
                }
            } catch (\Throwable) {
                $output->writeln("财务任务本轮异常，保留订单，下轮重试。");
            } finally {
                if ($acquired) {
                    Db::query(
                        "SELECT RELEASE_LOCK('starindex_commerce_worker')",
                    );
                }
            }
            if ($input->getOption("once")) {
                break;
            }
            for ($i = 0; $i < 60 && !$stop; $i++) {
                usleep(500000);
            }
        } while (!$stop);
        flock($lock, LOCK_UN);
        fclose($lock);
        return 0;
    }
}
