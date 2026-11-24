<?php
namespace app\common\service;

/** Anonymous public previews only. Does not use a user session or send messages. */
class PublicTelegramPage
{
    public static function get(string $path, int $timeout = 20): string
    {
        if (
            !preg_match(
                '#^/(?:(?:s/)?[A-Za-z0-9_]{4,32}(?:\?before=[0-9]+)?|[A-Za-z0-9_]{4,32}/[1-9][0-9]{0,19}\?embed=1&mode=tme)$#D',
                $path,
            )
        ) {
            throw new \RuntimeException("无效的公开预览路径。");
        }
        $curl = curl_init("https://t.me" . $path);
        $body = "";
        $timeout = max(1, min(20, $timeout));
        curl_setopt_array($curl, [
            CURLOPT_CONNECTTIMEOUT => min(8, $timeout),
            CURLOPT_TIMEOUT => $timeout,
            CURLOPT_FOLLOWLOCATION => false,
            CURLOPT_SSL_VERIFYPEER => true,
            CURLOPT_SSL_VERIFYHOST => 2,
            CURLOPT_USERAGENT => "StarIndexCatalog/1.0 (public-link directory)",
            CURLOPT_WRITEFUNCTION => static function (
                $curl,
                string $chunk,
            ) use (&$body): int {
                if (strlen($body) + strlen($chunk) > 3 * 1024 * 1024) {
                    return 0;
                }
                $body .= $chunk;
                return strlen($chunk);
            },
        ]);
        $ok = curl_exec($curl);
        $status = curl_getinfo($curl, CURLINFO_RESPONSE_CODE);
        curl_close($curl);
        if ($ok === false || $status !== 200) {
            throw new \RuntimeException("公开预览请求未成功，HTTP " . $status);
        }
        return $body;
    }

    public static function isBot(string $html): bool
    {
        return (bool) preg_match(
            '#class="tgme_action_button_new[^"]*"[^>]*>\s*Start Bot\s*</a>#i',
            $html,
        );
    }

    /** At most four independent preview requests; no authentication or redirects. */
    public static function getMany(array $paths): array
    {
        if (count($paths) > 4) {
            throw new \InvalidArgumentException("最多同时请求 4 个预览。");
        }
        $multi = curl_multi_init();
        $handles = $bodies = $results = [];
        try {
            foreach ($paths as $key => $path) {
                if (
                    !preg_match(
                        '#^/(?:s/)?[A-Za-z0-9_]{4,32}(?:\?before=[0-9]+)?$#D',
                        $path,
                    )
                ) {
                    throw new \RuntimeException("无效的公开预览路径。");
                }
                $handle = curl_init("https://t.me" . $path);
                $bodies[$key] = "";
                curl_setopt_array($handle, [
                    CURLOPT_CONNECTTIMEOUT => 8,
                    CURLOPT_TIMEOUT => 20,
                    CURLOPT_FOLLOWLOCATION => false,
                    CURLOPT_USERAGENT => "StarIndexCatalog/1.0 (public-link directory)",
                    CURLOPT_WRITEFUNCTION => static function (
                        $curl,
                        string $chunk,
                    ) use (&$bodies, $key): int {
                        if (
                            strlen($bodies[$key]) + strlen($chunk) >
                            3 * 1024 * 1024
                        ) {
                            return 0;
                        }
                        $bodies[$key] .= $chunk;
                        return strlen($chunk);
                    },
                ]);
                $handles[$key] = $handle;
                curl_multi_add_handle($multi, $handle);
            }
            do {
                $code = curl_multi_exec($multi, $running);
                if ($running && curl_multi_select($multi, 1.0) === -1) {
                    usleep(10000);
                }
            } while ($running && $code === CURLM_OK);
            foreach ($handles as $key => $handle) {
                $status = curl_getinfo($handle, CURLINFO_RESPONSE_CODE);
                if ($status === 429) {
                    throw new \RuntimeException(
                        "Telegram 返回限流，已停止本批请求，请稍后重跑。",
                    );
                }
                $results[$key] =
                    $status === 200 && curl_errno($handle) === 0
                        ? $bodies[$key]
                        : null;
            }
            return $results;
        } finally {
            foreach ($handles as $handle) {
                curl_multi_remove_handle($multi, $handle);
                curl_close($handle);
            }
            curl_multi_close($multi);
        }
    }
}
