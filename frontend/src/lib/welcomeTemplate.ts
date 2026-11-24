export interface WelcomeTag {
  key: string;
  token: string;
  label: string;
  group: string;
  description: string;
}

export interface WelcomeTemplateMetadata {
  tags: WelcomeTag[];
  limits: { welcome: number; hint: number };
  timezone: string;
  server_time: string;
  sample_values: Record<string, string>;
}

export function templateIssue(text: string, tags: WelcomeTag[]): string {
  const known = new Set(tags.map((tag) => tag.key));
  for (const match of text.matchAll(/(?<!\{)\{\{([^{}]*)\}\}(?!\})/gu)) {
    if (!known.has(match[1].trim())) {
      return `未知标签 {{${match[1].trim()}}}，请使用下方提供的标签。`;
    }
  }
  const rest = text.replace(/(?<!\{)\{\{([^{}]*)\}\}(?!\})/gu, "");
  return rest.includes("{{") || rest.includes("}}")
    ? "标签格式不完整，请使用 {{标签名}}。"
    : "";
}

export function renderTemplateText(
  text: string,
  values: Record<string, string>,
  limit: number,
) {
  const rendered = text.replace(
    /(?<!\{)\{\{([^{}]*)\}\}(?!\})/gu,
    (token, key: string) =>
      Object.hasOwn(values, key.trim()) ? values[key.trim()] : token,
  );
  const characters = Array.from(rendered);
  return {
    text:
      characters.length > limit
        ? characters.slice(0, limit - 1).join("") + "…"
        : rendered,
    truncated: characters.length > limit,
  };
}

export function previewTemplateValues(
  metadata: WelcomeTemplateMetadata,
  now: Date,
): Record<string, string> {
  const parts = Object.fromEntries(
    new Intl.DateTimeFormat("en-GB", {
      timeZone: metadata.timezone,
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hourCycle: "h23",
    })
      .formatToParts(now)
      .map((part) => [part.type, part.value]),
  );
  const date = `${parts.year}-${parts.month}-${parts.day}`;
  const time = `${parts.hour}:${parts.minute}:${parts.second}`;
  return {
    ...metadata.sample_values,
    date,
    time,
    datetime: `${date} ${time}`,
    weekday: new Intl.DateTimeFormat("zh-CN", {
      timeZone: metadata.timezone,
      weekday: "long",
    }).format(now),
  };
}
