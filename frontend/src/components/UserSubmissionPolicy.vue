<script setup lang="ts">
import { reactive, ref } from "vue";
import { ElMessage } from "element-plus";
import { api } from "../lib/api";
import { useAuth } from "../lib/auth";

const props = defineProps<{ user: Record<string, any> }>();
const emit = defineEmits<{ close: []; saved: [] }>();
const auth = useAuth();
const form = reactive({
  submission_allowed: Number(props.user.submission_allowed ?? 1),
  submission_note: props.user.submission_note ?? "",
});
const saving = ref(false);
const error = ref("");
async function save() {
  if (saving.value) return;
  saving.value = true;
  error.value = "";
  try {
    await api(
      `/admin/api/users/${props.user.id}/submission-policy`,
      auth.writable("users", form),
    );
    ElMessage.success("投稿权限已保存");
    emit("saved");
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <el-dialog
    :model-value="true"
    title="投稿权限"
    width="480px"
    :close-on-click-modal="!saving"
    :show-close="!saving"
    :close-on-press-escape="!saving"
    @close="emit('close')"
  >
    <p class="policy-user">
      {{ user.display_name || user.username || `用户 #${user.id}` }}
    </p>
    <p class="policy-help">
      单独控制提交收录的权限，不影响账号登录和资源搜索。
    </p>
    <el-form label-position="top" @submit.prevent="save">
      <el-form-item
        v-if="auth.readField('users', 'submission_allowed')"
        label="允许提交收录"
        for="submission-allowed"
      >
        <el-select
          id="submission-allowed"
          v-model="form.submission_allowed"
          :disabled="saving || !auth.writeField('users', 'submission_allowed')"
        >
          <el-option :value="1" label="允许投稿" />
          <el-option :value="0" label="禁止投稿" />
        </el-select>
      </el-form-item>
      <el-form-item
        v-if="auth.readField('users', 'submission_note')"
        label="原因说明"
        for="submission-note"
      >
        <el-input
          id="submission-note"
          v-model="form.submission_note"
          :disabled="saving || !auth.writeField('users', 'submission_note')"
          type="textarea"
          :rows="3"
          maxlength="500"
          show-word-limit
          placeholder="填写明确的原因，方便用户了解处理结果"
          aria-describedby="submission-note-help"
        />
        <p id="submission-note-help" class="field-help">
          禁止投稿时，用户提交链接会看到这段说明。
        </p>
      </el-form-item>
      <p v-if="error" class="form-error" role="alert">{{ error }}</p>
      <div class="policy-actions">
        <el-button :disabled="saving" @click="emit('close')">取消</el-button>
        <el-button type="primary" native-type="submit" :loading="saving"
          >保存权限</el-button
        >
      </div>
    </el-form>
  </el-dialog>
</template>

<style scoped>
.policy-user {
  margin: 0 0 8px;
  color: var(--ink);
  font-weight: 600;
  overflow-wrap: anywhere;
}
.policy-help {
  margin: 0 0 24px;
  color: var(--muted);
  line-height: 1.7;
}
.policy-actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  margin-top: 24px;
}
.policy-actions .el-button + .el-button {
  margin-left: 0;
}
</style>
