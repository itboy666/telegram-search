<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { ElMessageBox } from "element-plus";
import { ApiError, api } from "../lib/api";
import LoadError from "./LoadError.vue";

interface UpgradeStatus {
  current_version: string;
  target_version: string;
  pending_versions: string[];
  up_to_date: boolean;
  running: boolean;
}

const endpoint = "/admin/api/database-upgrade";
const status = ref<UpgradeStatus>();
const loading = ref(false);
const submitting = ref(false);
const error = ref("");
const statusMessage = computed(() => {
  if (submitting.value || status.value?.running) return "正在升级数据库…";
  if (loading.value) return "正在检查数据库版本…";
  if (status.value?.up_to_date) return "数据库已是当前版本";
  if (!status.value) return "等待检查数据库版本";
  return `有 ${status.value.pending_versions.length} 项待升级`;
});
let mounted = false;
let pollTimer: ReturnType<typeof setTimeout> | undefined;

function scheduleCheck() {
  clearTimeout(pollTimer);
  if (mounted && status.value?.running) {
    pollTimer = setTimeout(checkVersion, 3000);
  }
}

async function checkVersion() {
  if (loading.value || submitting.value) return;
  loading.value = true;
  error.value = "";
  try {
    status.value = await api<UpgradeStatus>(endpoint);
    scheduleCheck();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    loading.value = false;
  }
}

async function upgrade() {
  if (
    loading.value ||
    submitting.value ||
    status.value?.running ||
    status.value?.up_to_date
  )
    return;
  try {
    await ElMessageBox.confirm(
      `将数据库升级至 ${status.value?.target_version}。请在开始前确认已有可恢复的数据库备份。`,
      "确认升级数据库",
      {
        confirmButtonText: "开始升级",
        cancelButtonText: "取消",
        type: "warning",
        autofocus: false,
      },
    );
  } catch {
    return;
  }
  submitting.value = true;
  error.value = "";
  try {
    status.value = await api<UpgradeStatus>(endpoint, {});
  } catch (e) {
    error.value = (e as Error).message;
    if (e instanceof ApiError && [0, 409, 502, 504].includes(e.status)) {
      await api<UpgradeStatus>(endpoint)
        .then((result) => {
          status.value = result;
          if (result.running || result.up_to_date) error.value = "";
        })
        .catch(() => {});
    }
  } finally {
    submitting.value = false;
    scheduleCheck();
  }
}

onMounted(() => {
  mounted = true;
  void checkVersion();
});
onBeforeUnmount(() => {
  mounted = false;
  clearTimeout(pollTimer);
});
</script>

<template>
  <section
    class="panel database-upgrade"
    aria-labelledby="database-upgrade-title"
  >
    <div class="panel-heading">
      <div>
        <h2 id="database-upgrade-title">数据库升级</h2>
        <p>更新程序后，在这里应用新版本所需的数据库变更。</p>
      </div>
    </div>
    <LoadError :message="error" @retry="checkVersion" />
    <p role="status" class="database-upgrade-status">{{ statusMessage }}</p>
    <dl v-if="status" class="database-upgrade-versions">
      <div>
        <dt>当前版本</dt>
        <dd>
          <code>{{ status.current_version || "暂无版本记录" }}</code>
        </dd>
      </div>
      <div>
        <dt>程序所需版本</dt>
        <dd>
          <code>{{ status.target_version }}</code>
        </dd>
      </div>
    </dl>
    <details
      v-if="status?.pending_versions.length"
      class="database-upgrade-pending"
    >
      <summary>查看待升级版本</summary>
      <ul>
        <li v-for="version in status.pending_versions" :key="version">
          <code>{{ version }}</code>
        </li>
      </ul>
    </details>
    <div class="database-upgrade-actions">
      <el-button :loading="loading" :disabled="submitting" @click="checkVersion"
        >检查版本</el-button
      >
      <el-button
        v-if="status && !status.up_to_date"
        type="primary"
        :loading="submitting || status.running"
        :disabled="loading"
        @click="upgrade"
        >升级数据库</el-button
      >
    </div>
  </section>
</template>

<style scoped>
.database-upgrade {
  margin-top: 24px;
}
.database-upgrade-status {
  color: var(--ink);
  font-weight: 600;
}
.database-upgrade-versions {
  display: flex;
  flex-wrap: wrap;
  gap: 12px 32px;
  margin: 12px 0 16px;
}
.database-upgrade-versions dt {
  color: var(--muted);
}
.database-upgrade-versions dd {
  margin: 4px 0 0;
}
.database-upgrade-pending {
  margin-bottom: 16px;
}
.database-upgrade-pending summary {
  cursor: pointer;
  min-height: 28px;
}
.database-upgrade-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.database-upgrade-actions .el-button {
  margin: 0;
}
@media (max-width: 720px) {
  .database-upgrade-actions .el-button {
    min-height: 44px;
  }
  .database-upgrade-pending summary {
    min-height: 44px;
  }
}
</style>
