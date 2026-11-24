<script setup lang="ts">
import type { AdminNavigationItem } from "../lib/adminNavigation";
import { ArrowLeft } from "../lib/icons";

defineProps<{
  item: AdminNavigationItem;
  depth: number;
  currentPath: string;
  expandedIds: Set<string>;
  activeIds: Set<string>;
}>();
const emit = defineEmits<{
  toggle: [id: string];
  navigate: [];
}>();
</script>

<template>
  <li class="nav-node">
    <button
      v-if="item.children"
      :id="`admin-nav-${item.id}`"
      type="button"
      class="nav-item nav-group"
      :class="{ 'has-active': activeIds.has(item.id) }"
      :style="{ '--nav-depth': depth }"
      :aria-expanded="expandedIds.has(item.id)"
      :aria-controls="`admin-nav-${item.id}-children`"
      @click="emit('toggle', item.id)"
    >
      <el-icon aria-hidden="true"><component :is="item.icon" /></el-icon>
      <span class="nav-label">{{ item.label }}</span>
      <el-icon
        class="nav-indicator"
        :class="{ expanded: expandedIds.has(item.id) }"
        aria-hidden="true"
        ><ArrowLeft
      /></el-icon>
    </button>
    <router-link
      v-else
      :to="item.path"
      class="nav-item nav-link"
      :class="{ active: currentPath === item.path }"
      :style="{ '--nav-depth': depth }"
      :aria-current="currentPath === item.path ? 'page' : undefined"
      @click="emit('navigate')"
    >
      <el-icon aria-hidden="true"><component :is="item.icon" /></el-icon>
      <span class="nav-label">{{ item.label }}</span>
    </router-link>
    <ul
      v-if="item.children"
      v-show="expandedIds.has(item.id)"
      :id="`admin-nav-${item.id}-children`"
      class="nav-children"
      :aria-labelledby="`admin-nav-${item.id}`"
    >
      <AdminNavItem
        v-for="child in item.children"
        :key="child.id"
        :item="child"
        :depth="depth + 1"
        :current-path="currentPath"
        :expanded-ids="expandedIds"
        :active-ids="activeIds"
        @toggle="emit('toggle', $event)"
        @navigate="emit('navigate')"
      />
    </ul>
  </li>
</template>
