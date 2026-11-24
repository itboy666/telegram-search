<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import type { SelectV2Instance } from "element-plus";
import type { IconCatalog } from "../lib/iconCatalog";
import { ArrowLeft } from "../lib/icons";
import IosIcon from "./IosIcon.vue";

const props = defineProps<{
  modelValue: string;
  icons: IconCatalog;
  label: string;
}>();
const emit = defineEmits<{
  "update:modelValue": [value: string];
}>();
const query = ref("");
const select = ref<SelectV2Instance>();
const expanded = ref(false);
const dropdownHeight = ref(264);
const allOptions = computed(() => {
  const unique = new Map<string, { name: string; icon: IconCatalog[string] }>();
  for (const [name, icon] of Object.entries(props.icons)) {
    // Keep legacy aliases selectable without showing the same artwork twice.
    if (!unique.has(icon.id) || name === props.modelValue) {
      unique.set(icon.id, { name, icon });
    }
  }
  return [...unique.values()].map(({ name, icon }) => {
    const number = icon.position ? String(icon.position).padStart(3, "0") : "";
    const label =
      name.startsWith("ios_") && number
        ? `${icon.label} · ${number}`
        : icon.label;
    return {
      value: name,
      label,
      position: icon.position,
      search:
        `${label} ${number} ${name} ${icon.id} ${icon.emoji} ${icon.keywords || ""}`.toLocaleLowerCase(),
    };
  });
});
const selectedLabel = computed(() =>
  props.modelValue
    ? allOptions.value.find((option) => option.value === props.modelValue)
        ?.label || ""
    : "不使用图标",
);
const options = computed(() => {
  const terms = query.value
    .trim()
    .toLocaleLowerCase()
    .split(/\s+/)
    .filter(Boolean);
  return [
    {
      value: "",
      label: "不使用图标",
      position: undefined,
      search: "不使用图标 无 none",
    },
    ...allOptions.value,
  ].filter((option) =>
    terms.every((term) =>
      /^\d{1,3}$/.test(term)
        ? option.position === Number(term)
        : option.search.includes(term),
    ),
  );
});
async function filter(value: string) {
  query.value = value;
  await nextTick();
  if (expanded.value && options.value.length) {
    select.value?.scrollTo(0);
  }
}
function fitDropdown() {
  if (!expanded.value || !select.value) return;
  const rect = select.value.$el.getBoundingClientRect();
  const viewport = window.visualViewport;
  const top = rect.top - (viewport?.offsetTop || 0);
  const below = (viewport?.height || window.innerHeight) - top - rect.height;
  // Reserve room for the header, arrow and viewport gap.
  const rows = Math.floor((Math.max(top, below) - 72) / 44);
  dropdownHeight.value = Math.min(264, Math.max(44, rows * 44));
}
function onVisibleChange(visible: boolean) {
  query.value = "";
  expanded.value = visible;
  fitDropdown();
}
onMounted(() => {
  window.addEventListener("resize", fitDropdown);
  window.addEventListener("scroll", fitDropdown, {
    capture: true,
    passive: true,
  });
  window.visualViewport?.addEventListener("resize", fitDropdown);
});
onBeforeUnmount(() => {
  window.removeEventListener("resize", fitDropdown);
  window.removeEventListener("scroll", fitDropdown, true);
  window.visualViewport?.removeEventListener("resize", fitDropdown);
});
</script>

<template>
  <el-select-v2
    ref="select"
    class="telegram-icon-picker"
    :model-value="modelValue"
    :aria-label="label"
    :options="options"
    :filter-method="filter"
    :suffix-icon="ArrowLeft"
    :item-height="44"
    :height="dropdownHeight"
    :offset="8"
    :fallback-placements="['top-start', 'bottom-start']"
    :empty-values="[null, undefined]"
    :persistent="false"
    filterable
    default-first-option
    placeholder="搜索图标名称或编号"
    popper-class="telegram-icon-picker-dropdown"
    no-match-text="没有匹配的图标，试试其他名称或编号"
    no-data-text="没有匹配的图标，试试其他名称或编号"
    @update:model-value="emit('update:modelValue', $event)"
    @visible-change="onVisibleChange"
  >
    <template #label>{{ selectedLabel }}</template>
    <template v-if="modelValue" #prefix>
      <IosIcon :name="modelValue" />
    </template>
    <template #header>
      <div class="icon-picker-summary" role="status" aria-live="polite">
        <span>Telegram iOS Icons</span>
        <span>{{ allOptions.length }} 款图标</span>
      </div>
    </template>
    <template #default="{ item }">
      <span class="icon-picker-option">
        <IosIcon v-if="item.value" :name="item.value" />
        <span v-else class="icon-picker-none" aria-hidden="true">—</span>
        <span>{{ item.label }}</span>
      </span>
    </template>
  </el-select-v2>
</template>
