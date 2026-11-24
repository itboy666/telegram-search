<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { useRoute } from "vue-router";
import { ElMessage, ElMessageBox } from "element-plus";
import { Plus, Search, Refresh } from "../lib/icons";
import { api, query, types, statuses, dateTime, ApiError } from "../lib/api";
import { useLoad } from "../lib/load";
import LoadError from "../components/LoadError.vue";
import ResourceForm from "../components/ResourceForm.vue";
import AdminIdColumn from "../components/AdminIdColumn.vue";
import AdminRowActions from "../components/AdminRowActions.vue";
import AdminRecordDetails from "../components/AdminRecordDetails.vue";
import { useAuth } from "../lib/auth";
const auth = useAuth();
const filters = reactive({
    id: "",
    q: "",
    type: "",
    status: "",
    category_id: "",
    page: 1,
    page_size: 12,
  }),
  categories = ref<any[]>([]),
  route = useRoute();
const { data, loading, error, load } = useLoad({ items: [], total: 0 }, () =>
  api("/admin/api/resources?" + query(filters)),
);
const drawer = ref(false),
  details = ref<Record<string, any> | null>(null),
  busy = ref(false),
  saving = ref(false),
  editId = ref<number>(),
  form = reactive<Record<string, any>>({}),
  formError = ref(""),
  fields = ref<Record<string, string>>({});
async function edit(r?: any) {
  if (r) {
    busy.value = true;
    try {
      r = await api(`/admin/api/resources/${r.id}`);
    } catch (e) {
      ElMessage.error((e as Error).message);
      return;
    } finally {
      busy.value = false;
    }
  }
  editId.value = r?.id;
  Object.keys(form).forEach((k) => delete form[k]);
  Object.assign(
    form,
    r
      ? {
          ...r,
          category_id: r.category_id ? Number(r.category_id) : undefined,
          featured: Number(r.featured),
          members: r.members === null ? undefined : Number(r.members),
        }
      : {
          title: "",
          url: "",
          type: "channel",
          status: "published",
          status_reason: "",
          category_id: undefined,
          description: "",
          tags: "",
          featured: 0,
          members: undefined,
        },
  );
  fields.value = {};
  formError.value = "";
  drawer.value = true;
}
async function save() {
  saving.value = true;
  formError.value = "";
  fields.value = {};
  try {
    await api(
      "/admin/api/resources" + (editId.value ? "/" + editId.value : ""),
      auth.writable("resources", {
        ...form,
        members: form.members ?? null,
        category_id: form.category_id ?? null,
      }),
    );
    drawer.value = false;
    ElMessage.success("资源已保存");
    await load();
  } catch (e) {
    formError.value = (e as Error).message;
    if (e instanceof ApiError) fields.value = e.fields;
  } finally {
    saving.value = false;
  }
}
async function archive(r: any) {
  if (busy.value) return;
  try {
    await ElMessageBox.confirm(
      `归档「${r.title}」后，网站和机器人将不再展示。你可以随时重新发布。`,
      "归档资源",
      {
        confirmButtonText: "确认归档",
        cancelButtonText: "取消",
        type: "warning",
      },
    );
    busy.value = true;
    await api(`/admin/api/resources/${r.id}/archive`, {});
    ElMessage.success("已归档");
    await load();
  } catch (e) {
    if (e instanceof Error) ElMessage.error(e.message);
  } finally {
    busy.value = false;
  }
}
async function view(row: any) {
  busy.value = true;
  try {
    details.value = await api(`/admin/api/resources/${row.id}`);
  } catch (e) {
    ElMessage.error((e as Error).message);
  } finally {
    busy.value = false;
  }
}
function rowActions(row: any) {
  const actions = [];
  if (
    auth.can("resources.archive") &&
    auth.writeField("resources", "status") &&
    row.status !== "archived"
  )
    actions.push({ command: "archive", label: "归档资源" });
  if (auth.can("resources.delete"))
    actions.push({ command: "delete", label: "删除资源", danger: true });
  return actions;
}
async function remove(row: any) {
  if (busy.value) return;
  try {
    await ElMessageBox.confirm(
      `确认永久删除资源 #${row.id}「${row.title || "未显示名称"}」？相关收藏将同时清除，删除后无法恢复。已封禁、已失效或有关联收录历史的资源不能删除。`,
      "删除资源",
      {
        confirmButtonText: "确认删除",
        cancelButtonText: "取消",
        type: "warning",
      },
    );
    busy.value = true;
    await api(`/admin/api/resources/${row.id}`, {}, "DELETE");
    if (data.value.items.length === 1 && filters.page > 1) filters.page--;
    ElMessage.success("资源已删除");
    await load();
  } catch (e) {
    if (e instanceof Error) ElMessage.error(e.message);
  } finally {
    busy.value = false;
  }
}
function search() {
  filters.page = 1;
  load();
}
function reset() {
  Object.assign(filters, {
    id: "",
    q: "",
    type: "",
    status: "",
    category_id: "",
    page: 1,
  });
  load();
}
onMounted(async () => {
  try {
    if (auth.readField("resources", "category_id"))
      categories.value = await api("/admin/api/lookups/categories");
  } catch (e) {
    ElMessage.error((e as Error).message);
  }
  load();
  if (route.query.create && auth.can("resources.create")) edit();
});
</script>
<template>
  <div>
    <div class="page-heading">
      <div>
        <span class="eyebrow">RESOURCE LIBRARY</span>
        <h1>资源管理</h1>
        <p>让每一份内容清晰归位，连接更多有价值的发现。</p>
      </div>
      <el-button
        v-if="auth.can('resources.create')"
        type="primary"
        :icon="Plus"
        @click="edit()"
        >添加资源</el-button
      >
    </div>
    <LoadError :message="error" @retry="load" />
    <section class="panel table-panel">
      <form class="filter-bar resource-filters" @submit.prevent="search">
        <el-input
          v-model="filters.id"
          class="id-filter"
          inputmode="numeric"
          placeholder="精确编号"
          aria-label="数据库 ID"
          clearable
          ><template #prepend>ID</template></el-input
        >
        <el-input
          v-if="
            ['title', 'description', 'tags', 'url'].some((field) =>
              auth.readField('resources', field),
            )
          "
          v-model="filters.q"
          placeholder="搜索名称、链接或关键词"
          aria-label="搜索资源"
          :prefix-icon="Search"
          clearable
          class="search-input"
        /><el-select
          v-if="auth.readField('resources', 'type')"
          v-model="filters.type"
          placeholder="全部类型"
          aria-label="资源类型"
          clearable
          @change="search"
          ><el-option
            v-for="(name, key) in types"
            :key="key"
            :label="name"
            :value="key" /></el-select
        ><el-select
          v-if="auth.readField('resources', 'status')"
          v-model="filters.status"
          placeholder="全部状态"
          aria-label="发布状态"
          clearable
          @change="search"
          ><el-option
            v-for="key in [
              'published',
              'hidden',
              'archived',
              'blocked',
              'invalid',
            ]"
            :key="key"
            :label="statuses[key]"
            :value="key" /></el-select
        ><el-select
          v-if="auth.readField('resources', 'category_id')"
          v-model="filters.category_id"
          placeholder="全部分类"
          aria-label="资源分类"
          class="category-filter"
          clearable
          @change="search"
          ><el-option
            v-for="c in categories"
            :key="c.id"
            :label="c.name"
            :value="c.id" /></el-select
        ><el-button native-type="submit" :icon="Search">搜索</el-button
        ><el-button :icon="Refresh" @click="reset">重置</el-button>
      </form>
      <div class="table-summary">
        <strong
          >全部资源 <span>{{ data.total }}</span></strong
        ><span>管理已发布、下架与归档的内容</span>
      </div>
      <el-table
        v-loading="loading"
        :data="data.items"
        empty-text="没有匹配的资源，试试调整筛选条件"
        class="resource-table"
        ><AdminIdColumn /><el-table-column label="资源信息" min-width="260"
          ><template #default="{ row }"
            ><div class="resource-cell">
              <span class="resource-avatar">{{
                Array.from(row.title || "")[0] || "#"
              }}</span>
              <div>
                <strong>{{ row.title || `资源 #${row.id}` }}</strong><a
                  v-if="row.url"
                  :href="row.url"
                  target="_blank"
                  rel="noopener noreferrer"
                  >{{ row.url }}</a
                >
              </div>
            </div></template
          ></el-table-column
        ><el-table-column
          v-if="auth.readField('resources', 'type')"
          label="类型"
          width="96"
          ><template #default="{ row }"
            ><span class="type-chip">{{ types[row.type] }}</span></template
          ></el-table-column
        ><el-table-column
          v-if="auth.readField('resources', 'category_id')"
          prop="category_name"
          label="分类"
          min-width="110"
          ><template #default="{ row }">{{
            row.category_name || "未分类"
          }}</template></el-table-column
        ><el-table-column
          v-if="auth.readField('resources', 'status')"
          label="状态"
          width="100"
          ><template #default="{ row }"
            ><span class="status-dot" :class="row.status"></span
            >{{ statuses[row.status] }}</template
          ></el-table-column
        ><el-table-column
          v-if="auth.readField('resources', 'updated_at')"
          label="更新时间"
          min-width="155"
          ><template #default="{ row }"
            ><span class="muted">{{ dateTime(row.updated_at) }}</span></template
          ></el-table-column
        ><el-table-column
          label="操作"
          width="192"
          class-name="record-actions-column"
          ><template #default="{ row }"
            ><AdminRowActions
              :editable="auth.can('resources.update')"
              :busy="busy"
              :actions="rowActions(row)"
              @view="view(row)"
              @edit="edit(row)"
              @action="
                $event === 'archive' ? archive(row) : remove(row)
              " /></template></el-table-column
      ></el-table>
      <div class="pagination">
        <span>共 {{ data.total }} 条资源</span
        ><el-pagination
          v-model:current-page="filters.page"
          :total="data.total"
          :page-size="filters.page_size"
          layout="prev, pager, next"
          :pager-count="5"
          background
          @current-change="load"
        />
      </div>
    </section>
    <el-drawer
      v-model="drawer"
      :title="editId ? `编辑资源 #${editId}` : '添加资源'"
      size="560px"
      class="form-drawer"
      :close-on-click-modal="false"
      ><div v-if="formError" class="form-alert" role="alert">
        {{ formError }}
      </div>
      <ResourceForm
        :model="form"
        :categories="categories"
        :errors="fields"
        admin
      /><template #footer
        ><el-button @click="drawer = false">取消</el-button
        ><el-button type="primary" :loading="saving" @click="save"
          >保存资源</el-button
        ></template
      ></el-drawer
    >
    <AdminRecordDetails
      :record="details"
      :title="`资源详情 #${details?.id || ''}`"
      @close="details = null"
    />
  </div>
</template>
