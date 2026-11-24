<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { api, ApiError, query, dateTime } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useLoad } from "../lib/load";
import {
  adModes,
  verificationModes,
  groupIdentity,
  groupWelcomeIssue,
} from "../lib/botSecurity";
import { Refresh } from "../lib/icons";
import IosIcon from "../components/IosIcon.vue";
import AdminIdColumn from "../components/AdminIdColumn.vue";
import AdminListFilter from "../components/AdminListFilter.vue";
import GroupWelcomeEditor from "../components/GroupWelcomeEditor.vue";
import LoadError from "../components/LoadError.vue";

const auth = useAuth();
const read = (field: string) => auth.readField("bot_groups", field);
const write = (field: string) =>
  auth.can("bot_groups.update") && auth.writeField("bot_groups", field);
const filters = reactive({ id: "", q: "", page: 1 });
const { data, loading, error, load } = useLoad<any>(null, () =>
  api("/admin/api/bot-groups?" + query(filters)),
);
const open = ref(false),
  saving = ref(false),
  editing = ref(false);
const form = reactive<Record<string, any>>({});
const errors = ref<Record<string, string>>({});
const saveError = ref("");
const errorSummary = ref<HTMLElement>();
const current = ref<Record<string, any>>({});
const baseline = ref("");
const defaults: Record<string, any> = {
  verify_mode: "inherit",
  ad_mode: "off",
  ad_keywords: "",
  allow_domains: "",
  flood_limit: 0,
  message_max_chars: 0,
  block_media: 0,
  block_forwards: 0,
  join_verify_mode: "off",
  join_timeout_seconds: 180,
  join_max_attempts: 3,
  join_failure_action: "mute",
  welcome_enabled: 0,
  welcome_template: "",
};
const labels: Record<string, string> = {
  bot_id: "机器人 ID",
  chat_id: "Telegram 群 ID",
  inviter_id: "邀请人 ID",
  updated_by: "最近配置的 Telegram 管理员",
  status: "机器人群身份",
  can_delete: "删除消息权限",
  can_restrict: "限制成员权限",
  blocked_count: "已删除消息数",
  created_at: "接入时间",
  updated_at: "最近更新",
};
const firstFields = [
  "verify_mode",
  "ad_mode",
  "ad_keywords",
  "allow_domains",
  "flood_limit",
  "message_max_chars",
  "block_media",
  "block_forwards",
];
const joinFields = [
  "join_verify_mode",
  "join_timeout_seconds",
  "join_max_attempts",
  "join_failure_action",
];
const hasMessageFields = computed(() => firstFields.some(read));
const hasJoinFields = computed(() => joinFields.some(read));
const canSave = computed(() => Object.keys(form).some(write));
const joinEnabled = computed(
  () => form.join_verify_mode && form.join_verify_mode !== "off",
);
function search() {
  filters.page = 1;
  load();
}
function show(row: Record<string, any>, edit = false) {
  current.value = row;
  Object.keys(form).forEach((key) => delete form[key]);
  for (const key of Object.keys(defaults))
    if (read(key))
      form[key] = Object.hasOwn(row, key) ? row[key] : defaults[key];
  baseline.value = JSON.stringify(form);
  editing.value = edit;
  errors.value = {};
  saveError.value = "";
  open.value = true;
}
async function closeDrawer(done?: () => void) {
  if (saving.value) return;
  if (editing.value && JSON.stringify(form) !== baseline.value) {
    try {
      await ElMessageBox.confirm(
        "这次修改还没有保存，关闭后将丢弃。",
        "放弃本次修改？",
        {
          confirmButtonText: "放弃修改",
          cancelButtonText: "继续编辑",
          type: "warning",
        },
      );
    } catch {
      return;
    }
  }
  if (done) done();
  else open.value = false;
}
async function focusErrors() {
  await nextTick();
  errorSummary.value?.focus();
}
async function save() {
  if (saving.value || !canSave.value) return;
  errors.value = {};
  saveError.value = "";
  if (write("welcome_template")) {
    const text = String(form.welcome_template || "");
    const issue = groupWelcomeIssue(text);
    if (issue) {
      errors.value.welcome_template = issue;
      saveError.value = "请检查欢迎语模板后保存。";
      await focusErrors();
      return;
    }
  }
  saving.value = true;
  try {
    await api(
      "/admin/api/bot-groups/" + current.value.id,
      auth.writable("bot_groups", form),
    );
    baseline.value = JSON.stringify(form);
    open.value = false;
    ElMessage.success("群设置已保存");
    await load();
  } catch (e) {
    saveError.value = (e as Error).message;
    if (e instanceof ApiError) errors.value = e.fields;
    await focusErrors();
  } finally {
    saving.value = false;
  }
}
onMounted(load);
</script>

<template>
  <div>
    <div class="page-heading">
      <div>
        <p class="eyebrow">GROUP PROTECTION</p>
        <h1>群组安全</h1>
        <p class="muted">从消息管理到新人欢迎，让每个群保持有序。</p>
      </div>
      <el-button :icon="Refresh" :disabled="loading" @click="load"
        >刷新</el-button
      >
    </div>
    <section class="panel group-instructions">
      <div class="group-guide-heading">
        <span><IosIcon name="group" /></span>
        <div>
          <strong>统一管理，按群设置</strong>
          <p>
            将机器人设为群管理员，再通过群命令
            <code>/groupsettings</code> 或本页配置。
          </p>
        </div>
      </div>
      <div class="group-guide-grid">
        <div>
          <strong>消息拦截</strong>
          <p>
            超长、媒体及转发规则命中后静默删除，不继续搜索。需要删除消息权限。
          </p>
        </div>
        <div>
          <strong>新人验证</strong>
          <p>未验证新人先禁言，再前往机器人私聊完成验证。需要限制成员权限。</p>
        </div>
        <div>
          <strong>专属欢迎</strong>
          <p>
            按群配置文字与动态标签，欢迎每位新成员。所有示例可在保存前预览。
          </p>
        </div>
      </div>
    </section>
    <section class="panel">
      <AdminListFilter
        :filter="filters"
        :keyword-visible="read('title') || read('chat_id')"
        placeholder="搜索群名称或 Telegram 群 ID"
        :loading="loading"
        @search="search"
        @reset="
          Object.assign(filters, { id: '', q: '', page: 1 });
          load();
        "
      />
      <LoadError v-if="error" :message="error" @retry="load" />
      <el-table
        v-else
        v-loading="loading"
        :data="data?.items || []"
        empty-text="尚未登记群组。将机器人加入群，或在已有群发送 /groupsettings。"
      >
        <AdminIdColumn />
        <el-table-column
          v-if="read('title')"
          prop="title"
          label="群名称"
          min-width="180"
          show-overflow-tooltip
        />
        <el-table-column
          v-if="read('chat_id')"
          prop="chat_id"
          label="Telegram 群 ID"
          min-width="160"
        />
        <el-table-column
          v-if="read('verify_mode')"
          label="搜索验证"
          min-width="140"
          ><template #default="{ row }">{{
            row.verify_mode === "inherit"
              ? "继承后台"
              : verificationModes[row.verify_mode] || "未设置"
          }}</template></el-table-column
        >
        <el-table-column
          v-if="read('join_verify_mode')"
          label="新人验证"
          min-width="150"
          ><template #default="{ row }"
            ><span
              class="policy-value"
              :class="{ enabled: row.join_verify_mode !== 'off' }"
              >{{ verificationModes[row.join_verify_mode] || "不验证" }}</span
            ></template
          ></el-table-column
        >
        <el-table-column
          v-if="read('message_max_chars')"
          label="消息字数"
          min-width="120"
          ><template #default="{ row }">{{
            Number(row.message_max_chars) > 0
              ? `${row.message_max_chars} 字以内`
              : "不限字数"
          }}</template></el-table-column
        >
        <el-table-column v-if="read('ad_mode')" label="广告拦截" min-width="135"
          ><template #default="{ row }">{{
            adModes[row.ad_mode]
          }}</template></el-table-column
        >
        <el-table-column
          v-if="read('can_delete') || read('can_restrict')"
          label="机器人权限"
          min-width="150"
          ><template #default="{ row }"
            ><div class="group-permission-tags">
              <el-tag
                v-if="read('can_delete')"
                :type="row.can_delete ? 'success' : 'info'"
                effect="plain"
                >删消息{{ row.can_delete ? "已授权" : "未授权" }}</el-tag
              >
              <el-tag
                v-if="read('can_restrict')"
                :type="row.can_restrict ? 'success' : 'info'"
                effect="plain"
                >限制成员{{ row.can_restrict ? "已授权" : "未授权" }}</el-tag
              >
            </div></template
          ></el-table-column
        >
        <el-table-column
          v-if="read('blocked_count')"
          prop="blocked_count"
          label="已删除"
          width="95"
        />
        <el-table-column label="操作" fixed="right" width="140"
          ><template #default="{ row }"
            ><div class="group-actions">
              <el-button link @click="show(row)">查看</el-button
              ><el-button
                v-if="auth.can('bot_groups.update')"
                link
                type="primary"
                @click="show(row, true)"
                >编辑</el-button
              >
            </div></template
          ></el-table-column
        >
      </el-table>
      <el-pagination
        v-if="data?.total"
        v-model:current-page="filters.page"
        :total="data.total"
        :page-size="20"
        :pager-count="5"
        layout="prev, pager, next"
        @current-change="load"
      />
    </section>
    <el-drawer
      v-model="open"
      :title="`${editing ? '编辑群设置' : '群设置详情'} · #${current.id}`"
      size="760px"
      class="group-security-drawer"
      :close-on-click-modal="false"
      :close-on-press-escape="!saving"
      :show-close="!saving"
      :before-close="closeDrawer"
    >
      <div class="group-drawer-intro">
        <span class="group-drawer-icon"><IosIcon name="group" /></span>
        <div>
          <h2>{{ read("title") ? current.title : `群组 #${current.id}` }}</h2>
          <p>
            {{
              editing
                ? "修改后保存，仅影响当前群组。"
                : "当前群组的策略和机器人权限。"
            }}
          </p>
        </div>
      </div>
      <details class="group-details">
        <summary>群资料与权限状态</summary>
        <dl class="group-facts">
          <template v-for="(label, key) in labels" :key="key"
            ><template v-if="read(key)">
              <dt>{{ label }}</dt>
              <dd>
                {{
                  key === "status"
                    ? groupIdentity[current[key]] || current[key]
                    : ["can_delete", "can_restrict"].includes(key)
                      ? current[key]
                        ? "已授权"
                        : "未确认 / 无权限"
                      : key.endsWith("_at")
                        ? dateTime(current[key])
                        : (current[key] ?? "—")
                }}
              </dd>
            </template></template
          >
        </dl>
      </details>
      <el-alert
        v-if="read('last_error') && current.last_error"
        :title="current.last_error"
        type="warning"
        :closable="false"
        show-icon
      />
      <el-form
        label-position="top"
        :disabled="saving || !editing"
        @submit.prevent="save"
      >
        <div
          v-if="saveError"
          ref="errorSummary"
          class="form-alert group-error-summary"
          role="alert"
          tabindex="-1"
        >
          <strong>{{ saveError }}</strong>
          <ul v-if="Object.keys(errors).length">
            <li v-for="(message, field) in errors" :key="field">
              <a :href="`#group-${String(field).replaceAll('_', '-')}`">{{
                message
              }}</a>
            </li>
          </ul>
        </div>
        <section v-if="hasMessageFields" class="group-policy-section">
          <div class="group-section-heading">
            <span><IosIcon name="settings" /></span>
            <div>
              <h3>搜索与消息拦截</h3>
              <p>设置允许的内容和搜索前的验证方式。</p>
            </div>
          </div>
          <el-form-item
            v-if="read('verify_mode')"
            label="本群搜索验证"
            for="group-verify-mode"
            :error="errors.verify_mode"
          >
            <el-select
              id="group-verify-mode"
              v-model="form.verify_mode"
              :disabled="!write('verify_mode')"
              ><el-option value="inherit" label="继承总后台" /><el-option
                v-for="(label, key) in verificationModes"
                :key="key"
                :value="key"
                :label="label"
            /></el-select>
            <p class="field-help">
              后台关闭群自主配置时，以后台策略为准；此项与新人入群验证分别设置。
            </p>
          </el-form-item>
          <div class="group-control-grid">
            <el-form-item
              v-if="read('message_max_chars')"
              label="单条消息最多字数"
              for="group-message-max-chars"
              :error="errors.message_max_chars"
            >
              <el-input-number
                id="group-message-max-chars"
                v-model="form.message_max_chars"
                :min="0"
                :max="4096"
                :precision="0"
                controls-position="right"
                :disabled="!write('message_max_chars')"
              />
              <p class="field-help">
                0
                为关闭。文字和媒体说明均计数；超长消息静默删除，不回复、不搜索。
              </p>
            </el-form-item>
            <el-form-item
              v-if="read('flood_limit')"
              label="每人 10 秒内消息上限"
              for="group-flood-limit"
              :error="errors.flood_limit"
            >
              <el-input-number
                id="group-flood-limit"
                v-model="form.flood_limit"
                :min="0"
                :max="30"
                :precision="0"
                controls-position="right"
                :disabled="!write('flood_limit')"
              />
              <p class="field-help">
                0 为关闭，开启填写 4–30；同文 30 秒内超过 3 条也会删除。
              </p>
            </el-form-item>
          </div>
          <div
            v-if="read('block_media') || read('block_forwards')"
            class="group-switch-list"
          >
            <el-form-item
              v-if="read('block_media')"
              label="拦截媒体消息"
              for="group-block-media"
              :error="errors.block_media"
              class="group-switch-item"
            >
              <el-switch
                id="group-block-media"
                v-model="form.block_media"
                :active-value="1"
                :inactive-value="0"
                :disabled="!write('block_media')"
                aria-label="拦截媒体消息"
              />
              <p class="field-help">
                包括图片、视频、文件、语音、贴纸，以及联系人、位置、投票等非文字内容。
              </p>
            </el-form-item>
            <el-form-item
              v-if="read('block_forwards')"
              label="拦截转发消息"
              for="group-block-forwards"
              :error="errors.block_forwards"
              class="group-switch-item"
            >
              <el-switch
                id="group-block-forwards"
                v-model="form.block_forwards"
                :active-value="1"
                :inactive-value="0"
                :disabled="!write('block_forwards')"
                aria-label="拦截转发消息"
              />
              <p class="field-help">
                拦截带转发来源的普通成员消息。群管理员免拦截。
              </p>
            </el-form-item>
          </div>
          <el-form-item
            v-if="read('ad_mode')"
            label="广告拦截规则"
            for="group-ad-mode"
            :error="errors.ad_mode"
          >
            <el-select
              id="group-ad-mode"
              v-model="form.ad_mode"
              :disabled="!write('ad_mode')"
              ><el-option
                v-for="(label, key) in adModes"
                :key="key"
                :value="key"
                :label="label"
            /></el-select>
          </el-form-item>
          <div class="group-control-grid">
            <el-form-item
              v-if="read('ad_keywords')"
              label="广告关键词"
              for="group-ad-keywords"
              :error="errors.ad_keywords"
            >
              <el-input
                id="group-ad-keywords"
                v-model="form.ad_keywords"
                type="textarea"
                :rows="3"
                :maxlength="2000"
                :disabled="!write('ad_keywords')"
                placeholder="每行一个词，2–60 字，最多 50 行"
              />
              <p class="field-help">关键词规则开启后，按包含关系匹配。</p>
            </el-form-item>
            <el-form-item
              v-if="read('allow_domains')"
              label="允许的链接域名"
              for="group-allow-domains"
              :error="errors.allow_domains"
            >
              <el-input
                id="group-allow-domains"
                v-model="form.allow_domains"
                type="textarea"
                :rows="3"
                :maxlength="2000"
                :disabled="!write('allow_domains')"
                placeholder="每行一个域名，如 example.com"
              />
              <p class="field-help">
                含子域名；允许 t.me 会放行所有 Telegram 链接。
              </p>
            </el-form-item>
          </div>
          <p class="group-policy-note">
            <IosIcon
              name="lock"
            />删除后不可恢复。无法确认发送者身份时不删除消息，也不触发搜索。
          </p>
        </section>
        <section v-if="hasJoinFields" class="group-policy-section">
          <div class="group-section-heading">
            <span><IosIcon name="lock" /></span>
            <div>
              <h3>新人入群验证</h3>
              <p>先限制发言，通过验证后恢复群默认发言权限。</p>
            </div>
          </div>
          <div
            v-if="read('can_restrict') && !current.can_restrict"
            class="group-permission-notice"
          >
            <IosIcon name="lock" /><span
              >尚未确认「限制成员」权限。请先在 Telegram
              为机器人授权，验证策略才能执行。</span
            >
          </div>
          <el-form-item
            v-if="read('join_verify_mode')"
            label="新人验证方式"
            for="group-join-verify-mode"
            :error="errors.join_verify_mode"
          >
            <el-select
              id="group-join-verify-mode"
              v-model="form.join_verify_mode"
              :disabled="!write('join_verify_mode')"
              ><el-option
                v-for="(label, key) in verificationModes"
                :key="key"
                :value="key"
                :label="label"
            /></el-select>
            <p class="field-help">
              群里提供私聊入口，新人在机器人私聊中完成验证；图片文字与算式答案也在私聊输入，不受群禁言影响。点击确认仅适合轻量防刷。
            </p>
          </el-form-item>
          <div
            v-if="joinEnabled || !read('join_verify_mode')"
            class="group-control-grid"
          >
            <el-form-item
              v-if="read('join_timeout_seconds')"
              label="验证时限（秒）"
              for="group-join-timeout-seconds"
              :error="errors.join_timeout_seconds"
            >
              <el-input-number
                id="group-join-timeout-seconds"
                v-model="form.join_timeout_seconds"
                :min="30"
                :max="3600"
                :precision="0"
                controls-position="right"
                :disabled="!write('join_timeout_seconds')"
              />
            </el-form-item>
            <el-form-item
              v-if="read('join_max_attempts')"
              label="最多答错次数"
              for="group-join-max-attempts"
              :error="errors.join_max_attempts"
            >
              <el-input-number
                id="group-join-max-attempts"
                v-model="form.join_max_attempts"
                :min="1"
                :max="10"
                :precision="0"
                controls-position="right"
                :disabled="!write('join_max_attempts')"
              />
            </el-form-item>
            <el-form-item
              v-if="read('join_failure_action')"
              label="超时或验证失败后"
              for="group-join-failure-action"
              :error="errors.join_failure_action"
              class="group-full-width"
            >
              <el-select
                id="group-join-failure-action"
                v-model="form.join_failure_action"
                :disabled="!write('join_failure_action')"
                ><el-option
                  value="mute"
                  label="继续禁言，等待管理员处理" /><el-option
                  value="kick"
                  label="移出群组"
              /></el-select>
              <p class="field-help">
                {{
                  form.join_failure_action === "kick"
                    ? "达到失败条件会移出新人，请确认群规则允许这样处理。"
                    : "失败后保留群成员身份，管理员可在 Telegram 手动解除禁言。"
                }}
              </p>
            </el-form-item>
          </div>
        </section>
        <section
          v-if="read('welcome_enabled') || read('welcome_template')"
          class="group-policy-section"
        >
          <div class="group-section-heading">
            <span><IosIcon name="telegram" /></span>
            <div>
              <h3>欢迎语模板</h3>
              <p>新人入群时发送，使用本群自己的欢迎方式。</p>
            </div>
          </div>
          <el-form-item
            v-if="read('welcome_enabled')"
            label="发送入群欢迎语"
            for="group-welcome-enabled"
            :error="errors.welcome_enabled"
          >
            <el-switch
              id="group-welcome-enabled"
              v-model="form.welcome_enabled"
              :active-value="1"
              :inactive-value="0"
              :disabled="!write('welcome_enabled')"
              aria-label="发送入群欢迎语"
            />
            <p class="field-help">
              开启新人验证时，欢迎消息附验证入口；关闭欢迎语仍会发送必要的验证通知。
            </p>
          </el-form-item>
          <GroupWelcomeEditor
            v-if="read('welcome_template')"
            :key="current.id"
            v-model="form.welcome_template"
            :disabled="saving || !editing || !write('welcome_template')"
            :error="errors.welcome_template"
            :chat-title="read('title') ? current.title : undefined"
          />
        </section>
      </el-form>
      <template #footer
        ><div class="group-footer">
          <span v-if="editing && !canSave" class="field-help"
            >当前角色没有可编辑字段</span
          ><el-button :disabled="saving" @click="closeDrawer()">{{
            editing ? "取消" : "关闭"
          }}</el-button
          ><el-button
            v-if="editing && canSave"
            type="primary"
            :loading="saving"
            @click="save"
            >保存群设置</el-button
          >
        </div></template
      >
    </el-drawer>
  </div>
</template>

<style scoped>
.group-instructions {
  margin-bottom: 24px;
}
.group-guide-heading,
.group-drawer-intro,
.group-section-heading {
  display: flex;
  align-items: center;
  gap: 16px;
}
.group-guide-heading > span,
.group-drawer-icon,
.group-section-heading > span {
  display: grid;
  place-items: center;
  flex: 0 0 auto;
  width: 44px;
  height: 44px;
  border-radius: 12px;
  color: var(--primary);
  background: var(--primary-soft);
  font-size: 23px;
}
.group-guide-heading p,
.group-guide-grid p,
.group-drawer-intro p,
.group-section-heading p {
  margin: 8px 0 0;
  font-size: 13px;
  line-height: 1.8;
  color: var(--muted);
}
.group-guide-heading code {
  color: var(--deep);
  overflow-wrap: anywhere;
}
.group-guide-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 24px;
  margin-top: 24px;
  padding-top: 24px;
  border-top: 1px solid var(--line);
}
.group-guide-grid strong {
  font-size: 14px;
}
.group-actions,
.group-footer {
  display: flex;
  gap: 12px;
}
.group-actions .el-button + .el-button,
.group-footer .el-button + .el-button {
  margin-left: 0;
}
.group-permission-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding: 8px 0;
}
.policy-value {
  color: var(--muted);
}
.policy-value.enabled {
  color: var(--primary);
}
.group-drawer-intro {
  margin-bottom: 24px;
}
.group-drawer-intro > div {
  min-width: 0;
}
.group-drawer-intro h2 {
  margin: 0;
  font-size: 20px;
  overflow-wrap: anywhere;
}
.group-details {
  border: 1px solid var(--line);
  border-radius: 12px;
  padding: 16px;
  margin-bottom: 24px;
}
.group-details summary {
  cursor: pointer;
  min-height: 28px;
  color: var(--muted);
  font-size: 14px;
}
.group-details[open] summary {
  margin-bottom: 20px;
}
.group-facts {
  display: grid;
  grid-template-columns: minmax(110px, 1fr) minmax(0, 1.4fr);
  gap: 12px 16px;
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
}
.group-facts dt {
  color: var(--muted);
}
.group-facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.group-policy-section {
  border: 1px solid var(--line);
  border-radius: 16px;
  padding: 24px;
  margin-bottom: 24px;
}
.group-section-heading {
  margin-bottom: 24px;
  align-items: flex-start;
}
.group-section-heading h3 {
  margin: 0;
  font-size: 17px;
  color: var(--ink);
}
.group-section-heading p {
  margin-top: 6px;
}
.group-control-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 24px;
}
.group-full-width {
  grid-column: 1/-1;
}
.group-control-grid :deep(.el-input-number) {
  width: 100%;
}
.field-help {
  width: 100%;
  color: var(--muted);
  font-size: 13px;
  line-height: 1.7;
  margin: 8px 0 0;
}
.group-switch-list {
  background: var(--canvas, #f5f8fc);
  border: 1px solid var(--line);
  padding: 20px;
  border-radius: 12px;
  margin-bottom: 24px;
}
.group-switch-item:last-child {
  margin-bottom: 0;
}
.group-policy-note,
.group-permission-notice {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  color: var(--muted);
  font-size: 13px;
  line-height: 1.7;
  margin: 0;
}
.group-policy-note .tg-ios-icon,
.group-permission-notice .tg-ios-icon {
  flex-shrink: 0;
  margin-top: 3px;
}
.group-permission-notice {
  background: var(--primary-soft);
  border-radius: 10px;
  padding: 16px;
  margin-bottom: 24px;
  color: var(--deep);
}
.group-footer {
  align-items: center;
  justify-content: flex-end;
}
.group-error-summary {
  margin-bottom: 24px;
}
.group-error-summary ul {
  margin: 8px 0 0;
  padding-left: 20px;
}
.group-error-summary a {
  color: inherit;
  text-decoration: underline;
}
.el-alert {
  margin-bottom: 24px;
}
.el-pagination {
  margin-top: 24px;
  justify-content: flex-end;
}
@media (max-width: 900px) {
  .group-guide-grid {
    grid-template-columns: 1fr;
    gap: 20px;
  }
}
@media (max-width: 600px) {
  .group-policy-section {
    padding: 20px 16px;
  }
  .group-control-grid {
    grid-template-columns: 1fr;
  }
  .group-switch-list {
    padding: 16px;
  }
  .group-actions .el-button,
  .group-footer .el-button {
    min-height: 44px;
  }
  .group-footer .el-button {
    flex: 1;
  }
  .group-facts {
    grid-template-columns: minmax(0, 1fr);
    gap: 6px;
  }
  .group-facts dd {
    margin-bottom: 10px;
  }
  .group-footer {
    flex-wrap: wrap;
  }
}
</style>
<style>
.group-security-drawer {
  max-width: 100vw;
}
@media (max-width: 600px) {
  .group-security-drawer .el-drawer__body {
    padding: 16px;
  }
}
</style>
