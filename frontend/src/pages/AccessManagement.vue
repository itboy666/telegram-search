<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { ElMessage, ElMessageBox } from "element-plus";
import { api, ApiError, dateTime } from "../lib/api";
import { Plus, Key, User, Close } from "../lib/icons";
import LoadError from "../components/LoadError.vue";
import AdminIdColumn from "../components/AdminIdColumn.vue";
import AdminListFilter from "../components/AdminListFilter.vue";
import AdminRowActions from "../components/AdminRowActions.vue";
import AdminRecordDetails from "../components/AdminRecordDetails.vue";
import { useAuth } from "../lib/auth";
import { newPasswordError } from "../lib/passwordValidation";

type Definition = {
  label: string;
  actions: Record<string, string>;
  fields: Record<string, string>;
  writable: string[];
};
type Role = {
  id: number;
  name: string;
  description: string;
  status: number;
  permissions: string[];
  fields: Record<string, Record<string, string>>;
  admin_count: number;
};
const route = useRoute();
const router = useRouter(),
  auth = useAuth();
const isRoles = computed(() => route.path.endsWith("/roles"));
const details = ref<Record<string, any> | null>(null);
const filter = reactive({ id: "", q: "" }),
  applied = reactive({ id: "", q: "" }),
  page = ref(1);
const filtered = computed(() =>
  (isRoles.value ? roles.value : admins.value).filter(
    (row) =>
      (!applied.id || String(row.id) === applied.id.trim()) &&
      (!applied.q ||
        [row.name, row.description, row.username, row.display_name].some(
          (value) =>
            String(value || "")
              .toLowerCase()
              .includes(applied.q.trim().toLowerCase()),
        )),
  ),
);
const rows = computed(() =>
  filtered.value.slice((page.value - 1) * 20, page.value * 20),
);
const admins = ref<any[]>([]),
  roles = ref<Role[]>([]),
  catalog = ref<Record<string, Definition>>({});
const loading = ref(false),
  error = ref(""),
  saving = ref(false),
  drawer = ref(false);
const form = reactive<Record<string, any>>({
    permissions: [],
    fields: {},
    role_ids: [],
  }),
  formError = ref(""),
  fieldErrors = ref<Record<string, string>>({});
const errorSummary = ref<HTMLElement>(),
  expanded = ref(""),
  moduleFilter = ref("");
const shownModules = computed(() =>
  Object.fromEntries(
    Object.entries(catalog.value).filter(([key, definition]) =>
      `${key} ${definition.label}`
        .toLowerCase()
        .includes(moduleFilter.value.trim().toLowerCase()),
    ),
  ),
);
const selectedFieldCount = computed(() =>
  Object.values(form.fields || {}).reduce(
    (count: number, fields: any) => count + Object.keys(fields).length,
    0,
  ),
);
async function load() {
  loading.value = true;
  error.value = "";
  try {
    const result = await Promise.all([
      api("/admin/api/admins"),
      api("/admin/api/roles"),
      api("/admin/api/access/catalog"),
    ]);
    [admins.value, roles.value, catalog.value] = result;
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    loading.value = false;
  }
}
watch(
  isRoles,
  () => {
    drawer.value = false;
    details.value = null;
    Object.assign(filter, { id: "", q: "" });
    Object.assign(applied, filter);
    page.value = 1;
    load();
  },
  { immediate: true },
);
function edit(row?: any) {
  Object.keys(form).forEach((key) => delete form[key]);
  Object.assign(
    form,
    { permissions: [], fields: {}, role_ids: [] },
    isRoles.value
      ? {
          name: "",
          description: "",
          status: 1,
          permissions: [],
          fields: {},
          ...JSON.parse(JSON.stringify(row || {})),
        }
      : {
          username: "",
          display_name: "",
          password: "",
          status: 1,
          role_ids: [],
          ...JSON.parse(JSON.stringify(row || {})),
        },
  );
  if (isRoles.value) form.fields = { ...form.fields };
  form.status = Number(form.status);
  formError.value = "";
  fieldErrors.value = {};
  expanded.value = "";
  moduleFilter.value = "";
  drawer.value = true;
}
function moduleActions(module: string, values: unknown) {
  const actions = values as string[];
  const old = form.permissions as string[];
  form.permissions = [
    ...old.filter((p) => !p.startsWith(`${module}.`)),
    ...actions,
  ];
  if (!actions.includes(`${module}.read`)) {
    if (
      actions.some((p) => p !== `${module}.read`) &&
      !old.includes(`${module}.read`)
    )
      form.permissions.push(`${module}.read`);
    else {
      form.permissions = form.permissions.filter(
        (p: string) => !p.startsWith(`${module}.`),
      );
      delete form.fields[module];
    }
  }
}
function setField(module: string, field: string, value: unknown) {
  form.fields[module] ??= {};
  if (value === "hidden") delete form.fields[module][field];
  else form.fields[module][field] = value;
}
function readAll(module: string) {
  if (!form.permissions.includes(`${module}.read`))
    form.permissions.push(`${module}.read`);
  form.fields[module] = Object.fromEntries(
    Object.keys(catalog.value[module]!.fields).map((key) => [key, "read"]),
  );
}
function clearModule(module: string) {
  form.permissions = form.permissions.filter(
    (p: string) => !p.startsWith(`${module}.`),
  );
  delete form.fields[module];
}
async function save() {
  formError.value = "";
  fieldErrors.value = {};
  if (!isRoles.value && (!form.id || form.password)) {
    const invalidPassword = newPasswordError(form.password || "");
    if (invalidPassword) {
      fieldErrors.value.password = invalidPassword;
      return;
    }
  }
  saving.value = true;
  try {
    const keys = isRoles.value
      ? ["name", "description", "status", "permissions", "fields"]
      : ["username", "display_name", "password", "status", "role_ids"];
    await api(
      `/admin/api/${isRoles.value ? "roles" : "admins"}${form.id ? `/${form.id}` : ""}`,
      Object.fromEntries(keys.map((key) => [key, form[key]])),
    );
    drawer.value = false;
    if (
      !isRoles.value &&
      Number(form.id) === Number(auth.users.admin?.id) &&
      form.password
    ) {
      ElMessage.success("密码已更新，请重新登录");
      await router.replace("/admin/login");
      return;
    }
    ElMessage.success(
      isRoles.value ? "角色权限已保存，下次请求立即生效" : "管理员已保存",
    );
    await load();
  } catch (e) {
    formError.value = (e as Error).message;
    if (e instanceof ApiError) fieldErrors.value = e.fields;
    await nextTick();
    errorSummary.value?.focus();
  } finally {
    saving.value = false;
  }
}
function roleName(id: number) {
  return (
    roles.value.find((role) => Number(role.id) === id)?.name || `角色 #${id}`
  );
}
function search() {
  if (filter.id && !/^[1-9]\d*$/.test(filter.id.trim())) {
    ElMessage.error("ID 必须为正整数");
    return;
  }
  Object.assign(applied, filter);
  page.value = 1;
}
function reset() {
  Object.assign(filter, { id: "", q: "" });
  search();
}
function rowActions(row: any) {
  return isRoles.value ||
    (!row.is_super && Number(row.id) !== Number(auth.users.admin?.id))
    ? [
        {
          command: "delete",
          label: isRoles.value ? "删除角色" : "删除管理员",
          danger: true,
        },
      ]
    : [];
}
async function remove(row: any) {
  if (saving.value) return;
  try {
    const label = isRoles.value ? "角色" : "管理员";
    await ElMessageBox.confirm(
      `确认永久删除${label} #${row.id}「${row.name || row.display_name}」？${isRoles.value ? "已分配给管理员的角色无法删除，请先调整所属角色。" : "此账号的后台登录会话和角色关联将一并清除。"}删除后无法恢复。`,
      `删除${label}`,
      {
        confirmButtonText: "确认删除",
        cancelButtonText: "取消",
        type: "warning",
      },
    );
    saving.value = true;
    await api(
      `/admin/api/${isRoles.value ? "roles" : "admins"}/${row.id}`,
      {},
      "DELETE",
    );
    if (rows.value.length === 1 && page.value > 1) page.value--;
    ElMessage.success(`${label}已删除`);
    await load();
  } catch (e) {
    if (e instanceof Error) ElMessage.error(e.message);
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <div>
    <div class="page-heading">
      <div>
        <span class="eyebrow">ACCESS CONTROL</span>
        <h1>{{ isRoles ? "角色与权限" : "管理员管理" }}</h1>
        <p>
          {{
            isRoles
              ? "按职责分配操作与字段权限，让每个账号各司其职。"
              : "管理后台账号与所属角色，与普通用户账号独立。"
          }}
        </p>
      </div>
      <el-button
        type="primary"
        :icon="Plus"
        :disabled="loading || !!error"
        @click="edit()"
        >{{ isRoles ? "新建角色" : "添加管理员" }}</el-button
      >
    </div>
    <div class="access-tabs" aria-label="账号权限管理">
      <router-link
        to="/admin/admins"
        :aria-current="!isRoles ? 'page' : undefined"
        ><el-icon><User /></el-icon>管理员</router-link
      >
      <router-link
        to="/admin/roles"
        :aria-current="isRoles ? 'page' : undefined"
        ><el-icon><Key /></el-icon>角色与权限</router-link
      >
    </div>
    <LoadError :message="error" @retry="load" />
    <section class="panel table-panel" v-loading="loading">
      <AdminListFilter
        :filter="filter"
        :placeholder="isRoles ? '搜索角色名称或说明' : '搜索管理员账号或昵称'"
        @search="search"
        @reset="reset"
      />
      <template v-if="isRoles">
        <div class="table-summary">
          <strong>{{ filtered.length }} 个角色</strong
          ><span>多个角色的授权合并；未授权字段默认隐藏</span>
        </div>
        <el-table :data="rows" empty-text="没有匹配的角色">
          <AdminIdColumn />
          <el-table-column label="角色" min-width="200"
            ><template #default="{ row }"
              ><div class="table-text">
                <strong>{{ row.name }}</strong><small>{{
                  row.description || "未填写说明"
                }}</small>
              </div></template
            ></el-table-column
          >
          <el-table-column label="授权操作" width="110"
            ><template #default="{ row }"
              >{{ row.permissions.length }} 项</template
            ></el-table-column
          >
          <el-table-column prop="admin_count" label="管理员" width="100" />
          <el-table-column label="状态" width="100"
            ><template #default="{ row }"
              ><el-tag :type="row.status ? 'success' : 'info'">{{
                row.status ? "启用" : "已停用"
              }}</el-tag></template
            ></el-table-column
          >
          <el-table-column label="操作" width="220"
            ><template #default="{ row }"
              ><AdminRowActions
                editable
                edit-label="配置权限"
                :busy="saving"
                :actions="rowActions(row)"
                @view="details = row"
                @edit="edit(row)"
                @action="remove(row)" /></template
          ></el-table-column>
        </el-table>
      </template>
      <template v-else>
        <div class="table-summary">
          <strong>{{ filtered.length }} 位管理员</strong
          ><span>仅超级管理员可管理账号与授权</span>
        </div>
        <el-table :data="rows" empty-text="没有匹配的管理员">
          <AdminIdColumn />
          <el-table-column label="管理员" min-width="180"
            ><template #default="{ row }"
              ><div class="table-text">
                <strong>{{ row.display_name }}</strong><small>{{
                  row.username
                }}</small>
              </div></template
            ></el-table-column
          >
          <el-table-column label="角色" min-width="220"
            ><template #default="{ row }"
              ><div class="access-role-tags">
                <el-tag v-if="row.is_super" type="primary">超级管理员</el-tag
                ><template v-else
                  ><el-tag v-for="id in row.role_ids" :key="id" type="info">{{
                    roleName(id)
                  }}</el-tag
                  ><span v-if="!row.role_ids.length" class="muted"
                    >未分配角色</span
                  ></template
                >
              </div></template
            ></el-table-column
          >
          <el-table-column label="状态" width="100"
            ><template #default="{ row }"
              ><el-tag :type="row.status ? 'success' : 'info'">{{
                row.status ? "正常" : "已停用"
              }}</el-tag></template
            ></el-table-column
          >
          <el-table-column label="最近登录" min-width="160"
            ><template #default="{ row }">{{
              dateTime(row.last_login_at)
            }}</template></el-table-column
          >
          <el-table-column label="操作" width="192"
            ><template #default="{ row }"
              ><AdminRowActions
                editable
                :busy="saving"
                :actions="rowActions(row)"
                @view="details = row"
                @edit="edit(row)"
                @action="remove(row)" /></template
          ></el-table-column>
        </el-table>
      </template>
      <div class="pagination">
        <span>共 {{ filtered.length }} 条记录</span
        ><el-pagination
          v-model:current-page="page"
          :total="filtered.length"
          :page-size="20"
          :pager-count="5"
          layout="prev, pager, next"
        />
      </div>
    </section>
    <p class="field-help access-footnote">
      {{
        isRoles
          ? "停用角色后，其授权立即失效；其他有效角色的授权仍保留。记录 ID 始终可见，密码哈希和会话凭据不会返回。"
          : "管理员使用 /admin/login，普通用户使用 /user/login；两个账号体系和登录会话相互独立。"
      }}
    </p>
    <el-drawer
      v-model="drawer"
      :title="
        isRoles
          ? form.id
            ? `配置角色权限 #${form.id}`
            : '新建角色'
          : form.id
            ? `编辑管理员 #${form.id}`
            : '添加管理员'
      "
      size="720px"
      class="form-drawer access-drawer"
      :close-on-click-modal="false"
      destroy-on-close
      :close-icon="Close"
    >
      <div
        v-if="formError"
        ref="errorSummary"
        class="form-alert"
        role="alert"
        tabindex="-1"
      >
        {{ formError }}
      </div>
      <el-form
        v-if="drawer"
        label-position="top"
        :disabled="saving"
        @submit.prevent="save"
      >
        <template v-if="isRoles">
          <div class="form-grid">
            <el-form-item label="角色名称" required :error="fieldErrors.name"
              ><el-input
                v-model="form.name"
                maxlength="80"
                placeholder="例如：内容运营"
            /></el-form-item>
            <el-form-item label="角色状态"
              ><el-select v-model="form.status"
                ><el-option :value="1" label="启用" /><el-option
                  :value="0"
                  label="停用" /></el-select
            ></el-form-item>
          </div>
          <el-form-item label="职责说明"
            ><el-input
              v-model="form.description"
              maxlength="255"
              placeholder="说明这个角色负责的工作"
          /></el-form-item>
          <div class="permission-intro">
            <h3>模块操作与字段范围</h3>
            <p>
              先开启模块查看权限，再选择可操作事项和字段。字段可编辑还需要对应操作权限。
            </p>
            <div class="permission-legend">
              <span><i class="legend-hidden"></i>隐藏：不可见</span
              ><span><i class="legend-read"></i>只读：可以查看</span
              ><span><i class="legend-write"></i>可编辑：允许修改</span>
            </div>
          </div>
          <div class="permission-toolbar">
            <el-input
              v-model="moduleFilter"
              clearable
              placeholder="查找权限模块"
              aria-label="查找权限模块"
            />
            <span
              >{{ form.permissions.length }} 项操作 ·
              {{ selectedFieldCount }} 个字段</span
            >
          </div>
          <el-collapse v-model="expanded" accordion class="permission-modules">
            <el-collapse-item
              v-for="(definition, module) in shownModules"
              :key="module"
              :name="String(module)"
            >
              <template #title
                ><div class="permission-module-heading">
                  <strong>{{ definition.label }}</strong><span
                    >{{
                      form.permissions.filter((p: string) =>
                        p.startsWith(`${module}.`),
                      ).length
                    }}
                    项操作 ·
                    {{ Object.keys(form.fields[module] || {}).length }}
                    个字段</span
                  >
                </div></template
              >
              <div class="permission-module-body">
                <p class="permission-label">允许的操作</p>
                <el-checkbox-group
                  :model-value="
                    form.permissions.filter((p: string) =>
                      p.startsWith(`${module}.`),
                    )
                  "
                  @update:model-value="moduleActions(String(module), $event)"
                >
                  <el-checkbox
                    v-for="(label, action) in definition.actions"
                    :key="action"
                    :value="`${module}.${action}`"
                    >{{ label }}</el-checkbox
                  >
                </el-checkbox-group>
                <div class="permission-fields-heading">
                  <strong>字段权限</strong>
                  <div class="permission-bulk-actions">
                    <el-button
                      text
                      type="primary"
                      @click="readAll(String(module))"
                      >全部设为只读</el-button
                    ><el-button text @click="clearModule(String(module))"
                      >清空模块授权</el-button
                    >
                  </div>
                </div>
                <div
                  v-for="(label, field) in definition.fields"
                  :key="field"
                  class="permission-field-row"
                >
                  <span :id="`permission-label-${module}-${field}`"
                    >{{ label }}<small>{{ field }}</small></span
                  >
                  <el-select
                    :aria-label="`${label}权限`"
                    :aria-labelledby="`permission-label-${module}-${field}`"
                    :model-value="form.fields[module]?.[field] || 'hidden'"
                    :disabled="!form.permissions.includes(`${module}.read`)"
                    @update:model-value="
                      setField(String(module), String(field), $event)
                    "
                  >
                    <el-option value="hidden" label="隐藏" /><el-option
                      value="read"
                      label="只读"
                    /><el-option
                      v-if="definition.writable.includes(String(field))"
                      value="write"
                      label="可编辑"
                    />
                  </el-select>
                </div>
              </div>
            </el-collapse-item>
          </el-collapse>
          <el-empty
            v-if="!Object.keys(shownModules).length"
            description="没有匹配的权限模块"
            :image-size="60"
          />
        </template>
        <template v-else>
          <el-form-item
            label="管理员账号"
            required
            :error="fieldErrors.username"
            ><el-input
              v-model="form.username"
              maxlength="32"
              autocomplete="off"
              placeholder="4–32 位字母、数字或下划线"
          /></el-form-item>
          <el-form-item
            label="显示名称"
            required
            :error="fieldErrors.display_name"
            ><el-input
              v-model="form.display_name"
              maxlength="100"
              placeholder="例如：内容运营小林"
          /></el-form-item>
          <el-form-item
            :label="form.id ? '重置密码' : '登录密码'"
            :required="!form.id"
            :error="fieldErrors.password"
            ><el-input
              v-model="form.password"
              type="password"
              show-password
              autocomplete="new-password"
              maxlength="72"
              :placeholder="form.id ? '留空保留原密码' : '10–72 字节密码'"
            />
            <p class="field-help">
              重置密码后，该管理员的现有登录会话会失效。
            </p></el-form-item
          >
          <template v-if="form.is_super"
            ><div class="permission-intro">
              超级管理员拥有全部后台权限，保持启用，无需分配角色。
            </div></template
          >
          <template v-else>
            <el-form-item label="所属角色"
              ><el-select
                v-model="form.role_ids"
                multiple
                placeholder="选择一个或多个角色"
                ><el-option
                  v-for="role in roles"
                  :key="role.id"
                  :label="role.name + (role.status ? '' : '（已停用）')"
                  :value="Number(role.id)"
              /></el-select>
              <p class="field-help">
                未分配有效角色时，可以登录但无法访问业务页面。
              </p></el-form-item
            >
            <el-form-item label="账号状态" :error="fieldErrors.status"
              ><el-select v-model="form.status"
                ><el-option :value="1" label="启用" /><el-option
                  :value="0"
                  label="停用" /></el-select
            ></el-form-item>
          </template>
        </template>
      </el-form>
      <template #footer
        ><el-button :disabled="saving" @click="drawer = false">取消</el-button
        ><el-button type="primary" :loading="saving" @click="save">{{
          isRoles ? "保存角色权限" : "保存管理员"
        }}</el-button></template
      >
    </el-drawer>
    <AdminRecordDetails
      :record="details"
      :title="`${isRoles ? '角色' : '管理员'}详情 #${details?.id || ''}`"
      @close="details = null"
    />
  </div>
</template>
