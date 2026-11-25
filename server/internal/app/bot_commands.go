package app

import (
	"context"
	"fmt"
	"slices"
)

type botCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

type botCommandMenu struct {
	Scope    string
	Commands []botCommand
}

func botGroupAdminCommands() []botCommand {
	return []botCommand{
		{"groupsettings", "查看本群安全设置"},
		{"captcha", "设置本群搜索验证"},
		{"maxlength", "设置群消息字数上限"},
		{"joinverify", "设置新人入群验证"},
		{"welcome", "开启或关闭新人欢迎语"},
		{"ads", "设置本群广告拦截"},
		{"adwords", "设置广告拦截关键词"},
		{"allowdomain", "设置允许发送的域名"},
		{"flood", "设置本群防刷屏"},
		{"media", "设置媒体消息拦截"},
		{"forwards", "设置转发消息拦截"},
	}
}

func botCommandMenus() []botCommandMenu {
	group := []botCommand{
		{"start", "查看群内搜索说明"},
		{"search", "搜索群组与频道"},
		{"discover", "发现群组与频道"},
		{"help", "群内搜索帮助"},
	}
	private := []botCommand{
		{"start", "打开主菜单"},
		{"search", "搜索群组与频道"},
		{"discover", "发现群组与频道"},
		{"add", "提交公开链接或私人邀请"},
		{"account", "我的账户"},
		{"wallet", "钱包账单与继续填写"},
		{"recharge", "充值余额"},
		{"ads", "购买广告位"},
		{"cancel", "退出当前钱包填写"},
		{"login", "获取网站登录码（可选）"},
		{"help", "使用帮助"},
	}
	return []botCommandMenu{
		// Overwrite the legacy mixed default first, so an unset scope cannot
		// expose personal commands in a group.
		{"default", slices.Clone(group)},
		{"all_private_chats", private},
		{"all_group_chats", group},
		{"all_chat_administrators", append(slices.Clone(group), botGroupAdminCommands()...)},
	}
}

func botGroupAdminCommand(command string) bool {
	return slices.ContainsFunc(botGroupAdminCommands(), func(c botCommand) bool { return c.Command == command })
}

// Telegram command scopes only change the menu. Always enforce the chat
// boundary again for manually typed commands, before any input state machine.
// Group administrator commands still require GroupCommand's live role check.
func botCommandAllowed(command, chatType string) bool {
	if command == "" {
		return true
	}
	scope := "all_private_chats"
	switch chatType {
	case "private":
	case "group", "supergroup":
		scope = "all_chat_administrators"
	default:
		return false
	}
	for _, menu := range botCommandMenus() {
		if menu.Scope == scope {
			return slices.ContainsFunc(menu.Commands, func(c botCommand) bool { return c.Command == command })
		}
	}
	return false
}

func (a *App) syncBotCommands(ctx context.Context) error {
	for _, menu := range botCommandMenus() {
		result, err := a.Telegram(ctx, "setMyCommands", Row{
			"scope": Row{"type": menu.Scope}, "language_code": "", "commands": menu.Commands,
		})
		if err != nil {
			return fmt.Errorf("同步命令菜单 %s 失败：%w", menu.Scope, err)
		}
		if !boolean(result["ok"]) {
			return fmt.Errorf("同步命令菜单 %s 未获确认", menu.Scope)
		}
	}
	return nil
}

const botAddHelp = "提交 Telegram 资源\n\n发送 /add @用户名 或 /add https://t.me/用户名。\n私人群组和频道也可用 /add 后加完整邀请链接，支持 t.me/+… 和 t.me/joinchat/…。\n\n提交后由管理员审核。审核通过的链接可在公开搜索中找到，请提交你有权分享的邀请。"

const botPrivateHelp = "发送关键词或 /search 关键词 搜索群组和频道，点击「帖子」查看相关帖子。\n/discover 发现群组和频道\n/add @用户名或链接 申请收录，也支持私人邀请\n/account 我的账户与提交记录\n/wallet 余额、账单与继续填写\n/recharge 充值余额\n/ads 购买广告位\n/cancel 退出当前填写（不撤销已创建订单）\n/login 获取网站登录码（可选）\n/start 返回主菜单\n\n充值和广告购买可直接在当前私聊完成。结果按钮仅限搜索者本人操作，有效期 2 小时。"

const botGroupHelp = "直接发送关键词，例如：服务器，即可搜索群组和频道，无需 @机器人。\n/search 关键词 搜索群组和频道\n/discover 发现群组和频道\n/help 群内搜索帮助\n\n点击「帖子」查看相关帖子，翻页和筛选仅限发起搜索的人操作，有效期 2 小时。结果会回复对应关键词消息。"
