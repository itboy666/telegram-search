export const verificationModes: Record<string, string> = {
  off: "不验证",
  emoji: "选对 emoji",
  button: "点击确认 · 轻量",
  text: "图片字母数字",
  math: "四则运算",
  sequence: "按顺序点击 emoji",
  random: "随机组合",
};
export const adModes: Record<string, string> = {
  off: "关闭",
  links: "拦截链接",
  keywords: "拦截关键词",
  strict: "链接及关键词",
};
export const groupIdentity: Record<string, string> = {
  member: "普通成员",
  administrator: "管理员",
  creator: "群主",
  restricted: "受限制",
  left: "已离群",
  kicked: "已被移除",
};
import type { WelcomeTag } from "./welcomeTemplate";

export const groupWelcomeDefault = "欢迎 {{mention}} 加入 {{chat_title}}！";
export const groupWelcomeTags: WelcomeTag[] = [
  ["user_id", "用户 ID", "用户信息"],
  ["nickname", "昵称", "用户信息"],
  ["username", "用户名", "用户信息"],
  ["first_name", "名字", "用户信息"],
  ["last_name", "姓氏", "用户信息"],
  ["mention", "提及用户", "用户信息"],
  ["chat_id", "群 ID", "群与机器人"],
  ["chat_title", "群名称", "群与机器人"],
  ["chat_username", "群用户名", "群与机器人"],
  ["bot_username", "机器人用户名", "群与机器人"],
  ["datetime", "日期时间", "时间"],
  ["date", "日期", "时间"],
  ["time", "时间", "时间"],
  ["timezone", "时区", "时间"],
].map(([key, label, group]) => ({
  key,
  label,
  group,
  token: `{{${key}}}`,
  description: key === "mention" ? "可点击的 Telegram 用户提及" : label,
}));

export function groupWelcomeIssue(text: string): string {
  if (Array.from(text).length > 2000) return "欢迎语最多 2000 字。";
  const known = new Set(groupWelcomeTags.map((tag) => tag.key));
  for (const match of text.matchAll(/\{\{([a-z_]+)\}\}/g)) {
    if (!known.has(match[1])) return `不支持的欢迎语标签：${match[1]}`;
  }
  return /[{}]/.test(text.replace(/\{\{([a-z_]+)\}\}/g, ""))
    ? "请使用 {{nickname}} 形式的完整标签，标签中不要添加空格。"
    : "";
}
