<?php
// Pure rendering tests; no database writes or Telegram requests.
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();

use app\common\service\WelcomeTemplate;
use app\common\telegram\Icons;
use app\common\exception\DomainError;

$checks = 0;
function checkTemplate(bool $ok, string $label): void
{
    if (!$ok) {
        throw new RuntimeException("FAIL " . $label);
    }
    global $checks;
    $checks++;
    echo "PASS " . $label . PHP_EOL;
}
$fixtures = json_decode(
    file_get_contents(__DIR__ . "/fixtures/welcome-template.json"),
    true,
    512,
    JSON_THROW_ON_ERROR,
);
foreach ($fixtures as $fixture) {
    checkTemplate(
        WelcomeTemplate::render(
            $fixture["text"],
            $fixture["values"],
            $fixture["limit"],
        ) === $fixture["expected"],
        $fixture["name"],
    );
}
$time = new DateTimeImmutable("2026-11-15T16:05:06+00:00");
$values = WelcomeTemplate::values(
    [
        "id" => 8974065497,
        "first_name" => "小林",
        "last_name" => "同学",
        "username" => "lin_demo",
        "language_code" => "zh-hans",
        "is_premium" => true,
    ],
    $time,
    "测试站点",
);
checkTemplate(
    $values["nickname"] === "小林 同学" &&
        $values["first_name"] === "小林" &&
        $values["last_name"] === "同学" &&
        $values["username"] === "@lin_demo" &&
        $values["user_id"] === "8974065497" &&
        $values["language_code"] === "zh-hans" &&
        $values["is_premium"] === "是" &&
        $values["site_name"] === "测试站点",
    "user fields preserve their meaning and IDs remain strings",
);
checkTemplate(
    $values["datetime"] === "2026-11-16 00:05:06" &&
        $values["date"] === "2026-11-16" &&
        $values["time"] === "00:05:06" &&
        $values["weekday"] === "星期一" &&
        $values["timezone"] === "Asia/Shanghai",
    "date, midnight and weekday use the configured timezone",
);
$missing = WelcomeTemplate::values(["id" => 1], $time, "测试站点");
checkTemplate(
    $missing["nickname"] === "Telegram 用户" &&
        $missing["username"] === "未设置" &&
        $missing["last_name"] === "未设置" &&
        $missing["language_code"] === "未知" &&
        $missing["is_premium"] === "否",
    "optional user fields have explicit fallbacks",
);
foreach (array_keys($values) as $key) {
    WelcomeTemplate::validate("{{ " . $key . " }}", "welcome", "欢迎语");
}
checkTemplate(count($values) === 13, "all thirteen supported tags validate");
foreach (
    [
        "{{nicknme}}",
        "{{__proto__}}",
        "{{}}",
        "{{nickname",
        "{{{nickname}}}",
        "{{nickname}} }}",
    ]
    as $invalid
) {
    try {
        WelcomeTemplate::validate($invalid, "welcome", "欢迎语");
        checkTemplate(false, "invalid tag rejected: " . $invalid);
    } catch (DomainError $e) {
        checkTemplate(
            $e->status === 422 && isset($e->fields["welcome"]),
            "invalid tag rejected: " . $invalid,
        );
    }
}
$longValues = $values;
$longValues["nickname"] = str_repeat("😀", 100);
$welcome = WelcomeTemplate::render(
    str_repeat("{{nickname}}", 150),
    $longValues,
    1800,
);
$hint = WelcomeTemplate::render(
    str_repeat("{{nickname}}", 20),
    $longValues,
    240,
);
$units = fn(string $text) => strlen(
    mb_convert_encoding($text, "UTF-16LE", "UTF-8"),
) / 2;
$largestIcon = max(
    array_map(fn($icon) => $units($icon["emoji"]), Icons::catalog()),
);
checkTemplate(
    mb_strlen($welcome) === 1800 &&
        mb_strlen($hint) === 240 &&
        $units($welcome . "\n\n" . $hint) + $largestIcon + 1 <= 4096,
    "repeated tags cannot exceed Telegram message length, including emoji",
);
echo "ALL $checks WELCOME TEMPLATE CHECKS PASSED" . PHP_EOL;
