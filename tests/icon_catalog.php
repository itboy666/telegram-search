<?php
// Verify the complete pack offline; no Telegram requests or database writes.
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();

use app\common\service\BotMenu;
use app\common\telegram\Icons;

$checks = 0;
function checkIcons(bool $ok, string $label): void
{
    if (!$ok) {
        throw new RuntimeException("FAIL " . $label);
    }
    global $checks;
    $checks++;
    echo "PASS " . $label . PHP_EOL;
}
$root = dirname(__DIR__);
$catalog = Icons::catalog();
$manifest = json_decode(
    file_get_contents($root . "/resources/icons/telegram-ios/manifest.json"),
    true,
    512,
    JSON_THROW_ON_ERROR,
);
$packIds = array_column($manifest["icons"], "id");
$catalogIds = array_values(array_unique(array_column($catalog, "id")));
sort($packIds);
sort($catalogIds);
checkIcons(
    $manifest["name"] === "tgiosicons" &&
        count($packIds) > 0 &&
        count($packIds) === count(array_unique($packIds)),
    "snapshot contains a nonempty, unique Telegram iOS Icons pack",
);
checkIcons(
    $packIds === $catalogIds,
    "catalog covers every original icon without foreign IDs",
);
checkIcons(
    $catalog["search"]["id"] === "6032850693348399258" &&
        $catalog["hot"]["id"] === "5938539885907415367" &&
        $catalog["menu"]["id"] === $catalog["other"]["id"],
    "existing names and duplicate legacy aliases retain their original artwork",
);
$manifestById = array_column($manifest["icons"], null, "id");
$menu = [
    "welcome" => "图标验证",
    "welcome_icon" => "",
    "hint" => "",
    "rows" => [
        [
            [
                "id" => "test",
                "text" => "按钮",
                "icon" => "",
                "action" => "help",
                "value" => "",
                "style" => "default",
                "enabled" => true,
            ],
        ],
    ],
];
foreach ($catalog as $name => $icon) {
    if (!is_string($icon["id"]) || !ctype_digit($icon["id"])) {
        throw new RuntimeException("Emoji ID must be a string: " . $name);
    }
    $source = $root . "/resources/icons/telegram-ios/" . $icon["id"] . ".tgs";
    if (
        !is_file($source) ||
        hash_file("sha256", $source) !==
            $manifestById[$icon["id"]]["source_sha256"]
    ) {
        throw new RuntimeException("Source mismatch: " . $name);
    }
    $svg = file_get_contents(
        $root . "/public/icons/telegram-ios/" . $name . ".svg",
    );
    if (
        !str_contains($svg, "<svg") ||
        !str_contains($svg, "<path") ||
        preg_match(
            "/<(script|image|foreignObject)\b|\bon[a-z]+\s*=|(?:href|src)\s*=/i",
            $svg,
        )
    ) {
        throw new RuntimeException("Invalid SVG: " . $name);
    }
    if (
        empty($icon["label"]) ||
        empty($icon["position"]) ||
        !array_key_exists("keywords", $icon)
    ) {
        throw new RuntimeException("Missing search metadata: " . $name);
    }
    $menu["welcome_icon"] = $menu["rows"][0][0]["icon"] = $name;
    $validated = BotMenu::validate($menu);
    if (
        $validated["welcome_icon"] !== $name ||
        $validated["rows"][0][0]["icon"] !== $name
    ) {
        throw new RuntimeException("Menu rejected icon: " . $name);
    }
    if (
        Icons::button($name, ["text" => "测试"])["icon_custom_emoji_id"] !==
        $icon["id"]
    ) {
        throw new RuntimeException("Button ID mismatch: " . $name);
    }
    if (!str_contains(Icons::html($name), 'emoji-id="' . $icon["id"] . '"')) {
        throw new RuntimeException("Welcome ID mismatch: " . $name);
    }
}
checkIcons(
    true,
    "all original TGS hashes and safe SVG assets match the catalog",
);
checkIcons(
    true,
    "all IDs remain strings and every icon has searchable metadata",
);
checkIcons(true, "all icons validate for both welcome text and menu buttons");
checkIcons(
    true,
    "all welcome and button payloads use the original Telegram IDs",
);
echo "ALL $checks ICON CATALOG CHECKS PASSED (" .
    count($packIds) .
    " unique icons)" .
    PHP_EOL;
