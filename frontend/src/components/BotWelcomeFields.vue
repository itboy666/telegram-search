<script setup lang="ts">
import { computed, nextTick, reactive, ref } from "vue";
import { ElMessage } from "element-plus";
import type { InputInstance } from "element-plus";
import { templateIssue } from "../lib/welcomeTemplate";
import type {
  WelcomeTag,
  WelcomeTemplateMetadata,
} from "../lib/welcomeTemplate";
import IosIcon from "./IosIcon.vue";

type Field = "welcome" | "hint";
const props = withDefaults(
  defineProps<{
    welcome: string;
    hint: string;
    metadata: WelcomeTemplateMetadata;
    disabled: boolean;
    showWelcome?: boolean;
    showHint?: boolean;
    welcomeReadonly?: boolean;
    hintReadonly?: boolean;
  }>(),
  {
    showWelcome: true,
    showHint: true,
    welcomeReadonly: false,
    hintReadonly: false,
  },
);
const emit = defineEmits<{
  "update:welcome": [value: string];
  "update:hint": [value: string];
}>();
const welcomeInput = ref<InputInstance>();
const hintInput = ref<InputInstance>();
const target = ref<Field>(
  props.showWelcome && !props.welcomeReadonly ? "welcome" : "hint",
);
const positions = reactive<
  Record<Field, { start: number; end: number } | null>
>({
  welcome: null,
  hint: null,
});
const touched = reactive({ welcome: false, hint: false });
const announcement = ref("");
const targetLabel = computed(() =>
  target.value === "welcome" ? "欢迎语" : "底部提示语",
);
const groups = computed(() => [
  ...new Set(props.metadata.tags.map((tag) => tag.group)),
]);
const issues = computed(() => ({
  welcome: touched.welcome
    ? templateIssue(props.welcome, props.metadata.tags)
    : "",
  hint: touched.hint ? templateIssue(props.hint, props.metadata.tags) : "",
}));

function textarea(field: Field) {
  return (field === "welcome" ? welcomeInput.value : hintInput.value)?.textarea;
}
function remember(field: Field, validate = false) {
  const input = textarea(field);
  if (input)
    positions[field] = { start: input.selectionStart, end: input.selectionEnd };
  target.value = field;
  if (validate) touched[field] = true;
}
async function insert(tag: WelcomeTag) {
  if (
    props.disabled ||
    (target.value === "welcome" ? props.welcomeReadonly : props.hintReadonly)
  )
    return;
  const field = target.value;
  const value = props[field];
  const range = positions[field] ?? { start: value.length, end: value.length };
  const start = Math.min(range.start, value.length);
  const end = Math.min(range.end, value.length);
  const next = value.slice(0, start) + tag.token + value.slice(end);
  if (next.length > props.metadata.limits[field]) {
    ElMessage.warning(
      `${targetLabel.value}已达字数上限，请先删减文字再插入标签。`,
    );
    return;
  }
  if (field === "welcome") emit("update:welcome", next);
  else emit("update:hint", next);
  await nextTick();
  const input = textarea(field);
  const cursor = start + tag.token.length;
  input?.focus({ preventScroll: true });
  input?.setSelectionRange(cursor, cursor);
  input?.scrollIntoView({ block: "nearest", behavior: "instant" });
  positions[field] = { start: cursor, end: cursor };
  announcement.value = `已将${tag.label}标签插入${targetLabel.value}。`;
}
</script>

<template>
  <el-form-item
    v-if="showWelcome"
    label="欢迎语"
    for="bot-welcome-template"
    required
    :error="issues.welcome"
  >
    <el-input
      id="bot-welcome-template"
      ref="welcomeInput"
      :model-value="welcome"
      type="textarea"
      :rows="4"
      :maxlength="metadata.limits.welcome"
      :disabled="disabled || welcomeReadonly"
      show-word-limit
      aria-describedby="bot-template-help"
      @update:model-value="emit('update:welcome', $event)"
      @focus="remember('welcome')"
      @click="remember('welcome')"
      @keyup="remember('welcome')"
      @select="remember('welcome')"
      @blur="remember('welcome', true)"
    />
  </el-form-item>
  <section
    v-if="(showWelcome && !welcomeReadonly) || (showHint && !hintReadonly)"
    class="welcome-template-tools"
    aria-label="动态标签"
  >
    <div class="template-tools-heading">
      <div>
        <strong>插入动态标签</strong>
        <p>插入到{{ targetLabel }}的光标位置</p>
      </div>
      <el-radio-group
        v-model="target"
        :disabled="disabled"
        aria-label="标签插入位置"
      >
        <el-radio-button
          value="welcome"
          :disabled="!showWelcome || welcomeReadonly"
          >欢迎语</el-radio-button
        >
        <el-radio-button value="hint" :disabled="!showHint || hintReadonly"
          >底部提示语</el-radio-button
        >
      </el-radio-group>
    </div>
    <div
      v-for="(group, index) in groups"
      :key="group"
      class="template-tag-group"
    >
      <div class="template-group-label">
        <IosIcon :name="index === 0 ? 'user' : 'clock'" />{{ group }}
      </div>
      <div class="template-tag-buttons">
        <el-button
          v-for="tag in metadata.tags.filter((tag) => tag.group === group)"
          :key="tag.key"
          :disabled="disabled"
          :title="`${tag.token} · ${tag.description}`"
          :aria-label="`插入${tag.label}标签`"
          @click="insert(tag)"
          >{{ tag.label }}</el-button
        >
      </div>
    </div>
    <p id="bot-template-help" class="template-help">
      点击标签或直接输入 <code v-text="'{{nickname}}'" />。用户名包含
      @；未设置时显示「未设置」。 时间使用
      {{ metadata.timezone }}，在发送时生成。
    </p>
    <span class="sr-only" role="status" aria-live="polite">{{
      announcement
    }}</span>
  </section>
  <el-form-item
    v-if="showHint"
    label="底部提示语"
    for="bot-hint-template"
    :error="issues.hint"
  >
    <el-input
      id="bot-hint-template"
      ref="hintInput"
      :model-value="hint"
      type="textarea"
      :rows="2"
      :maxlength="metadata.limits.hint"
      :disabled="disabled || hintReadonly"
      show-word-limit
      aria-describedby="bot-template-help"
      @update:model-value="emit('update:hint', $event)"
      @focus="remember('hint')"
      @click="remember('hint')"
      @keyup="remember('hint')"
      @select="remember('hint')"
      @blur="remember('hint', true)"
    />
  </el-form-item>
  <p class="template-limit-note">
    标签替换后仍按上述字数上限显示；超出部分以省略号收尾。
  </p>
</template>
