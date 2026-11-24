<script setup lang="ts">
import { Search, Refresh } from "../lib/icons";
defineProps<{
  filter: { id: string; q: string };
  placeholder?: string;
  keywordVisible?: boolean;
}>();
defineEmits<{ search: []; reset: [] }>();
</script>
<template>
  <form class="filter-bar admin-list-filter" @submit.prevent="$emit('search')">
    <el-input
      v-model="filter.id"
      class="id-filter"
      inputmode="numeric"
      placeholder="精确编号"
      aria-label="数据库 ID"
      clearable
      ><template #prepend>ID</template></el-input
    >
    <el-input
      v-if="keywordVisible !== false"
      v-model="filter.q"
      class="search-input"
      :placeholder="placeholder || '搜索名称或关键词'"
      aria-label="关键词"
      :prefix-icon="Search"
      clearable
      maxlength="120"
    />
    <el-button native-type="submit" :icon="Search">查询</el-button>
    <el-button :icon="Refresh" @click="$emit('reset')">重置</el-button>
  </form>
</template>
