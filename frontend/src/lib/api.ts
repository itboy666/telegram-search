export const csrf: Record<string, string> = {};
export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
    public fields: Record<string, string> = {},
  ) {
    super(message);
  }
}
export async function api<T = any>(
  url: string,
  body?: unknown,
  method?: string,
  headers: Record<string, string> = {},
): Promise<T> {
  const scope = url.startsWith("/admin/") ? "admin" : "user";
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 20000);
  try {
    const res = await fetch(url, {
      method: method ?? (body === undefined ? "GET" : "POST"),
      credentials: "same-origin",
      headers: {
        Accept: "application/json",
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
        ...(csrf[scope] ? { "X-CSRF-Token": csrf[scope] } : {}),
        ...headers,
      },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: controller.signal,
    });
    const json = await res
      .json()
      .catch(() => ({ message: "服务暂时无法响应，请稍后重试。" }));
    if (!res.ok || json.code !== 0)
      throw new ApiError(
        json.message || "请求失败，请重试。",
        res.status,
        json.errors || {},
      );
    return json.data as T;
  } catch (e) {
    if (e instanceof ApiError) throw e;
    throw new ApiError(
      e instanceof DOMException && e.name === "AbortError"
        ? "请求超时，请重试。"
        : "连接失败，请检查服务是否启动。",
      0,
    );
  } finally {
    clearTimeout(timer);
  }
}
export function query(values: Record<string, unknown>): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(values))
    if (v !== "" && v !== undefined && v !== null) p.set(k, String(v));
  return p.toString();
}
export const types: Record<string, string> = {
  channel: "频道",
  group: "群组",
  bot: "机器人",
  video: "视频",
  article: "图文",
  audio: "音乐",
  file: "文件",
  shop: "店铺",
  other: "其他",
  website: "网站",
};
export const statuses: Record<string, string> = {
  published: "已发布",
  hidden: "已下架",
  archived: "已归档",
  blocked: "已封禁",
  invalid: "已失效",
  pending: "待审核",
  approved: "已通过",
  rejected: "未通过",
};
export function dateTime(value?: string) {
  return value ? value.replace(/:\d{2}$/, "") : "—";
}
