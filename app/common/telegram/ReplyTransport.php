<?php
namespace app\common\telegram;

/** Send an optional reply even if its target disappears during processing. */
final class ReplyTransport implements Transport
{
    public function __construct(private Transport $client) {}

    public function call(string $method, array $params = []): mixed
    {
        try {
            return $this->client->call($method, $params);
        } catch (TelegramError $e) {
            if (
                $e->apiCode !== 400 ||
                !$e->replyNotFound ||
                !in_array($method, ["sendMessage", "sendPhoto"], true) ||
                empty($params["reply_parameters"]["message_id"]) ||
                ($params["reply_parameters"]["allow_sending_without_reply"] ??
                    false) !== true
            ) {
                throw $e;
            }
        }
        // Telegram explicitly rejected the send, so no message was delivered.
        // Retry once without the reply, preserving the topic, content and buttons.
        unset($params["reply_parameters"]);
        return $this->client->call($method, $params);
    }
}
