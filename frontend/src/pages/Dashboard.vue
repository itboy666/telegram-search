<script setup lang="ts">
import { onMounted, computed } from "vue";
import {
  Collection,
  Search,
  DocumentChecked,
  User,
  Plus,
  ArrowRight,
  Promotion,
  Clock,
} from "../lib/icons";
import { api, dateTime } from "../lib/api";
import { useLoad } from "../lib/load";
import LoadError from "../components/LoadError.vue";
import { useAuth } from "../lib/auth";
import IosIcon from "../components/IosIcon.vue";
const auth = useAuth();
const today = new Intl.DateTimeFormat("zh-CN", {
  month: "long",
  day: "numeric",
  weekday: "long",
  timeZone: "Asia/Shanghai",
}).format(new Date());
const { data, loading, error, load } = useLoad<any>(null, () =>
  api("/admin/api/dashboard"),
);
onMounted(load);
const metrics = [
  {
    key: "resources",
    title: "已收录资源",
    hint: "已发布的公开资源",
    icon: Collection,
    color: "blue",
  },
  {
    key: "searches_today",
    title: "今日搜索",
    hint: "网站、API 与机器人",
    icon: Search,
    color: "purple",
  },
  {
    key: "pending",
    title: "待审核申请",
    hint: "等待你的审核",
    icon: DocumentChecked,
    color: "orange",
  },
  {
    key: "users",
    title: "注册用户",
    hint: "包含 Telegram 用户",
    icon: User,
    color: "green",
  },
];
const total = computed(
  () => data.value?.trend?.reduce((n: number, r: any) => n + r.count, 0) || 0,
);
const max = computed(() =>
  Math.max(1, ...(data.value?.trend?.map((r: any) => r.count) || [])),
);
</script>
<template>
  <div>
    <div class="page-heading">
      <div>
        <span class="eyebrow">WORKSPACE OVERVIEW</span>
        <h1>工作台概览</h1>
        <p>{{ today }} · 欢迎回来，{{ auth.users.admin?.display_name }}。</p>
      </div>
      <router-link
        v-if="auth.can('resources.create')"
        to="/admin/resources?create=1"
        ><el-button type="primary" :icon="Plus"
          >添加资源</el-button
        ></router-link
      >
    </div>
    <section class="dashboard-welcome">
      <div>
        <span>YOUR CONNECTED WORKSPACE</span>
        <h2>让每一次发现，都更有价值。</h2>
        <p>内容收录、用户服务与业务运营，在这里一目了然。</p>
      </div>
      <div class="welcome-mark" aria-hidden="true">
        <IosIcon name="telegram" /><span></span><span></span>
      </div>
      <router-link
        v-if="auth.can('submissions.read')"
        to="/admin/submissions"
        class="welcome-action"
        >处理收录申请<IosIcon name="right"
      /></router-link>
    </section>
    <LoadError :message="error" @retry="load" /><el-skeleton
      v-if="loading && !data"
      :rows="10"
      animated
    /><template v-if="data"
      ><div v-if="data.stats" class="stats-grid">
        <section v-for="m in metrics" :key="m.key" class="metric-card">
          <div class="metric-top">
            <span>{{ m.title }}</span><span class="metric-icon" :class="m.color"
              ><el-icon><component :is="m.icon" /></el-icon
            ></span>
          </div>
          <strong>{{ Number(data.stats[m.key]).toLocaleString() }}</strong>
          <p><span class="tiny-line" :class="m.color"></span>{{ m.hint }}</p>
        </section>
      </div>
      <div class="dashboard-grid">
        <section v-if="data.trend" class="panel trend-panel">
          <div class="panel-heading">
            <div>
              <h2>搜索趋势</h2>
              <p>最近 7 天的搜索活动</p>
            </div>
            <span class="subtle-pill">近 7 天</span>
          </div>
          <div class="trend-total">
            <strong>{{ total }}</strong><span>次搜索</span
            ><span class="chart-legend">搜索次数</span>
          </div>
          <div
            v-if="total"
            class="bar-chart"
            role="img"
            :aria-label="
              data.trend.map((d: any) => `${d.date}：${d.count}次`).join('，')
            "
          >
            <div v-for="d in data.trend" :key="d.date" class="bar-col">
              <span>{{ d.count }}</span>
              <div class="bar-track">
                <div
                  class="bar-fill"
                  :style="{ height: `${(d.count / max) * 100}%` }"
                ></div>
              </div>
              <small>{{ d.date.slice(5) }}</small>
            </div>
          </div>
          <div v-else class="chart-empty">
            <span class="empty-chart-icon"
              ><el-icon><Search /></el-icon></span
            ><strong>等待第一份搜索数据</strong>
            <p>用户开始搜索后，趋势会在这里呈现。</p>
            <router-link to="/">去网站体验搜索 <span>↗</span></router-link>
          </div>
          <p class="panel-footnote">首次关键词搜索计入统计，翻页不重复计数。</p>
        </section>
        <section class="panel quick-panel">
          <div class="panel-heading">
            <div>
              <h2>把工作安排好</h2>
              <p>从这里开始今天的管理</p>
            </div>
          </div>
          <router-link
            v-if="auth.can('submissions.read')"
            to="/admin/submissions"
            class="quick-action"
            ><span class="quick-icon orange"
              ><el-icon><DocumentChecked /></el-icon
            ></span>
            <div>
              <strong>收录审核</strong
              ><small>{{
                data.stats?.pending
                  ? `${data.stats.pending} 条申请等待处理`
                  : "暂时没有待审核申请"
              }}</small>
            </div>
            <el-icon><ArrowRight /></el-icon></router-link
          ><router-link
            v-if="auth.can('categories.read')"
            to="/admin/categories"
            class="quick-action"
            ><span class="quick-icon blue"
              ><el-icon><Collection /></el-icon
            ></span>
            <div>
              <strong>整理资源分类</strong><small>让内容更容易被发现</small>
            </div>
            <el-icon><ArrowRight /></el-icon
          ></router-link>
          <div v-if="data.bot" class="bot-mini">
            <div>
              <span class="bot-symbol"
                ><el-icon><Promotion /></el-icon></span
              ><el-tag
                v-if="data.bot.running !== undefined"
                :type="data.bot.running ? 'success' : 'info'"
                effect="light"
                >{{ data.bot.running ? "运行中" : "未运行" }}</el-tag
              >
            </div>
            <h3>Telegram 机器人</h3>
            <p>
              {{
                data.bot.username ? "@" + data.bot.username : "还未连接机器人"
              }}
            </p>
            <router-link to="/admin/bot"
              >查看连接详情 <el-icon><ArrowRight /></el-icon
            ></router-link>
          </div>
        </section>
      </div>
      <div class="dashboard-grid lower">
        <section v-if="data.recent_resources" class="panel">
          <div class="panel-heading">
            <h2>最近收录</h2>
            <router-link to="/admin/resources" class="text-link"
              >查看全部 <el-icon><ArrowRight /></el-icon
            ></router-link>
          </div>
          <div
            v-for="r in data.recent_resources"
            :key="r.id"
            class="recent-row"
          >
            <span class="resource-avatar">{{
              Array.from(r.title || "")[0] || "#"
            }}</span>
            <div>
              <strong
                ><span class="database-id">#{{ r.id }}</span>
                {{ r.title || "资源" }}</strong
              ><small>{{ r.url }}</small>
            </div>
            <el-tag v-if="r.status" type="success" effect="plain"
              >已发布</el-tag
            >
          </div>
          <el-empty
            v-if="!data.recent_resources.length"
            description="还没有资源，添加你的第一条资源吧"
            :image-size="70"
          />
        </section>
        <section v-if="data.activity" class="panel">
          <div class="panel-heading">
            <h2>最近动态</h2>
            <el-icon class="muted"><Clock /></el-icon>
          </div>
          <div v-if="data.activity.length" class="activity-list">
            <div v-for="a in data.activity.slice(0, 4)" :key="a.id">
              <span class="activity-dot"></span>
              <div>
                <strong
                  ><span class="database-id">#{{ a.id }}</span>
                  {{ a.action }}</strong
                >
                <p>{{ a.detail }}</p>
                <small>{{ dateTime(a.created_at) }}</small>
              </div>
            </div>
          </div>
          <el-empty
            v-else
            description="操作动态将在这里记录"
            :image-size="70"
          />
        </section></div
    ></template>
  </div>
</template>
