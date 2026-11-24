<script setup lang="ts">
import IosIcon from "./IosIcon.vue";
withDefaults(defineProps<{ name?: string; admin?: boolean }>(), {
  name: "星索",
  admin: false,
});
</script>
<template>
  <router-link :to="admin ? '/admin' : '/'" class="brand"
    ><span class="brand-symbol"><IosIcon name="telegram" /></span
    ><span>{{ name }}<small>STARINDEX</small></span></router-link
  >
</template>
