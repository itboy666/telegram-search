<?php
namespace app\common\telegram;

class CaptchaPuzzle
{
    public static function make(string $method): array
    {
        if ($method === "random") {
            $methods = ["emoji", "math", "sequence"];
            if (extension_loaded("gd")) {
                $methods[] = "text";
            }
            $method = $methods[random_int(0, count($methods) - 1)];
        }
        $p = ["method" => $method, "choices" => [], "answers" => []];
        if ($method === "text") {
            if (!extension_loaded("gd")) {
                throw new \app\common\exception\DomainError(
                    "图片验证码暂不可用，请管理员安装 PHP GD 扩展。",
                );
            }
            $letters = "ABCDEFGHJKLMNPQRSTUVWXYZ";
            $digits = "23456789";
            $code =
                $letters[random_int(0, strlen($letters) - 1)] .
                $digits[random_int(0, strlen($digits) - 1)];
            $alphabet = $letters . $digits;
            for ($i = 0; $i < 4; $i++) {
                $code .= $alphabet[random_int(0, strlen($alphabet) - 1)];
            }
            $code = self::shuffle(str_split($code));
            $code = implode("", $code);
            $p["prompt"] =
                "请回复这张验证图片，输入图中的 6 位字母数字（不区分大小写）。";
            $p["answers"] = [$code];
            $p["photo"] = self::image($code);
        } elseif ($method === "math") {
            $a = random_int(2, 15);
            $b = random_int(2, 9);
            $op = ["+", "−", "×", "÷"][random_int(0, 3)];
            if ($op === "÷") {
                $a *= $b;
            } elseif ($op === "−" && $a < $b) {
                [$a, $b] = [$b, $a];
            }
            $answer = match ($op) {
                "+" => $a + $b,
                "−" => $a - $b,
                "×" => $a * $b,
                "÷" => intdiv($a, $b),
            };
            $p["prompt"] = "请回复这条消息，输入计算结果：\n\n$a $op $b = ?";
            $p["answers"] = [(string) $answer];
        } elseif ($method === "button") {
            $key = bin2hex(random_bytes(6));
            $p["choices"] = [$key => "点击验证"];
            $p["answers"] = [$key];
            $p["prompt"] = "点击下方按钮，确认后继续搜索。";
        } else {
            $emojis = self::shuffle([
                "🍎",
                "🍋",
                "🍇",
                "🍉",
                "🚗",
                "✈️",
                "🐱",
                "🐶",
                "🌻",
                "⚽",
                "🎸",
                "⭐",
            ]);
            foreach (array_slice($emojis, 0, 6) as $emoji) {
                $p["choices"][bin2hex(random_bytes(6))] = $emoji;
            }
            $keys = self::shuffle(array_keys($p["choices"]));
            $p["answers"] = array_slice(
                $keys,
                0,
                $method === "sequence" ? 3 : 1,
            );
            $targets = array_map(
                fn($key) => $p["choices"][$key],
                $p["answers"],
            );
            $p["prompt"] =
                $method === "sequence"
                    ? "请按顺序点击这 3 个 emoji：\n\n" .
                        implode(" → ", $targets)
                    : "请点击和这个相同的 emoji：\n\n" . $targets[0];
        }
        return $p;
    }
    private static function shuffle(array $items): array
    {
        for ($i = count($items) - 1; $i > 0; $i--) {
            $j = random_int(0, $i);
            [$items[$i], $items[$j]] = [$items[$j], $items[$i]];
        }
        return $items;
    }
    private static function image(string $code): string
    {
        $image = imagecreatetruecolor(300, 96);
        imagefill($image, 0, 0, imagecolorallocate($image, 247, 249, 253));
        for ($i = 0; $i < 9; $i++) {
            imageline(
                $image,
                random_int(0, 299),
                random_int(0, 95),
                random_int(0, 299),
                random_int(0, 95),
                imagecolorallocate($image, 170, 185, 210),
            );
        }
        foreach (str_split($code) as $i => $char) {
            $glyph = imagecreatetruecolor(10, 17);
            imagefill($glyph, 0, 0, imagecolorallocate($glyph, 247, 249, 253));
            imagestring(
                $glyph,
                5,
                0,
                0,
                $char,
                imagecolorallocate(
                    $glyph,
                    random_int(25, 70),
                    random_int(35, 80),
                    random_int(70, 120),
                ),
            );
            imagecopyresized(
                $image,
                $glyph,
                20 + $i * 45,
                random_int(16, 27),
                0,
                0,
                30,
                51,
                10,
                17,
            );
            imagedestroy($glyph);
        }
        ob_start();
        imagepng($image);
        $png = ob_get_clean();
        imagedestroy($image);
        return $png;
    }
}
