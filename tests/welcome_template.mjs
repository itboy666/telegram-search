// Node 22.18+ / 24: native TypeScript stripping; no browser or npm test runtime.
import assert from "node:assert/strict";
import fs from "node:fs";
import {
  previewTemplateValues,
  renderTemplateText,
  templateIssue,
} from "../frontend/src/lib/welcomeTemplate.ts";

const fixtures = JSON.parse(
  fs.readFileSync(
    new URL("./fixtures/welcome-template.json", import.meta.url),
    "utf8",
  ),
);
for (const fixture of fixtures) {
  assert.equal(
    renderTemplateText(fixture.text, fixture.values, fixture.limit).text,
    fixture.expected,
    fixture.name,
  );
}
const metadata = {
  timezone: "Asia/Shanghai",
  sample_values: { nickname: "小林", user_id: "8974065497" },
};
const values = previewTemplateValues(
  metadata,
  new Date("2026-11-15T16:05:06+00:00"),
);
assert.equal(values.datetime, "2026-11-16 00:05:06");
assert.equal(values.weekday, "星期一");
assert.equal(values.nickname, "小林");
assert.equal(values.user_id, "8974065497");
const tags = [{ key: "nickname" }];
assert.equal(templateIssue("你好 {{ nickname }}", tags), "");
assert.match(templateIssue("{{nicknme}}", tags), /未知标签/);
assert.match(templateIssue("{{nickname", tags), /格式不完整/);
assert.match(templateIssue("{{{nickname}}}", tags), /格式不完整/);
assert.equal(renderTemplateText("{{toString}}", {}, 100).text, "{{toString}}");
assert.equal(
  renderTemplateText("{{nickname}}", { nickname: "😀😀😀😀" }, 3).truncated,
  true,
);
console.log(
  "PASS shared rendering fixtures, timezone preview, invalid tags and expansion boundaries",
);
