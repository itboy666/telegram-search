<script setup lang="ts">
import { computed, nextTick, ref } from "vue";
import { ElMessage } from "element-plus";
import type { InputInstance } from "element-plus";
import {
  groupWelcomeDefault,
  groupWelcomeTags,
  groupWelcomeIssue,
} from "../lib/botSecurity";
import {
  previewTemplateValues,
  renderTemplateText,
} from "../lib/welcomeTemplate";
import IosIcon from "./IosIcon.vue";

const props = defineProps<{
  modelValue: string;
  disabled: boolean;
  error?: string;
  chatTitle?: string;
}>();
const emit = defineEmits<{ "update:modelValue": [value: string] }>();
const input = ref<InputInstance>();
const touched = ref(false);
const announcement = ref("");
let position: { start: number; end: number } | null = null;
const count = computed(() => Array.from(props.modelValue).length);
const issue = computed(
  () =>
    props.error ||
    (touched.value ? groupWelcomeIssue(props.modelValue) : "") ||
    (count.value > 2000 ? "欢迎语最多 2000 字，请缩短后保存。" : ""),
);
const groups = ["用户信息", "群与机器人", "时间"];
const preview = computed(() => {
  const values = previewTemplateValues(
    {
      tags: groupWelcomeTags,
      limits: { welcome: 2000, hint: 0 },
      timezone: "Asia/Shanghai",
      server_time: "",
      sample_values: {
        user_id: "123456789",
        nickname: "小林",
        username: "lin_demo",
        first_name: "小林",
        last_name: "",
        mention: "小林",
        chat_id: "-1001234567890",
        chat_title: props.chatTitle || "星索交流社区",
        chat_username: "starindex_demo",
        bot_username: "starindex_demo_bot",
        timezone: "Asia/Shanghai",
      },
    },
    new Date(),
  );
  return renderTemplateText(
    props.modelValue || groupWelcomeDefault,
    values,
    2000,
  ).text;
});
function remember() {
  const el = input.value?.textarea;
  if (el) position = { start: el.selectionStart, end: el.selectionEnd };
}
async function insert(token: string, label: string) {
  if (props.disabled) return;
  const value = props.modelValue;
  const start = Math.min(position?.start ?? value.length, value.length);
  const end = Math.min(position?.end ?? value.length, value.length);
  const next = value.slice(0, start) + token + value.slice(end);
  if (Array.from(next).length > 2000) {
    ElMessage.warning("字数已达上限，请先删减文字。");
    return;
  }
  emit("update:modelValue", next);
  await nextTick();
  input.value?.textarea?.focus({ preventScroll: true });
  input.value?.textarea?.setSelectionRange(
    start + token.length,
    start + token.length,
  );
  position = { start: start + token.length, end: start + token.length };
  announcement.value = `已插入${label}标签。`;
}
</script>

<template>
  <div class="group-welcome-editor">
    <el-form-item
      label="入群欢迎语"
      for="group-welcome-template"
      :error="issue"
    >
      <el-input
        id="group-welcome-template"
        ref="input"
        :model-value="modelValue"
        type="textarea"
        :rows="5"
        :disabled="disabled"
        :placeholder="groupWelcomeDefault"
        aria-describedby="group-welcome-help"
        @update:model-value="emit('update:modelValue', $event)"
        @focus="remember"
        @click="remember"
        @keyup="remember"
        @select="remember"
        @blur="
          remember();
          touched = true;
        "
      />
      <div class="welcome-counter">
        <span>留空使用默认欢迎语</span
        ><span :class="{ invalid: count > 2000 }">{{ count }} / 2000 字</span>
      </div>
    </el-form-item>
    <div v-if="!disabled" class="group-template-tags">
      <strong>插入动态标签</strong>
      <p>点击标签，插入到文字光标处。昵称和群名称会自动安全处理。</p>
      <div v-for="group in groups" :key="group" class="group-tag-section">
        <span>{{ group }}</span>
        <div>
          <el-button
            v-for="tag in groupWelcomeTags.filter(
              (item) => item.group === group,
            )"
            :key="tag.key"
            :title="`${tag.token} · ${tag.description}`"
            :aria-label="`插入${tag.label}标签`"
            @click="insert(tag.token, tag.label)"
            >{{ tag.label }}</el-button
          >
        </div>
      </div>
    </div>
    <div class="group-welcome-preview">
      <div class="preview-heading">
        <IosIcon name="telegram" /><strong>欢迎语预览</strong
        ><span>示例用户</span>
      </div>
      <p>{{ preview }}</p>
      <small id="group-welcome-help"
        >这是文字示例，最多预览 2000 字。「提及用户」在 Telegram
        中显示为可点击链接。实际发送以入群时的信息为准。</small
      >
    </div>
    <span class="sr-only" role="status" aria-live="polite">{{
      announcement
    }}</span>
  </div>
</template>

<style scoped>
.group-welcome-editor {
  min-width: 0;
}
.welcome-counter {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  width: 100%;
  font-size: 12px;
  color: var(--muted);
  margin-top: 8px;
}
.welcome-counter .invalid {
  color: var(--el-color-danger);
}
.group-template-tags {
  margin: 0 0 24px;
}
.group-template-tags > p {
  color: var(--muted);
  font-size: 13px;
  line-height: 1.7;
  margin: 8px 0 16px;
}
.group-tag-section {
  display: grid;
  grid-template-columns: 88px minmax(0, 1fr);
  gap: 12px;
  align-items: start;
  margin-top: 12px;
}
.group-tag-section > span {
  padding-top: 12px;
  color: var(--muted);
  font-size: 13px;
}
.group-tag-section > div {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.group-tag-section .el-button {
  margin: 0;
  min-height: 44px;
  padding: 0 12px;
}
.group-welcome-preview {
  border: 1px solid var(--line);
  border-radius: 12px;
  background: var(--primary-soft);
  padding: 20px;
}
.preview-heading {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  color: var(--deep);
}
.preview-heading > span:last-child {
  margin-left: auto;
  font-size: 12px;
  color: var(--muted);
}
.group-welcome-preview p {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  line-height: 1.8;
  margin: 16px 0;
  padding: 16px;
  border-radius: 10px;
  background: var(--surface-raised);
}
.group-welcome-preview small {
  display: block;
  color: var(--muted);
  line-height: 1.7;
}
@media (max-width: 600px) {
  .group-tag-section {
    grid-template-columns: 1fr;
    gap: 8px;
  }
  .group-tag-section > span {
    padding: 0;
  }
  .group-welcome-preview {
    padding: 16px;
  }
}
</style>
