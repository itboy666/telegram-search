<script setup lang="ts">
defineProps<{ message: string }>();
defineEmits(["retry"]);
</script>
<template>
  <div v-if="message" role="alert" class="load-error">
    <span>{{ message }}</span
    ><el-button @click="$emit('retry')">重试</el-button>
  </div>
</template>
