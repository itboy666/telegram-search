<?php
// +----------------------------------------------------------------------
// | 控制台配置
// +----------------------------------------------------------------------
return [
    // 指令定义
    "commands" => [
        "app:install" => \app\command\Install::class,
        "app:upgrade-commerce" => \app\command\UpgradeCommerce::class,
        "commerce:work" => \app\command\CommerceWork::class,
        "app:upgrade-access" => \app\command\UpgradeAccess::class,
        "app:upgrade-bot-search" => \app\command\UpgradeBotSearch::class,
        "app:upgrade-bot-security" => \app\command\UpgradeBotSecurity::class,
        "app:upgrade-catalog" => \app\command\UpgradeCatalog::class,
        "bot:check" => \app\command\BotCheck::class,
        "bot:poll" => \app\command\BotPoll::class,
        "bot:icons" => \app\command\SyncIcons::class,
        "app:maintain" => \app\command\Maintain::class,
        "resources:import" => \app\command\ImportResources::class,
        "resources:collect-dataset" => \app\command\CollectDataset::class,
        "resources:collect-catalog" => \app\command\CollectCatalog::class,
        "resources:collect-public" => \app\command\CollectPublicPosts::class,
    ],
];
