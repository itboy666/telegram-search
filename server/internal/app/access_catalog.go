package app

import "strings"

type ActionSpec struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}
type FieldSpec struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	ReadOnly bool   `json:"read_only"`
}
type ModuleSpec struct {
	Key     string       `json:"key"`
	Label   string       `json:"label"`
	Actions []ActionSpec `json:"actions"`
	Fields  []FieldSpec  `json:"fields"`
}

func moduleSpec(key, label, actions, fields, writable string) ModuleSpec {
	m := ModuleSpec{Key: key, Label: label, Actions: []ActionSpec{}, Fields: []FieldSpec{}}
	for _, pair := range strings.Split(actions, "|") {
		p := strings.SplitN(pair, ":", 2)
		if len(p) == 2 {
			m.Actions = append(m.Actions, ActionSpec{key + "." + p[0], p[1]})
		}
	}
	w := strings.Fields(writable)
	for _, pair := range strings.Split(fields, "|") {
		p := strings.SplitN(pair, ":", 2)
		if len(p) == 2 {
			ro := true
			for _, k := range w {
				if k == p[0] {
					ro = false
				}
			}
			m.Fields = append(m.Fields, FieldSpec{p[0], p[1], ro})
		}
	}
	return m
}

var PermissionCatalog = []ModuleSpec{
	moduleSpec("dashboard", "工作台", "read:查看概览", "stats:统计总数|trend:搜索趋势|popular:热门关键词|recent_resources:最近资源|activity:近期操作|bot:机器人状态|updated_at:统计时间", ""),
	moduleSpec("resources", "资源管理", "read:查看资源|create:新增资源|update:编辑资源|archive:归档资源|delete:删除资源", "title:资源名称|url:链接|description:简介|type:类型|category_id:分类|status:发布状态|status_reason:状态说明|members:成员数量|tags:关键词标签|featured:精选|source:来源|owner_id:提交用户 ID|created_at:创建时间|updated_at:更新时间", "title url description type category_id status status_reason members tags featured"),
	moduleSpec("categories", "分类管理", "read:查看分类|create:新增分类|update:编辑分类|delete:删除分类", "name:分类名称|description:说明|sort_order:排序|resource_count:资源数量|created_at:创建时间", "name description sort_order"),
	moduleSpec("submissions", "收录审核", "read:查看申请|create:新增待审核申请|update:编辑待审核申请|delete:删除待审核申请|review:审核并发布 / 拒绝", "title:资源名称|url:链接|type:类型|description:简介|category_id:分类|user_id:提交用户 ID|submitter:提交人|status:审核状态|review_note:审核备注|resource_id:资源 ID|reviewer_id:审核管理员 ID|created_at:提交时间|reviewed_at:审核时间", "title url type description category_id user_id status review_note"),
	moduleSpec("users", "普通用户", "read:查看用户|create:新增网站用户|update:管理账号与投稿权限|delete:删除无关联用户", "username:账号|display_name:昵称|telegram_id:Telegram ID|status:状态|submission_allowed:是否允许提交收录|submission_note:禁止投稿原因|created_at:注册时间|last_login_at:最近登录|password:设置登录密码（不回显）", "username display_name password status submission_allowed submission_note"),
	moduleSpec("settings", "系统设置", "read:查看设置|update:修改设置", "site_name:网站名称|site_description:网站描述|public_api_enabled:开放 API|submissions_enabled:接受资源提交", "site_name site_description public_api_enabled submissions_enabled"),
	moduleSpec("bot_menu", "欢迎语与菜单", "read:查看菜单|update:修改菜单", "welcome:欢迎语|welcome_icon:欢迎图标|hint:底部提示语|rows:按钮布局与动作", "welcome welcome_icon hint rows"),
	moduleSpec("bot_security", "搜索验证策略", "read:查看策略|update:修改策略", "private_mode:私聊验证方式|group_mode:群聊默认验证|allow_group_override:允许群内自主配置|trust_minutes:免验证时长|challenge_seconds:答题有效期|max_attempts:最多答错次数|cooldown_seconds:失败冷却时间|search_limit:每人每分钟搜索上限|group_limit:每群每分钟搜索上限", "private_mode group_mode allow_group_override trust_minutes challenge_seconds max_attempts cooldown_seconds search_limit group_limit"),
	moduleSpec("bot_groups", "群安全管理", "read:查看群设置|update:修改群设置", "bot_id:机器人 ID|chat_id:Telegram 群 ID|title:群名称|inviter_id:邀请人 ID|updated_by:最近操作的 Telegram 管理员 ID|status:机器人群身份|can_delete:删消息权限|can_restrict:限制成员权限|verify_mode:搜索验证方式|ad_mode:广告拦截方式|ad_keywords:广告关键词|allow_domains:允许的域名|flood_limit:10 秒消息上限|message_max_chars:消息字数上限|block_media:拦截媒体|block_forwards:拦截转发|join_verify_mode:新人验证方式|join_timeout_seconds:新人验证时限|join_max_attempts:新人最多答错次数|join_failure_action:新人验证失败处理|welcome_enabled:新人欢迎语开关|welcome_template:新人欢迎语模板|blocked_count:已删除消息数|last_error:群管错误|created_at:接入时间|updated_at:更新时间", "verify_mode ad_mode ad_keywords allow_domains flood_limit message_max_chars block_media block_forwards join_verify_mode join_timeout_seconds join_max_attempts join_failure_action welcome_enabled welcome_template"),
	moduleSpec("keys", "开放 API 密钥", "read:查看密钥|create:创建密钥|revoke:撤销密钥|update:修改密钥名称与额度|delete:删除已撤销密钥", "name:名称|prefix:密钥前缀|daily_limit:每日额度|status:状态|created_at:创建时间|last_used_at:最近使用|token:创建时查看完整密钥", "name daily_limit status"),
	moduleSpec("logs", "操作日志", "read:查看日志", "action:操作|detail:详情|actor_id:管理员 ID|created_at:时间", ""),
	moduleSpec("bot", "机器人连接", "read:查看连接与设置|update:修改机器人配置", "username:用户名|name:名称|running:运行状态|state:进程状态|configured:凭据状态|mode:连接方式|checked_at:最近验证|last_update_at:最近交互|heartbeat:心跳时间|last_error:错误信息|commands:指令|custom_emoji:图标状态|token:Bot Token（只回显配置状态）|page_size:搜索每页条数", "token page_size"),
	moduleSpec("wallets", "钱包余额", "read:查看", "user_id:用户 ID|balance:余额|updated_at:更新时间", ""),
	moduleSpec("wallet_entries", "资金流水", "read:查看", "user_id:用户 ID|amount:变动金额|balance_after:变动后余额|kind:类型|business_key:业务编号|note:说明|created_at:时间", ""),
	moduleSpec("recharges", "充值订单", "read:查看|review:线下充值审核|check:链上核查", "user_id:用户 ID|method:充值方式|amount:金额|receive_address:收款地址|txid:链上交易哈希|status:状态|proof:转账凭证|review_note:审核说明|reviewer_id:审核管理员|created_at:创建时间|expires_at:付款截止时间|paid_at:入账时间|decision:审核决定", "decision review_note txid"),
	moduleSpec("ad_slots", "广告位", "read:查看|create:创建|update:编辑|delete:删除无订单广告位", "name:名称|kind:类型|description:说明|price:价格|duration_days:天数|capacity:容量|target_chat:目标聊天|enabled:开放购买|created_at:创建时间|updated_at:更新时间", "name kind description price duration_days capacity target_chat enabled"),
	moduleSpec("ad_orders", "广告订单", "read:查看|review:审核与退款|resolve:确认异常投放", "user_id:用户 ID|slot_id:广告位 ID|slot_name:广告位名称|kind:类型|price:实付金额|duration_days:天数|target_chat:目标聊天|title:标题|url:链接|content:内容|keyword:关键词|status:状态|review_note:审核说明|reviewer_id:审核管理员|created_at:创建时间|starts_at:开始时间|ends_at:结束时间|decision:审核决定|message_id:投放消息 ID|delivery_state:投放状态|delivery_error:投放错误", "decision review_note message_id"),
	moduleSpec("commerce_settings", "支付与奖励设置", "read:查看|update:修改", "trc20_enabled:启用 USDT-TRC20|receive_address:TRC20 收款地址|api_key:Tronscan 密钥（只写不回显）|key_configured:密钥配置状态|offline_enabled:启用线下转账|offline_instructions:线下收款说明|reward_enabled:启用搜索奖励|reward_amount:每日首搜奖励|reward_daily_cap:全站每日奖励上限|reward_user_cap:单用户累计奖励上限|public_url:网站根地址", "trc20_enabled receive_address api_key offline_enabled offline_instructions reward_enabled reward_amount reward_daily_cap reward_user_cap public_url"),
}

// The API keeps the existing Vue permission editor's keyed catalog contract.
func permissionCatalogJSON() Row {
	out := Row{}
	for _, m := range PermissionCatalog {
		actions, fields := Row{}, Row{}
		writable := []string{}
		for _, v := range m.Actions {
			actions[strings.TrimPrefix(v.Key, m.Key+".")] = v.Label
		}
		for _, v := range m.Fields {
			fields[v.Key] = v.Label
			if !v.ReadOnly {
				writable = append(writable, v.Key)
			}
		}
		out[m.Key] = Row{"label": m.Label, "actions": actions, "fields": fields, "writable": writable}
	}
	return out
}

func catalogReadable(access *Access, module, field string) bool {
	if access == nil {
		return false
	}
	for _, v := range access.Fields[module].Read {
		if v == field {
			return true
		}
	}
	return false
}

func catalogWritable(access *Access, module, field string) bool {
	if access == nil {
		return false
	}
	for _, v := range access.Fields[module].Write {
		if v == field {
			return true
		}
	}
	return false
}
