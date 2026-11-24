<?php
namespace app\command;
use think\console\{Command, Input, Output};
use think\console\input\Option;
use app\common\telegram\{Client, Bot, TelegramError, UpdateProcessor};
use app\common\service\{BotState, BotCredentials};
use app\common\exception\DomainError;

class BotPoll extends Command
{
    protected function configure(): void
    {
        $this->setName("bot:poll")
            ->setDescription("本地 Telegram 长轮询（单进程锁）")
            ->addOption("once", null, Option::VALUE_NONE, "只处理一次轮询");
    }
    protected function execute(Input $input, Output $output): int
    {
        $dir = root_path() . "runtime/";
        if (!is_dir($dir)) {
            mkdir($dir, 0755, true);
        }
        $lock = fopen($dir . "bot.lock", "c+");
        if (!flock($lock, LOCK_EX | LOCK_NB)) {
            $output->writeln("已有 Bot 进程运行，请勿重复启动。");
            return 1;
        }
        $stop = false;
        if (function_exists("pcntl_async_signals")) {
            pcntl_async_signals(true);
            pcntl_signal(SIGTERM, function () use (&$stop) {
                $stop = true;
            });
            pcntl_signal(SIGINT, function () use (&$stop) {
                $stop = true;
            });
        }
        $client = null;
        $processor = null;
        $version = "";
        $blockedVersion = null;
        $offset = 0;
        $failures = 0;
        $once = (bool) $input->getOption("once");
        try {
            do {
                try {
                    $credentials = BotCredentials::snapshot();
                    if ($version !== $credentials["version"]) {
                        $version = $credentials["version"];
                        $client = null;
                        $processor = null;
                        $blockedVersion = null;
                        $failures = 0;
                    }
                    if ($blockedVersion === $version) {
                        if ($once) {
                            return 1;
                        }
                        sleep(1);
                        continue;
                    }
                    if (
                        !preg_match(
                            '/^\d+:[A-Za-z0-9_-]+$/D',
                            $credentials["token"],
                        )
                    ) {
                        BotState::write(
                            [
                                "state" => "waiting",
                                "heartbeat" => time(),
                                "last_error" =>
                                    "请在后台「机器人连接」保存 Bot Token。",
                            ],
                            $version,
                        );
                        $blockedVersion = $version;
                        if ($once) {
                            return 1;
                        }
                        continue;
                    }
                    if ($client === null) {
                        BotState::write(
                            [
                                "state" => "starting",
                                "heartbeat" => time(),
                                "last_error" => null,
                            ],
                            $version,
                        );
                        $next = new Client($credentials["token"], $version);
                        $me = $next->call("getMe");
                        $botId = (int) $me["id"];
                        if (!BotCredentials::current($version)) {
                            continue;
                        }
                        $hook = $next->call("getWebhookInfo");
                        if (!empty($hook["url"])) {
                            $next->call("deleteWebhook", [
                                "drop_pending_updates" => false,
                            ]);
                        }
                        $next->call("setMyCommands", [
                            "commands" => [
                                [
                                    "command" => "start",
                                    "description" => "开始使用星索",
                                ],
                                [
                                    "command" => "discover",
                                    "description" => "浏览全部资源",
                                ],
                                [
                                    "command" => "search",
                                    "description" => "搜索资源：/search 关键词",
                                ],
                                [
                                    "command" => "add",
                                    "description" =>
                                        "提交链接，自动获取标题（私聊）",
                                ],
                                [
                                    "command" => "account",
                                    "description" =>
                                        "用户中心与一次性网页登录码（私聊）",
                                ],
                                [
                                    "command" => "wallet",
                                    "description" => "余额、充值与账单（私聊）",
                                ],
                                [
                                    "command" => "help",
                                    "description" => "搜索与提交帮助",
                                ],
                                [
                                    "command" => "groupsettings",
                                    "description" => "群管理员安全设置",
                                ],
                            ],
                        ]);
                        $next->call("setMyCommands", [
                            "scope" => ["type" => "all_chat_administrators"],
                            "commands" => [
                                [
                                    "command" => "search",
                                    "description" => "搜索资源",
                                ],
                                [
                                    "command" => "discover",
                                    "description" => "浏览全部资源",
                                ],
                                [
                                    "command" => "groupsettings",
                                    "description" => "查看群安全设置",
                                ],
                                [
                                    "command" => "captcha",
                                    "description" => "设置搜索验证方式",
                                ],
                                [
                                    "command" => "ads",
                                    "description" => "设置广告拦截",
                                ],
                                [
                                    "command" => "adwords",
                                    "description" =>
                                        "设置广告关键词，用 | 分隔",
                                ],
                                [
                                    "command" => "allowdomain",
                                    "description" => "设置允许的链接域名",
                                ],
                                [
                                    "command" => "flood",
                                    "description" => "设置群防刷屏",
                                ],
                                [
                                    "command" => "help",
                                    "description" => "使用帮助",
                                ],
                            ],
                        ]);
                        if (!BotCredentials::current($version)) {
                            continue;
                        }
                        $state = BotState::read();
                        $offset =
                            (int) ($state["id"] ?? 0) === $botId
                                ? (int) ($state["offset"] ?? 0)
                                : 0;
                        $processor = new UpdateProcessor(
                            new Bot(
                                $next,
                                (string) ($me["username"] ?? ""),
                                $botId,
                            ),
                        );
                        $client = $next;
                        $output->writeln(
                            "@" .
                                $me["username"] .
                                " 配置已加载，长轮询运行中。",
                        );
                    }
                    BotState::write(
                        [
                            "id" => $botId,
                            "username" => $me["username"],
                            "name" => $me["first_name"],
                            "state" => "running",
                            "heartbeat" => time(),
                            "pid" => getmypid(),
                            "offset" => $offset,
                            "last_error" => null,
                            "webhook_enabled" => false,
                            "can_join_groups" =>
                                (bool) ($me["can_join_groups"] ?? false),
                            "can_read_all_group_messages" =>
                                (bool) ($me["can_read_all_group_messages"] ??
                                    false),
                        ],
                        $version,
                    );
                    $updates = $client->call("getUpdates", [
                        "offset" => $offset,
                        "timeout" => $once
                            ? 0
                            : max(
                                1,
                                min(
                                    20,
                                    (int) config("telegram.poll_timeout", 20),
                                ),
                            ),
                        "limit" => 20,
                        "allowed_updates" => [
                            "message",
                            "edited_message",
                            "callback_query",
                            "my_chat_member",
                        ],
                    ]);
                    foreach ($updates as $update) {
                        if ($stop || !BotCredentials::current($version)) {
                            break;
                        }
                        $offset = $processor->process($botId, $update);
                        BotState::write(
                            [
                                "offset" => $offset,
                                "heartbeat" => time(),
                                "last_update_at" => date("Y-m-d H:i:s"),
                            ],
                            $version,
                        );
                    }
                    $failures = 0;
                } catch (TelegramError $e) {
                    if (!BotCredentials::current($version)) {
                        continue;
                    }
                    $fatal = in_array($e->apiCode, [401, 404, 409], true);
                    $message = match ($e->apiCode) {
                        401, 404 => "Token 已失效，请在后台保存新的 Token。",
                        409
                            => "机器人连接冲突，请停止其他长轮询进程后重新保存 Token。",
                        default => $e->getMessage(),
                    };
                    BotState::write(
                        [
                            "state" => $fatal ? "error" : "retrying",
                            "last_error" => $message,
                            "heartbeat" => time(),
                        ],
                        $version,
                    );
                    $output->writeln($message);
                    if ($once) {
                        return 1;
                    }
                    if ($fatal) {
                        // Keep the local web supervisor alive so an admin can repair the token.
                        $blockedVersion = $version;
                        continue;
                    }
                    $wait = max(
                        $e->retryAfter,
                        min(30, 2 ** min(5, ++$failures)),
                    );
                    for ($i = 0; $i < $wait && !$stop; $i++) {
                        sleep(1);
                        if (!BotCredentials::current($version)) {
                            break;
                        }
                    }
                } catch (DomainError $e) {
                    $output->writeln($e->getMessage());
                    if ($once) {
                        return 1;
                    }
                    for ($i = 0; $i < 5 && !$stop; $i++) {
                        sleep(1);
                    }
                }
            } while (!$stop && !$once);
            BotState::write(
                ["state" => "stopped", "heartbeat" => time()],
                $version,
            );
            return 0;
        } catch (\Throwable $e) {
            try {
                BotState::write(
                    [
                        "state" => "error",
                        "last_error" =>
                            "长轮询中断，请运行 bot:check 并检查本地数据库。",
                        "heartbeat" => time(),
                    ],
                    $version,
                );
            } catch (\Throwable) {
                // An unavailable database/key must not expose credentials in an error trace.
            }
            $output->writeln(
                "长轮询已停止：" .
                    ($e instanceof TelegramError
                        ? $e->getMessage()
                        : get_class($e)),
            );
            return 1;
        } finally {
            flock($lock, LOCK_UN);
            fclose($lock);
        }
    }
}
