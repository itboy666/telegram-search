<?php
/** Offline checks: no network, credentials or database access. */
require dirname(__DIR__) . "/vendor/autoload.php";

use app\common\telegram\{ReplyTransport, TelegramError, Transport};

final class ReplyProbe implements Transport
{
    public array $calls = [];
    public function __construct(private array $responses) {}
    public function call(string $method, array $params = []): mixed
    {
        $this->calls[] = [$method, $params];
        if (!$this->responses) {
            throw new RuntimeException("Unexpected extra Telegram request");
        }
        $response = array_shift($this->responses);
        if ($response instanceof Throwable) {
            throw $response;
        }
        return $response;
    }
}
$checks = 0;
function replyCheck(bool $ok, string $label): void
{
    global $checks;
    if (!$ok) {
        throw new RuntimeException("FAIL " . $label);
    }
    $checks++;
    echo "PASS " . $label . PHP_EOL;
}
function replyError(callable $call, TelegramError $expected): bool
{
    try {
        $call();
    } catch (TelegramError $e) {
        return $e === $expected;
    }
    return false;
}

$missing = TelegramError::fromResponse(
    400,
    "Bad Request: message to be replied not found",
);
replyCheck($missing->replyNotFound, "classifies Telegram's missing-reply response");
foreach (
    [
        [400, "Bad Request: message thread not found"],
        [400, "Bad Request: chat not found"],
        [400, "Bad Request: message to edit not found"],
        [400, "Bad Request: can't parse entities"],
        [400, "Bad Request: message is too long"],
        [403, "Bad Request: message to be replied not found"],
        [429, "Too Many Requests: retry after 10"],
        [500, "Internal Server Error"],
        [0, ""],
    ] as [$code, $description]
) {
    replyCheck(
        !TelegramError::fromResponse($code, $description)->replyNotFound,
        "does not classify unrelated error as missing reply: " . $code . " " . $description,
    );
}
replyCheck(
    TelegramError::fromResponse(400, "Bad Request: message is not modified")->notModified &&
        TelegramError::fromResponse(429, "Too Many Requests", 10)->retryAfter === 10,
    "preserves not-modified classification and retry-after",
);
$secretDescription = "Bad Request: diagnostic https://invalid.test/private-credential";
$safeError = TelegramError::fromResponse(400, $secretDescription);
replyCheck(
    !str_contains((string) $safeError, "private-credential"),
    "raw error descriptions are absent from exception messages and traces",
);

$params = [
    "chat_id" => -100123,
    "message_thread_id" => 17,
    "text" => "<b>搜索结果</b>",
    "parse_mode" => "HTML",
    "reply_parameters" => [
        "message_id" => 42,
        "allow_sending_without_reply" => true,
    ],
    "link_preview_options" => ["is_disabled" => true],
    "reply_markup" => [
        "inline_keyboard" => [[["text" => "下一页", "callback_data" => "fixture"]]],
    ],
];
$delivered = ["message_id" => 100, "message_thread_id" => 17];
$probe = new ReplyProbe([$delivered]);
replyCheck(
    (new ReplyTransport($probe))->call("sendMessage", $params) === $delivered &&
        $probe->calls === [["sendMessage", $params]],
    "normal reply makes one request and returns the delivered message",
);
foreach (["sendMessage", "sendPhoto"] as $method) {
    $request = $params;
    if ($method === "sendPhoto") {
        $request["photo"] = new CURLStringFile("test image", "captcha.png", "image/png");
        $request["caption"] = $request["text"];
        unset($request["text"]);
    }
    $plain = $request;
    unset($plain["reply_parameters"]);
    $probe = new ReplyProbe([$missing, $delivered]);
    replyCheck(
        (new ReplyTransport($probe))->call($method, $request) === $delivered &&
            $probe->calls === [[$method, $request], [$method, $plain]],
        $method . " falls back once, keeping content, topic, controls and any upload",
    );
}
foreach (
    [
        new TelegramError(0),
        new TelegramError(400),
        new TelegramError(403),
        new TelegramError(429, 10),
        new TelegramError(500),
    ] as $error
) {
    $probe = new ReplyProbe([$error]);
    replyCheck(
        replyError(fn() => (new ReplyTransport($probe))->call("sendMessage", $params), $error) &&
            count($probe->calls) === 1,
        "propagates non-reply error without duplicate sends: " . $error->apiCode,
    );
}
$required = $params;
$required["reply_parameters"]["allow_sending_without_reply"] = false;
$implicit = $params;
unset($implicit["reply_parameters"]["allow_sending_without_reply"]);
$plain = $params;
unset($plain["reply_parameters"]);
foreach (
    [
        ["sendMessage", $required],
        ["sendMessage", $implicit],
        ["sendMessage", $plain],
        ["editMessageText", $params],
        ["answerCallbackQuery", $params],
    ] as [$method, $request]
) {
    $probe = new ReplyProbe([$missing]);
    replyCheck(
        replyError(fn() => (new ReplyTransport($probe))->call($method, $request), $missing) &&
            count($probe->calls) === 1,
        "only an explicitly optional outgoing reply can fall back: " . $method,
    );
}
foreach ([$missing, new TelegramError(400), new TelegramError(429, 5)] as $secondError) {
    $probe = new ReplyProbe([$missing, $secondError]);
    replyCheck(
        replyError(fn() => (new ReplyTransport($probe))->call("sendMessage", $params), $secondError) &&
            count($probe->calls) === 2,
        "a failed fallback propagates without a third request: " . $secondError->apiCode,
    );
}
echo "ALL $checks TELEGRAM REPLY CHECKS PASSED\n";
