<?php
/** Foreground supervisor. Stops only the child processes it creates. */
$root = dirname(__DIR__);
chdir($root);
if (
    !is_file($root . "/vendor/autoload.php") ||
    !is_file($root . "/public/build/index.html")
) {
    fwrite(STDERR, "请先安装 Composer 依赖并构建 frontend。\n");
    exit(1);
}
if (!is_dir($root . "/runtime")) {
    mkdir($root . "/runtime", 0755, true);
}
$lock = fopen($root . "/runtime/dev.lock", "c+");
if (($argv[1] ?? "") === "stop") {
    if (flock($lock, LOCK_EX | LOCK_NB)) {
        flock($lock, LOCK_UN);
        fclose($lock);
        echo "开发服务未运行。\n";
        exit(0);
    }
    rewind($lock);
    $pid = (int) trim(stream_get_contents($lock));
    fclose($lock);
    if (
        $pid > 1 &&
        function_exists("posix_kill") &&
        posix_kill($pid, SIGTERM)
    ) {
        echo "已请求开发服务停止。\n";
        exit(0);
    }
    fwrite(STDERR, "无法发送停止信号，请在启动终端使用 Ctrl+C。\n");
    exit(1);
}
if (!flock($lock, LOCK_EX | LOCK_NB)) {
    fwrite(STDERR, "本地开发服务已运行。\n");
    exit(1);
}
ftruncate($lock, 0);
rewind($lock);
fwrite($lock, (string) getmypid());
fflush($lock);
$socket = @stream_socket_client("tcp://127.0.0.1:8088", $errno, $error, 1);
if ($socket) {
    fclose($socket);
    fwrite(STDERR, "8088 端口已使用，请先停止现有服务。\n");
    exit(1);
}
$stop = false;
if (function_exists("pcntl_async_signals")) {
    pcntl_async_signals(true);
    foreach ([SIGINT, SIGTERM] as $signal) {
        pcntl_signal($signal, function () use (&$stop) {
            $stop = true;
        });
    }
}
$children = [];
try {
    foreach (
        [
            "web" => [
                PHP_BINARY,
                "-S",
                "127.0.0.1:8088",
                "-t",
                "public",
                "public/router.php",
            ],
            "bot" => [PHP_BINARY, "think", "bot:poll"],
            "commerce" => [PHP_BINARY, "think", "commerce:work"],
        ]
        as $name => $command
    ) {
        $process = proc_open(
            $command,
            [
                0 => ["pipe", "r"],
                1 => ["file", $root . "/runtime/" . $name . ".log", "a"],
                2 => ["file", $root . "/runtime/" . $name . ".log", "a"],
            ],
            $pipes,
            $root,
        );
        if (!is_resource($process)) {
            throw new RuntimeException("无法启动 " . $name);
        }
        fclose($pipes[0]);
        $children[$name] = $process;
    }
    echo "网站：http://127.0.0.1:8088\n后台：http://127.0.0.1:8088/admin\n机器人：本地长轮询\n财务：自动核账与投放\n日志：runtime/web.log、runtime/bot.log、runtime/commerce.log\nCtrl+C 停止本次启动的服务。\n";
    while (!$stop) {
        foreach ($children as $name => $process) {
            if (!proc_get_status($process)["running"]) {
                echo $name .
                    " 进程已退出，请检查 runtime/" .
                    $name .
                    ".log。\n";
                $stop = true;
            }
        }
        usleep(250000);
    }
} finally {
    foreach ($children as $process) {
        if (proc_get_status($process)["running"]) {
            proc_terminate($process);
        }
    }
    foreach ($children as $process) {
        proc_close($process);
    }
    flock($lock, LOCK_UN);
    fclose($lock);
}
