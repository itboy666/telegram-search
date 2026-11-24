import type { Component } from "vue";
import {
  Odometer,
  Collection,
  DocumentChecked,
  FolderOpened,
  User,
  Promotion,
  Key,
  Setting,
  Clock,
  Expand,
  Connection,
} from "./icons";

type NavigationBase = {
  id: string;
  label: string;
  icon: Component;
};

export type AdminNavigationItem = NavigationBase &
  (
    | { path: string; permission: string; children?: never }
    | { children: AdminNavigationItem[]; path?: never; permission?: never }
  );

export const adminNavigation: AdminNavigationItem[] = [
  {
    id: "dashboard",
    label: "工作台概览",
    path: "/admin",
    permission: "dashboard.read",
    icon: Odometer,
  },
  {
    id: "content",
    label: "内容管理",
    icon: Collection,
    children: [
      {
        id: "resources",
        label: "资源管理",
        path: "/admin/resources",
        permission: "resources.read",
        icon: Collection,
      },
      {
        id: "submissions",
        label: "收录审核",
        path: "/admin/submissions",
        permission: "submissions.read",
        icon: DocumentChecked,
      },
      {
        id: "categories",
        label: "分类管理",
        path: "/admin/categories",
        permission: "categories.read",
        icon: FolderOpened,
      },
    ],
  },
  {
    id: "users",
    label: "用户管理",
    path: "/admin/users",
    permission: "users.read",
    icon: User,
  },
  {
    id: "telegram",
    label: "机器人管理",
    icon: Promotion,
    children: [
      {
        id: "bot",
        label: "机器人连接",
        path: "/admin/bot",
        permission: "bot.read",
        icon: Connection,
      },
      {
        id: "bot-menu",
        label: "欢迎语与菜单",
        path: "/admin/bot-menu",
        permission: "bot_menu.read",
        icon: Expand,
      },
      {
        id: "bot-security",
        label: "搜索验证",
        path: "/admin/bot-security",
        permission: "bot_security.read",
        icon: Key,
      },
      {
        id: "bot-groups",
        label: "群组安全",
        path: "/admin/bot-groups",
        permission: "bot_groups.read",
        icon: User,
      },
    ],
  },
  {
    id: "finance",
    label: "财务管理",
    icon: Odometer,
    children: [
      {
        id: "wallets",
        label: "用户余额",
        path: "/admin/wallets",
        permission: "wallets.read",
        icon: User,
      },
      {
        id: "recharges",
        label: "充值订单",
        path: "/admin/recharges",
        permission: "recharges.read",
        icon: DocumentChecked,
      },
      {
        id: "wallet-entries",
        label: "资金流水",
        path: "/admin/wallet-entries",
        permission: "wallet_entries.read",
        icon: Clock,
      },
      {
        id: "commerce-settings",
        label: "支付与奖励设置",
        path: "/admin/commerce-settings",
        permission: "commerce_settings.read",
        icon: Setting,
      },
    ],
  },
  {
    id: "marketing",
    label: "营销管理",
    icon: Promotion,
    children: [
      {
        id: "ad-slots",
        label: "广告位管理",
        path: "/admin/ad-slots",
        permission: "ad_slots.read",
        icon: Expand,
      },
      {
        id: "ad-orders",
        label: "广告订单",
        path: "/admin/ad-orders",
        permission: "ad_orders.read",
        icon: DocumentChecked,
      },
    ],
  },
  {
    id: "system",
    label: "系统管理",
    icon: Setting,
    children: [
      {
        id: "admins",
        label: "管理员管理",
        path: "/admin/admins",
        permission: "access.manage",
        icon: Key,
      },
      {
        id: "roles",
        label: "角色与权限",
        path: "/admin/roles",
        permission: "access.manage",
        icon: Setting,
      },
      {
        id: "keys",
        label: "开放 API",
        path: "/admin/keys",
        permission: "keys.read",
        icon: Key,
      },
      {
        id: "settings",
        label: "系统设置",
        path: "/admin/settings",
        permission: "settings.read",
        icon: Setting,
      },
      {
        id: "logs",
        label: "操作日志",
        path: "/admin/logs",
        permission: "logs.read",
        icon: Clock,
      },
    ],
  },
];

export function filterAdminNavigation(
  items: AdminNavigationItem[],
  can: (permission: string) => boolean,
): AdminNavigationItem[] {
  return items.flatMap((item): AdminNavigationItem[] => {
    if (item.children) {
      const children = filterAdminNavigation(item.children, can);
      return children.length ? [{ ...item, children }] : [];
    }
    return can(item.permission) ? [item] : [];
  });
}

export function findAdminNavigationTrail(
  items: AdminNavigationItem[],
  path: string,
): AdminNavigationItem[] {
  for (const item of items) {
    if (item.path === path) return [item];
    if (item.children) {
      const trail = findAdminNavigationTrail(item.children, path);
      if (trail.length) return [item, ...trail];
    }
  }
  return [];
}
