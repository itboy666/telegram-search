<?php
namespace app\command;
use think\console\{Command, Input, Output};
use think\console\input\Option;
use app\common\telegram\{Client, Icons};

class SyncIcons extends Command
{
    protected function configure(): void
    {
        $this->setName("bot:icons")
            ->setDescription("从 Telegram iOS Icons 原始 TGS 导出清晰 SVG")
            ->addOption(
                "converter",
                null,
                Option::VALUE_REQUIRED,
                "lottie_convert.py 的路径",
            )
            ->addOption(
                "all",
                null,
                Option::VALUE_NONE,
                "同步整个图标包，保留现有图标名称",
            )
            ->addOption(
                "workers",
                null,
                Option::VALUE_REQUIRED,
                "并行导出进程数，1–4",
                4,
            );
    }
    protected function execute(Input $input, Output $output): int
    {
        $converter =
            $input->getOption("converter") ?:
            root_path() . "runtime/icon-tools/bin/lottie_convert.py";
        if (!is_executable($converter)) {
            throw new \RuntimeException(
                "仅重新生成图标时需要 python-lottie 0.7.2；请按 README 安装或指定 --converter。",
            );
        }
        $client = Client::configured();
        $set = $this->retry(
            fn() => $client->call("getStickerSet", ["name" => "tgiosicons"]),
        );
        if (
            ($set["name"] ?? "") !== "tgiosicons" ||
            ($set["sticker_type"] ?? "") !== "custom_emoji" ||
            empty($set["stickers"])
        ) {
            throw new \RuntimeException("Telegram 返回了不匹配的图标包。");
        }
        $stickers = array_column($set["stickers"], null, "custom_emoji_id");
        $catalog = Icons::catalog();
        if ($input->getOption("all")) {
            $families = require root_path() .
                "resources/icons/telegram-ios-labels.php";
            $existing = array_column($catalog, null, "id");
            foreach ($set["stickers"] as $position => $sticker) {
                $id = (string) $sticker["custom_emoji_id"];
                $emoji = (string) ($sticker["emoji"] ?? "");
                $family = $families[
                    str_replace(["\u{FE0F}", "\u{FE0E}"], "", $emoji)
                ] ?? ["图标", ""];
                if (!isset($existing[$id])) {
                    $catalog["ios_" . $id] = [
                        "label" => $family[0],
                        "emoji" => $emoji ?: "▫️",
                        "id" => $id,
                    ];
                }
                foreach ($catalog as &$icon) {
                    if ($icon["id"] === $id) {
                        $icon["position"] = $position + 1;
                        $icon["keywords"] = $family[1];
                    }
                }
                unset($icon);
            }
        }
        $dir = root_path() . "public/icons/telegram-ios/";
        $sources = root_path() . "resources/icons/telegram-ios/";
        foreach ([$dir, $sources] as $path) {
            if (!is_dir($path)) {
                mkdir($path, 0755, true);
            }
        }
        $jobs = [];
        foreach ($catalog as $name => $icon) {
            $sticker = $stickers[$icon["id"]] ?? null;
            if (
                !preg_match('/^[a-z0-9_]+$/', $name) ||
                !preg_match('/^\d+$/', $icon["id"]) ||
                !$sticker ||
                empty($sticker["is_animated"]) ||
                empty($sticker["needs_repainting"])
            ) {
                throw new \RuntimeException(
                    "图标包中缺少可用单色矢量图标：" . $name,
                );
            }
            $jobs[$icon["id"]]["sticker"] = $sticker;
            $jobs[$icon["id"]]["names"][] = $name;
        }
        $jobs = array_values($jobs);
        $workers = filter_var(
            $input->getOption("workers"),
            FILTER_VALIDATE_INT,
            ["options" => ["min_range" => 1, "max_range" => 4]],
        );
        if (!$workers) {
            throw new \RuntimeException("--workers 必须为 1–4。");
        }
        $output->writeln(
            "核对到 " .
                count($stickers) .
                " 个原始图标，本次导出 " .
                count($jobs) .
                " 个；并行进程 " .
                $workers .
                "。",
        );
        if ($workers === 1) {
            $this->exportSlice($jobs, 0, 1, $converter, $output);
        } else {
            // Start fresh PHP processes: inherited macOS network state is not
            // safe to use after fork. The plan contains no bot credentials.
            $plan = tempnam(runtime_path(), "icon-export-");
            if ($plan === false) {
                throw new \RuntimeException("无法创建图标导出任务。");
            }
            chmod($plan, 0600);
            $children = [];
            $failed = false;
            try {
                file_put_contents(
                    $plan,
                    json_encode(
                        ["jobs" => $jobs, "converter" => $converter],
                        JSON_THROW_ON_ERROR,
                    ),
                );
                for ($worker = 0; $worker < $workers; $worker++) {
                    $process = proc_open(
                        [
                            PHP_BINARY,
                            root_path() . "scripts/export-icons.php",
                            $plan,
                            (string) $worker,
                            (string) $workers,
                        ],
                        [0 => ["pipe", "r"], 1 => STDOUT, 2 => STDERR],
                        $pipes,
                        root_path(),
                    );
                    if (!is_resource($process)) {
                        $failed = true;
                        break;
                    }
                    fclose($pipes[0]);
                    $children[] = $process;
                }
                foreach ($children as $process) {
                    if (proc_close($process) !== 0) {
                        $failed = true;
                    }
                }
            } finally {
                unlink($plan);
            }
            if ($failed) {
                throw new \RuntimeException(
                    "未更新图标清单；已下载资源可供再次运行时续用。",
                );
            }
        }
        if ($input->getOption("all")) {
            $manifest = [
                "name" => $set["name"],
                "title" => $set["title"],
                "synced_at" => gmdate(DATE_ATOM),
                "icons" => array_map(
                    fn($sticker) => [
                        "id" => (string) $sticker["custom_emoji_id"],
                        "emoji" => $sticker["emoji"] ?? "",
                        "source_sha256" => hash_file(
                            "sha256",
                            $sources . $sticker["custom_emoji_id"] . ".tgs",
                        ),
                    ],
                    $set["stickers"],
                ),
            ];
            $this->write(
                $sources . "manifest.json",
                json_encode(
                    $manifest,
                    JSON_PRETTY_PRINT |
                        JSON_UNESCAPED_UNICODE |
                        JSON_THROW_ON_ERROR,
                ) . "\n",
            );
            $this->write(
                root_path() . "config/tgios_icons.php",
                "<?php\n// Telegram iOS Icons: https://t.me/addemoji/tgiosicons\n// Generated by php think bot:icons --all; existing keys are preserved.\n// IDs must remain strings (beyond JavaScript safe integers).\nreturn " .
                    var_export($catalog, true) .
                    ";\n",
            );
        }
        $output->writeln(
            "完成 " .
                count($jobs) .
                " 个原始图标、" .
                count($catalog) .
                " 个兼容名称。已保留 TGS；网页使用静态 SVG，Telegram 使用原始 custom emoji ID。",
        );
        return 0;
    }
    public function exportSlice(
        array $jobs,
        int $worker,
        int $workers,
        string $converter,
        Output $output,
    ): void {
        $client = Client::configured();
        $done = 0;
        foreach ($jobs as $index => $job) {
            if ($index % $workers !== $worker) {
                continue;
            }
            $this->export(
                $client,
                $job,
                root_path() . "resources/icons/telegram-ios/",
                root_path() . "public/icons/telegram-ios/",
                $converter,
            );
            if (++$done % 20 === 0) {
                $output->writeln(
                    "导出进程 " . ($worker + 1) . " 已完成 " . $done . " 个。",
                );
            }
        }
    }
    private function export(
        Client $client,
        array $job,
        string $sources,
        string $dir,
        string $converter,
    ): void {
        $sticker = $job["sticker"];
        $id = (string) $sticker["custom_emoji_id"];
        $source = $sources . $id . ".tgs";
        if (!is_file($source)) {
            $file = $this->retry(
                fn() => $client->call("getFile", [
                    "file_id" => $sticker["file_id"],
                ]),
            );
            $bytes = $this->retry(
                fn() => $client->download($file["file_path"]),
            );
        } else {
            $bytes = file_get_contents($source);
        }
        if (
            substr($bytes, 0, 2) !== "\x1f\x8b" ||
            !json_decode(gzdecode($bytes), true)
        ) {
            throw new \RuntimeException("Telegram 未返回有效 TGS：" . $id);
        }
        if (!is_file($source)) {
            $this->write($source, $bytes);
        }
        $target = $dir . $job["names"][0] . ".svg";
        if (is_file($target)) {
            $svg = file_get_contents($target);
            $this->checkSvg($svg, $id);
            foreach (array_slice($job["names"], 1) as $name) {
                $this->write($dir . $name . ".svg", $svg);
            }
            return;
        }
        $temp = $target . ".tmp.svg";
        $process = proc_open(
            [$converter, $source, $temp, "--frame", "0"],
            [],
            $pipes,
        );
        if (
            !is_resource($process) ||
            proc_close($process) !== 0 ||
            !is_file($temp)
        ) {
            throw new \RuntimeException("图标矢量转换失败：" . $id);
        }
        $svg = file_get_contents($temp);
        $this->checkSvg($svg, $id);
        if (!rename($temp, $target)) {
            throw new \RuntimeException("无法发布 SVG：" . $id);
        }
        foreach (array_slice($job["names"], 1) as $name) {
            $this->write($dir . $name . ".svg", $svg);
        }
    }
    private function checkSvg(string $svg, string $id): void
    {
        if (
            !str_contains($svg, "<svg") ||
            !str_contains($svg, "<path") ||
            preg_match(
                "/<(script|image|foreignObject)\b|\bon[a-z]+\s*=|(?:href|src)\s*=/i",
                $svg,
            )
        ) {
            throw new \RuntimeException("图标 SVG 校验失败：" . $id);
        }
    }
    private function write(string $path, string $bytes): void
    {
        $temp = $path . ".tmp";
        if (
            file_put_contents($temp, $bytes) !== strlen($bytes) ||
            !rename($temp, $path)
        ) {
            throw new \RuntimeException("无法写入图标资源。");
        }
    }
    private function retry(callable $action): mixed
    {
        for ($attempt = 0; ; $attempt++) {
            try {
                return $action();
            } catch (\Throwable $e) {
                if ($attempt >= 2) {
                    throw $e;
                }
                sleep(1 + $attempt);
            }
        }
    }
}
