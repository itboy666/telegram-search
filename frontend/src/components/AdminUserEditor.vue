<script setup lang="ts">
import { computed, reactive, ref } from "vue";
import { ElMessage } from "element-plus";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { newPasswordError } from "../lib/passwordValidation";
const props = defineProps<{ user?: Record<string, any> }>();
const emit = defineEmits<{ close: []; saved: [] }>();
const auth = useAuth();
const botOnly = computed(
  () =>
    Boolean(props.user?.telegram_id) &&
    String(props.user?.username || "").startsWith("tg_"),
);
const read = (field: string) => auth.readField("users", field);
const write = (field: string) => auth.writeField("users", field);
const form = reactive<Record<string, any>>({
  username: "",
  display_name: "",
  password: "",
  status: 1,
  submission_allowed: 1,
  submission_note: "",
  ...props.user,
});
const saving = ref(false),
  error = ref(""),
  errors = ref<Record<string, string>>({});
async function save() {
  error.value = "";
  errors.value = {};
  if (write("password") && !botOnly.value && (!props.user || form.password)) {
    const invalidPassword = newPasswordError(form.password);
    if (invalidPassword) {
      errors.value.password = invalidPassword;
      return;
    }
  }
  saving.value = true;
  try {
    await api(
      `/admin/api/users${props.user ? `/${props.user.id}` : ""}`,
      auth.writable("users", {
        username: form.username,
        display_name: form.display_name,
        password: form.password,
        status: form.status,
        submission_allowed: form.submission_allowed,
        submission_note: form.submission_note,
      }),
    );
    ElMessage.success(props.user ? "用户已保存" : "用户已创建");
    emit("saved");
  } catch (e) {
    error.value = (e as Error).message;
    if (e instanceof ApiError) errors.value = e.fields;
  } finally {
    saving.value = false;
  }
}
</script>
<template>
  <el-drawer
    :model-value="true"
    :title="user ? `编辑用户 #${user.id}` : '添加网站用户'"
    size="520px"
    class="form-drawer"
    :close-on-click-modal="false"
    @close="emit('close')"
  >
    <div v-if="error" role="alert" class="form-alert">{{ error }}</div>
    <el-form label-position="top" :disabled="saving" @submit.prevent="save">
      <el-form-item
        v-if="read('username')"
        label="用户账号"
        required
        :error="errors.username"
      >
        <el-input
          v-model="form.username"
          :disabled="!write('username') || botOnly"
          maxlength="80"
          placeholder="4–32 位字母、数字或下划线"
          autocomplete="off"
        />
      </el-form-item>
      <el-form-item
        v-if="read('display_name')"
        label="昵称"
        required
        :error="errors.display_name"
      >
        <el-input
          v-model="form.display_name"
          :disabled="!write('display_name')"
          maxlength="100"
        />
      </el-form-item>
      <el-form-item
        v-if="write('password') && !botOnly"
        :label="user ? '重置登录密码' : '登录密码'"
        :required="!user"
        :error="errors.password"
      >
        <el-input
          v-model="form.password"
          type="password"
          show-password
          autocomplete="new-password"
          maxlength="72"
          :placeholder="user ? '留空保留原密码' : '10–72 字节密码'"
        />
        <p v-if="user" class="field-help">重置密码后，该用户需要重新登录。</p>
      </el-form-item>
      <p v-if="botOnly" class="field-help">
        Telegram 身份由机器人维护，此处可管理昵称、账号状态和投稿权限。
      </p>
      <div class="form-grid">
        <el-form-item v-if="read('status')" label="账号状态">
          <el-select v-model="form.status" :disabled="!write('status')"
            ><el-option :value="1" label="启用" /><el-option
              :value="0"
              label="停用"
          /></el-select>
        </el-form-item>
        <el-form-item v-if="read('submission_allowed')" label="收录投稿">
          <el-select
            v-model="form.submission_allowed"
            :disabled="!write('submission_allowed')"
            ><el-option :value="1" label="允许投稿" /><el-option
              :value="0"
              label="禁止投稿"
          /></el-select>
        </el-form-item>
      </div>
      <el-form-item
        v-if="read('submission_note')"
        label="投稿权限说明"
        :error="errors.submission_note"
      >
        <el-input
          v-model="form.submission_note"
          :disabled="!write('submission_note')"
          type="textarea"
          :rows="3"
          maxlength="500"
          show-word-limit
        />
      </el-form-item>
    </el-form>
    <template #footer
      ><el-button :disabled="saving" @click="emit('close')">取消</el-button
      ><el-button type="primary" :loading="saving" @click="save"
        >保存用户</el-button
      ></template
    >
  </el-drawer>
</template>
