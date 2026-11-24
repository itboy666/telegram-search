<script setup lang="ts">
import { computed } from "vue";
const props = defineProps<{ items: any[] }>();
const sources = computed(() => {
  const entries = new Map<string, any>();
  for (const item of props.items) {
    if (item.source_attribution)
      entries.set(item.source_attribution.key, item.source_attribution);
  }
  return [...entries.values()];
});
</script>
<template>
  <aside
    v-if="sources.length"
    class="source-attributions"
    aria-label="数据来源"
  >
    <div v-for="source in sources" :key="source.key">
      <div
        v-if="source.key.startsWith('curlie_')"
        title="Curlie Directory Attribution"
      >
        With content from
        <a
          href="https://curlie.org/"
          target="_blank"
          rel="noopener noreferrer"
          title="Curlie - the largest human-edited directory of the web"
          >Curlie.org</a
        >
        - the largest human-edited directory of the web. Contribute by
        submitting a website or becoming an editor.
      </div>
      <div v-else>
        来源：{{ source.author }} ·
        <a :href="source.url" target="_blank" rel="noopener noreferrer">{{
          source.title
        }}</a>
      </div>
      <div>
        <a
          v-if="source.license_url"
          :href="source.license_url"
          target="_blank"
          rel="noopener noreferrer"
          >{{ source.license }}</a
        >
        <span v-else>{{ source.license }}</span> · {{ source.changes }}
      </div>
    </div>
  </aside>
</template>
<style scoped>
.source-attributions {
  color: var(--text-muted, #64748b);
  font-size: 13px;
  line-height: 1.7;
  margin: 20px 0;
  overflow-wrap: anywhere;
}
.source-attributions > div + div {
  margin-top: 8px;
}
.source-attributions a {
  color: var(--primary, #365cf5);
  text-decoration: underline;
  text-underline-offset: 3px;
}
</style>
