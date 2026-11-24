<script setup lang="ts">
defineProps<{
  editable?: boolean;
  editLabel?: string;
  busy?: boolean;
  actions?: {
    command: string;
    label: string;
    danger?: boolean;
    disabled?: boolean;
  }[];
}>();
defineEmits<{ view: []; edit: []; action: [command: string] }>();
</script>
<template>
  <div class="admin-row-actions">
    <el-button text :disabled="busy" @click="$emit('view')">查看</el-button>
    <el-button
      v-if="editable"
      text
      type="primary"
      :disabled="busy"
      @click="$emit('edit')"
      >{{ editLabel || "编辑" }}</el-button
    >
    <el-dropdown
      v-if="actions?.length"
      trigger="click"
      :disabled="busy"
      @command="$emit('action', $event)"
    >
      <el-button text :disabled="busy" aria-label="更多记录操作"
        >更多</el-button
      >
      <template #dropdown>
        <el-dropdown-menu>
          <el-dropdown-item
            v-for="item in actions"
            :key="item.command"
            :command="item.command"
            :disabled="item.disabled"
            :class="{ 'danger-action': item.danger }"
            >{{ item.label }}</el-dropdown-item
          >
        </el-dropdown-menu>
      </template>
    </el-dropdown>
  </div>
</template>
