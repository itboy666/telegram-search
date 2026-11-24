<script setup lang="ts">
import {
  computed,
  nextTick,
  onMounted,
  onBeforeUnmount,
  reactive,
  ref,
} from "vue";
import { onBeforeRouteLeave } from "vue-router";
import { ElMessage, ElMessageBox } from "element-plus";
import { api } from "../lib/api";
import { Plus, Check, Refresh, Close } from "../lib/icons";
import IosIcon from "../components/IosIcon.vue";
import TelegramIconPicker from "../components/TelegramIconPicker.vue";
import BotWelcomeFields from "../components/BotWelcomeFields.vue";
import type { IconCatalog } from "../lib/iconCatalog";
import {
  previewTemplateValues,
  renderTemplateText,
} from "../lib/welcomeTemplate";
import type { WelcomeTemplateMetadata } from "../lib/welcomeTemplate";
import LoadError from "../components/LoadError.vue";
import { useAuth } from "../lib/auth";
const auth = useAuth();
const canRead = (field: string) => auth.readField("bot_menu", field);
const canWrite = (field: string) =>
  auth.can("bot_menu.update") && auth.writeField("bot_menu", field);

interface ButtonConfig {
  id: string;
  text: string;
  icon: string;
  action: string;
  value: string;
  style: "default" | "primary" | "success" | "danger";
  enabled: boolean;
}
interface MenuConfig {
  welcome: string;
  welcome_icon: string;
  hint: string;
  rows: ButtonConfig[][];
}
interface Payload {
  config: MenuConfig;
  icons: IconCatalog;
  actions: Record<string, string>;
  template: WelcomeTemplateMetadata;
  delivery: Record<
    string,
    { status: string; requested: number; retained: number; checked_at: string }
  >;
}
const form = reactive<MenuConfig>({
  welcome: "",
  welcome_icon: "",
  hint: "",
  rows: [],
});
const icons = ref<Payload["icons"]>({}),
  actions = ref<Payload["actions"]>({}),
  delivery = ref<Payload["delivery"]>({});
const template = ref<WelcomeTemplateMetadata>();
const previewNow = ref(Date.now());
let previewTimer: ReturnType<typeof setInterval> | undefined;
let serverClockOffset = 0;
function syncTemplate(metadata: WelcomeTemplateMetadata) {
  template.value = metadata;
  serverClockOffset = Date.parse(metadata.server_time) - Date.now();
  previewNow.value = Date.now() + serverClockOffset;
}
const previewValues = computed(() =>
  template.value
    ? previewTemplateValues(template.value, new Date(previewNow.value))
    : {},
);
const previewWelcome = computed(() =>
  renderTemplateText(
    form.welcome,
    previewValues.value,
    template.value?.limits.welcome ?? 1800,
  ),
);
const previewHint = computed(() =>
  renderTemplateText(
    form.hint,
    previewValues.value,
    template.value?.limits.hint ?? 240,
  ),
);
const iconsNotRetained = computed(() =>
  Object.values(delivery.value).some(
    (check) => check.status === "not_retained",
  ),
);
const selectedId = ref(""),
  baseline = ref(""),
  loading = ref(true),
  saving = ref(false);
const error = ref(""),
  formError = ref(""),
  errorSummary = ref<HTMLElement | null>(null);
const buttonEditor = ref<HTMLElement | null>(null);
async function selectPreview(id: string) {
  selectedId.value = id;
  await nextTick();
  buttonEditor.value?.focus({ preventScroll: true });
  buttonEditor.value?.scrollIntoView({
    block: "nearest",
    behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches
      ? "instant"
      : "smooth",
  });
}
const dirty = computed(
  () => !!baseline.value && JSON.stringify(form) !== baseline.value,
);
const selected = computed(() =>
  form.rows.flat().find((b) => b.id === selectedId.value),
);
const selectedRow = computed(() =>
  form.rows.findIndex((row) => row.some((b) => b.id === selectedId.value)),
);
const total = computed(() => form.rows.flat().length);
const previewRows = computed(() =>
  form.rows
    .map((row) => row.filter((b) => b.enabled))
    .filter((row) => row.length),
);
async function load() {
  loading.value = true;
  error.value = "";
  try {
    const result: Payload = await api("/admin/api/bot-menu");
    Object.assign(form, result.config);
    icons.value = result.icons;
    actions.value = result.actions;
    delivery.value = result.delivery || {};
    syncTemplate(result.template);
    baseline.value = JSON.stringify(form);
    selectedId.value = form.rows[0]?.[0]?.id || "";
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    loading.value = false;
  }
}
onMounted(() => {
  load();
  previewTimer = setInterval(() => {
    previewNow.value = Date.now() + serverClockOffset;
  }, 1000);
});
onBeforeUnmount(() => clearInterval(previewTimer));
async function save() {
  saving.value = true;
  formError.value = "";
  try {
    const result: Payload = await api(
      "/admin/api/bot-menu",
      auth.writable("bot_menu", form),
    );
    Object.assign(form, result.config);
    delivery.value = result.delivery || {};
    syncTemplate(result.template);
    baseline.value = JSON.stringify(form);
    ElMessage.success("菜单已保存，重新发送 /start 即可看到新配置");
  } catch (e) {
    formError.value = (e as Error).message;
    await nextTick();
    errorSummary.value?.focus();
  } finally {
    saving.value = false;
  }
}
function newButton(): ButtonConfig {
  return {
    id: "b_" + crypto.randomUUID().replaceAll("-", "").slice(0, 12),
    text: "新按钮",
    icon: "discover",
    action: "discover",
    value: "",
    style: "default",
    enabled: true,
  };
}
function addRow() {
  if (form.rows.length >= 10 || total.value >= 24) return;
  const button = newButton();
  form.rows.push([button]);
  selectedId.value = button.id;
}
function addButton(row: ButtonConfig[]) {
  if (row.length >= 5 || total.value >= 24) return;
  const button = newButton();
  row.push(button);
  selectedId.value = button.id;
}
function moveRow(index: number, delta: number) {
  const to = index + delta;
  if (to < 0 || to >= form.rows.length) return;
  const row = form.rows.splice(index, 1)[0]!;
  form.rows.splice(to, 0, row);
}
function moveButton(delta: number) {
  const row = form.rows[selectedRow.value];
  if (!row) return;
  const index = row.findIndex((b) => b.id === selectedId.value),
    to = index + delta;
  if (to < 0 || to >= row.length) return;
  row.splice(to, 0, row.splice(index, 1)[0]!);
}
async function removeRow(index: number) {
  try {
    await ElMessageBox.confirm(
      "此行按钮将从草稿中移除，保存后才会生效。",
      "删除这一行",
      { confirmButtonText: "删除", cancelButtonText: "取消", type: "warning" },
    );
    form.rows.splice(index, 1);
    selectedId.value = form.rows[0]?.[0]?.id || "";
  } catch {
    /* Cancel keeps the draft. */
  }
}
function removeButton() {
  const rowIndex = selectedRow.value;
  const row = form.rows[rowIndex];
  if (!row) return;
  row.splice(
    row.findIndex((b) => b.id === selectedId.value),
    1,
  );
  if (!row.length) form.rows.splice(rowIndex, 1);
  selectedId.value = form.rows[0]?.[0]?.id || "";
}
function undo() {
  Object.assign(form, JSON.parse(baseline.value));
  selectedId.value = form.rows[0]?.[0]?.id || "";
  formError.value = "";
}
function beforeUnload(event: BeforeUnloadEvent) {
  if (dirty.value) {
    event.preventDefault();
    event.returnValue = "";
  }
}
window.addEventListener("beforeunload", beforeUnload);
onBeforeUnmount(() => window.removeEventListener("beforeunload", beforeUnload));
onBeforeRouteLeave(async () => {
  if (!dirty.value) return true;
  try {
    await ElMessageBox.confirm(
      "当前菜单有未保存的修改，离开后这些修改将丢失。",
      "离开编辑页面？",
      {
        confirmButtonText: "放弃并离开",
        cancelButtonText: "继续编辑",
        confirmButtonType: "danger",
        closeIcon: Close,
        closeOnClickModal: false,
        autofocus: false,
      },
    );
    return true;
  } catch {
    return false;
  }
});
</script>
<template>
  <div>
    <div class="page-heading">
      <div>
        <span class="eyebrow">BOT EXPERIENCE</span>
        <h1>欢迎语与菜单</h1>
        <p>让第一次见面，就有清晰的下一步。</p>
      </div>
      <div class="heading-actions">
        <el-button :icon="Refresh" :disabled="!dirty || saving" @click="undo"
          >撤销修改</el-button
        >
        <el-button
          v-if="auth.can('bot_menu.update')"
          type="primary"
          :icon="Check"
          :loading="saving"
          :disabled="loading || !!error"
          @click="save"
          >保存菜单</el-button
        >
      </div>
    </div>
    <LoadError :message="error" @retry="load" />
    <el-skeleton v-if="loading" :rows="10" animated />
    <div v-else-if="baseline" class="bot-editor-grid">
      <div class="bot-editor-fields">
        <div
          v-if="formError"
          ref="errorSummary"
          role="alert"
          tabindex="-1"
          class="form-alert"
        >
          {{ formError }}
        </div>
        <section
          v-if="
            canRead('welcome') || canRead('hint') || canRead('welcome_icon')
          "
          class="panel bot-editor-panel"
        >
          <div class="panel-heading">
            <div>
              <h2>/start 欢迎消息</h2>
              <p>支持换行与动态标签；发送时自动替换用户信息。</p>
            </div>
            <el-tag v-if="dirty" type="warning">未保存</el-tag>
          </div>
          <el-form label-position="top" :disabled="saving" @submit.prevent>
            <el-form-item v-if="canRead('welcome_icon')" label="欢迎语图标">
              <TelegramIconPicker
                :disabled="!canWrite('welcome_icon')"
                v-model="form.welcome_icon"
                :icons="icons"
                label="欢迎语图标"
              />
            </el-form-item>
            <BotWelcomeFields
              v-if="template"
              v-model:welcome="form.welcome"
              v-model:hint="form.hint"
              :metadata="template"
              :disabled="saving"
              :show-welcome="canRead('welcome')"
              :show-hint="canRead('hint')"
              :welcome-readonly="!canWrite('welcome')"
              :hint-readonly="!canWrite('hint')"
            />
          </el-form>
        </section>
        <fieldset
          v-if="canRead('rows')"
          :disabled="!canWrite('rows')"
          class="panel bot-editor-panel permission-fieldset"
        >
          <div class="panel-heading">
            <div>
              <h2>
                按钮布局 <span class="muted">{{ total }} / 24</span>
              </h2>
              <p>每行最多 5 个按钮，点击按钮编辑内容。</p>
            </div>
          </div>
          <div v-if="!form.rows.length" class="menu-empty">
            还没有按钮，添加一行开始配置。
          </div>
          <div v-for="(row, r) in form.rows" :key="r" class="menu-layout-row">
            <div class="menu-row-heading">
              <span>第 {{ r + 1 }} 行</span>
              <div class="menu-row-tools">
                <el-button
                  text
                  aria-label="上移一行"
                  :disabled="r === 0 || saving"
                  @click="moveRow(r, -1)"
                  ><IosIcon name="up"
                /></el-button>
                <el-button
                  text
                  aria-label="下移一行"
                  :disabled="r === form.rows.length - 1 || saving"
                  @click="moveRow(r, 1)"
                  ><IosIcon name="up" class="rotate-icon"
                /></el-button>
                <el-button
                  text
                  type="danger"
                  :aria-label="`删除第 ${r + 1} 行`"
                  :disabled="saving"
                  @click="removeRow(r)"
                  ><IosIcon name="close"
                /></el-button>
              </div>
            </div>
            <div class="menu-row-buttons">
              <button
                v-for="button in row"
                :key="button.id"
                class="menu-edit-button"
                :class="{
                  selected: selectedId === button.id,
                  muted: !button.enabled,
                }"
                :aria-label="`编辑按钮：${button.text}`"
                :aria-pressed="selectedId === button.id"
                :disabled="saving"
                @click="selectedId = button.id"
              >
                <IosIcon :name="button.icon" /><span>{{ button.text }}</span
                ><small v-if="!button.enabled">已停用</small>
              </button>
              <el-button
                v-if="row.length < 5"
                text
                :disabled="total >= 24 || saving"
                :aria-label="`第 ${r + 1} 行添加按钮`"
                @click="addButton(row)"
                ><IosIcon name="plus"
              /></el-button>
            </div>
          </div>
          <el-button
            class="menu-add-row"
            :icon="Plus"
            :disabled="form.rows.length >= 10 || total >= 24 || saving"
            @click="addRow"
            >添加一行</el-button
          >
        </fieldset>
        <section
          v-if="selected && canRead('rows')"
          id="bot-button-editor"
          ref="buttonEditor"
          tabindex="-1"
          class="panel bot-editor-panel"
        >
          <div class="panel-heading">
            <div>
              <h2>编辑「{{ selected.text }}」</h2>
              <p>图标统一来自 Telegram iOS Icons。</p>
            </div>
          </div>
          <el-form
            label-position="top"
            :disabled="saving || !canWrite('rows')"
            @submit.prevent
          >
            <div class="menu-field-pair">
              <el-form-item label="按钮文字" required
                ><el-input v-model="selected.text" maxlength="24"
              /></el-form-item>
              <el-form-item label="启用按钮"
                ><el-switch v-model="selected.enabled" aria-label="启用按钮"
              /></el-form-item>
            </div>
            <div class="menu-field-pair">
              <el-form-item label="按钮图标">
                <TelegramIconPicker
                  v-model="selected.icon"
                  :icons="icons"
                  label="按钮图标"
                />
              </el-form-item>
              <el-form-item label="按钮颜色">
                <el-select
                  v-model="selected.style"
                  aria-label="按钮颜色"
                  aria-describedby="menu-color-help"
                >
                  <el-option
                    label="跟随 Telegram 主题"
                    value="default"
                  /><el-option label="蓝色" value="primary" /><el-option
                    label="绿色"
                    value="success"
                  /><el-option label="红色" value="danger" />
                </el-select>
              </el-form-item>
            </div>
            <p id="menu-color-help" class="menu-color-help">
              Telegram
              按钮支持蓝、绿、红三种颜色，也可跟随主题；暂不支持自定义色值。
            </p>
            <el-form-item label="点击后执行" required>
              <el-select
                v-model="selected.action"
                aria-label="点击后执行"
                @change="selected.value = ''"
              >
                <el-option
                  v-for="(label, action) in actions"
                  :key="action"
                  :label="label"
                  :value="action"
                />
              </el-select>
            </el-form-item>
            <el-form-item
              v-if="['url', 'search', 'message'].includes(selected.action)"
              :label="
                selected.action === 'url'
                  ? 'HTTPS 链接'
                  : selected.action === 'search'
                    ? '搜索关键词'
                    : '回复文字'
              "
              required
            >
              <el-input
                v-model="selected.value"
                :type="selected.action === 'message' ? 'textarea' : 'text'"
                :rows="4"
                :maxlength="
                  selected.action === 'message'
                    ? 1800
                    : selected.action === 'search'
                      ? 120
                      : 500
                "
                :placeholder="
                  selected.action === 'url' ? 'https://t.me/your_channel' : ''
                "
              />
            </el-form-item>
            <div class="menu-edit-actions">
              <el-button
                :disabled="form.rows[selectedRow]?.[0]?.id === selectedId"
                @click="moveButton(-1)"
                >向左移</el-button
              >
              <el-button
                :disabled="form.rows[selectedRow]?.at(-1)?.id === selectedId"
                @click="moveButton(1)"
                >向右移</el-button
              >
              <el-button text type="danger" @click="removeButton"
                >移除按钮</el-button
              >
            </div>
          </el-form>
        </section>
      </div>
      <aside class="bot-preview-column" aria-label="Telegram 菜单预览">
        <div class="bot-preview-heading">
          <span><IosIcon name="telegram" /> Telegram 预览</span
          ><el-tag type="info">/start</el-tag>
        </div>
        <p v-if="template" class="template-preview-caption">
          示例用户：{{ template.sample_values.nickname }}（{{
            template.sample_values.username
          }}）。 实际发送时使用当前用户信息；时间按
          {{ template.timezone }} 显示。
        </p>
        <div class="telegram-preview">
          <div class="telegram-preview-top">
            <span class="preview-avatar"><IosIcon name="bot" /></span>
            <div><strong>你的搜索机器人</strong><small>机器人</small></div>
          </div>
          <div class="telegram-preview-chat">
            <div class="preview-outgoing">
              /start <span><IosIcon name="check" /></span>
            </div>
            <div class="preview-message">
              <p v-if="canRead('welcome') || canRead('welcome_icon')">
                <IosIcon :name="form.welcome_icon" />
                {{ previewWelcome.text || "欢迎语会显示在这里" }}
              </p>
              <p v-if="previewHint.text">{{ previewHint.text }}</p>
              <span class="preview-message-time">刚刚</span>
            </div>
            <div
              v-for="(row, r) in previewRows"
              :key="r"
              class="preview-keyboard-row"
            >
              <button
                v-for="button in row"
                :key="button.id"
                :class="['preview-keyboard-button', button.style]"
                :aria-label="`预览并编辑：${button.text}`"
                :disabled="saving"
                aria-controls="bot-button-editor"
                @click="selectPreview(button.id)"
              >
                <IosIcon :name="button.icon" /><span>{{ button.text }}</span
                ><IosIcon
                  v-if="button.action === 'url'"
                  name="link"
                  class="preview-link-icon"
                />
              </button>
            </div>
          </div>
          <div class="preview-compose">
            <span>输入关键词…</span><IosIcon name="telegram" />
          </div>
        </div>
        <div class="menu-preview-note">
          <p
            v-if="previewWelcome.truncated || previewHint.truncated"
            class="template-preview-warning"
            role="status"
          >
            预览内容已达到字数上限，末尾已省略。可减少文字或重复标签。
          </p>
          <p><IosIcon name="check" /> 保存后立即生效，无需重启机器人。</p>
          <p>
            重新发送 /start
            会收到新菜单；旧菜单会提示刷新。预览仅编辑草稿，不会向聊天发送消息。
          </p>
          <p>
            图标包：Telegram iOS Icons。实际配色由 Telegram
            客户端呈现；自定义图标需要机器人账号满足使用资格。
          </p>
          <p
            v-if="iconsNotRetained"
            class="icon-delivery-warning"
            role="status"
          >
            <IosIcon name="warning" /> 最近一次实测：Telegram
            未保留全部自定义图标。菜单文字和动作仍可使用；需核对 Bot 所有者
            Premium 或 Fragment 用户名资格。
          </p>
          <p v-else-if="Object.keys(delivery).length">
            最近一次实测：Telegram
            返回了自定义图标信息；请在客户端确认显示效果。
          </p>
          <p v-else>尚未检测发送结果，保存后向机器人发送 /start 即可验证。</p>
        </div>
      </aside>
    </div>
  </div>
</template>
