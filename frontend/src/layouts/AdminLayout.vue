<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { TopRight, Expand, Close, SwitchButton, Promotion } from "../lib/icons";
import { ElMessage } from "element-plus";
import Brand from "../components/Brand.vue";
import AdminNavItem from "../components/AdminNavItem.vue";
import { useAuth } from "../lib/auth";
import {
  adminNavigation,
  filterAdminNavigation,
  findAdminNavigationTrail,
} from "../lib/adminNavigation";
const route = useRoute(),
  router = useRouter(),
  auth = useAuth(),
  open = ref(false);
const mobileQuery = window.matchMedia("(max-width: 767px)");
const mobile = ref(mobileQuery.matches);
const sidebar = ref<HTMLElement | null>(null);
const menuTrigger = ref<HTMLButtonElement | null>(null);
const nav = computed(() => filterAdminNavigation(adminNavigation, auth.can));
const currentPath = computed(() => route.path.replace(/\/+$/, ""));
const activeTrail = computed(() =>
  findAdminNavigationTrail(nav.value, currentPath.value),
);
const activeIds = computed(
  () => new Set(activeTrail.value.map((item) => item.id)),
);
const expandedIds = ref(new Set<string>());
const breadcrumbs = computed(() => {
  const parents = activeTrail.value.filter((item) => item.children);
  return [
    ...parents.map((item) => item.label),
    String(route.meta.title || "工作空间"),
  ];
});
function expandCurrentBranch() {
  const expanded = new Set(expandedIds.value);
  activeTrail.value.forEach((item) => {
    if (item.children) expanded.add(item.id);
  });
  expandedIds.value = expanded;
}
watch(activeTrail, expandCurrentBranch, { immediate: true });
function toggleGroup(id: string) {
  const expanded = new Set(expandedIds.value);
  if (expanded.has(id)) expanded.delete(id);
  else expanded.add(id);
  expandedIds.value = expanded;
}
function syncViewport(event: MediaQueryListEvent) {
  mobile.value = event.matches;
  if (!event.matches) open.value = false;
}
mobileQuery.addEventListener("change", syncViewport);
onBeforeUnmount(() => mobileQuery.removeEventListener("change", syncViewport));
async function openNavigation() {
  expandCurrentBranch();
  open.value = true;
  await nextTick();
  (
    sidebar.value?.querySelector<HTMLElement>('[aria-current="page"]') ||
    sidebar.value?.querySelector<HTMLElement>("a[href]")
  )?.focus();
}
async function closeNavigation(restoreFocus = true) {
  const wasOpen = open.value;
  open.value = false;
  await nextTick();
  if (wasOpen && mobile.value) {
    if (restoreFocus) menuTrigger.value?.focus();
    else document.querySelector<HTMLElement>("#main")?.focus();
  }
}
function navigationKeydown(event: KeyboardEvent) {
  if (!mobile.value || !open.value) return;
  if (event.key === "Escape") {
    event.preventDefault();
    closeNavigation();
  } else if (event.key === "Tab") {
    const items = Array.from(
      sidebar.value?.querySelectorAll<HTMLElement>(
        "a[href], button:not([disabled])",
      ) || [],
    ).filter((item) => item.getClientRects().length > 0);
    if (!items.length) return;
    const first = items[0]!,
      last = items[items.length - 1]!;
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }
}
async function treeKeydown(event: KeyboardEvent) {
  const target = event.target as HTMLElement;
  const row = target.closest<HTMLElement>(".nav-item");
  const tree = event.currentTarget as HTMLElement;
  if (!row || event.altKey || event.ctrlKey || event.metaKey) return;
  if (
    ![
      "ArrowDown",
      "ArrowUp",
      "Home",
      "End",
      "ArrowRight",
      "ArrowLeft",
    ].includes(event.key)
  )
    return;
  event.preventDefault();
  const rows = Array.from(
    tree.querySelectorAll<HTMLElement>(".nav-item"),
  ).filter((item) => item.getClientRects().length > 0);
  const index = rows.indexOf(row);
  let next: HTMLElement | undefined | null;
  switch (event.key) {
    case "ArrowDown":
      next = rows[Math.min(index + 1, rows.length - 1)];
      break;
    case "ArrowUp":
      next = rows[Math.max(index - 1, 0)];
      break;
    case "Home":
      next = rows[0];
      break;
    case "End":
      next = rows[rows.length - 1];
      break;
    case "ArrowRight":
      if (!row.hasAttribute("aria-expanded")) return;
      if (row.getAttribute("aria-expanded") === "false") {
        row.click();
        await nextTick();
      }
      next = row.nextElementSibling?.querySelector<HTMLElement>(".nav-item");
      break;
    case "ArrowLeft":
      if (row.getAttribute("aria-expanded") === "true") row.click();
      else
        next = row.closest(".nav-children")
          ?.previousElementSibling as HTMLElement | null;
      break;
    default:
      return;
  }
  next?.focus();
}
async function logout() {
  try {
    await auth.logout("admin");
    router.push("/admin/login");
  } catch (e) {
    ElMessage.error((e as Error).message);
  }
}
</script>
<template>
  <div class="admin-shell" @keydown="navigationKeydown">
    <div
      v-if="open && mobile"
      class="sidebar-shade"
      aria-hidden="true"
      @click="closeNavigation()"
    ></div>
    <aside
      id="admin-navigation"
      ref="sidebar"
      class="sidebar"
      :class="{ open }"
      :inert="mobile && !open"
      :aria-hidden="mobile && !open ? true : undefined"
      :role="mobile && open ? 'dialog' : undefined"
      :aria-modal="mobile && open ? true : undefined"
      aria-label="管理导航"
    >
      <div class="sidebar-heading">
        <Brand admin @click="closeNavigation(false)" />
        <button
          v-if="mobile"
          class="el-button sidebar-close"
          aria-label="关闭导航"
          @click="closeNavigation()"
        >
          <el-icon><Close /></el-icon>
        </button>
      </div>
      <div class="workspace-label">
        <span>WORKSPACE</span><small>管理工作空间</small>
      </div>
      <nav aria-label="后台导航" @keydown="treeKeydown">
        <ul class="nav-tree">
          <AdminNavItem
            v-for="item in nav"
            :key="item.id"
            :item="item"
            :depth="0"
            :current-path="currentPath"
            :expanded-ids="expandedIds"
            :active-ids="activeIds"
            @toggle="toggleGroup"
            @navigate="closeNavigation(false)"
          />
        </ul>
      </nav>
      <div class="sidebar-bottom">
        <span class="mini-symbol"
          ><el-icon><Promotion /></el-icon
        ></span>
        <div>星索管理中心<small>让每一次连接更有价值</small></div>
      </div>
    </aside>
    <div class="admin-content" :inert="mobile && open">
      <header class="admin-topbar">
        <div class="breadcrumb">
          <button
            ref="menuTrigger"
            class="mobile-menu"
            :aria-expanded="open"
            aria-controls="admin-navigation"
            aria-label="打开导航"
            @click="openNavigation"
          >
            <el-icon><Expand /></el-icon>
          </button>
          <nav class="breadcrumb-trail" aria-label="当前位置">
            <ol>
              <li
                v-for="(label, index) in breadcrumbs"
                :key="index"
                :class="{ 'breadcrumb-parent': index < breadcrumbs.length - 1 }"
              >
                <span v-if="index > 0" class="slash" aria-hidden="true">/</span>
                <strong
                  v-if="index === breadcrumbs.length - 1"
                  aria-current="page"
                  >{{ label }}</strong
                >
                <span v-else>{{ label }}</span>
              </li>
            </ol>
          </nav>
        </div>
        <div class="top-actions">
          <a href="/" target="_blank" rel="noopener" class="visit-site"
            >访问网站<el-icon><TopRight /></el-icon></a
          ><span class="top-divider"></span
          ><span class="avatar">{{
            (auth.users.admin?.display_name || "管").slice(0, 1)
          }}</span
          ><span class="admin-name">{{ auth.users.admin?.display_name }}</span
          ><el-button
            text
            :icon="SwitchButton"
            aria-label="退出登录"
            @click="logout"
          />
        </div>
      </header>
      <main id="main" class="admin-main" tabindex="-1"><router-view /></main>
      <footer class="admin-footer">
        STARINDEX WORKSPACE <span>连接内容与价值。</span>
      </footer>
    </div>
  </div>
</template>
