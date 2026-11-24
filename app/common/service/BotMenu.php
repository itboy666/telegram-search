<?php
namespace app\common\service;
use app\common\exception\DomainError;
use app\common\telegram\Icons;
use think\facade\Db;

class BotMenu
{
    public const ACTIONS = [
        "hot" => "查看热搜",
        "discover" => "发现资源",
        "rank" => "群频道榜",
        "mine" => "我的资料",
        "commerce" => "营销中心与钱包",
        "help" => "使用帮助",
        "submit" => "提交收录说明",
        "search" => "搜索指定关键词",
        "message" => "回复自定义文字",
        "url" => "打开 HTTPS 链接",
    ];
    public static function defaults(): array
    {
        $rows = [];
        foreach (
            [["hot", "discover"], ["rank", "mine"], ["help", "submit"]]
            as $actions
        ) {
            $row = [];
            foreach ($actions as $action) {
                $row[] = [
                    "id" => $action,
                    "text" => [
                        "hot" => "热搜",
                        "discover" => "发现",
                        "rank" => "群频道榜",
                        "mine" => "我的",
                        "help" => "帮助",
                        "submit" => "提交收录",
                    ][$action],
                    "icon" => $action === "mine" ? "user" : $action,
                    "action" => $action,
                    "value" => "",
                    "style" => "default",
                    "enabled" => true,
                ];
            }
            $rows[] = $row;
        }
        return [
            "welcome" => Settings::get("bot_welcome"),
            "welcome_icon" => "search",
            "hint" => "在输入框发送你想找的关键词，或点击下方按钮。",
            "rows" => $rows,
        ];
    }
    public static function get(): array
    {
        $stored = json_decode(Settings::get("bot_menu_layout", ""), true);
        $menu = is_array($stored) ? $stored : self::defaults();
        $menu["welcome"] = Settings::get("bot_welcome");
        return $menu;
    }
    public static function version(?array $menu = null): string
    {
        return substr(hash("sha256", json_encode($menu ?? self::get())), 0, 10);
    }
    public static function payload(): array
    {
        return [
            "config" => self::get(),
            "icons" => Icons::catalog(),
            "actions" => self::ACTIONS,
            "pack" => "tgiosicons",
            "delivery" => BotState::read()["custom_emoji"] ?? [],
            "template" => WelcomeTemplate::metadata(),
        ];
    }
    public static function validate(array $input): array
    {
        $text = function (
            mixed $value,
            int $min,
            int $max,
            string $label,
        ): string {
            if (!is_string($value)) {
                throw new DomainError($label . "必须是文字。");
            }
            $value = trim($value);
            if (
                mb_strlen($value) < $min ||
                mb_strlen($value) > $max ||
                preg_match('/[\x00-\x08\x0B\x0C\x0E-\x1F]/u', $value)
            ) {
                throw new DomainError(
                    $label . "须为 " . $min . "–" . $max . " 字。",
                );
            }
            return $value;
        };
        $icon = function (mixed $value): string {
            if (
                !is_string($value) ||
                ($value !== "" && !isset(Icons::catalog()[$value]))
            ) {
                throw new DomainError("请选择 Telegram iOS Icons 中的图标。");
            }
            return $value;
        };
        $menu = [
            "welcome" => $text($input["welcome"] ?? null, 1, 1800, "欢迎语"),
            "welcome_icon" => $icon($input["welcome_icon"] ?? ""),
            "hint" => $text($input["hint"] ?? "", 0, 240, "提示语"),
            "rows" => [],
        ];
        WelcomeTemplate::validate($menu["welcome"], "welcome", "欢迎语");
        WelcomeTemplate::validate($menu["hint"], "hint", "提示语");
        if (
            !isset($input["rows"]) ||
            !is_array($input["rows"]) ||
            !array_is_list($input["rows"]) ||
            count($input["rows"]) > 10
        ) {
            throw new DomainError("菜单最多 10 行。");
        }
        $seen = [];
        foreach ($input["rows"] as $row) {
            if (
                !is_array($row) ||
                !array_is_list($row) ||
                count($row) < 1 ||
                count($row) > 5
            ) {
                throw new DomainError("每行须有 1–5 个按钮。");
            }
            $buttons = [];
            foreach ($row as $button) {
                if (!is_array($button)) {
                    throw new DomainError("按钮格式无效。");
                }
                $id = $text($button["id"] ?? "", 1, 20, "按钮标识");
                if (
                    !preg_match('/^[a-z0-9_]+$/', $id) ||
                    isset($seen[$id]) ||
                    $id === "home"
                ) {
                    throw new DomainError("按钮标识无效或重复。");
                }
                $seen[$id] = true;
                if (count($seen) > 24) {
                    throw new DomainError("菜单最多 24 个按钮。");
                }
                $action = $text($button["action"] ?? "", 1, 20, "按钮动作");
                if (!isset(self::ACTIONS[$action])) {
                    throw new DomainError("请选择有效的按钮动作。");
                }
                $value = $text(
                    $button["value"] ?? "",
                    in_array($action, ["url", "search", "message"], true)
                        ? 1
                        : 0,
                    $action === "search"
                        ? 120
                        : ($action === "url"
                            ? 500
                            : 1800),
                    "动作内容",
                );
                if ($action === "url") {
                    $parts = parse_url($value);
                    if (
                        !filter_var($value, FILTER_VALIDATE_URL) ||
                        ($parts["scheme"] ?? "") !== "https" ||
                        isset($parts["user"]) ||
                        isset($parts["pass"])
                    ) {
                        throw new DomainError(
                            "按钮链接须为不含账号密码的完整 HTTPS 地址。",
                        );
                    }
                }
                $style = $button["style"] ?? "default";
                if (
                    !in_array(
                        $style,
                        ["default", "primary", "success", "danger"],
                        true,
                    )
                ) {
                    throw new DomainError("按钮颜色无效。");
                }
                if (!is_bool($button["enabled"] ?? null)) {
                    throw new DomainError("按钮启用状态无效。");
                }
                $buttons[] = [
                    "id" => $id,
                    "text" => $text($button["text"] ?? "", 1, 24, "按钮文字"),
                    "icon" => $icon($button["icon"] ?? ""),
                    "action" => $action,
                    "value" => in_array(
                        $action,
                        ["url", "search", "message"],
                        true,
                    )
                        ? $value
                        : "",
                    "style" => $style,
                    "enabled" => $button["enabled"],
                ];
            }
            $menu["rows"][] = $buttons;
        }
        return $menu;
    }
    public static function save(array $input, int $actor): array
    {
        $menu = self::validate($input);
        $layout = $menu;
        unset($layout["welcome"]);
        Db::transaction(function () use ($menu, $layout, $actor) {
            foreach (
                [
                    "bot_welcome" => $menu["welcome"],
                    "bot_menu_layout" => json_encode(
                        $layout,
                        JSON_UNESCAPED_UNICODE,
                    ),
                ]
                as $key => $value
            ) {
                Db::execute(
                    "INSERT INTO ts_settings (name,value) VALUES (?,?) ON DUPLICATE KEY UPDATE value=?",
                    [$key, $value, $value],
                );
            }
            Settings::audit(
                "更新机器人菜单",
                "/start 欢迎语、图标与按钮布局已保存",
                $actor,
            );
        });
        return self::payload();
    }
}
