<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { ElMessage } from "element-plus";
import { ApiError, api } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useLoad } from "../lib/load";
import { verificationModes } from "../lib/botSecurity";
import { Key, Refresh } from "../lib/icons";
import LoadError from "../components/LoadError.vue";

const auth = useAuth();
const read = (key: string) => auth.readField("bot_security", key);
const write = (key: string) =>
  auth.can("bot_security.update") && auth.writeField("bot_security", key);
const form = reactive<Record<string, any>>({});
const baseline = ref("");
const saving = ref(false);
const saveError = ref("");
const errors = ref<Record<string, string>>({});
const numbers = [
  {
    key: "trust_minutes",
    label: "通过后免验证",
    unit: "分钟",
    min: 1,
    max: 1440,
    hint: "仅在当前私聊或当前群有效；到期后重新验证。",
  },
  {
    key: "challenge_seconds",
    label: "每题有效期",
    unit: "秒",
    min: 30,
    max: 600,
    hint: "超时后重新发送关键词获取新题。",
  },
  {
    key: "max_attempts",
    label: "最多答错次数",
    unit: "次",
    min: 1,
    max: 5,
    hint: "顺序题答错后从第一步重来，不重置答错次数。",
  },
  {
    key: "cooldown_seconds",
    label: "失败后冷却",
    unit: "秒",
    min: 30,
    max: 3600,
    hint: "次数用完后暂停该用户在当前聊天中的搜索验证。",
  },
  {
    key: "search_limit",
    label: "每人搜索上限",
    unit: "次 / 分钟",
    min: 1,
    max: 60,
    hint: "计入搜索、验证题发送与结果翻页，已验证用户也受限制。",
  },
  {
    key: "group_limit",
    label: "每群搜索上限",
    unit: "次 / 分钟",
    min: 1,
    max: 20,
    hint: "全群共享额度，控制机器人的回复频率。",
  },
];
const { loading, error, load } = useLoad(null, async () => {
  const values = await api("/admin/api/bot-security");
  Object.keys(form).forEach((key) => delete form[key]);
  Object.assign(form, values);
  baseline.value = JSON.stringify(form);
  return values;
});
const dirty = computed(() => JSON.stringify(form) !== baseline.value);
const canSave = computed(() => Object.keys(form).some(write));
onMounted(load);
async function save() {
  if (saving.value) return;
  errors.value = {};
  saveError.value = "";
  saving.value = true;
  try {
    await api("/admin/api/bot-security", auth.writable("bot_security", form));
    baseline.value = JSON.stringify(form);
    ElMessage.success("验证策略已保存，即时生效");
  } catch (e) {
    saveError.value = (e as Error).message;
    if (e instanceof ApiError) errors.value = e.fields;
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <div class="security-page">
    <div class="page-heading">
      <div>
        <p class="eyebrow">SEARCH PROTECTION</p>
        <h1>搜索验证</h1>
        <p class="muted">
          分别配置私聊和群聊，在交互体验与防刷保护之间找到合适的强度。
        </p>
      </div>
      <el-button :icon="Refresh" :disabled="saving || loading" @click="load"
        >刷新</el-button
      >
    </div>
    <LoadError v-if="error" :message="error" @retry="load" />
    <el-skeleton v-else-if="!baseline" :rows="10" animated />
    <div v-else v-loading="loading">
      <div class="security-note">
        <el-icon :size="22"><Key /></el-icon>
        <div>
          <strong>验证 + 限流，共同保护搜索</strong>
          <p>
            每题仅本人可答，绑定聊天与消息。图片及顺序题适合提高交互门槛；点击确认强度较低。任何内置题型都不能保证识别所有自动化脚本。
          </p>
        </div>
      </div>
      <el-form label-position="top" :disabled="saving" @submit.prevent="save">
        <section class="panel security-section">
          <div class="panel-heading">
            <div>
              <h2>验证范围</h2>
              <p>修改后即时生效，已有免验证状态会按新策略重新检查。</p>
            </div>
          </div>
          <div class="security-grid">
            <el-form-item
              v-if="read('private_mode')"
              label="私聊搜索"
              for="security-private"
              :error="errors.private_mode"
            >
              <el-select
                id="security-private"
                v-model="form.private_mode"
                :disabled="!write('private_mode')"
              >
                <el-option
                  v-for="(label, key) in verificationModes"
                  :key="key"
                  :value="key"
                  :label="label"
                />
              </el-select>
              <p class="field-help">
                直接发关键词、搜索命令和菜单搜索均执行此策略。
              </p>
            </el-form-item>
            <el-form-item
              v-if="read('group_mode')"
              label="群聊默认验证"
              for="security-group"
              :error="errors.group_mode"
            >
              <el-select
                id="security-group"
                v-model="form.group_mode"
                :disabled="!write('group_mode')"
              >
                <el-option
                  v-for="(label, key) in verificationModes"
                  :key="key"
                  :value="key"
                  :label="label"
                />
              </el-select>
              <p class="field-help">新群及选择「继承后台」的群使用此设置。</p>
            </el-form-item>
          </div>
          <div v-if="read('allow_group_override')" class="security-switch">
            <div>
              <strong>允许群管理员自主配置</strong>
              <p>关闭后，所有群统一执行上方群聊策略，群内命令无法覆盖。</p>
            </div>
            <el-switch
              v-model="form.allow_group_override"
              :active-value="1"
              :inactive-value="0"
              :disabled="!write('allow_group_override')"
              aria-label="允许群管理员自主配置"
            />
          </div>
        </section>
        <section class="panel security-section">
          <div class="panel-heading">
            <div>
              <h2>有效期与防刷限制</h2>
              <p>重复发送关键词不会反复生成验证消息，也不会清空答错次数。</p>
            </div>
          </div>
          <div class="security-grid">
            <el-form-item
              v-for="item in numbers.filter((item) => read(item.key))"
              :key="item.key"
              :label="item.label"
              :for="'security-' + item.key"
              :error="errors[item.key]"
            >
              <div class="security-number">
                <el-input-number
                  :id="'security-' + item.key"
                  v-model="form[item.key]"
                  :min="item.min"
                  :max="item.max"
                  :precision="0"
                  :disabled="!write(item.key)"
                /><span>{{ item.unit }}</span>
              </div>
              <p class="field-help">{{ item.hint }}</p>
            </el-form-item>
          </div>
        </section>
        <p v-if="saveError" class="security-error" role="alert">
          {{ saveError }}
        </p>
        <div class="security-save">
          <el-button
            v-if="canSave"
            type="primary"
            native-type="submit"
            :loading="saving"
            :disabled="!dirty || loading"
            >保存验证策略</el-button
          >
          <span class="muted">{{
            canSave
              ? "保存后私聊和群聊按各自策略执行。"
              : "当前为只读权限，请联系超级管理员调整。"
          }}</span>
        </div>
      </el-form>
    </div>
  </div>
</template>

<style scoped>
.security-page {
  max-width: 1120px;
}
.security-note {
  display: flex;
  align-items: flex-start;
  gap: 16px;
  padding: 20px 24px;
  margin: 0 0 24px;
  background: var(--el-color-primary-light-9);
  border: 1px solid var(--line);
  border-radius: 12px;
}
.security-note .el-icon {
  flex: 0 0 auto;
  margin-top: 2px;
  color: var(--primary);
}
.security-note p,
.security-switch p {
  margin: 6px 0 0;
  color: var(--muted);
  line-height: 1.7;
}
.security-section {
  margin-bottom: 24px;
}
.security-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 32px;
  row-gap: 8px;
}
.security-grid .el-form-item {
  min-width: 0;
  margin-bottom: 20px;
}
.field-help {
  margin: 8px 0 0;
  width: 100%;
  color: var(--muted);
  font-size: 13px;
  line-height: 1.7;
}
.security-switch {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  border-top: 1px solid var(--line);
  padding-top: 20px;
}
.security-switch .el-switch {
  flex-shrink: 0;
}
.security-number {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.security-number .el-input-number {
  width: 168px;
}
.security-number > span {
  color: var(--muted);
}
.security-save {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 16px;
  padding-bottom: 24px;
}
.security-error {
  color: var(--el-color-danger);
}
@media (max-width: 700px) {
  .security-grid {
    grid-template-columns: 1fr;
  }
  .security-note {
    padding: 16px;
    gap: 12px;
  }
  .security-switch {
    align-items: flex-start;
    gap: 16px;
  }
  .security-save {
    align-items: stretch;
    flex-direction: column;
  }
  .security-save .el-button {
    min-height: 44px;
  }
}
</style>
