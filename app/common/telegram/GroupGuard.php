<?php
namespace app\common\telegram;

use app\common\service\{BotSecuritySettings, RateLimit, Settings};
use app\common\exception\DomainError;
use think\facade\Db;

class GroupGuard
{
    public const COMMANDS = [
        "groupsettings",
        "captcha",
        "ads",
        "adwords",
        "allowdomain",
        "flood",
    ];
    public function __construct(
        private Transport $client,
        private int $botId,
    ) {}

    public function membership(array $event): void
    {
        if (
            !in_array(
                $event["chat"]["type"] ?? "",
                ["group", "supergroup"],
                true,
            ) ||
            (int) ($event["new_chat_member"]["user"]["id"] ?? 0) !==
                $this->botId ||
            !$this->botId
        ) {
            return;
        }
        $member = $event["new_chat_member"];
        $old = $event["old_chat_member"]["status"] ?? "left";
        $joined =
            in_array($old, ["left", "kicked"], true) &&
            !in_array($member["status"], ["left", "kicked"], true);
        $row = $this->remember(
            $event["chat"],
            $joined ? (int) ($event["from"]["id"] ?? 0) : null,
        );
        $canDelete =
            $member["status"] === "administrator" &&
            ($event["chat"]["type"] === "group" ||
                !empty($member["can_delete_messages"]));
        Db::name("bot_groups")
            ->where("id", $row["id"])
            ->update([
                "status" => $member["status"],
                "can_delete" => (int) $canDelete,
                "last_error" => "",
                "updated_at" => date("Y-m-d H:i:s"),
            ]);
        if ($joined || in_array($member["status"], ["left", "kicked"], true)) {
            Db::name("bot_verifications")
                ->where([
                    "bot_id" => $this->botId,
                    "chat_id" => $event["chat"]["id"],
                ])
                ->delete();
        }
        if ($joined) {
            try {
                RateLimit::hit(
                    "group-welcome:{$this->botId}:" . $event["chat"]["id"],
                    1,
                    300,
                );
                $this->client->call("sendMessage", [
                    "chat_id" => $event["chat"]["id"],
                    "text" =>
                        "已加入本群。群主和当前管理员可发送 /groupsettings 配置搜索验证、广告拦截和防刷屏。\n\n/captcha emoji 开启 emoji 验证\n/ads on 拦截普通成员的链接\n/flood on 开启防刷屏\n\n广告拦截需要将机器人设为管理员并允许删除消息。",
                ]);
            } catch (DomainError) {
            } catch (TelegramError $e) {
                if (!in_array($e->apiCode, [400, 403], true)) {
                    throw $e;
                }
            }
        }
    }
    public function command(
        array $m,
        string $command,
        string $argument,
        array $context,
    ): void {
        $chat = (int) $m["chat"]["id"];
        $uid = (int) $m["from"]["id"];
        if ($chat >= 0) {
            $this->send(
                $chat,
                "请在要管理的群里发送 /groupsettings。仅群主及当前管理员可以配置。",
                $context,
            );
            return;
        }
        RateLimit::hit("group-config:{$this->botId}:$chat:$uid", 6, 60);
        try {
            $member = $this->client->call("getChatMember", [
                "chat_id" => $chat,
                "user_id" => $uid,
            ]);
        } catch (TelegramError) {
            throw new DomainError(
                "暂时无法确认管理员身份。请将机器人设为群管理员后重试。",
                403,
            );
        }
        if (
            !in_array(
                $member["status"] ?? "",
                ["creator", "administrator"],
                true,
            )
        ) {
            throw new DomainError(
                "只有本群群主和当前管理员可以修改群设置。",
                403,
            );
        }
        $row = $this->remember($m["chat"]);
        $changes = [];
        $arg = strtolower(trim($argument));
        if ($command === "captcha") {
            if ($arg === "") {
                $this->send(
                    $chat,
                    "用法：/captcha off|emoji|button|text|math|sequence|random|inherit\n依次为关闭、emoji、点击确认、图片字母数字、算术、顺序点击、随机组合、继承后台。",
                    $context,
                );
                return;
            }
            if (!BotSecuritySettings::get()["allow_group_override"]) {
                throw new DomainError(
                    "总后台已统一管理群搜索验证，本群不能覆盖。",
                );
            }
            $changes["verify_mode"] = $arg;
        } elseif ($command === "ads") {
            $changes["ad_mode"] = $arg === "on" ? "links" : $arg;
        } elseif ($command === "adwords") {
            $changes["ad_keywords"] =
                $arg === "clear" ? "" : str_replace("|", "\n", trim($argument));
        } elseif ($command === "allowdomain") {
            $changes["allow_domains"] =
                $arg === "clear" ? "" : str_replace("|", "\n", $arg);
        } elseif ($command === "flood") {
            $changes["flood_limit"] =
                $arg === "on" ? 6 : ($arg === "off" ? 0 : $arg);
        }
        if ($changes) {
            $next = self::validate($changes);
            if (
                (isset($next["ad_mode"]) && $next["ad_mode"] !== "off") ||
                ($next["flood_limit"] ?? 0) > 0
            ) {
                $canDelete = false;
                try {
                    $self = $this->client->call("getChatMember", [
                        "chat_id" => $chat,
                        "user_id" => $this->botId,
                    ]);
                    $canDelete =
                        ($self["status"] ?? "") === "administrator" &&
                        ($m["chat"]["type"] === "group" ||
                            !empty($self["can_delete_messages"]));
                    Db::name("bot_groups")
                        ->where("id", $row["id"])
                        ->update([
                            "status" => $self["status"] ?? "member",
                            "can_delete" => (int) $canDelete,
                        ]);
                } catch (TelegramError) {
                }
                if (!$canDelete) {
                    throw new DomainError(
                        "请先将机器人设为群管理员，并授予删除消息权限，再开启拦截。",
                    );
                }
            }
            self::save((int) $row["id"], $next, null, $uid);
        }
        $row = Db::name("bot_groups")->where("id", $row["id"])->find();
        $policy = BotSecuritySettings::policy($this->botId, $chat);
        $this->send(
            $chat,
            ($changes ? "群设置已保存。\n\n" : "本群安全设置\n\n") .
                "搜索验证：" .
                BotSecuritySettings::MODES[$policy["mode"]] .
                "\n来源：" .
                (!BotSecuritySettings::get()["allow_group_override"]
                    ? "总后台统一管理"
                    : ($row["verify_mode"] === "inherit"
                        ? "继承总后台"
                        : "本群设置")) .
                "\n广告规则：" .
                [
                    "off" => "关闭",
                    "links" => "拦截链接",
                    "keywords" => "拦截关键词",
                    "strict" => "链接及关键词",
                ][$row["ad_mode"]] .
                "\n防刷屏：" .
                ($row["flood_limit"]
                    ? "每人 10 秒最多 " .
                        $row["flood_limit"] .
                        " 条，重复内容 30 秒最多 3 条"
                    : "关闭") .
                "\n\n/captcha emoji（off / button / text / math / sequence / random / inherit）" .
                "\n/ads on（off / links / keywords / strict）\n/adwords 关键词一|关键词二\n/allowdomain example.com|example.org\n/flood on（off 或 4–30）" .
                "\n\nadwords、allowdomain 使用 clear 清空。仅处理普通成员，群主与管理员免拦截。后台强制规则优先。",
            $context,
        );
    }
    public function moderate(array $m): bool
    {
        $chat = (int) ($m["chat"]["id"] ?? 0);
        if (
            $chat >= 0 ||
            !isset($m["message_id"]) ||
            !empty($m["is_automatic_forward"])
        ) {
            return false;
        }
        $r = Db::name("bot_groups")
            ->where(["bot_id" => $this->botId, "chat_id" => $chat])
            ->find();
        if (!$r || ($r["ad_mode"] === "off" && !$r["flood_limit"])) {
            return false;
        }
        if (
            !empty($m["sender_chat"]) &&
            (int) $m["sender_chat"]["id"] === $chat
        ) {
            return false; // Anonymous group administrators, not channel-origin ads.
        }
        $uid = (int) ($m["from"]["id"] ?? 0);
        if ($uid === $this->botId) {
            return false;
        }
        $text = (string) ($m["text"] ?? ($m["caption"] ?? ""));
        $reason = self::adReason($m, $r);
        if ($uid > 0 && $r["flood_limit"] && empty($m["edit_date"])) {
            try {
                RateLimit::hit(
                    "group-flood:{$this->botId}:$chat:$uid",
                    (int) $r["flood_limit"],
                    10,
                );
                if ($text !== "") {
                    RateLimit::hit(
                        "group-repeat:{$this->botId}:$chat:$uid:" .
                            hash("sha256", self::clean($text)),
                        3,
                        30,
                    );
                }
            } catch (DomainError) {
                $reason = "刷屏";
            }
        }
        if ($reason === "") {
            return false;
        }
        // Never delete on an uncertain administrator lookup.
        if (empty($m["sender_chat"]) && $uid > 0) {
            try {
                $member = $this->client->call("getChatMember", [
                    "chat_id" => $chat,
                    "user_id" => $uid,
                ]);
                if (
                    in_array(
                        $member["status"] ?? "",
                        ["administrator", "creator"],
                        true,
                    )
                ) {
                    return false;
                }
            } catch (TelegramError) {
                $this->recordError($r, "无法检查发言者身份，已跳过删除。");
                return true; // Do not echo this possible advertisement as a search.
            }
        }
        try {
            $this->client->call("deleteMessage", [
                "chat_id" => $chat,
                "message_id" => $m["message_id"],
            ]);
            Db::name("bot_groups")
                ->where("id", $r["id"])
                ->inc("blocked_count")
                ->update(["last_error" => ""]);
        } catch (TelegramError $e) {
            $this->recordError(
                $r,
                "拦截未能删除消息，请检查机器人管理员和删除权限（Telegram " .
                    $e->apiCode .
                    "）。",
            );
            if (!in_array($e->apiCode, [400, 403], true)) {
                throw $e;
            }
        }
        return true;
    }
    public static function adReason(array $m, array $r): string
    {
        $text = self::clean((string) ($m["text"] ?? ($m["caption"] ?? "")));
        if (in_array($r["ad_mode"], ["keywords", "strict"], true)) {
            foreach (preg_split("/\R/u", $r["ad_keywords"]) as $word) {
                $word = self::clean(trim($word));
                if ($word !== "" && str_contains($text, $word)) {
                    return "关键词";
                }
            }
        }
        if (!in_array($r["ad_mode"], ["links", "strict"], true)) {
            return "";
        }
        $urls = [];
        foreach (
            array_merge($m["entities"] ?? [], $m["caption_entities"] ?? [])
            as $e
        ) {
            if ($e["type"] === "text_link") {
                $urls[] = $e["url"];
            } elseif ($e["type"] === "url") {
                $utf16 = mb_convert_encoding(
                    (string) ($m["text"] ?? ($m["caption"] ?? "")),
                    "UTF-16LE",
                    "UTF-8",
                );
                $urls[] = mb_convert_encoding(
                    substr($utf16, $e["offset"] * 2, $e["length"] * 2),
                    "UTF-8",
                    "UTF-16LE",
                );
            }
        }
        preg_match_all(
            "~(?:https?://|tg://|www\.|t\.me/|telegram\.me/)[^\s<>()]+~iu",
            $text,
            $matches,
        );
        $urls = array_merge($urls, $matches[0]);
        $allowed = array_filter(explode("\n", $r["allow_domains"]));
        foreach ($urls as $url) {
            $url = self::clean($url);
            $host = strtolower(
                (string) parse_url(
                    preg_match("~^[a-z]+://~i", $url)
                        ? $url
                        : "https://" . $url,
                    PHP_URL_HOST,
                ),
            );
            $host = rtrim($host, ".");
            $ok = false;
            foreach ($allowed as $domain) {
                if ($host === $domain || str_ends_with($host, "." . $domain)) {
                    $ok = true;
                }
            }
            if (!$ok) {
                return "链接";
            }
        }
        return "";
    }
    public static function validate(array $input): array
    {
        $out = [];
        foreach ($input as $key => $value) {
            $valid = true;
            if ($key === "verify_mode") {
                $valid =
                    is_string($value) &&
                    ($value === "inherit" ||
                        isset(BotSecuritySettings::MODES[$value]));
            } elseif ($key === "ad_mode") {
                $valid =
                    is_string($value) &&
                    in_array(
                        $value,
                        ["off", "links", "keywords", "strict"],
                        true,
                    );
            } elseif ($key === "flood_limit") {
                $valid =
                    (is_int($value) ||
                        (is_string($value) &&
                            preg_match('/^\d+$/D', $value))) &&
                    ((int) $value === 0 ||
                        ((int) $value >= 4 && (int) $value <= 30));
                $value = (int) $value;
            } elseif (in_array($key, ["ad_keywords", "allow_domains"], true)) {
                $valid = is_string($value) && mb_strlen($value) <= 2000;
                if ($valid) {
                    $lines = array_values(
                        array_unique(
                            array_filter(
                                array_map("trim", preg_split("/\R/u", $value)),
                            ),
                        ),
                    );
                    $valid = count($lines) <= 50;
                    foreach ($lines as &$line) {
                        if ($key === "allow_domains") {
                            $line = strtolower($line);
                            $valid =
                                $valid &&
                                (bool) preg_match(
                                    '/^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/D',
                                    $line,
                                );
                        } else {
                            $valid =
                                $valid &&
                                mb_strlen($line) >= 2 &&
                                mb_strlen($line) <= 60;
                        }
                    }
                    unset($line);
                    $value = implode("\n", array_unique($lines));
                }
            } else {
                $valid = false;
            }
            if (!$valid) {
                throw new DomainError("群设置格式无效，请查看命令说明。", 422, [
                    $key =>
                        "请检查选项；关键词每行 2–60 字，域名不含协议与路径，最多 50 行",
                ]);
            }
            $out[$key] = $value;
        }
        return $out;
    }
    public static function save(
        int $id,
        array $input,
        ?int $admin,
        ?int $telegramUser = null,
    ): array {
        $values = self::validate($input);
        return Db::transaction(function () use (
            $id,
            $values,
            $admin,
            $telegramUser,
        ) {
            $r = Db::name("bot_groups")->where("id", $id)->lock(true)->find();
            if (!$r) {
                throw new DomainError("群记录不存在。", 404);
            }
            Db::name("bot_groups")
                ->where("id", $id)
                ->update(
                    $values + [
                        "updated_by" => $telegramUser,
                        "updated_at" => date("Y-m-d H:i:s"),
                    ],
                );
            Settings::audit(
                "更新机器人群设置",
                "群记录 #{$id}；" .
                    ($telegramUser ? "Telegram 管理员 {$telegramUser}；" : "") .
                    "字段：" .
                    implode("、", array_keys($values)),
                $admin,
            );
            return Db::name("bot_groups")->where("id", $id)->find();
        });
    }
    private function remember(array $chat, ?int $inviter = null): array
    {
        Db::execute(
            "INSERT IGNORE INTO ts_bot_groups (bot_id,chat_id,title,inviter_id,ad_keywords,allow_domains,created_at,updated_at) VALUES (?,?,?,?,'','',?,?)",
            [
                $this->botId,
                $chat["id"],
                mb_substr($chat["title"] ?? "", 0, 255),
                $inviter ?: null,
                date("Y-m-d H:i:s"),
                date("Y-m-d H:i:s"),
            ],
        );
        $key = ["bot_id" => $this->botId, "chat_id" => $chat["id"]];
        $values = ["title" => mb_substr($chat["title"] ?? "", 0, 255)];
        if ($inviter) {
            $values["inviter_id"] = $inviter;
        }
        Db::name("bot_groups")->where($key)->update($values);
        return Db::name("bot_groups")->where($key)->find();
    }
    private function recordError(array $row, string $message): void
    {
        Db::name("bot_groups")
            ->where("id", $row["id"])
            ->update(["last_error" => $message]);
    }
    private static function clean(string $text): string
    {
        return mb_strtolower(
            preg_replace("/[\x{200B}-\x{200D}\x{FEFF}]/u", "", $text),
        );
    }
    private function send(int $chat, string $text, array $context): void
    {
        $this->client->call(
            "sendMessage",
            ["chat_id" => $chat, "text" => $text] + $context,
        );
    }
}
