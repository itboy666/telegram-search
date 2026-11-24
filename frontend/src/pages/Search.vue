<script setup lang="ts">
import {
  computed,
  onBeforeUnmount,
  onMounted,
  reactive,
  ref,
  watch,
} from "vue";
import { useRoute, useRouter } from "vue-router";
import { Search, TopRight, Star, Promotion } from "../lib/icons";
import { ElMessage } from "element-plus";
import { api, ApiError, query, types } from "../lib/api";
import { useAuth } from "../lib/auth";
import LoadError from "../components/LoadError.vue";
import SourceAttribution from "../components/SourceAttribution.vue";
import IosIcon from "../components/IosIcon.vue";
const route = useRoute(),
  router = useRouter(),
  auth = useAuth();
const filters = reactive({
    q: "",
    type: "",
    category_id: "",
    page: 1,
    page_size: 12,
    search_token: "",
  }),
  config = ref<any>({
    site_name: "星索",
    site_description: "发现值得收藏的 Telegram 资源",
  }),
  categories = ref<any[]>([]),
  saved = ref<number[]>([]),
  saving = ref<number[]>([]),
  detail = ref<any>(null);
const data = ref<any>({ items: [], total: 0, window_total: 0, duration_ms: 0 });
const loading = ref(false),
  error = ref(""),
  snapshotExpired = ref(false);
const displayQuery = ref("");
let requestVersion = 0,
  reflectedRoute = "";
let disposed = false,
  snapshotCriteria = "";
const criteria = (value: typeof filters) =>
  JSON.stringify([value.q.trim(), value.type, value.category_id]);
const pageTotal = computed(() =>
  Number(data.value.window_total ?? Math.min(data.value.total, 1200)),
);
function routeValues(values: Record<string, unknown> = filters) {
  const result: Record<string, string> = {};
  for (const [key, value] of Object.entries(values))
    if (value && key !== "page_size" && !(key === "page" && value === 1))
      result[key] = String(value);
  return result;
}
function routeKey(value: Record<string, unknown>) {
  return JSON.stringify(
    Object.entries(value).sort(([a], [b]) => a.localeCompare(b)),
  );
}
async function load() {
  if (disposed) return;
  const version = ++requestVersion;
  loading.value = true;
  error.value = "";
  snapshotExpired.value = false;
  const requested = { ...filters };
  const requestedUser = auth.users.user?.id;
  try {
    const result = auth.users.user
      ? await api("/user/api/commerce/search", requested)
      : await api("/api/public/search?" + query(requested));
    if (version !== requestVersion) return;
    data.value = result;
    displayQuery.value = requested.q;
    snapshotCriteria = criteria(requested);
    filters.search_token = result.search_token || "";
    if (result.page) filters.page = result.page;
    const nextQuery = routeValues({
      ...requested,
      search_token: filters.search_token,
      page: result.page || requested.page,
    });
    if (routeKey(route.query) !== routeKey(nextQuery)) {
      reflectedRoute = routeKey(nextQuery);
      await router.replace({ path: "/", query: nextQuery });
    }
    if (version !== requestVersion) return;
    if (result.reward)
      ElMessage.success(`首次有效搜索奖励 ${result.reward.amount} USDT 已到账`);
    saved.value = [];
    if (requestedUser && result.items.length) {
      const ids = result.items.map((r: any) => r.id).join(",");
      const favoriteIDs = await api<number[]>(
        "/user/api/favorite-ids?ids=" + ids,
      ).catch(() => []);
      if (version !== requestVersion || requestedUser !== auth.users.user?.id)
        return;
      saved.value = favoriteIDs;
    }
  } catch (e) {
    if (version !== requestVersion) return;
    error.value = (e as Error).message;
    snapshotExpired.value =
      e instanceof ApiError && [409, 410].includes(e.status);
  } finally {
    if (version === requestVersion) loading.value = false;
  }
}
const topAds = computed(() =>
  Object.values<any>(data.value.ads || {}).filter(
    (ad) => ad.kind !== "search_bottom",
  ),
);
const typeIcons: Record<string, string> = {
  channel: "channel",
  group: "group",
  bot: "bot",
  video: "video",
  article: "article",
  audio: "audio",
  file: "file",
  shop: "shop",
  other: "other",
  website: "link",
};
function quickSearch(value: string) {
  filters.q = value;
  search();
}
function sync() {
  if (reflectedRoute === routeKey(route.query)) {
    reflectedRoute = "";
    return;
  }
  reflectedRoute = "";
  filters.q = String(route.query.q || "");
  filters.type = String(route.query.type || "");
  filters.category_id = String(route.query.category_id || "");
  filters.page = Math.max(1, Number(route.query.page) || 1);
  filters.search_token = String(route.query.search_token || "");
  load();
}
watch(() => route.query, sync);
function search(reset = true) {
  // Invalidate a pending response before navigation can commit a new query.
  requestVersion++;
  reflectedRoute = "";
  if (!reset && snapshotCriteria !== criteria(filters)) reset = true;
  if (reset) {
    filters.page = 1;
    filters.search_token = "";
  }
  const q = routeValues();
  if (routeKey(route.query) === routeKey(q)) load();
  else router.push({ path: "/", query: q });
}
async function favorite(r: any) {
  if (!auth.users.user && !(await auth.ensure("user"))) {
    router.push({ path: "/user/login", query: { next: "/user/collections" } });
    return;
  }
  saving.value.push(r.id);
  try {
    await api(`/user/api/favorites/${r.id}`, {
      saved: !saved.value.includes(r.id),
    });
    saved.value = saved.value.includes(r.id)
      ? saved.value.filter((i) => i !== r.id)
      : [...saved.value, r.id];
    ElMessage.success(saved.value.includes(r.id) ? "已加入收藏" : "已取消收藏");
  } catch (e) {
    ElMessage.error((e as Error).message);
  } finally {
    saving.value = saving.value.filter((i) => i !== r.id);
  }
}
onMounted(async () => {
  await auth.ensure("user");
  if (disposed) return;
  sync();
  try {
    [config.value, categories.value] = await Promise.all([
      api("/api/public/config"),
      api("/api/public/categories"),
    ]);
  } catch (e) {
    ElMessage.error((e as Error).message);
  }
});
onBeforeUnmount(() => {
  disposed = true;
  requestVersion++;
});
</script>
<template>
  <div class="search-page">
    <section class="search-hero">
      <div class="search-hero-copy">
        <span class="hero-kicker"
          ><span></span>DISCOVER · CONNECT · EXPLORE</span
        >
        <h1>让有价值的内容，<br /><span>与你相遇。</span></h1>
        <p>
          {{ config.site_description }}。从一个关键词开始，连接更广阔的世界。
        </p>
        <form class="hero-search" @submit.prevent="search()">
          <el-icon><Search /></el-icon
          ><input
            v-model="filters.q"
            aria-label="搜索 Telegram 资源与网站"
            maxlength="120"
            placeholder="搜索频道、群组、机器人与更多…"
          /><el-button type="primary" native-type="submit" :loading="loading"
            >开始探索 <el-icon><TopRight /></el-icon
          ></el-button>
        </form>
        <div class="search-suggestions">
          <span>试试搜索</span
          ><button
            v-for="term in ['人工智能', '开发', '设计', '音乐']"
            :key="term"
            @click="quickSearch(term)"
          >
            {{ term }}<IosIcon name="right" />
          </button>
        </div>
        <div class="hero-bottom">
          <span
            >{{
              Number(config.resource_count ?? 0).toLocaleString()
            }}
            个公开资源，等你发现</span
          ><a
            v-if="config.bot_username"
            :href="'https://t.me/' + config.bot_username"
            target="_blank"
            rel="noopener noreferrer"
            ><el-icon><Promotion /></el-icon>在 Telegram 搜索 <span>↗</span></a
          >
        </div>
      </div>
      <div class="search-network" aria-hidden="true">
        <div class="network-grid"></div>
        <span class="network-orbit orbit-outer"></span>
        <span class="network-orbit orbit-inner"></span>
        <div class="network-center">
          <IosIcon name="search" /><strong>STARINDEX</strong
          ><small>发现的连接点</small>
        </div>
        <div class="network-node node-channel">
          <span><IosIcon name="channel" /></span>
          <div><strong>优质频道</strong><small>CHANNELS</small></div>
        </div>
        <div class="network-node node-bot">
          <span><IosIcon name="bot" /></span>
          <div><strong>实用机器人</strong><small>SMART TOOLS</small></div>
        </div>
        <div class="network-node node-group">
          <span><IosIcon name="group" /></span>
          <div><strong>兴趣社群</strong><small>COMMUNITIES</small></div>
        </div>
        <div class="network-node node-web">
          <span><IosIcon name="link" /></span>
          <div><strong>更多好内容</strong><small>EXPLORE MORE</small></div>
        </div>
        <span class="network-caption"><span></span>将内容，连接成可能</span>
      </div>
    </section>
    <div class="discovery-strip">
      <span><IosIcon name="search" />一个入口，探索多个领域</span>
      <span><IosIcon name="star" />收藏值得留下的灵感</span>
      <router-link to="/user/security"
        ><IosIcon name="telegram" />连接 Telegram 账户<IosIcon name="right"
      /></router-link>
    </div>
    <section class="catalog">
      <div
        v-if="!loading && !error && topAds.length"
        class="search-promotions"
        aria-label="推广内容"
      >
        <a
          v-for="ad in topAds"
          :key="ad.id"
          :href="ad.url"
          target="_blank"
          rel="noopener noreferrer"
          ><el-tag size="small" type="info">广告</el-tag
          ><span>{{ ad.title }}</span
          ><el-icon><TopRight /></el-icon
        ></a>
      </div>
      <div class="catalog-top">
        <div>
          <span class="eyebrow">THE RESOURCE COLLECTION</span>
          <h2>{{ displayQuery ? "搜索结果" : "从这里，发现更多" }}</h2>
        </div>
        <router-link to="/user/collections" class="text-link"
          >分享一个好资源 <el-icon><TopRight /></el-icon
        ></router-link>
      </div>
      <div class="catalog-filters">
        <div class="type-tabs" role="group" aria-label="资源类型">
          <button
            :class="{ active: !filters.type }"
            :aria-pressed="!filters.type"
            @click="
              filters.type = '';
              search();
            "
          >
            全部资源</button
          ><button
            v-for="(name, key) in types"
            :key="key"
            :class="{ active: filters.type === key }"
            :aria-pressed="filters.type === key"
            @click="
              filters.type = String(key);
              search();
            "
          >
            {{ name }}
          </button>
        </div>
        <div class="secondary-filters">
          <span
            >{{ Number(data.total).toLocaleString() }} 条结果<span
              v-if="displayQuery"
            >
              · “{{ displayQuery }}”</span
            ></span
          >
          <div>
            <el-select
              v-model="filters.category_id"
              placeholder="全部分类"
              aria-label="资源分类"
              clearable
              @change="search()"
              ><el-option
                v-for="c in categories"
                :key="c.id"
                :label="c.name"
                :value="String(c.id)" /></el-select
            ><el-button :disabled="loading" @click="search()"
              ><IosIcon name="refresh" />换一组</el-button
            >
          </div>
        </div>
        <div class="search-order-note">
          <span
            ><IosIcon name="refresh" />结果随机展示，翻页时保持顺序。</span
          >
          <span v-if="pageTotal < Number(data.total)"
            >本次可浏览
            {{ pageTotal.toLocaleString() }} 条，换一组发现更多</span
          >
        </div>
      </div>
      <LoadError
        :message="error"
        @retry="snapshotExpired ? search() : load()"
      /><el-skeleton v-if="loading" :rows="6" animated />
      <div v-else-if="!error" class="resource-grid">
        <article v-for="r in data.items" :key="r.id" class="public-resource">
          <div class="resource-card-top">
            <span
              class="resource-avatar large"
              :class="`resource-type-${r.type}`"
              ><IosIcon :name="typeIcons[r.type] || 'other'" /></span
            ><span class="type-chip">{{ types[r.type] }}</span
            ><span v-if="r.featured" class="featured-label">精选</span>
          </div>
          <h3>
            <button :title="r.title" @click="detail = r">{{ r.title }}</button>
          </h3>
          <p>{{ r.description || "这个资源还没有简介，打开链接了解更多。" }}</p>
          <div class="resource-tags">
            <span>{{ r.category_name || "未分类" }}</span><span
              v-if="r.members !== null"
              >{{ Number(r.members).toLocaleString() }} 位成员</span
            >
          </div>
          <div class="resource-card-bottom">
            <a :href="r.url" target="_blank" rel="noopener noreferrer"
              >{{ r.type === "website" ? "访问网站" : "打开 Telegram" }}
              <el-icon><TopRight /></el-icon></a
            ><el-button
              :icon="Star"
              :loading="saving.includes(r.id)"
              :type="saved.includes(r.id) ? 'primary' : undefined"
              text
              :aria-label="
                saved.includes(r.id) ? `取消收藏${r.title}` : `收藏${r.title}`
              "
              @click="favorite(r)"
            />
          </div>
        </article>
      </div>
      <SourceAttribution v-if="!loading && !error" :items="data.items" />
      <el-empty
        v-if="!loading && !data.items.length && !error"
        :description="
          Number(data.total) > 0
            ? '本页资源已下架，可继续翻页或换一组发现更多。'
            : '暂时没有找到，试试更短的关键词或其他分类'
        "
        ><el-button
          @click="
            if (!Number(data.total)) {
              filters.q = '';
              filters.type = '';
              filters.category_id = '';
            }
            search();
          "
          >{{ Number(data.total) > 0 ? "换一组" : "浏览全部资源" }}</el-button
        ></el-empty
      >
      <div v-if="!error && pageTotal > 12" class="pagination">
        <span>每页 12 条</span
        ><el-pagination
          v-model:current-page="filters.page"
          :total="pageTotal"
          :page-size="12"
          :pager-count="5"
          layout="prev, pager, next"
          background
          @current-change="search(false)"
        />
      </div>
      <div
        v-if="!loading && !error && data.ads?.search_bottom"
        class="search-promotions"
        aria-label="底部推广"
      >
        <a
          :href="data.ads.search_bottom.url"
          target="_blank"
          rel="noopener noreferrer"
          ><el-tag size="small" type="info">广告</el-tag
          ><span>{{ data.ads.search_bottom.title }}</span
          ><el-icon><TopRight /></el-icon
        ></a>
      </div>
      <div class="share-banner">
        <span class="share-symbol"
          ><el-icon><Promotion /></el-icon
        ></span>
        <div>
          <h3>发现了好资源？分享给更多人。</h3>
          <p>提交公开链接，让每一份有价值的内容被看见。</p>
        </div>
        <router-link to="/user/collections"
          ><el-button
            >提交资源 <el-icon><TopRight /></el-icon></el-button
        ></router-link>
      </div>
    </section>
    <el-dialog
      :model-value="!!detail"
      title="资源详情"
      width="560px"
      @close="detail = null"
      ><template v-if="detail"
        ><span class="type-chip">{{ types[detail.type] }}</span>
        <h2>{{ detail.title }}</h2>
        <p class="detail-description">{{ detail.description || "暂无简介" }}</p>
        <p class="muted">{{ detail.tags }}</p>
        <SourceAttribution :items="[detail]" />
        <a
          :href="detail.url"
          target="_blank"
          rel="noopener noreferrer"
          class="break-word"
          >{{ detail.url }} ↗</a
        ></template
      ></el-dialog
    >
  </div>
</template>

<style scoped>
.search-order-note {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 8px 24px;
  padding: 16px 0 4px;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.7;
}
.search-order-note > span:first-child {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}
.search-order-note .tg-ios-icon {
  flex-shrink: 0;
  color: var(--primary);
  font-size: 16px;
  margin-top: 2px;
}
.secondary-filters > div {
  flex-wrap: wrap;
}
@media (max-width: 600px) {
  .search-order-note {
    display: grid;
    gap: 8px;
  }
  .secondary-filters > div {
    width: 100%;
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 12px;
  }
  .secondary-filters :deep(.el-select) {
    width: 100%;
    min-width: 0;
  }
  .secondary-filters .el-button {
    margin: 0;
    min-height: 44px;
  }
}
</style>
