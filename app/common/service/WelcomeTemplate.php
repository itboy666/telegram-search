<?php
namespace app\common\service;

use app\common\exception\DomainError;

/** Plain-text placeholders only; never evaluate PHP, HTML or a template engine. */
class WelcomeTemplate
{
    public const LIMITS = ["welcome" => 1800, "hint" => 240];
    private const PATTERN = "/(?<!\{)\{\{([^{}]*)\}\}(?!\})/u";
    private const TAGS = [
        "nickname" => [
            "昵称",
            "用户信息",
            "名字与姓氏组合；缺失时显示 Telegram 用户",
        ],
        "username" => ["用户名", "用户信息", "包含 @；未设置时显示「未设置」"],
        "user_id" => ["用户 ID", "用户信息", "当前 Telegram 用户的数字 ID"],
        "first_name" => [
            "名字",
            "用户信息",
            "Telegram 名字；缺失时显示 Telegram 用户",
        ],
        "last_name" => [
            "姓氏",
            "用户信息",
            "Telegram 姓氏；未设置时显示「未设置」",
        ],
        "language_code" => [
            "语言",
            "用户信息",
            "Telegram 提供的语言代码；缺失时显示「未知」",
        ],
        "is_premium" => [
            "Premium",
            "用户信息",
            "用户是否为 Telegram Premium：是 / 否",
        ],
        "datetime" => [
            "当前时间",
            "时间与站点",
            "发送时的日期与时间，精确到秒",
        ],
        "date" => ["日期", "时间与站点", "格式：年-月-日"],
        "time" => ["时间", "时间与站点", "格式：时:分:秒，24 小时制"],
        "weekday" => ["星期", "时间与站点", "星期一至星期日"],
        "timezone" => ["时区", "时间与站点", "使用项目配置的时区"],
        "site_name" => ["站点名称", "时间与站点", "系统设置中的站点名称"],
    ];

    public static function metadata(): array
    {
        $now = new \DateTimeImmutable("now", self::timezone());
        $tags = [];
        foreach (self::TAGS as $key => [$label, $group, $description]) {
            $tags[] = [
                "key" => $key,
                "token" => "{{" . $key . "}}",
                "label" => $label,
                "group" => $group,
                "description" => $description,
            ];
        }
        return [
            "tags" => $tags,
            "limits" => self::LIMITS,
            "timezone" => $now->getTimezone()->getName(),
            "server_time" => $now->format(DATE_ATOM),
            "sample_values" => self::values(
                [
                    "id" => 123456789,
                    "first_name" => "小林",
                    "username" => "lin_demo",
                    "language_code" => "zh-hans",
                    "is_premium" => true,
                ],
                $now,
            ),
        ];
    }

    public static function values(
        array $from,
        ?\DateTimeImmutable $now = null,
        ?string $siteName = null,
    ): array {
        $now = ($now ?? new \DateTimeImmutable())->setTimezone(
            self::timezone(),
        );
        $first = self::userText($from["first_name"] ?? "");
        $last = self::userText($from["last_name"] ?? "");
        $nickname = trim($first . " " . $last);
        $username = ltrim(self::userText($from["username"] ?? ""), "@");
        return [
            "nickname" => $nickname !== "" ? $nickname : "Telegram 用户",
            "username" => $username !== "" ? "@" . $username : "未设置",
            "user_id" => (string) ($from["id"] ?? ""),
            "first_name" => $first !== "" ? $first : "Telegram 用户",
            "last_name" => $last !== "" ? $last : "未设置",
            "language_code" =>
                self::userText($from["language_code"] ?? "") ?: "未知",
            "is_premium" => !empty($from["is_premium"]) ? "是" : "否",
            "datetime" => $now->format("Y-m-d H:i:s"),
            "date" => $now->format("Y-m-d"),
            "time" => $now->format("H:i:s"),
            "weekday" => [
                "星期日",
                "星期一",
                "星期二",
                "星期三",
                "星期四",
                "星期五",
                "星期六",
            ][(int) $now->format("w")],
            "timezone" => $now->getTimezone()->getName(),
            "site_name" => $siteName ?? Settings::get("site_name", "星索"),
        ];
    }

    public static function validate(
        string $text,
        string $field,
        string $label,
    ): void {
        preg_match_all(self::PATTERN, $text, $matches);
        foreach ($matches[1] as $key) {
            if (!isset(self::TAGS[trim($key)])) {
                $message =
                    $label .
                    "包含未知标签 {{" .
                    trim($key) .
                    "}}，请使用下方提供的标签。";
                throw new DomainError($message, 422, [$field => $message]);
            }
        }
        $remaining = preg_replace(self::PATTERN, "", $text);
        if (str_contains($remaining, "{{") || str_contains($remaining, "}}")) {
            $message = $label . "的标签格式不完整，请使用 {{标签名}}。";
            throw new DomainError($message, 422, [$field => $message]);
        }
    }

    public static function render(
        string $text,
        array $values,
        int $limit,
    ): string {
        // One pass: user names containing "{{date}}" remain literal text.
        $rendered = preg_replace_callback(
            self::PATTERN,
            fn($match) => $values[trim($match[1])] ?? $match[0],
            $text,
        );
        // The two fields together fit Telegram's message budget, even with
        // supplementary Unicode characters. Escape HTML only after rendering.
        return mb_strlen($rendered) <= $limit
            ? $rendered
            : mb_substr($rendered, 0, $limit - 1) . "…";
    }

    private static function timezone(): \DateTimeZone
    {
        return new \DateTimeZone(
            (string) config("app.default_timezone", "Asia/Shanghai"),
        );
    }

    private static function userText(mixed $text): string
    {
        return trim(preg_replace('/[\x00-\x1F\x7F]/u', " ", (string) $text));
    }
}
