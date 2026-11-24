<script setup lang="ts">
defineProps<{ name: string }>();
</script>
<template>
  <span
    v-if="name"
    class="tg-ios-icon"
    aria-hidden="true"
    :style="{ maskImage: `url(/icons/telegram-ios/${name}.svg)` }"
  />
</template>
