<?php
namespace app\common\service;

use app\common\exception\DomainError;

class Tronscan
{
    public const USDT = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t";

    public function __construct(private ?\Closure $fetch = null) {}

    public static function validAddress(string $address): bool
    {
        if (!preg_match('/^T[1-9A-HJ-NP-Za-km-z]{33}$/D', $address)) {
            return false;
        }
        $alphabet =
            "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz";
        $bytes = [0];
        foreach (str_split($address) as $char) {
            $carry = strpos($alphabet, $char);
            for ($i = count($bytes) - 1; $i >= 0; $i--) {
                $carry += $bytes[$i] * 58;
                $bytes[$i] = $carry & 255;
                $carry >>= 8;
            }
            while ($carry) {
                array_unshift($bytes, $carry & 255);
                $carry >>= 8;
            }
        }
        $raw = pack("C*", ...$bytes);
        return strlen($raw) === 25 &&
            ord($raw[0]) === 0x41 &&
            hash_equals(
                substr($raw, 21),
                substr(
                    hash(
                        "sha256",
                        hash("sha256", substr($raw, 0, 21), true),
                        true,
                    ),
                    0,
                    4,
                ),
            );
    }

    public function request(string $path, array $params): array
    {
        if ($this->fetch) {
            return ($this->fetch)($path, $params);
        }
        $curl = curl_init(
            "https://apilist.tronscanapi.com/api/" .
                $path .
                "?" .
                http_build_query($params),
        );
        curl_setopt_array($curl, [
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_CONNECTTIMEOUT => 3,
            CURLOPT_TIMEOUT => 5,
            CURLOPT_FOLLOWLOCATION => false,
            CURLOPT_SSL_VERIFYPEER => true,
            CURLOPT_SSL_VERIFYHOST => 2,
            CURLOPT_HTTPHEADER => [
                "Accept: application/json",
                "TRON-PRO-API-KEY: " . CommerceConfig::secret(),
            ],
        ]);
        $raw = curl_exec($curl);
        $code = (int) curl_getinfo($curl, CURLINFO_HTTP_CODE);
        curl_close($curl);
        if (!is_string($raw) || $code !== 200) {
            throw new DomainError(
                $code === 429
                    ? "Tronscan 请求频繁，请稍后重试。"
                    : "暂时无法连接 Tronscan，订单仍保留，请稍后核查。",
                503,
            );
        }
        $data = json_decode($raw, true, 512, JSON_BIGINT_AS_STRING);
        if (
            !is_array($data) ||
            isset($data["Error"]) ||
            isset($data["error"])
        ) {
            throw new DomainError(
                "Tronscan 返回异常，未入账，请稍后重试。",
                503,
            );
        }
        return $data;
    }

    public function transfers(
        string $address,
        int $since,
        int $start = 0,
    ): array {
        $data = $this->request("token_trc20/transfers", [
            "relatedAddress" => $address,
            "contract_address" => self::USDT,
            "start_timestamp" => $since * 1000,
            "start" => $start,
            "limit" => 50,
            "sort" => "-timestamp",
        ]);
        if (
            !isset($data["token_transfers"]) ||
            !is_array($data["token_transfers"])
        ) {
            throw new DomainError("Tronscan 未返回有效转账列表，未入账。", 503);
        }
        return $data["token_transfers"];
    }

    public function transaction(string $hash): array
    {
        if (!preg_match('/^[a-f0-9]{64}$/D', $hash)) {
            throw new DomainError("交易哈希须为 64 位十六进制字符。");
        }
        return $this->request("transaction-info", ["hash" => $hash]);
    }

    /** Fail closed on a missing or changed API field. Hash alone never assigns ownership. */
    public static function verify(array $tx, array $order, string $hash): void
    {
        if (
            ($tx["hash"] ?? "") !== $hash ||
            ($tx["contractRet"] ?? "") !== "SUCCESS" ||
            ($tx["confirmed"] ?? null) !== true ||
            ($tx["revert"] ?? null) !== false ||
            (int) ($tx["confirmations"] ?? 0) < 19
        ) {
            throw new DomainError("交易尚未成功确认，请稍后核查。", 409);
        }
        $time = (int) ($tx["timestamp"] ?? 0);
        if (
            $time < strtotime($order["created_at"]) * 1000 ||
            $time > strtotime($order["expires_at"]) * 1000
        ) {
            throw new DomainError(
                "转账时间不在此订单的付款时段内，请联系管理员核对。",
                409,
            );
        }
        $matches = array_filter(
            $tx["trc20TransferInfo"] ?? [],
            fn($item) => ($item["contract_address"] ?? "") === self::USDT &&
                ($item["to_address"] ?? "") === $order["receive_address"] &&
                ($item["type"] ?? "") === "Transfer" &&
                ($item["tokenType"] ?? "") === "trc20" &&
                (int) ($item["decimals"] ?? -1) === 6 &&
                isset($item["amount_str"]) &&
                (string) $item["amount_str"] === (string) $order["amount"],
        );
        if (count($matches) !== 1) {
            throw new DomainError(
                "币种、收款地址或金额与此订单不符，未入账。",
                409,
            );
        }
    }
}
