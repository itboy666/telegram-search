<script setup lang="ts">
import { computed, ref } from "vue";
import { ElMessage } from "element-plus";
import type { InputInstance } from "element-plus";
import { api } from "../lib/api";
import { Key } from "../lib/icons";

const props = defineProps<{
  status: { configured: boolean; source: string; updated_at?: string };
  editable: boolean;
}>();
const emit = defineEmits<{ saved: [] }>();
const token = ref("");
const visible = ref(false);
const saving = ref(false);
const error = ref("");
const saved = ref(false);
const input = ref<InputInstance>();
const source = computed(
  () =>
    ({
      database: "当前使用后台保存的配置",
      environment: "当前使用本地环境配置",
      none: "保存 Token 后即可连接机器人",
    })[props.status.source] || "尚未配置",
);

async function save() {
  if (saving.value || !props.editable) return;
  error.value = "";
  saved.value = false;
  if (!/^[1-9]\d{4,19}:[A-Za-z0-9_-]{20,128}$/.test(token.value.trim())) {
    error.value = "Token 格式不正确，请粘贴 BotFather 提供的完整内容。";
    input.value?.focus();
    return;
  }
  saving.value = true;
  try {
    await api("/admin/api/bot", { token: token.value.trim() });
    token.value = "";
    visible.value = false;
    saved.value = true;
    emit("saved");
    ElMessage.success("Token 已验证并保存");
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <section class="panel bot-token-panel" aria-labelledby="bot-token-heading">
    <div class="panel-heading">
      <div>
        <h2 id="bot-token-heading">Bot Token</h2>
        <p>在这里配置或更换你的机器人</p>
      </div>
      <el-icon class="bot-token-icon"><Key /></el-icon>
    </div>
    <div class="bot-token-status">
      <el-tag :type="status.configured ? 'success' : 'info'">
        {{ status.configured ? "已配置" : "未配置" }}
      </el-tag>
      <span>{{ source }}</span>
    </div>
    <el-form v-if="editable" label-position="top" @submit.prevent="save">
      <el-form-item label="新的 Bot Token" for="bot-token-input">
        <el-input
          id="bot-token-input"
          ref="input"
          v-model="token"
          :type="visible ? 'text' : 'password'"
          :disabled="saving"
          :aria-invalid="!!error"
          aria-describedby="bot-token-help bot-token-error"
          autocomplete="new-password"
          autocapitalize="off"
          autocorrect="off"
          :spellcheck="false"
          placeholder="粘贴完整的 Bot Token"
          maxlength="150"
          @input="
            error = '';
            saved = false;
          "
        >
          <template #append>
            <el-button
              :disabled="saving"
              :aria-pressed="visible"
              :aria-label="visible ? '隐藏输入的 Token' : '显示输入的 Token'"
              @click="visible = !visible"
              >{{ visible ? "隐藏" : "显示" }}</el-button
            >
          </template>
        </el-input>
        <p id="bot-token-help" class="field-help">
          从 BotFather 获取。已保存的 Token 不会回显；留空不会修改原配置。
        </p>
        <p id="bot-token-error" class="bot-token-error" role="alert">
          {{ error }}
        </p>
      </el-form-item>
      <el-button
        type="primary"
        native-type="submit"
        :loading="saving"
        :disabled="!token.trim()"
        >{{ saving ? "正在验证连接…" : "验证并保存" }}</el-button
      >
    </el-form>
    <p v-else class="field-help">
      当前为只读权限，如需更换 Token，请联系超级管理员。
    </p>
    <p v-if="saved" class="bot-token-saved" role="status">
      已保存。长轮询将在当前请求结束后自动加载新配置，连接状态会自动刷新。
    </p>
    <div class="bot-token-note">
      <p>验证通过后才会保存，验证失败会保留原配置。</p>
      <p>Token 加密存储，修改后无需手动重启机器人。</p>
    </div>
  </section>
</template>

<style scoped>
.bot-token-panel {
  min-width: 0;
}
.bot-token-icon {
  color: var(--muted);
  font-size: 22px;
  flex: 0 0 auto;
}
.bot-token-status {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-bottom: 24px;
  font-size: 12px;
  color: var(--muted);
}
.bot-token-panel .el-form-item {
  margin-bottom: 16px;
}
.bot-token-panel .field-help {
  width: 100%;
  font-size: 12px;
  margin: 8px 0 0;
}
.bot-token-error {
  color: var(--el-color-danger);
  font-size: 12px;
  line-height: 1.6;
  margin: 8px 0 0;
  overflow-wrap: anywhere;
}
.bot-token-error:empty {
  display: none;
}
.bot-token-saved {
  font-size: 13px;
  color: var(--success);
  line-height: 1.7;
  margin: 16px 0 0;
}
.bot-token-note {
  border-top: 1px solid var(--line);
  margin-top: 24px;
  padding-top: 16px;
  font-size: 12px;
  color: var(--muted);
  line-height: 1.7;
}
.bot-token-note p {
  margin: 0;
}
.bot-token-note p + p {
  margin-top: 4px;
}
</style>
