<?php
namespace app\common\telegram;
use app\common\service\{
    ResourceService,
    SearchService,
    Settings,
    RateLimit,
    SubmissionService,
    BotMenu,
    BotSearchSettings,
    WelcomeTemplate,
    TelegramMetadata,
    Ads,
    BotLogin,
    Wallet,
    CommerceConfig,
    SearchReward,
};
use app\common\exception\DomainError;
use think\facade\Db;

class Bot
{
    private const RESULT_TEXT_WIDTH = 80;

    private BotCaptcha $captcha;
    private GroupGuard $groups;
    public function __construct(
        private Transport $client,
        private string $username = "",
        private int $botId = 0,
        private ?TelegramMetadata $submissionMetadata = null,
    ) {
        $this->client = new ReplyTransport($client);
        $this->captcha = new BotCaptcha($this->client, $botId);
        $this->groups = new GroupGuard($this->client, $botId);
    }
    public function handle(array $update): void
    {
        if (isset($update["my_chat_member"])) {
            $this->groups->membership($update["my_chat_member"]);
            return;
        }
        if (isset($update["callback_query"])) {
            $this->callback($update["callback_query"]);
            return;
        }
        $m = $update["message"] ?? ($update["edited_message"] ?? null);
        if ($m && $this->groups->moderate($m)) {
            return;
        }
        if (isset($update["edited_message"])) {
            return;
        }
        if (
            !$m ||
            !isset($m["text"]) ||
            !in_array(
                $m["chat"]["type"] ?? "",
                ["private", "group", "supergroup"],
                true,
            ) ||
            !empty($m["is_automatic_forward"])
        ) {
            return;
        }
        $chat = (int) $m["chat"]["id"];
        $group = $m["chat"]["type"] !== "private";
        $text = trim($m["text"]);
        if ($text === "") {
            return;
        }
        $command = null;
        $argument = "";
        if (
            preg_match(
                '#^/([a-zA-Z0-9_]+)(?:@([a-zA-Z0-9_]+))?(?:\s+(.*))?$#suD',
                $text,
                $match,
            )
        ) {
            // A bot with group-message access also sees commands for other bots.
            if (
                !empty($match[2]) &&
                strcasecmp($match[2], $this->username) !== 0
            ) {
                return;
            }
            $command = strtolower($match[1]);
            if ($command === "submit") {
                $command = "add";
            }
            $argument = trim($match[3] ?? "");
        }
        $context = $this->replyContext($m);
        if (!empty($m["sender_chat"])) {
            // Anonymous admins share a synthetic sender ID; never make it an owner.
            if (
                $group &&
                in_array(
                    $command,
                    ["search", "start", "help", "discover"],
                    true,
                )
            ) {
                try {
                    RateLimit::hit("bot-anonymous:" . $chat, 3, 60);
                    $this->send(
                        $chat,
                        "请切换为个人身份后搜索，才能由你本人操作搜索结果。",
                        $context,
                    );
                } catch (DomainError) {
                    // Avoid repeating the same notice in a busy group.
                }
            }
            return;
        }
        $uid = (int) ($m["from"]["id"] ?? 0);
        if ($uid <= 0 || !empty($m["from"]["is_bot"])) {
            return;
        }
        if (
            $group &&
            str_starts_with($text, "/") &&
            !in_array(
                $command,
                [
                    "start",
                    "help",
                    "search",
                    "discover",
                    "add",
                    ...GroupGuard::COMMANDS,
                ],
                true,
            )
        ) {
            return;
        }
        try {
            RateLimit::hit("bot:" . $uid, 30, 60);
            $user = $this->user($m["from"]);
            if (
                in_array($command, ["account", "wallet", "ads"], true) &&
                !$group
            ) {
                $this->commerce($chat, $user);
                return;
            }
            if (in_array($command, GroupGuard::COMMANDS, true)) {
                $this->groups->command($m, $command, $argument, $context);
                return;
            }
            $verification = $this->captcha->text($m);
            if ($verification["handled"]) {
                if (!empty($verification["resume"])) {
                    $this->resumeSearch($verification["resume"]);
                }
                return;
            }
            if ($command === "start") {
                if ($group) {
                    $this->send($chat, $this->groupHelp(), $context);
                } else {
                    $this->start($chat, $m["from"]);
                }
                return;
            }
            if ($command === "help") {
                if ($group) {
                    $this->send($chat, $this->groupHelp(), $context);
                } else {
                    $this->menuText(
                        $chat,
                        "help",
                        "使用帮助",
                        $this->helpText(),
                    );
                }
                return;
            }
            if ($command === "add") {
                if ($group) {
                    $this->send(
                        $chat,
                        "请私聊机器人发送 /add 提交收录。",
                        $context,
                    );
                    return;
                }
                if ($argument === "") {
                    $this->menuText(
                        $chat,
                        "submit",
                        "提交收录",
                        $this->submissionHelp(),
                    );
                    return;
                }
                // Older command examples may still include pipe-separated fields.
                $link = trim(explode("|", $argument, 2)[0]);
                $r = SubmissionService::createFromTelegram(
                    (int) $user["id"],
                    $link,
                    $this->submissionMetadata,
                );
                $this->send(
                    $chat,
                    "已自动获取标题：" .
                        $r["title"] .
                        "\n\n收录申请 #" .
                        $r["id"] .
                        " 已提交，请等待管理员审核。",
                    $context,
                );
                return;
            }
            if ($command === "search") {
                if ($argument === "") {
                    $this->send(
                        $chat,
                        "请在指令后输入关键词，例如：" .
                            $this->searchCommand($group) .
                            " Python",
                        $context,
                    );
                } else {
                    $this->search($chat, $uid, $argument, $context);
                }
                return;
            }
            if (str_starts_with($text, "/") && $command !== "discover") {
                $this->send(
                    $chat,
                    "发送关键词开始搜索，或用 /help 查看用法。",
                    $context,
                );
                return;
            }
            $this->search(
                $chat,
                $uid,
                $command === "discover" ? "" : $text,
                $context,
            );
        } catch (DomainError $e) {
            try {
                RateLimit::hit(
                    "bot-notice:{$this->botId}:$chat:$uid:" .
                        ($e->status === 429
                            ? "rate_limit"
                            : hash("sha256", $e->getMessage())),
                    1,
                    10,
                );
                $this->send($chat, $e->getMessage(), $context);
            } catch (DomainError) {
            }
        }
    }
    private function search(
        int $chat,
        int $uid,
        string $keyword,
        array $context = [],
        bool $resuming = false,
    ): void {
        if (!$this->captcha->gate($chat, $uid, $keyword, $context, $resuming)) {
            return;
        }
        $pageSize = BotSearchSettings::pageSize();
        $result = new SearchService()->search(
            ["q" => $keyword, "page_size" => $pageSize],
            "bot",
        );
        $session = [
            "id" => bin2hex(random_bytes(12)),
            "telegram_user_id" => $uid,
            "chat_id" => $chat,
            "query" => $keyword,
            "type" => "",
            "page" => 1,
            "page_size" => $pageSize,
            "filters_expanded" => 0,
            "revision" => 1,
            "expires_at" => date("Y-m-d H:i:s", time() + 7200),
        ];
        Db::name("bot_sessions")->insert($session);
        $message = $this->client->call(
            "sendMessage",
            ["chat_id" => $chat] + $context + $this->render($session, $result),
        );
        Db::name("bot_sessions")
            ->where("id", $session["id"])
            ->update(["message_id" => $message["message_id"]]);
        $account = Db::name("users")->where("telegram_id", $uid)->value("id");
        if ($account) {
            try {
                SearchReward::grant((int) $account, $keyword, $result, "bot");
            } catch (\Throwable) {
                \think\facade\Log::warning(
                    "本次搜索奖励未完成，用户可重新搜索。",
                );
            }
        }
    }
    private function user(array $from): array
    {
        $id = (int) $from["id"];
        $u = Db::name("users")->where("telegram_id", $id)->find();
        if (!$u) {
            Db::name("users")->insert([
                "username" => "tg_" . $id,
                "display_name" => mb_substr(
                    (string) ($from["first_name"] ?? "Telegram 用户"),
                    0,
                    100,
                ),
                "telegram_id" => $id,
                "created_at" => date("Y-m-d H:i:s"),
            ]);
            $u = Db::name("users")->where("telegram_id", $id)->find();
        }
        if (!$u["status"]) {
            throw new DomainError("当前账号已停用，请联系管理员。", 403);
        }
        return $u;
    }
    private function callback(array $cb): void
    {
        $answer = function (string $text = "", bool $alert = false) use ($cb) {
            try {
                $this->client->call("answerCallbackQuery", [
                    "callback_query_id" => $cb["id"],
                    "text" => $text,
                    "show_alert" => $alert,
                    "cache_time" => 0,
                ]);
            } catch (TelegramError $e) {
                if (!in_array($e->apiCode, [400, 403], true)) {
                    throw $e;
                }
            }
        };
        if (str_starts_with((string) ($cb["data"] ?? ""), "v:")) {
            try {
                $this->user($cb["from"]);
                $resume = $this->captcha->callback($cb, $answer);
                if ($resume) {
                    $this->resumeSearch($resume);
                }
            } catch (DomainError $e) {
                $answer($e->getMessage(), true);
            }
            return;
        }
        if (str_starts_with((string) ($cb["data"] ?? ""), "m:")) {
            $this->menuCallback($cb, $answer);
            return;
        }
        if (
            !preg_match(
                '/^s:([a-f0-9]{24}):(\d+):(p\d+|t[a-z]*|f[01]|h)$/',
                (string) ($cb["data"] ?? ""),
                $parts,
            )
        ) {
            $answer("按钮无效，请重新搜索。");
            return;
        }
        $s = Db::name("bot_sessions")->where("id", $parts[1])->find();
        if (!$s) {
            $answer("搜索已过期，请重新发送关键词。", true);
            return;
        }
        if (
            (string) $s["telegram_user_id"] !==
            (string) ($cb["from"]["id"] ?? "")
        ) {
            $answer(
                "不能操作别人的搜索结果。请发送关键词，创建你自己的搜索。",
                true,
            );
            return;
        }
        if (
            (string) $s["chat_id"] !==
                (string) ($cb["message"]["chat"]["id"] ?? "") ||
            (string) $s["message_id"] !==
                (string) ($cb["message"]["message_id"] ?? "")
        ) {
            $answer("这个按钮不属于当前消息，请重新发送关键词。", true);
            return;
        }
        if (strtotime($s["expires_at"]) <= time()) {
            $answer("搜索已过期，请重新发送关键词。", true);
            return;
        }
        if ((int) $s["revision"] !== (int) $parts[2]) {
            $answer("页面已经更新，请使用最新的按钮。");
            return;
        }
        try {
            RateLimit::hit("bot-callback:" . $cb["from"]["id"], 40, 60);
            $this->user($cb["from"]);
        } catch (DomainError $e) {
            $answer($e->getMessage());
            return;
        }
        $next = $s;
        $action = $parts[3];
        if ($action === "h") {
            if ((int) $s["chat_id"] < 0) {
                $answer(
                    "只有搜索者本人可以翻页或筛选。\n发送新关键词可重新搜索；没有响应时用 " .
                        $this->searchCommand(true) .
                        " 关键词。\n主菜单和个人信息请在私聊中通过 /start 查看。",
                    true,
                );
            } else {
                $answer();
                $this->start((int) $s["chat_id"], $cb["from"]);
            }
            return;
        }
        if (
            !$this->captcha->trusted(
                (int) $s["chat_id"],
                (int) $s["telegram_user_id"],
            )
        ) {
            $answer("需要重新验证，请发送搜索关键词完成验证后继续。", true);
            return;
        }
        try {
            $this->captcha->throttle(
                (int) $s["chat_id"],
                (int) $s["telegram_user_id"],
            );
        } catch (DomainError $e) {
            $answer($e->getMessage(), true);
            return;
        }
        if ($action[0] === "p") {
            $next["page"] = max(1, min(100, (int) substr($action, 1)));
        } elseif ($action[0] === "f") {
            $next["filters_expanded"] = (int) substr($action, 1);
        } else {
            $type = substr($action, 1);
            if ($type !== "" && !isset(ResourceService::TYPES[$type])) {
                $answer("分类无效。");
                return;
            }
            $next["type"] = $type;
            $next["page"] = 1;
        }
        $answer();
        $result = new SearchService()->search(
            [
                "q" => $s["query"],
                "type" => $next["type"],
                "page" => $next["page"],
                "page_size" => (int) $s["page_size"],
            ],
            "bot",
            false,
            false,
        );
        if ($next["page"] > max(1, $result["total_pages"])) {
            $next["page"] = max(1, min(100, $result["total_pages"]));
            $result = new SearchService()->search(
                [
                    "q" => $s["query"],
                    "type" => $next["type"],
                    "page" => $next["page"],
                    "page_size" => (int) $s["page_size"],
                ],
                "bot",
                false,
                false,
            );
        }
        $next["revision"] = (int) $s["revision"] + 1;
        try {
            $this->client->call(
                "editMessageText",
                ["chat_id" => $s["chat_id"], "message_id" => $s["message_id"]] +
                    $this->render($next, $result),
            );
        } catch (TelegramError $e) {
            if (!$e->notModified) {
                throw $e;
            }
        }
        Db::name("bot_sessions")
            ->where("id", $s["id"])
            ->where("revision", $s["revision"])
            ->update([
                "type" => $next["type"],
                "page" => $next["page"],
                "filters_expanded" => $next["filters_expanded"],
                "revision" => $next["revision"],
            ]);
    }
    private function render(array $s, array $result): array
    {
        $escape = fn($v) => htmlspecialchars(
            (string) $v,
            ENT_QUOTES | ENT_SUBSTITUTE,
            "UTF-8",
        );
        $text =
            Icons::html($s["query"] !== "" ? "search" : "discover") .
            " <b>" .
            ($s["query"] !== ""
                ? "搜索：" . $escape($s["query"])
                : "发现资源") .
            "</b>" .
            "\n" .
            ($s["type"] !== ""
                ? ResourceService::TYPES[$s["type"]]
                : "全部类型") .
            "\n\n";
        $ads = Ads::search((string) $s["query"], (string) $s["id"]);
        foreach (["search_top", "keyword", "brand"] as $kind) {
            if (isset($ads[$kind])) {
                $text .= Ads::html($ads[$kind]) . "\n";
            }
        }
        if ($ads) {
            $text .= "\n";
        }
        foreach ($result["items"] as $r) {
            $members = $r["members"]
                ? "  " . $this->members((int) $r["members"])
                : "";
            // Show richer titles; let clients wrap naturally and reserve room for counts.
            $titleWidth = max(
                8,
                self::RESULT_TEXT_WIDTH - mb_strwidth($members, "UTF-8"),
            );
            $text .=
                "\u{200E}" .
                Icons::html($r["type"]) .
                " \u{2068}" .
                '<a href="' .
                $escape($r["url"]) .
                '">' .
                $escape($this->compactTitle($r["title"], $titleWidth)) .
                "</a>" .
                // Isolate RTL titles so the icon and count stay on the left-to-right row.
                "\u{2069}\u{200E}" .
                $members .
                "\n";
        }
        if (!$result["items"]) {
            $text .= "暂时没有匹配的资源，试试更短的关键词或其他类型。\n";
        }
        $text .=
            "\n共 " .
            $result["total"] .
            " 条结果，当前第 " .
            $s["page"] .
            " 页，共 " .
            max(1, $result["total_pages"]) .
            " 页";
        if ((int) $s["chat_id"] < 0) {
            $text .= "\n仅搜索者本人可操作下方按钮";
        }
        if ($result["total_pages"] > 100) {
            $text .= "\n可浏览前 100 页，更多结果请细化关键词或选择分类。";
        }
        $data = fn($action) => "s:" .
            $s["id"] .
            ":" .
            $s["revision"] .
            ":" .
            $action;
        $typeButton = function (string $type) use ($s, $data): array {
            $button = [
                "text" => $type === "" ? "全部" : ResourceService::TYPES[$type],
                "callback_data" => $data("t" . $type),
            ];
            if ($s["type"] === $type) {
                $button["style"] = "primary";
            }
            return Icons::button($type ?: "discover", $button);
        };
        $expanded = (bool) $s["filters_expanded"];
        $rows = [
            [
                $typeButton("group"),
                $typeButton("channel"),
                $typeButton("video"),
                Icons::button($expanded ? "up" : "plus", [
                    "text" => $expanded ? "收起筛选" : "更多筛选",
                    "callback_data" => $data($expanded ? "f0" : "f1"),
                ]),
            ],
        ];
        if ($expanded) {
            $rows[] = array_map($typeButton, [
                "",
                "audio",
                "article",
                "other",
                "shop",
            ]);
            $rows[] = array_map($typeButton, ["bot", "file", "website"]);
        }
        $nav = [];
        if ($s["page"] > 1) {
            $nav[] = Icons::button("left", [
                "text" => "上一页",
                "callback_data" => $data("p" . ($s["page"] - 1)),
            ]);
        }
        $group = (int) $s["chat_id"] < 0;
        $nav[] = Icons::button($group ? "help" : "menu", [
            "text" => $group ? "搜索帮助" : "返回主菜单",
            "callback_data" => $data("h"),
        ]);
        if ($result["has_more"] && $s["page"] < 100) {
            $nav[] = Icons::button("right", [
                "text" => "下一页",
                "callback_data" => $data("p" . ($s["page"] + 1)),
            ]);
        }
        $rows[] = $nav;
        if (isset($ads["search_bottom"])) {
            $rows[] = [
                Icons::button("link", [
                    "text" => "广告 · " . $ads["search_bottom"]["title"],
                    "url" => $ads["search_bottom"]["url"],
                ]),
            ];
        }
        return [
            "text" => $text,
            "parse_mode" => "HTML",
            "link_preview_options" => ["is_disabled" => true],
            "reply_markup" => ["inline_keyboard" => $rows],
        ];
    }
    private function compactTitle(string $title, int $maxWidth): string
    {
        $title = trim(preg_replace("/[\s\p{Z}]+/u", " ", $title) ?? $title);
        // Ignore imported direction overrides and invisible break controls.
        // Preserve ZWNJ/ZWJ: Persian shaping and joined emoji need those characters.
        $title = trim(
            preg_replace(
                "/[\p{Cc}\x{00AD}\x{061C}\x{200B}\x{200E}\x{200F}\x{202A}-\x{202E}\x{2060}\x{2066}-\x{2069}\x{FEFF}]/u",
                "",
                $title,
            ) ?? $title,
        );
        if ($title === "") {
            return "未命名资源";
        }
        // Count display columns (CJK/emoji are wider than Latin), not just code units.
        // The separate UTF-16 cap also bounds unusually large combining clusters.
        preg_match_all("/\X/u", $title, $clusters);
        $parts = [];
        $width = $units = 0;
        foreach ($clusters[0] as $cluster) {
            $clusterWidth = preg_match(
                "/\p{Extended_Pictographic}|\p{Regional_Indicator}|\x{20E3}/u",
                $cluster,
            )
                ? 2
                : mb_strwidth(
                    preg_replace("/[\p{M}\x{200C}\x{200D}]/u", "", $cluster),
                    "UTF-8",
                );
            $clusterUnits =
                strlen(mb_convert_encoding($cluster, "UTF-16LE", "UTF-8")) / 2;
            $parts[] = [$cluster, $clusterWidth, $clusterUnits];
            $width += $clusterWidth;
            $units += $clusterUnits;
        }
        if ($width <= $maxWidth && $units <= 64) {
            return $title;
        }
        $short = "";
        $width = $units = 0;
        foreach ($parts as [$cluster, $clusterWidth, $clusterUnits]) {
            $width += $clusterWidth;
            $units += $clusterUnits;
            if ($width > $maxWidth - 1 || $units > 63) {
                break;
            }
            $short .= $cluster;
        }
        return rtrim($short) . "…";
    }
    private function escape(string $text): string
    {
        return htmlspecialchars($text, ENT_QUOTES | ENT_SUBSTITUTE, "UTF-8");
    }
    private function members(int $count): string
    {
        return $count >= 1000
            ? rtrim(rtrim(number_format($count / 1000, 1, ".", ""), "0"), ".") .
                    " K"
            : (string) $count;
    }
    private function homeRow(): array
    {
        return [
            Icons::button("menu", [
                "text" => "返回主菜单",
                "callback_data" => "m:" . BotMenu::version() . ":home",
            ]),
        ];
    }
    private function start(int $chat, array $from): void
    {
        $menu = BotMenu::get();
        $version = BotMenu::version($menu);
        $values = WelcomeTemplate::values($from);
        $welcome = WelcomeTemplate::render(
            $menu["welcome"],
            $values,
            WelcomeTemplate::LIMITS["welcome"],
        );
        $hint = WelcomeTemplate::render(
            $menu["hint"],
            $values,
            WelcomeTemplate::LIMITS["hint"],
        );
        $rows = [];
        foreach ($menu["rows"] as $row) {
            $buttons = [];
            foreach ($row as $button) {
                if (!$button["enabled"]) {
                    continue;
                }
                $b = ["text" => $button["text"]];
                if ($button["action"] === "url") {
                    $b["url"] = $button["value"];
                } else {
                    $b["callback_data"] = "m:" . $version . ":" . $button["id"];
                }
                if ($button["style"] !== "default") {
                    $b["style"] = $button["style"];
                }
                $buttons[] = Icons::button($button["icon"], $b);
            }
            if ($buttons) {
                $rows[] = $buttons;
            }
        }
        $this->client->call("sendMessage", [
            "chat_id" => $chat,
            "text" =>
                ($menu["welcome_icon"]
                    ? Icons::html($menu["welcome_icon"]) . " "
                    : "") .
                $this->escape($welcome) .
                ($hint !== "" ? "\n\n" . $this->escape($hint) : ""),
            "parse_mode" => "HTML",
            "link_preview_options" => ["is_disabled" => true],
            "reply_markup" => ["inline_keyboard" => $rows],
        ]);
    }
    private function helpText(): string
    {
        return "直接发送关键词搜索已收录的公开资源。\n点击类型按钮筛选，点击上一页 / 下一页翻页。\n\n/start · 主菜单\n/search 关键词 · 搜索资源\n/discover · 浏览全部资源\n/add 链接 · 自动获取标题并提交收录（私聊）\n/account · 用户中心与登录码（私聊）\n/wallet · 余额、充值与广告购买（私聊）\n\n也支持群聊搜索：" .
            $this->searchCommand(true) .
            " 关键词。\n搜索按钮有效期 2 小时，仅限搜索者本人操作。\n启用搜索验证时，先按提示完成验证，随后继续原搜索。\n群管理员可用 /groupsettings 配置验证、广告拦截和防刷屏。";
    }
    private function submissionHelp(): string
    {
        return Settings::get("submissions_enabled") === "1"
            ? "发送链接即可申请收录：\n\n/add https://t.me/username\n\n系统会自动获取标题，无需填写名称和简介。仅接受公开 Telegram 链接，通过后台审核后发布。"
            : "当前暂未开放收录提交，请稍后再试。";
    }
    private function searchCommand(bool $group): string
    {
        return "/search" .
            ($group && $this->username !== "" ? "@" . $this->username : "");
    }
    private function groupHelp(): string
    {
        return "群聊搜索已支持。\n发送 " .
            $this->searchCommand(true) .
            " Python 开始搜索。\n机器人能接收群内普通消息时，也可以直接发送关键词。\n\n结果会回复你的关键词消息；翻页、筛选等按钮仅限你本人操作，有效期 2 小时。\n请使用个人身份搜索，匿名管理员或频道身份无法绑定本人。\n\n主菜单、个人信息与提交收录请在私聊中使用 /start。";
    }
    private function replyContext(array $message): array
    {
        $context = [];
        if (!empty($message["message_id"])) {
            $context["reply_parameters"] = [
                "message_id" => (int) $message["message_id"],
                "allow_sending_without_reply" => true,
            ];
        }
        if (!empty($message["message_thread_id"])) {
            $context["message_thread_id"] = (int) $message["message_thread_id"];
        }
        return $context;
    }
    private function menuText(
        int $chat,
        string $icon,
        string $title,
        string $body,
    ): void {
        $this->client->call("sendMessage", [
            "chat_id" => $chat,
            "text" =>
                Icons::html($icon) .
                " <b>" .
                $this->escape($title) .
                "</b>\n\n" .
                $this->escape($body),
            "parse_mode" => "HTML",
            "link_preview_options" => ["is_disabled" => true],
            "reply_markup" => ["inline_keyboard" => [$this->homeRow()]],
        ]);
    }
    private function menuCallback(array $cb, callable $answer): void
    {
        if (
            ($cb["message"]["chat"]["type"] ?? "") !== "private" ||
            (string) ($cb["message"]["chat"]["id"] ?? "") !==
                (string) ($cb["from"]["id"] ?? "")
        ) {
            $answer("请在私聊中发送 /start，打开自己的菜单。");
            return;
        }
        $menu = BotMenu::get();
        if (
            !preg_match(
                '/^m:([a-f0-9]{10}):([a-z0-9_]{1,20})$/',
                (string) ($cb["data"] ?? ""),
                $parts,
            ) ||
            !hash_equals(BotMenu::version($menu), $parts[1])
        ) {
            $answer("菜单已更新，请发送 /start 打开最新菜单。");
            return;
        }
        $id = $parts[2];
        $button = null;
        foreach ($menu["rows"] as $row) {
            foreach ($row as $b) {
                if (
                    $b["id"] === $id &&
                    $b["enabled"] &&
                    $b["action"] !== "url"
                ) {
                    $button = $b;
                }
            }
        }
        if ($id !== "home" && !$button) {
            $answer("此按钮已停用，请发送 /start。");
            return;
        }
        try {
            RateLimit::hit("bot-callback:" . $cb["from"]["id"], 40, 60);
            $user = $this->user($cb["from"]);
        } catch (DomainError $e) {
            $answer($e->getMessage());
            return;
        }
        $chat = (int) $cb["message"]["chat"]["id"];
        if ($id === "home") {
            $answer();
            $this->start($chat, $cb["from"]);
            return;
        }
        $action = $button["action"];
        if (in_array($action, ["mine", "commerce"], true)) {
            $answer();
            $this->commerce($chat, $user);
            return;
        }
        if ($action === "discover" || $action === "search") {
            try {
                $this->search(
                    $chat,
                    (int) $cb["from"]["id"],
                    $action === "search" ? $button["value"] : "",
                );
                $answer();
            } catch (DomainError $e) {
                $answer($e->getMessage(), true);
            }
            return;
        }
        $answer();
        $body = "";
        if ($action === "hot") {
            $items = Db::name("search_logs")
                ->where(
                    "created_at",
                    ">=",
                    date("Y-m-d H:i:s", time() - 7 * 86400),
                )
                ->field("query,COUNT(*) AS searches")
                ->group("query")
                ->order("searches", "desc")
                ->limit(10)
                ->select()
                ->toArray();
            $body = $items
                ? "最近 7 天的搜索词：\n\n" .
                    implode(
                        "\n",
                        array_map(
                            fn($r) => $r["query"] .
                                " · " .
                                $r["searches"] .
                                " 次",
                            $items,
                        ),
                    ) .
                    "\n\n发送其中一个关键词开始搜索。"
                : "最近 7 天还没有搜索记录。发送关键词，开始第一次发现。";
        } elseif ($action === "rank") {
            $items = Db::name("resources")
                ->where("status", "published")
                ->whereIn("type", ["group", "channel"])
                ->where("members", ">", 0)
                ->order("members", "desc")
                ->order("id", "desc")
                ->limit(10)
                ->select()
                ->toArray();
            if ($items) {
                $lines = [];
                foreach ($items as $i => $r) {
                    $lines[] =
                        $i +
                        1 .
                        ". " .
                        $r["title"] .
                        " · " .
                        $this->members((int) $r["members"]) .
                        "\n" .
                        $r["url"];
                }
                $body =
                    "按已录入成员数排序（非实时数据）：\n\n" .
                    implode("\n\n", $lines);
            } else {
                $body =
                    "还没有录入群组或频道的成员数，暂不生成榜单。\n可以先从「发现」浏览已收录内容。";
            }
        } elseif ($action === "mine") {
            $submissions = Db::name("submissions")
                ->where("user_id", $user["id"])
                ->count();
            $pending = Db::name("submissions")
                ->where("user_id", $user["id"])
                ->where("status", "pending")
                ->count();
            $body =
                "你好，" .
                $user["display_name"] .
                "\nTelegram ID：" .
                $cb["from"]["id"] .
                "\n已提交：" .
                $submissions .
                " 条\n待审核：" .
                $pending .
                " 条\n\n网站账号与 Telegram 身份目前独立。";
        } elseif ($action === "submit") {
            $body = $this->submissionHelp();
        } elseif ($action === "message") {
            $body = $button["value"];
        } else {
            $body = $this->helpText();
        }
        $this->menuText($chat, $button["icon"], $button["text"], $body);
    }
    private function send(int $chat, string $text, array $context = []): void
    {
        $this->client->call(
            "sendMessage",
            [
                "chat_id" => $chat,
                "text" => $text,
            ] + $context,
        );
    }
    private function commerce(int $chat, array $user): void
    {
        $config = CommerceConfig::read();
        $code = BotLogin::issue((int) $user["id"]);
        $reward = $config["reward_enabled"]
            ? "每日首次有效搜索奖励 " . $config["reward_amount"] . " USDT"
            : "搜索奖励暂未开启";
        $text =
            Icons::html("user") .
            " <b>用户与营销中心</b>\n\n" .
            "账户 ID：" .
            $user["id"] .
            "\n余额：" .
            Wallet::balance((int) $user["id"]) .
            " USDT\n" .
            $reward .
            "\n\n可购买关键词排名、品牌展示、顶部轮播、底部按钮、群置顶及频道广告。\n\n" .
            "网站登录码：<code>" .
            $code .
            "</code>\n有效期 5 分钟，仅可使用一次。请勿转发。\n" .
            "打开用户中心，选择「Telegram 登录」并输入上方登录码。";
        $this->client->call("sendMessage", [
            "chat_id" => $chat,
            "text" => $text,
            "parse_mode" => "HTML",
            "reply_markup" => [
                "inline_keyboard" => [
                    [
                        Icons::button("user", [
                            "text" => "用户中心 · 钱包与广告",
                            "url" =>
                                $config["public_url"] . "/user/telegram-login",
                        ]),
                    ],
                    [
                        Icons::button("star", [
                            "text" => "购买广告位",
                            "url" =>
                                $config["public_url"] .
                                "/user/telegram-login?tab=marketing",
                        ]),
                        Icons::button("plus", [
                            "text" => "充值余额",
                            "url" =>
                                $config["public_url"] .
                                "/user/telegram-login?tab=recharges",
                        ]),
                    ],
                    [
                        Icons::button("clock", [
                            "text" => "历史账单",
                            "url" =>
                                $config["public_url"] .
                                "/user/telegram-login?tab=ledger",
                        ]),
                    ],
                    $this->homeRow(),
                ],
            ],
        ]);
    }
    private function resumeSearch(array $verification): void
    {
        $this->search(
            (int) $verification["chat_id"],
            (int) $verification["user_id"],
            $verification["pending_query"],
            json_decode($verification["reply_context"], true) ?: [],
            true,
        );
    }
}
