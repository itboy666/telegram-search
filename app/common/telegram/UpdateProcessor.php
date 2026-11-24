<?php
namespace app\common\telegram;
use think\facade\Db;
class UpdateProcessor
{
    public function __construct(private Bot $bot) {}
    public function process(int $botId, array $update): int
    {
        $id = (int) $update["update_id"];
        $key = ["bot_id" => $botId, "update_id" => $id];
        $saved = Db::name("bot_updates")->where($key)->find();
        if (!$saved || !in_array($saved["state"], ["done", "ignored"], true)) {
            if (!$saved) {
                Db::name("bot_updates")->insert(
                    $key + ["created_at" => date("Y-m-d H:i:s")],
                );
            }
            $status = "done";
            try {
                $this->bot->handle($update);
            } catch (TelegramError $e) {
                if (in_array($e->apiCode, [400, 403], true)) {
                    $status = "ignored";
                } else {
                    throw $e;
                }
            }
            Db::name("bot_updates")
                ->where($key)
                ->update([
                    "state" => $status,
                    "completed_at" => date("Y-m-d H:i:s"),
                ]);
        }
        return $id + 1;
    }
}
