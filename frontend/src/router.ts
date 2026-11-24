import { createRouter, createWebHistory } from "vue-router";
import { nextTick } from "vue";
import { useAuth } from "./lib/auth";
export const router = createRouter({
  history: createWebHistory(),
  scrollBehavior: (_, __, saved) => saved || { top: 0 },
  routes: [
    {
      path: "/install",
      component: () => import("./pages/Install.vue"),
      meta: { title: "初始化安装" },
    },
    {
      path: "/admin/login",
      component: () => import("./pages/Login.vue"),
      meta: { scope: "admin", title: "管理员登录" },
    },
    {
      path: "/admin",
      component: () => import("./layouts/AdminLayout.vue"),
      meta: { auth: "admin" },
      children: [
        {
          path: "commerce-settings",
          component: () => import("./pages/CommerceSettings.vue"),
          meta: {
            title: "支付与奖励设置",
            permission: "commerce_settings.read",
          },
        },
        ...[
          ["wallets", "用户余额"],
          ["wallet_entries", "资金流水"],
          ["recharges", "充值订单"],
          ["ad_slots", "广告位管理"],
          ["ad_orders", "广告订单"],
        ].map(([module, title]) => ({
          path: module!.replaceAll("_", "-"),
          component: () => import("./pages/AdminCommerce.vue"),
          meta: { title, commerce: module, permission: `${module}.read` },
        })),
        {
          path: "no-access",
          component: () => import("./pages/AccessDenied.vue"),
          meta: { title: "访问权限" },
        },
        ...["admins", "roles"].map((path) => ({
          path,
          component: () => import("./pages/AccessManagement.vue"),
          meta: {
            title: path === "admins" ? "管理员管理" : "角色与权限",
            permission: "access.manage",
          },
        })),
        {
          path: "bot-menu",
          component: () => import("./pages/BotMenuEditor.vue"),
          meta: { title: "欢迎语与菜单", permission: "bot_menu.read" },
        },
        {
          path: "bot-security",
          component: () => import("./pages/BotSecurity.vue"),
          meta: { title: "搜索验证", permission: "bot_security.read" },
        },
        {
          path: "bot-groups",
          component: () => import("./pages/BotGroups.vue"),
          meta: { title: "群组安全", permission: "bot_groups.read" },
        },
        {
          path: "",
          component: () => import("./pages/Dashboard.vue"),
          meta: { title: "工作台概览", permission: "dashboard.read" },
        },
        {
          path: "resources",
          component: () => import("./pages/Resources.vue"),
          meta: { title: "资源管理", permission: "resources.read" },
        },
        ...[
          "submissions",
          "categories",
          "users",
          "bot",
          "keys",
          "settings",
          "logs",
        ].map((path, i) => ({
          path,
          component: () => import("./pages/Operations.vue"),
          meta: {
            permission: `${path}.read`,
            title: [
              "收录审核",
              "分类管理",
              "用户管理",
              "Telegram 机器人",
              "开放 API",
              "系统设置",
              "操作日志",
            ][i],
          },
        })),
      ],
    },
    {
      path: "/",
      component: () => import("./layouts/SiteLayout.vue"),
      children: [
        {
          path: "",
          component: () => import("./pages/Search.vue"),
          meta: { title: "发现资源" },
        },
        {
          path: "user/login",
          component: () => import("./pages/Login.vue"),
          meta: { scope: "user", title: "登录" },
        },
        {
          path: "user",
          component: () => import("./pages/UserCommerce.vue"),
          meta: { auth: "user", title: "用户与营销中心" },
        },
        {
          path: "user/telegram-login",
          component: () => import("./pages/TelegramLogin.vue"),
          meta: { title: "Telegram 登录" },
        },
        {
          path: "user/collections",
          component: () => import("./pages/UserCenter.vue"),
          meta: { auth: "user", title: "我的空间" },
        },
        {
          path: "user/security",
          component: () => import("./pages/AccountSecurity.vue"),
          meta: { auth: "user", title: "账户与安全" },
        },
        {
          path: ":pathMatch(.*)*",
          component: () => import("./pages/NotFound.vue"),
          meta: { title: "页面不存在" },
        },
      ],
    },
  ],
});
router.beforeEach(async (to) => {
  const scope = to.meta.auth as string;
  if (scope && !(await useAuth().ensure(scope)))
    return { path: `/${scope}/login`, query: { next: to.fullPath } };
  if (
    scope === "admin" &&
    to.meta.permission &&
    !useAuth().can(String(to.meta.permission))
  ) {
    if (to.path === "/admin") {
      const entry = [
        "resources",
        "submissions",
        "categories",
        "users",
        "bot",
        "bot-menu",
        "bot-security",
        "bot-groups",
        "keys",
        "settings",
        "logs",
        "wallets",
        "wallet-entries",
        "recharges",
        "ad-slots",
        "ad-orders",
        "commerce-settings",
      ].find((path) => useAuth().can(`${path.replaceAll("-", "_")}.read`));
      if (entry) return `/admin/${entry}`;
    }
    return "/admin/no-access";
  }
});
router.afterEach(async (to) => {
  document.title = `${to.meta.title || "发现"} · 星索`;
  await nextTick();
  document.querySelector<HTMLElement>("#main")?.focus({ preventScroll: true });
});
