export const adKinds: Record<string, string> = {
  keyword: "关键词排名",
  brand: "品牌展示",
  search_top: "搜索顶部轮播",
  search_bottom: "搜索底部按钮",
  group_pin: "群组置顶",
  channel: "频道广告",
};
export const commerceStatuses: Record<string, string> = {
  pending: "待处理",
  review: "待核账",
  paid: "已入账",
  rejected: "已拒绝",
  expired: "付款时段已结束",
  dispatching: "等待投放",
  active: "投放中",
  delivery_issue: "投放待核实",
  completed: "已到期",
  refunded: "已退款",
};
export const entryKinds: Record<string, string> = {
  recharge: "充值入账",
  search_reward: "搜索奖励",
  ad_purchase: "广告购买",
  ad_refund: "广告退款",
};
export const commerceLabels: Record<string, string> = {
  id: "ID",
  user_id: "用户 ID",
  balance: "余额 / USDT",
  amount: "金额 / USDT",
  balance_after: "变动后余额",
  kind: "类型",
  note: "说明",
  business_key: "业务编号",
  method: "方式",
  receive_address: "收款地址",
  txid: "链上交易哈希",
  status: "状态",
  proof: "转账凭证",
  review_note: "审核说明",
  reviewer_id: "审核人 ID",
  created_at: "创建时间",
  updated_at: "更新时间",
  expires_at: "付款截止",
  paid_at: "入账时间",
  name: "广告位名称",
  description: "说明",
  price: "价格 / USDT",
  duration_days: "天数",
  capacity: "容量",
  target_chat: "目标群 / 频道",
  enabled: "开放购买",
  slot_id: "广告位 ID",
  slot_name: "广告位",
  title: "广告标题",
  url: "推广链接",
  content: "广告正文",
  keyword: "关键词",
  starts_at: "开始时间",
  ends_at: "结束时间",
  message_id: "消息 ID",
  delivery_state: "投放状态",
  delivery_error: "投放说明",
};
export function commerceValue(key: string, value: any): string {
  if (value === null || value === undefined || value === "") return "—";
  if (key === "kind")
    return adKinds[value] || entryKinds[value] || String(value);
  if (key === "status") return commerceStatuses[value] || String(value);
  if (key === "method") return value === "trc20" ? "USDT-TRC20" : "线下转账";
  if (key === "enabled") return Number(value) ? "已开放" : "已停用";
  return String(value);
}
export function requestKey() {
  return crypto.randomUUID().replaceAll("-", "");
}
