<?php
return (static function (): array {
    $records = [
        "wallets" => [
            "钱包余额",
            [
                "user_id" => "用户 ID",
                "balance" => "余额",
                "updated_at" => "更新时间",
            ],
        ],
        "wallet_entries" => [
            "资金流水",
            [
                "user_id" => "用户 ID",
                "amount" => "变动金额",
                "balance_after" => "变动后余额",
                "kind" => "类型",
                "business_key" => "业务编号",
                "note" => "说明",
                "created_at" => "时间",
            ],
        ],
        "recharges" => [
            "充值订单",
            [
                "user_id" => "用户 ID",
                "method" => "充值方式",
                "amount" => "金额",
                "receive_address" => "收款地址",
                "txid" => "链上交易哈希",
                "status" => "状态",
                "proof" => "转账凭证",
                "review_note" => "审核说明",
                "reviewer_id" => "审核管理员",
                "created_at" => "创建时间",
                "expires_at" => "付款截止时间",
                "paid_at" => "入账时间",
                "decision" => "审核决定",
            ],
        ],
        "ad_slots" => [
            "广告位",
            [
                "name" => "名称",
                "kind" => "类型",
                "description" => "说明",
                "price" => "价格",
                "duration_days" => "天数",
                "capacity" => "容量",
                "target_chat" => "目标聊天",
                "enabled" => "开放购买",
                "created_at" => "创建时间",
                "updated_at" => "更新时间",
            ],
        ],
        "ad_orders" => [
            "广告订单",
            [
                "user_id" => "用户 ID",
                "slot_id" => "广告位 ID",
                "slot_name" => "广告位名称",
                "kind" => "类型",
                "price" => "实付金额",
                "duration_days" => "天数",
                "target_chat" => "目标聊天",
                "title" => "标题",
                "url" => "链接",
                "content" => "内容",
                "keyword" => "关键词",
                "status" => "状态",
                "review_note" => "审核说明",
                "reviewer_id" => "审核管理员",
                "created_at" => "创建时间",
                "starts_at" => "开始时间",
                "ends_at" => "结束时间",
                "decision" => "审核决定",
                "message_id" => "投放消息 ID",
                "delivery_state" => "投放状态",
                "delivery_error" => "投放错误",
            ],
        ],
    ];
    $result = [];
    foreach ($records as $name => [$label, $fields]) {
        $result[$name] = [
            "label" => $label,
            "actions" => ["read" => "查看"],
            "fields" => $fields,
            "writable" => [],
        ];
    }
    $result["recharges"]["actions"] += [
        "review" => "线下充值审核",
        "check" => "链上核查",
    ];
    $result["recharges"]["writable"] = ["decision", "review_note", "txid"];
    $result["ad_slots"]["actions"] += [
        "create" => "创建",
        "update" => "编辑",
        "delete" => "删除无订单广告位",
    ];
    $result["ad_slots"]["writable"] = [
        "name",
        "kind",
        "description",
        "price",
        "duration_days",
        "capacity",
        "target_chat",
        "enabled",
    ];
    $result["ad_orders"]["actions"] += [
        "review" => "审核与退款",
        "resolve" => "确认异常投放",
    ];
    $result["ad_orders"]["writable"] = [
        "decision",
        "review_note",
        "message_id",
    ];
    $result["commerce_settings"] = [
        "label" => "支付与奖励设置",
        "actions" => ["read" => "查看", "update" => "修改"],
        "fields" => [
            "trc20_enabled" => "启用 USDT-TRC20",
            "receive_address" => "TRC20 收款地址",
            "api_key" => "Tronscan 密钥（只写不回显）",
            "key_configured" => "密钥配置状态",
            "offline_enabled" => "启用线下转账",
            "offline_instructions" => "线下收款说明",
            "reward_enabled" => "启用搜索奖励",
            "reward_amount" => "每日首搜奖励",
            "reward_daily_cap" => "全站每日奖励上限",
            "reward_user_cap" => "单用户累计奖励上限",
            "public_url" => "网站根地址",
        ],
        "writable" => [
            "trc20_enabled",
            "receive_address",
            "api_key",
            "offline_enabled",
            "offline_instructions",
            "reward_enabled",
            "reward_amount",
            "reward_daily_cap",
            "reward_user_cap",
            "public_url",
        ],
    ];
    return $result;
})();
