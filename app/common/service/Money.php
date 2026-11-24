<?php
namespace app\common\service;

use app\common\exception\DomainError;

/** USDT has six decimal places. Never perform money arithmetic with a float. */
class Money
{
    public const MAX = 1000000000000;

    public static function parse(mixed $value, bool $zero = false): int
    {
        if (!is_string($value) && !is_int($value)) {
            throw new DomainError("金额必须为数字文本，最多六位小数。");
        }
        $value = trim((string) $value);
        if (!preg_match('/^(0|[1-9]\d{0,6})(?:\.(\d{1,6}))?$/D', $value, $m)) {
            throw new DomainError("金额格式不正确，最多六位小数。");
        }
        $amount = (int) $m[1] * 1000000 + (int) str_pad($m[2] ?? "", 6, "0");
        if ($amount > self::MAX || $amount < ($zero ? 0 : 1)) {
            throw new DomainError("金额须大于零且不超过 1,000,000 USDT。");
        }
        return $amount;
    }

    public static function format(int|string $amount): string
    {
        $n = abs((int) $amount);
        return ((int) $amount < 0 ? "-" : "") .
            intdiv($n, 1000000) .
            "." .
            str_pad((string) ($n % 1000000), 6, "0", STR_PAD_LEFT);
    }

    public static function row(array $row): array
    {
        if (array_key_exists("enabled", $row)) {
            $row["enabled"] = (bool) (int) $row["enabled"];
        }
        foreach (["amount", "balance", "balance_after", "price"] as $key) {
            if (array_key_exists($key, $row)) {
                $row[$key] = self::format($row[$key]);
            }
        }
        return $row;
    }
}
