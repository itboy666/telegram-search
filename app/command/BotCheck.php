<?php
namespace app\command;
use think\console\{Command, Input, Output};
use app\common\telegram\Client;
use app\common\service\BotState;
use app\common\service\BotCredentials;
class BotCheck extends Command
{
    protected function configure(): void
    {
        $this->setName("bot:check")->setDescription(
            "验证 Bot Token 和 Telegram 连接",
        );
    }
    protected function execute(Input $input, Output $output): int
    {
        try {
            $credentials = BotCredentials::snapshot();
            $client = new Client(
                $credentials["token"],
                $credentials["version"],
            );
            $me = $client->call("getMe");
            $webhook = $client->call("getWebhookInfo");
            $saved = BotState::write(
                [
                    "id" => $me["id"],
                    "username" => $me["username"],
                    "name" => $me["first_name"],
                    "checked_at" => date("Y-m-d H:i:s"),
                    "webhook_enabled" => !empty($webhook["url"]),
                    "can_join_groups" =>
                        (bool) ($me["can_join_groups"] ?? false),
                    "can_read_all_group_messages" =>
                        (bool) ($me["can_read_all_group_messages"] ?? false),
                ],
                $credentials["version"],
            );
            if (!$saved) {
                $output->writeln("验证期间 Token 已更新，请重新验证。");
                return 1;
            }
            $output->writeln(
                "连接成功：@" . $me["username"] . " · Bot ID " . $me["id"],
            );
            $output->writeln(
                "加入群组：" .
                    (!empty($me["can_join_groups"])
                        ? "已允许"
                        : "未允许，请在 BotFather 开启 Allow Groups"),
            );
            $output->writeln(
                "群聊普通关键词：" .
                    (!empty($me["can_read_all_group_messages"])
                        ? "隐私模式已关闭，可接收群内普通消息"
                        : "隐私模式已开启；使用 /search@" .
                            $me["username"] .
                            " 关键词，或回复机器人的消息"),
            );
            return 0;
        } catch (\Throwable $e) {
            $output->writeln("连接失败：" . $e->getMessage());
            return 1;
        }
    }
}
