<script setup lang="ts">
import { ref, watch } from "vue";
import { ElMessage } from "element-plus";
import type { InputNumberInstance } from "element-plus";
import { api } from "../lib/api";

const props = defineProps<{ pageSize: number; editable: boolean }>();
const emit = defineEmits<{ saved: [] }>();
const pageSize = ref<number | undefined>(props.pageSize);
const saving = ref(false);
const error = ref("");
const input = ref<InputNumberInstance>();
watch(
  () => props.pageSize,
  (value) => {
    pageSize.value = value;
  },
);

async function save() {
  if (saving.value || !props.editable) return;
  error.value = "";
  if (
    !Number.isInteger(pageSize.value) ||
    !pageSize.value ||
    pageSize.value < 1 ||
    pageSize.value > 30
  ) {
    error.value = "请输入 1–30 的整数。";
    input.value?.focus();
    return;
  }
  saving.value = true;
  try {
    await api("/admin/api/bot", { page_size: pageSize.value });
    ElMessage.success("搜索设置已保存，新搜索生效");
    emit("saved");
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <section class="panel bot-search-panel" aria-labelledby="bot-search-heading">
    <div class="panel-heading">
      <div>
        <h2 id="bot-search-heading">搜索展示</h2>
        <p>调整机器人每页展示的结果数量</p>
      </div>
    </div>
    <el-form v-if="editable" label-position="top" @submit.prevent="save">
      <el-form-item label="每页显示条数" for="bot-page-size">
        <div class="bot-page-size-control">
          <el-input-number
            id="bot-page-size"
            ref="input"
            v-model="pageSize"
            :min="1"
            :max="30"
            :step="1"
            :precision="0"
            :disabled="saving"
            :aria-invalid="!!error"
            aria-describedby="bot-page-size-help bot-page-size-error"
            @change="error = ''"
          />
          <span>条 / 页</span>
        </div>
        <p id="bot-page-size-help" class="field-help">
          默认 12 条，可设为 1–30 条。保存后新搜索生效，已发送的搜索保持原条数。
        </p>
        <p id="bot-page-size-error" class="bot-search-error" role="alert">
          {{ error }}
        </p>
      </el-form-item>
      <el-button
        native-type="submit"
        :loading="saving"
        :disabled="pageSize === props.pageSize"
        >保存搜索设置</el-button
      >
    </el-form>
    <div v-else class="bot-search-readonly">
      <p>
        每页显示 <strong>{{ pageSize }}</strong> 条结果
      </p>
      <p class="field-help">当前为只读权限，如需调整，请联系超级管理员。</p>
    </div>
  </section>
</template>

<style scoped>
.bot-search-panel {
  min-width: 0;
}
.bot-page-size-control {
  display: flex;
  width: 100%;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
}
.bot-page-size-control .el-input-number {
  width: 160px;
  max-width: 100%;
  flex: 0 0 auto;
}
.bot-page-size-control > span {
  font-size: 13px;
  color: var(--muted);
}
.bot-search-panel .field-help {
  width: 100%;
  font-size: 12px;
  line-height: 1.7;
  margin: 8px 0 0;
}
.bot-search-panel .el-form-item {
  margin-bottom: 16px;
}
.bot-search-error {
  color: var(--el-color-danger);
  font-size: 12px;
  margin: 8px 0 0;
}
.bot-search-error:empty {
  display: none;
}
.bot-search-readonly > p:first-child {
  margin: 0;
}
</style>
