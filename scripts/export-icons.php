<?php
// Internal worker for `php think bot:icons`; credentials stay in .env.
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();

try {
    $path = realpath($argv[1] ?? "");
    $worker = filter_var($argv[2] ?? null, FILTER_VALIDATE_INT);
    $workers = filter_var($argv[3] ?? null, FILTER_VALIDATE_INT);
    if (
        !$path ||
        dirname($path) !== realpath(runtime_path()) ||
        !str_starts_with(basename($path), "icon-export-") ||
        $worker === false ||
        $workers === false ||
        $workers < 1 ||
        $workers > 4 ||
        $worker < 0 ||
        $worker >= $workers
    ) {
        throw new RuntimeException("图标导出任务参数无效。");
    }
    $plan = json_decode(
        file_get_contents($path),
        true,
        512,
        JSON_THROW_ON_ERROR,
    );
    new app\command\SyncIcons()->exportSlice(
        $plan["jobs"],
        $worker,
        $workers,
        $plan["converter"],
        new think\console\Output(),
    );
} catch (Throwable $e) {
    fwrite(STDERR, "导出失败：" . $e->getMessage() . PHP_EOL);
    exit(1);
}
