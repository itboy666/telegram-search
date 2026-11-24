<?php
namespace app\common\service;

use app\common\exception\DomainError;
use think\facade\Db;

/** Retry the entire outer transaction; callbacks must contain only database work. */
class CatalogTransaction
{
    public static function run(callable $callback): mixed
    {
        $pdo = Db::connect()->getPdo();
        if ($pdo && $pdo->inTransaction()) {
            // A deadlock rolls back the full MySQL transaction, not just a savepoint.
            // Let the owner of that outer transaction decide whether to retry.
            return Db::transaction($callback);
        }
        for ($attempt = 0; $attempt < 5; $attempt++) {
            try {
                return Db::transaction($callback);
            } catch (\Throwable $e) {
                $code =
                    $e instanceof \think\db\exception\PDOException
                        ? $e->getData()["PDO Error Info"][
                                "Driver Error Code"
                            ] ?? 0
                        : ($e instanceof \PDOException
                            ? $e->errorInfo[1] ?? 0
                            : 0);
                if (!in_array((int) $code, [1205, 1213], true)) {
                    throw $e;
                }
                if ($attempt === 4) {
                    throw new DomainError(
                        "当前有其他操作正在处理，请稍后重试。",
                        503,
                    );
                }
                usleep(random_int(20000, 60000) * ($attempt + 1));
            }
        }
        throw new \LogicException("Unreachable transaction state");
    }
}
