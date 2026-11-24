export function newPasswordError(password: string): string {
  const bytes = new TextEncoder().encode(password).length;
  return bytes < 10 || bytes > 72
    ? "密码长度须为 10–72 字节，英文或数字每位 1 字节，中文通常每字 3 字节。"
    : "";
}
