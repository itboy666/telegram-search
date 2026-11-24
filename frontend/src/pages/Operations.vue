<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from "vue";
import { useRoute } from "vue-router";
import { ElMessage, ElMessageBox } from "element-plus";
import {
  Plus,
  Refresh,
  Promotion,
  Link,
  Key,
  Check,
  Clock,
  Edit,
} from "../lib/icons";
import { api, query, types, statuses, dateTime } from "../lib/api";
import { useLoad } from "../lib/load";
import LoadError from "../components/LoadError.vue";
import DatabaseUpgrade from "../components/DatabaseUpgrade.vue";
import BotTokenSettings from "../components/BotTokenSettings.vue";
import BotSearchSettings from "../components/BotSearchSettings.vue";
import AdminIdColumn from "../components/AdminIdColumn.vue";
import AdminListFilter from "../components/AdminListFilter.vue";
import AdminRowActions from "../components/AdminRowActions.vue";
import AdminRecordDetails from "../components/AdminRecordDetails.vue";
import AdminUserEditor from "../components/AdminUserEditor.vue";
import AdminSubmissionEditor from "../components/AdminSubmissionEditor.vue";
import { useAuth } from "../lib/auth";
const auth = useAuth();
const details = ref<Record<string, any> | null>(null);
const userEditor = ref(false),
  submissionEditor = ref(false),
  editing = ref<Record<string, any>>();
const busy = ref(false);
let botRefreshTimer: ReturnType<typeof setTimeout> | undefined;
onBeforeUnmount(() => clearTimeout(botRefreshTimer));
const route = useRoute(),
  kind = computed(() => route.path.split("/").pop() || "settings");
const detailTitle = computed(
  () => `${route.meta.title} · 详情 #${details.value?.id || ""}`,
);
const can = (action: string) => auth.can(`${kind.value}.${action}`);
const read = (field: string) => auth.readField(kind.value, field);
const write = (field: string) => auth.writeField(kind.value, field);
const descriptions: Record<string, string> = {
  submissions: "认真对待每一份分享，让值得发现的内容留下来。",
  categories: "用清晰的分类，为内容建立有序的导航。",
  users: "查看用户与 Telegram 访客，分别管理账号访问和收录投稿权限。",
  bot: "同一份资源目录，在 Telegram 里继续发现。",
  keys: "为你的应用创建访问密钥，按需开放搜索能力。",
  settings: "让站点和机器人，按照你的方式运行。",
  logs: "每一次管理操作，都有迹可循。",
};
const labels: Record<string, string> = {
  submissions: "CONTENT REVIEW",
  categories: "CATEGORIES",
  users: "YOUR COMMUNITY",
  bot: "TELEGRAM CONNECTION",
  keys: "DEVELOPER ACCESS",
  settings: "PREFERENCES",
  logs: "ACTIVITY LOG",
};
const filter = reactive({ status: "pending", id: "", q: "", page: 1 });
const listKinds = ["submissions", "categories", "users", "keys", "logs"];
const keywordFields: Record<string, string[]> = {
  submissions: ["title", "url", "description"],
  categories: ["name", "description"],
  users: ["username", "display_name", "telegram_id"],
  keys: ["name", "prefix"],
  logs: ["action", "detail", "actor_id"],
};
const keywordVisible = computed(() =>
  (keywordFields[kind.value] || []).some(read),
);
const searchPlaceholder = computed(
  () =>
    ({
      users: "搜索账号、昵称或 Telegram ID",
      submissions: "搜索资源名称、链接或简介",
      categories: "搜索分类名称或说明",
      keys: "搜索密钥名称或前缀",
      logs: "搜索操作、详情或管理员 ID",
    })[kind.value] || "搜索关键词",
);
const { data, loading, error, load } = useLoad<any>(null, () =>
  api(
    "/admin/api/" +
      kind.value +
      "?" +
      query(
        listKinds.includes(kind.value)
          ? {
              id: filter.id,
              q: filter.q,
              page: filter.page,
              status:
                kind.value === "submissions" && read("status")
                  ? filter.status
                  : "",
            }
          : {},
      ),
  ),
);
const settings = reactive<Record<string, string>>({
    site_name: "",
    site_description: "",
    submissions_enabled: "0",
    public_api_enabled: "0",
  }),
  dialog = ref(false),
  form = reactive<Record<string, any>>({}),
  saving = ref(false),
  formError = ref(""),
  secret = ref(""),
  selected = ref<any>(null),
  reviewOpen = ref(false),
  reviewNote = ref(""),
  reviewError = ref("");
watch(
  kind,
  () => {
    clearTimeout(botRefreshTimer);
    Object.assign(filter, { page: 1, id: "", q: "", status: "pending" });
    data.value = null;
    dialog.value = false;
    reviewOpen.value = false;
    details.value = null;
    userEditor.value = false;
    submissionEditor.value = false;
    secret.value = "";
    load();
  },
  { immediate: true },
);
async function refreshBotAfterSave() {
  await load();
  let remaining = 15;
  const refresh = async () => {
    if (kind.value !== "bot") return;
    await load();
    if (--remaining > 0 && kind.value === "bot" && !data.value?.running) {
      botRefreshTimer = setTimeout(refresh, 4000);
    }
  };
  clearTimeout(botRefreshTimer);
  if (kind.value === "bot") botRefreshTimer = setTimeout(refresh, 4000);
}
watch(data, (value) => {
  if (kind.value === "settings" && value) Object.assign(settings, value);
});
function edit(item?: any) {
  Object.keys(form).forEach((k) => delete form[k]);
  Object.assign(
    form,
    item || { name: "", description: "", sort_order: 0, daily_limit: 1000 },
  );
  formError.value = "";
  dialog.value = true;
}
async function save() {
  saving.value = true;
  formError.value = "";
  try {
    const r = await api(
      "/admin/api/" + kind.value + (form.id ? "/" + form.id : ""),
      auth.writable(
        kind.value,
        Object.fromEntries(
          (kind.value === "keys"
            ? ["name", "daily_limit"]
            : ["name", "description", "sort_order"]
          ).map((key) => [key, form[key]]),
        ),
      ),
    );
    dialog.value = false;
    if (kind.value === "keys" && !form.id && r.token) secret.value = r.token;
    ElMessage.success("已保存");
    await load();
  } catch (e) {
    formError.value = (e as Error).message;
  } finally {
    saving.value = false;
  }
}
async function action(
  url: string,
  body: any,
  title: string,
  message: string,
  method?: string,
) {
  if (busy.value) return;
  try {
    await ElMessageBox.confirm(message, title, {
      confirmButtonText: "确认",
      cancelButtonText: "取消",
      type: "warning",
    });
    busy.value = true;
    await api(url, body, method);
    if (
      method === "DELETE" &&
      data.value?.items?.length === 1 &&
      filter.page > 1
    )
      filter.page--;
    ElMessage.success("操作成功");
    await load();
  } catch (e) {
    if (e instanceof Error) ElMessage.error(e.message);
  } finally {
    busy.value = false;
  }
}
function openEditor(row?: Record<string, any>) {
  editing.value = row;
  if (kind.value === "users") userEditor.value = true;
  else if (kind.value === "submissions") submissionEditor.value = true;
  else edit(row);
}
async function savedEditor() {
  userEditor.value = false;
  submissionEditor.value = false;
  await load();
}
function rowActions(row: any) {
  const actions: { command: string; label: string; danger?: boolean }[] = [];
  if (
    kind.value === "submissions" &&
    row.status === "pending" &&
    can("review") &&
    write("status")
  )
    actions.push({ command: "review", label: "审核申请" });
  if (kind.value === "keys" && row.status && can("revoke") && write("status"))
    actions.push({ command: "revoke", label: "撤销密钥", danger: true });
  if (
    can("delete") &&
    (kind.value !== "submissions" || row.status === "pending")
  )
    actions.push({ command: "delete", label: "删除记录", danger: true });
  return actions;
}
function rowAction(command: string, row: any) {
  if (command === "review") return review(row);
  if (command === "revoke")
    return action(
      `/admin/api/keys/${row.id}/revoke`,
      {},
      "撤销密钥",
      `确认撤销密钥 #${row.id}？使用此密钥的应用将无法继续调用接口。`,
    );
  const notes: Record<string, string> = {
    categories: "关联资源或收录申请的分类无法删除。",
    users:
      "相关登录会话和收藏将一并清除；受限用户或有关联资源、收录历史的用户不能删除。",
    submissions: "仅待审核申请可删除，已审核历史需要保留。",
    keys: "密钥必须先撤销，才可以删除。",
  };
  return action(
    `/admin/api/${kind.value}/${row.id}`,
    {},
    "删除记录",
    `确认永久删除 #${row.id}「${row.name || row.title || row.display_name || "此记录"}」？删除后无法恢复。${notes[kind.value] || ""}`,
    "DELETE",
  );
}
function review(row: any) {
  selected.value = row;
  reviewNote.value = "";
  reviewError.value = "";
  reviewOpen.value = true;
}
async function decide(status: string) {
  saving.value = true;
  reviewError.value = "";
  try {
    await api(
      `/admin/api/submissions/${selected.value.id}/review`,
      auth.writable("submissions", {
        status,
        review_note: reviewNote.value,
      }),
    );
    reviewOpen.value = false;
    ElMessage.success(
      status === "approved" ? "已通过，资源已发布" : "审核结果已保存",
    );
    await load();
  } catch (e) {
    reviewError.value = (e as Error).message;
  } finally {
    saving.value = false;
  }
}
async function saveSettings() {
  saving.value = true;
  formError.value = "";
  try {
    Object.assign(
      settings,
      await api("/admin/api/settings", auth.writable("settings", settings)),
    );
    ElMessage.success("设置已保存");
  } catch (e) {
    formError.value = (e as Error).message;
  } finally {
    saving.value = false;
  }
}
async function copy() {
  try {
    await navigator.clipboard.writeText(secret.value);
    ElMessage.success("密钥已复制");
  } catch {
    ElMessage.info("请手动选择并复制密钥。");
  }
}
function search() {
  filter.page = 1;
  load();
}
function reset() {
  Object.assign(filter, { id: "", q: "", page: 1, status: "pending" });
  load();
}
</script>
<template>
  <div>
    <div class="page-heading">
      <div>
        <span class="eyebrow">{{ labels[kind] }}</span>
        <h1>{{ route.meta.title }}</h1>
        <p>{{ descriptions[kind] }}</p>
      </div>
      <el-button
        v-if="
          ['categories', 'keys', 'users', 'submissions'].includes(kind) &&
          can('create')
        "
        type="primary"
        :icon="Plus"
        @click="openEditor()"
        >{{
          kind === "keys"
            ? "创建密钥"
            : kind === "users"
              ? "添加用户"
              : kind === "submissions"
                ? "新增申请"
                : "新建分类"
        }}</el-button><el-button
        v-else-if="kind !== 'settings'"
        :icon="Refresh"
        :loading="loading"
        @click="load"
        >刷新</el-button
      >
    </div>
    <LoadError :message="error" @retry="load" /><el-skeleton
      v-if="loading && !data"
      :rows="8"
      animated
    />
    <template v-if="data">
      <section v-if="kind === 'submissions'" class="panel table-panel">
        <AdminListFilter
          :filter="filter"
          :placeholder="searchPlaceholder"
          :keyword-visible="keywordVisible"
          @search="search"
          @reset="reset"
        />
        <div class="filter-bar">
          <el-radio-group
            v-if="read('status')"
            v-model="filter.status"
            @change="search"
            ><el-radio-button label="待审核" value="pending" /><el-radio-button
              label="已通过"
              value="approved" /><el-radio-button
              label="未通过"
              value="rejected" /><el-radio-button
              label="全部"
              value="" /></el-radio-group
          ><span class="muted">共 {{ data.total }} 条申请</span>
        </div>
        <el-table
          v-loading="loading"
          :data="data.items"
          empty-text="当前没有收录申请"
          ><AdminIdColumn /><el-table-column label="提交的资源" min-width="260"
            ><template #default="{ row }"
              ><div class="table-text">
                <strong>{{ row.title || `申请 #${row.id}` }}</strong><a
                  v-if="row.url"
                  :href="row.url"
                  target="_blank"
                  rel="noopener noreferrer"
                  >{{ row.url }}</a
                >
              </div></template
            ></el-table-column
          ><el-table-column
            v-if="read('submitter')"
            prop="submitter"
            label="提交人"
            min-width="120" /><el-table-column
            v-if="read('type')"
            label="类型"
            width="90"
            ><template #default="{ row }">{{
              types[row.type]
            }}</template></el-table-column
          ><el-table-column v-if="read('status')" label="状态" width="100"
            ><template #default="{ row }"
              ><el-tag
                :type="
                  row.status === 'approved'
                    ? 'success'
                    : row.status === 'pending'
                      ? 'warning'
                      : 'info'
                "
                >{{ statuses[row.status] }}</el-tag
              ></template
            ></el-table-column
          ><el-table-column
            v-if="read('created_at')"
            label="提交时间"
            min-width="160"
            ><template #default="{ row }">{{
              dateTime(row.created_at)
            }}</template></el-table-column
          ><el-table-column label="操作" width="192"
            ><template #default="{ row }"
              ><AdminRowActions
                :editable="can('update') && row.status === 'pending'"
                :busy="busy"
                :actions="rowActions(row)"
                @view="details = row"
                @edit="openEditor(row)"
                @action="rowAction($event, row)" /></template></el-table-column
        ></el-table>
        <div class="pagination">
          <span>按提交时间排序</span
          ><el-pagination
            v-model:current-page="filter.page"
            :total="data.total"
            :page-size="20"
            layout="prev, pager, next"
            :pager-count="5"
            @current-change="load"
          />
        </div>
      </section>
      <section v-if="kind === 'categories'" class="panel table-panel">
        <AdminListFilter
          :filter="filter"
          :placeholder="searchPlaceholder"
          :keyword-visible="keywordVisible"
          @search="search"
          @reset="reset"
        />
        <div class="table-summary">
          <strong
            >资源分类 <span>{{ data.length }}</span></strong
          ><span>排序数值越小，展示越靠前</span>
        </div>
        <el-table :data="data" empty-text="还没有分类"
          ><AdminIdColumn /><el-table-column label="分类名称" min-width="160"
            ><template #default="{ row }"
              ><strong>{{ row.name || `分类 #${row.id}` }}</strong></template
            ></el-table-column
          ><el-table-column
            v-if="read('description')"
            prop="description"
            label="分类说明"
            min-width="240" /><el-table-column
            v-if="read('resource_count')"
            prop="resource_count"
            label="资源数量"
            width="110" /><el-table-column
            v-if="read('sort_order')"
            prop="sort_order"
            label="排序"
            width="90" /><el-table-column label="操作" width="192"
            ><template #default="{ row }"
              ><AdminRowActions
                :editable="can('update')"
                :busy="busy"
                :actions="rowActions(row)"
                @view="details = row"
                @edit="edit(row)"
                @action="rowAction($event, row)" /></template></el-table-column
        ></el-table>
      </section>
      <section v-if="kind === 'users'" class="panel table-panel">
        <AdminListFilter
          :filter="filter"
          :placeholder="searchPlaceholder"
          :keyword-visible="keywordVisible"
          @search="search"
          @reset="reset"
        />
        <div class="table-summary">
          <strong
            >全部用户 <span>{{ data.total }}</span></strong
          ><span>网站账号与 Telegram 访客</span>
        </div>
        <el-table :data="data.items" empty-text="没有匹配的用户"
          ><AdminIdColumn /><el-table-column label="用户" min-width="180"
            ><template #default="{ row }"
              ><div class="table-text">
                <strong>{{
                  row.display_name || `用户 #${row.id}`
                }}</strong><small>{{ row.username }}</small>
              </div></template
            ></el-table-column
          ><el-table-column
            v-if="read('telegram_id')"
            label="Telegram ID"
            min-width="150"
            ><template #default="{ row }">{{
              row.telegram_id || "网站注册"
            }}</template></el-table-column
          ><el-table-column
            v-if="read('created_at')"
            label="注册时间"
            min-width="160"
            ><template #default="{ row }">{{
              dateTime(row.created_at)
            }}</template></el-table-column
          ><el-table-column v-if="read('status')" label="状态" width="100"
            ><template #default="{ row }"
              ><span
                class="status-dot"
                :class="row.status ? 'published' : 'hidden'"
              ></span
              >{{ row.status ? "正常" : "已停用" }}</template
            ></el-table-column
          ><el-table-column
            v-if="read('submission_allowed')"
            label="收录投稿"
            width="130"
            ><template #default="{ row }"
              ><el-tag
                :type="
                  Number(row.submission_allowed) === 1 ? 'success' : 'danger'
                "
                >{{
                  Number(row.submission_allowed) === 1 ? "允许投稿" : "禁止投稿"
                }}</el-tag
              ></template
            ></el-table-column
          ><el-table-column label="操作" width="192"
            ><template #default="{ row }"
              ><AdminRowActions
                :editable="can('update')"
                :busy="busy"
                :actions="rowActions(row)"
                @view="details = row"
                @edit="openEditor(row)"
                @action="rowAction($event, row)" /></template></el-table-column
        ></el-table>
        <div class="pagination">
          <span>用户账号与访问管理</span
          ><el-pagination
            v-model:current-page="filter.page"
            :total="data.total"
            :page-size="20"
            layout="prev, pager, next"
            :pager-count="5"
            @current-change="load"
          />
        </div>
      </section>
      <div v-if="kind === 'bot'" class="settings-grid bot-settings-grid">
        <div class="bot-settings-stack">
          <section class="panel">
            <div class="connection-hero">
              <span class="connection-icon"
                ><el-icon><Promotion /></el-icon
              ></span>
              <div>
                <h2>{{ data.name || "Telegram Bot" }}</h2>
                <p>
                  {{ data.username ? "@" + data.username : "等待验证连接" }}
                </p>
              </div>
              <el-tag
                v-if="read('running')"
                :type="data.running ? 'success' : 'warning'"
                >{{
                  data.running
                    ? "运行中"
                    : data.state === "starting"
                      ? "正在连接"
                      : "未运行"
                }}</el-tag
              >
            </div>
            <el-descriptions :column="1" border
              ><el-descriptions-item v-if="read('mode')" label="连接方式"
                >本地长轮询</el-descriptions-item
              ><el-descriptions-item label="Bot ID">{{
                data.id || "尚未验证"
              }}</el-descriptions-item
              ><el-descriptions-item
                v-if="read('configured')"
                label="凭据状态"
                >{{
                  data.configured ? "已配置" : "未配置"
                }}</el-descriptions-item
              ><el-descriptions-item
                v-if="read('checked_at')"
                label="最近连接验证"
                >{{ dateTime(data.checked_at) }}</el-descriptions-item
              ><el-descriptions-item
                v-if="read('last_update_at')"
                label="最近交互"
                >{{ dateTime(data.last_update_at) }}</el-descriptions-item
              ></el-descriptions
            >
            <div v-if="data.last_error" class="form-alert">
              {{ data.last_error }}
            </div>
            <div
              v-if="auth.can('bot_menu.read') || data.username"
              class="connection-actions"
            >
              <router-link
                v-if="auth.can('bot_menu.read')"
                v-slot="{ href, navigate }"
                to="/admin/bot-menu"
                custom
              >
                <el-button tag="a" :href="href" :icon="Edit" @click="navigate"
                  >自定义 /start 欢迎语与按钮</el-button
                >
              </router-link>
              <el-button
                v-if="data.username"
                tag="a"
                :href="'https://t.me/' + data.username"
                target="_blank"
                rel="noopener noreferrer"
                :icon="Link"
                >在 Telegram 打开</el-button
              >
            </div>
          </section>
          <BotSearchSettings
            v-if="read('page_size')"
            :page-size="data.page_size"
            :editable="can('update') && write('page_size')"
            @saved="load"
          />
        </div>
        <div class="bot-settings-stack">
          <BotTokenSettings
            v-if="read('token') && data.token"
            :status="data.token"
            :editable="can('update') && write('token')"
            @saved="refreshBotAfterSave"
          />
          <section class="panel">
            <div class="panel-heading"><h2>已经连接的能力</h2></div>
            <ul class="feature-list">
              <li>
                <el-icon><Check /></el-icon>关键词搜索与分类筛选
              </li>
              <li>
                <el-icon><Check /></el-icon>上一页 / 下一页原消息更新
              </li>
              <li>
                <el-icon><Check /></el-icon>提交链接，进入后台审核
              </li>
              <li>
                <el-icon><Check /></el-icon>按钮归属校验与过期提示
              </li>
            </ul>
          </section>
        </div>
      </div>
      <template v-if="kind === 'keys'"
        ><section class="info-banner">
          <span class="quick-icon blue"
            ><el-icon><Key /></el-icon
          ></span>
          <div>
            <strong>为每个应用使用独立密钥</strong>
            <p>
              密钥仅在创建时显示一次；在「系统设置」开启开放 API 后即可调用。
            </p>
          </div>
          <router-link v-if="auth.can('settings.read')" to="/admin/settings"
            >前往设置 →</router-link
          >
        </section>
        <section v-if="secret" class="secret-box" role="status">
          <strong>密钥创建成功，请立即保存</strong>
          <p>关闭此页面后无法再次查看完整密钥。</p>
          <div>
            <el-input
              :model-value="secret"
              readonly
              aria-label="新创建的 API 密钥"
            /><el-button @click="copy">复制密钥</el-button
            ><el-button @click="secret = ''">已保存</el-button>
          </div>
        </section>
        <section class="panel table-panel">
          <AdminListFilter
            :filter="filter"
            :placeholder="searchPlaceholder"
            :keyword-visible="keywordVisible"
            @search="search"
            @reset="reset"
          />
          <el-table :data="data" empty-text="还没有访问密钥，创建一个开始使用"
            ><AdminIdColumn /><el-table-column
              v-if="read('name')"
              prop="name"
              label="密钥名称"
              min-width="180" /><el-table-column
              v-if="read('prefix')"
              label="密钥前缀"
              min-width="160"
              ><template #default="{ row }"
                ><code>{{ row.prefix }}…</code></template
              ></el-table-column
            ><el-table-column
              v-if="read('daily_limit')"
              label="每日额度"
              width="110"
              ><template #default="{ row }"
                >{{ row.daily_limit }} 次</template
              ></el-table-column
            ><el-table-column v-if="read('status')" label="状态" width="100"
              ><template #default="{ row }"
                ><el-tag :type="row.status ? 'success' : 'info'">{{
                  row.status ? "有效" : "已撤销"
                }}</el-tag></template
              ></el-table-column
            ><el-table-column
              v-if="read('last_used_at')"
              label="最近使用"
              min-width="150"
              ><template #default="{ row }">{{
                dateTime(row.last_used_at)
              }}</template></el-table-column
            ><el-table-column label="操作" width="192"
              ><template #default="{ row }"
                ><AdminRowActions
                  :editable="can('update')"
                  :busy="busy"
                  :actions="rowActions(row)"
                  @view="details = row"
                  @edit="edit(row)"
                  @action="
                    rowAction($event, row)
                  " /></template></el-table-column
          ></el-table>
        </section>
        <section class="panel api-example">
          <h2>快速接入</h2>
          <p>使用 Bearer Token 访问搜索接口，支持关键词、类型、分类与分页。</p>
          <pre>
GET /api/v1/search?q=Telegram&amp;type=channel&amp;page=1
Authorization: Bearer YOUR_API_KEY</pre>
          <p class="field-help">
            page_size 默认 12，最多 50；page 最多 100。成功响应包含
            items、total、page 和 has_more。
          </p>
        </section></template
      >
      <div v-if="kind === 'settings'" class="settings-form">
        <div v-if="formError" class="form-alert" role="alert">
          {{ formError }}
        </div>
        <section class="panel">
          <div class="panel-heading">
            <div>
              <h2>基本信息</h2>
              <p>向访客介绍你的资源目录</p>
            </div>
          </div>
          <el-form label-position="top" @submit.prevent
            ><el-form-item v-if="read('site_name')" label="网站名称" required
              ><el-input
                :disabled="!can('update') || !write('site_name')"
                v-model="settings.site_name"
                maxlength="40" /></el-form-item
            ><el-form-item v-if="read('site_description')" label="网站描述"
              ><el-input
                :disabled="!can('update') || !write('site_description')"
                v-model="settings.site_description"
                maxlength="160"
                type="textarea"
                :rows="2" /></el-form-item
          ></el-form>
        </section>
        <section class="panel">
          <div class="panel-heading"><h2>访问与收录</h2></div>
          <div v-if="read('submissions_enabled')" class="setting-row">
            <div>
              <strong>接受资源提交</strong>
              <p>允许网站用户和 Telegram 用户提交公开链接</p>
            </div>
            <el-switch
              :disabled="!can('update') || !write('submissions_enabled')"
              v-model="settings.submissions_enabled"
              active-value="1"
              inactive-value="0"
              aria-label="接受资源提交"
            />
          </div>
          <div v-if="read('public_api_enabled')" class="setting-row">
            <div>
              <strong>开放搜索 API</strong>
              <p>开启后，持有效密钥的应用可访问 /api/v1/search</p>
            </div>
            <el-switch
              :disabled="!can('update') || !write('public_api_enabled')"
              v-model="settings.public_api_enabled"
              active-value="1"
              inactive-value="0"
              aria-label="开放搜索 API"
            />
          </div>
        </section>
        <section v-if="auth.can('bot_menu.read')" class="panel">
          <div class="panel-heading"><h2>机器人欢迎语与菜单</h2></div>
          <p class="muted">
            在菜单编辑器中统一配置 /start 欢迎语、图标、按钮和点击动作。
          </p>
          <router-link to="/admin/bot-menu" class="button-link"
            >打开菜单编辑器</router-link
          >
        </section>
        <el-button
          v-if="can('update')"
          type="primary"
          native-type="button"
          :loading="saving"
          @click="saveSettings"
          >保存设置</el-button
        >
      </div>
      <DatabaseUpgrade v-if="kind === 'settings' && auth.access.super" />
      <section v-if="kind === 'logs'" class="panel table-panel">
        <AdminListFilter
          :filter="filter"
          :placeholder="searchPlaceholder"
          :keyword-visible="keywordVisible"
          @search="search"
          @reset="reset"
        />
        <div class="table-summary">
          <strong
            >操作记录 <span>{{ data.total }}</span></strong
          ><span>由系统自动记录，只读留存</span>
        </div>
        <el-table :data="data.items" empty-text="还没有管理操作记录"
          ><AdminIdColumn /><el-table-column label="操作" min-width="150"
            ><template #default="{ row }"
              ><el-icon class="inline-icon"><Clock /></el-icon
              >{{ row.action || `日志 #${row.id}` }}</template
            ></el-table-column
          ><el-table-column
            v-if="read('detail')"
            prop="detail"
            label="详情"
            min-width="300"
            show-overflow-tooltip
          /><el-table-column v-if="read('actor_id')" label="操作人" width="130"
            ><template #default="{ row }">{{
              row.actor_id ? "管理员 #" + row.actor_id : "系统"
            }}</template></el-table-column
          ><el-table-column v-if="read('created_at')" label="时间" width="180"
            ><template #default="{ row }">{{
              dateTime(row.created_at)
            }}</template></el-table-column
          ><el-table-column label="操作" width="90"
            ><template #default="{ row }"
              ><el-button text @click="details = row">查看</el-button></template
            ></el-table-column
          ></el-table
        >
        <div class="pagination">
          <span>按记录时间排序</span
          ><el-pagination
            v-model:current-page="filter.page"
            :total="data.total"
            :page-size="20"
            layout="prev, pager, next"
            :pager-count="5"
            @current-change="load"
          />
        </div>
      </section>
    </template>
    <el-dialog
      v-model="dialog"
      :title="
        kind === 'keys'
          ? form.id
            ? `编辑密钥 #${form.id}`
            : '创建访问密钥'
          : form.id
            ? `编辑分类 #${form.id}`
            : '新建分类'
      "
      width="480px"
      :close-on-click-modal="false"
      ><div v-if="formError" class="form-alert" role="alert">
        {{ formError }}
      </div>
      <el-form label-position="top" @submit.prevent
        ><el-form-item
          v-if="read('name')"
          :label="kind === 'keys' ? '密钥名称' : '分类名称'"
          required
          ><el-input
            :disabled="!write('name')"
            v-model="form.name"
            maxlength="80"
            :placeholder="
              kind === 'keys' ? '例如：个人网站搜索' : '例如：开发工具'
            " /></el-form-item
        ><template v-if="kind === 'categories'"
          ><el-form-item v-if="read('description')" label="分类说明"
            ><el-input
              :disabled="!write('description')"
              v-model="form.description"
              type="textarea"
              :rows="3"
              maxlength="255" /></el-form-item
          ><el-form-item v-if="read('sort_order')" label="排序"
            ><el-input-number
              :disabled="!write('sort_order')"
              v-model="form.sort_order"
              :precision="0" /></el-form-item></template
        ><el-form-item v-else-if="read('daily_limit')" label="每日请求额度"
          ><el-input-number
            :disabled="!write('daily_limit')"
            v-model="form.daily_limit"
            :min="1"
            :max="100000"
            :precision="0" /></el-form-item></el-form
      ><template #footer
        ><el-button @click="dialog = false">取消</el-button
        ><el-button type="primary" :loading="saving" @click="save">{{
          kind === "keys" ? (form.id ? "保存密钥" : "创建密钥") : "保存分类"
        }}</el-button></template
      ></el-dialog
    >
    <el-drawer
      v-model="reviewOpen"
      :title="`审核收录申请 #${selected?.id || ''}`"
      size="520px"
      class="form-drawer"
      ><template v-if="selected"
        ><el-tag
          v-if="read('status')"
          :type="selected.status === 'pending' ? 'warning' : 'info'"
          >{{ statuses[selected.status] }}</el-tag
        >
        <h2>{{ selected.title || `申请 #${selected.id}` }}</h2>
        <a
          v-if="selected.url"
          :href="selected.url"
          target="_blank"
          rel="noopener noreferrer"
          class="break-word"
          >{{ selected.url }} ↗</a
        >
        <div v-if="read('description')" class="detail-description">
          {{ selected.description || "暂无简介" }}
        </div>
        <p v-if="read('submitter') || read('created_at')" class="muted">
          由 {{ selected.submitter }} 于
          {{ dateTime(selected.created_at) }} 提交
        </p>
        <div v-if="reviewError" class="form-alert" role="alert">
          {{ reviewError }}
        </div>
        <el-form label-position="top" @submit.prevent
          ><el-form-item v-if="read('review_note')" label="审核备注"
            ><el-input
              v-if="
                selected.status === 'pending' &&
                can('review') &&
                write('review_note')
              "
              v-model="reviewNote"
              type="textarea"
              :rows="3"
              maxlength="500"
              placeholder="可填写修改建议或审核说明"
            />
            <p v-else>{{ selected.review_note || "没有备注" }}</p></el-form-item
          ></el-form
        ></template
      ><template #footer
        ><template
          v-if="
            selected?.status === 'pending' && can('review') && write('status')
          "
          ><el-button
            type="danger"
            plain
            :disabled="saving"
            @click="decide('rejected')"
            >拒绝收录</el-button
          ><el-button
            type="primary"
            :loading="saving"
            @click="decide('approved')"
            >通过并发布</el-button
          ></template
        ><el-button v-else @click="reviewOpen = false"
          >关闭</el-button
        ></template
      ></el-drawer
    >
    <AdminRecordDetails
      :record="details"
      :title="detailTitle"
      @close="details = null"
    />
    <AdminUserEditor
      v-if="userEditor"
      :user="editing"
      @close="userEditor = false"
      @saved="savedEditor"
    />
    <AdminSubmissionEditor
      v-if="submissionEditor"
      :submission="editing"
      @close="submissionEditor = false"
      @saved="savedEditor"
    />
  </div>
</template>
