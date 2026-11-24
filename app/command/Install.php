<?php
namespace app\command;
use think\console\Command;
use think\console\Input;
use think\console\Output;
use think\facade\Db;
class Install extends Command
{
    protected function configure(): void
    {
        $this->setName("app:install")->setDescription(
            "创建星索数据库表和本地管理员（可重复运行）",
        );
    }
    protected function execute(Input $input, Output $output): int
    {
        $sql = file_get_contents(
            root_path() . "database/migrations/001_initial.sql",
        );
        foreach (explode(";", $sql) as $statement) {
            if (trim($statement) !== "") {
                Db::execute($statement);
            }
        }
        \app\common\service\AccessMigration::run();
        \app\common\service\BotSearchMigration::run();
        \app\common\service\CatalogMigration::run();
        \app\common\service\BotSecuritySettings::migrate();
        \app\common\service\CommerceMigration::run();
        $now = date("Y-m-d H:i:s");
        if (!Db::name("admins")->count()) {
            $password = "Star@" . bin2hex(random_bytes(6));
            Db::name("admins")->insert([
                "username" => "admin",
                "display_name" => "管理员",
                "password_hash" => password_hash($password, PASSWORD_DEFAULT),
                "is_super" => 1,
                "created_at" => $now,
            ]);
            file_put_contents(
                root_path() . "本地登录信息.txt",
                "后台地址：http://127.0.0.1:8088/admin\n账号：admin\n初始密码：" .
                    $password .
                    "\n请勿将此文件提交到版本库。\n",
            );
            chmod(root_path() . "本地登录信息.txt", 0600);
            $output->writeln("管理员已创建，初始密码保存在 本地登录信息.txt。");
        }
        if (!Db::name("categories")->count()) {
            foreach (
                [
                    ["官方资讯", "Telegram 官方公告与产品动态"],
                    ["开发工具", "开发者工具、技术社区与机器人"],
                    ["学习知识", "学习资料与知识分享"],
                    ["生活兴趣", "发现生活中的同好与兴趣"],
                ]
                as $i => $c
            ) {
                Db::name("categories")->insert([
                    "name" => $c[0],
                    "description" => $c[1],
                    "sort_order" => $i * 10,
                    "created_at" => $now,
                ]);
            }
        }
        if (!Db::name("resources")->count()) {
            $category = Db::name("categories")
                ->where("name", "官方资讯")
                ->value("id");
            foreach (
                [
                    [
                        "Telegram 官方频道",
                        "telegram",
                        "Telegram 官方产品更新与公告。",
                    ],
                    [
                        "Telegram Tips",
                        "TelegramTips",
                        "Telegram 功能介绍和使用技巧。",
                    ],
                    [
                        "Bot News",
                        "BotNews",
                        "Telegram Bot API 的官方更新与开发公告。",
                    ],
                ]
                as $r
            ) {
                \app\common\service\ResourceService::save([
                    "title" => $r[0],
                    "url" => "https://t.me/" . $r[1],
                    "description" => $r[2],
                    "type" => "channel",
                    "category_id" => $category,
                    "featured" => 1,
                    "source" => "initial",
                ]);
            }
        }
        $output->writeln(
            "数据库初始化完成：MySQL " .
                Db::query("SELECT VERSION() AS version")[0]["version"],
        );
        return 0;
    }
}
