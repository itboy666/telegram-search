<script setup lang="ts">
import { onMounted, reactive, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { ElMessage } from "element-plus";
import { Plus, TopRight, SwitchButton } from "../lib/icons";
import { api, types, statuses, dateTime, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useLoad } from "../lib/load";
import LoadError from "../components/LoadError.vue";
import ResourceForm from "../components/ResourceForm.vue";
import SourceAttribution from "../components/SourceAttribution.vue";
const auth = useAuth(),
  router = useRouter(),
  tab = ref("favorites"),
  page = ref(1),
  drawer = ref(false),
  saving = ref(false),
  form = reactive<Record<string, any>>({}),
  errorForm = ref(""),
  fields = ref<Record<string, string>>({}),
  categories = ref<any[]>([]);
const { data, loading, error, load } = useLoad({ items: [], total: 0 }, () =>
  api(`/user/api/${tab.value}?page=${page.value}`),
);
watch(tab, () => {
  page.value = 1;
  load();
});
onMounted(async () => {
  load();
  try {
    categories.value = await api("/api/public/categories");
  } catch (e) {
    ElMessage.error((e as Error).message);
  }
});
function submitNew() {
  Object.assign(form, {
    title: "",
    url: "",
    type: "channel",
    description: "",
    category_id: undefined,
  });
  fields.value = {};
  errorForm.value = "";
  drawer.value = true;
}
async function submit() {
  saving.value = true;
  errorForm.value = "";
  try {
    await api("/user/api/submissions", form);
    drawer.value = false;
    ElMessage.success("提交成功，等待管理员审核");
    tab.value = "submissions";
    await load();
  } catch (e) {
    errorForm.value = (e as Error).message;
    if (e instanceof ApiError) fields.value = e.fields;
  } finally {
    saving.value = false;
  }
}
async function remove(id: number) {
  try {
    await api(`/user/api/favorites/${id}`, { saved: false });
    await load();
    ElMessage.success("已取消收藏");
  } catch (e) {
    ElMessage.error((e as Error).message);
  }
}
async function logout() {
  try {
    await auth.logout("user");
    router.push("/");
  } catch (e) {
    ElMessage.error((e as Error).message);
  }
}
</script>
<template>
  <div class="user-center">
    <div class="page-heading">
      <div>
        <span class="eyebrow">YOUR SPACE</span>
        <h1>你好，{{ auth.users.user?.display_name }}</h1>
        <p>收藏喜欢的内容，也分享你的新发现。</p>
      </div>
      <div class="heading-actions">
        <router-link to="/user" class="commerce-link"
          >钱包与营销中心</router-link
        >
        <el-button :icon="SwitchButton" @click="logout">退出登录</el-button
        ><el-button type="primary" :icon="Plus" @click="submitNew"
          >提交资源</el-button
        >
      </div>
    </div>
    <div class="user-tabs">
      <button
        :class="{ active: tab === 'favorites' }"
        @click="tab = 'favorites'"
      >
        我的收藏</button
      ><button
        :class="{ active: tab === 'submissions' }"
        @click="tab = 'submissions'"
      >
        我的提交
      </button>
    </div>
    <LoadError :message="error" @retry="load" /><el-skeleton
      v-if="loading"
      :rows="6"
      animated
    />
    <div v-else-if="tab === 'favorites'" class="resource-grid">
      <article v-for="r in data.items" :key="r.id" class="public-resource">
        <span class="type-chip">{{ types[r.type] }}</span>
        <h3>{{ r.title }}</h3>
        <p>{{ r.description || "暂无简介" }}</p>
        <div class="resource-card-bottom">
          <a :href="r.url" target="_blank" rel="noopener noreferrer"
            >打开资源 <el-icon><TopRight /></el-icon></a
          ><el-button text @click="remove(r.id)">取消收藏</el-button>
        </div>
      </article>
    </div>
    <div v-else class="submission-list">
      <article v-for="s in data.items" :key="s.id" class="panel">
        <div class="submission-heading">
          <h3>{{ s.title }}</h3>
          <el-tag
            :type="
              s.status === 'approved'
                ? 'success'
                : s.status === 'pending'
                  ? 'warning'
                  : 'info'
            "
            >{{ statuses[s.status] }}</el-tag
          >
        </div>
        <a
          :href="s.url"
          target="_blank"
          rel="noopener noreferrer"
          class="break-word"
          >{{ s.url }}</a
        >
        <p>{{ s.description }}</p>
        <p v-if="s.review_note" class="review-note">
          审核备注：{{ s.review_note }}
        </p>
        <small class="muted">提交于 {{ dateTime(s.created_at) }}</small>
      </article>
    </div>
    <SourceAttribution
      v-if="tab === 'favorites' && !loading"
      :items="data.items"
    />
    <el-empty
      v-if="!loading && !data.items.length && !error"
      :description="
        tab === 'favorites'
          ? '你的收藏夹还是空的，去发现一些好内容吧。'
          : '还没有提交过资源，分享你的第一份发现吧。'
      "
      ><router-link v-if="tab === 'favorites'" to="/"
        ><el-button type="primary">发现资源</el-button></router-link
      ><el-button v-else type="primary" @click="submitNew"
        >提交资源</el-button
      ></el-empty
    >
    <div v-if="data.total > 12" class="pagination">
      <span>共 {{ data.total }} 条</span
      ><el-pagination
        v-model:current-page="page"
        :total="data.total"
        :page-size="12"
        :pager-count="5"
        layout="prev,pager,next"
        @current-change="load"
      />
    </div>
    <el-drawer
      v-model="drawer"
      title="分享一份好资源"
      size="540px"
      class="form-drawer"
      :close-on-click-modal="false"
      ><p class="drawer-intro">
        提交后将由管理员审核，通过后会出现在网站与机器人搜索中。
      </p>
      <div v-if="errorForm" class="form-alert" role="alert">
        {{ errorForm }}
      </div>
      <ResourceForm
        :model="form"
        :categories="categories"
        :errors="fields"
      /><template #footer
        ><el-button @click="drawer = false">取消</el-button
        ><el-button type="primary" :loading="saving" @click="submit"
          >提交审核</el-button
        ></template
      ></el-drawer
    >
  </div>
</template>
