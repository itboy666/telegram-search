<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { ElMessage } from "element-plus";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import ResourceForm from "./ResourceForm.vue";
const props = defineProps<{ submission?: Record<string, any> }>();
const emit = defineEmits<{ close: []; saved: [] }>();
const auth = useAuth();
const form = reactive<Record<string, any>>({
  title: "",
  url: "",
  type: "channel",
  description: "",
  category_id: null,
  user_id: null,
  ...props.submission,
});
const saving = ref(false),
  error = ref(""),
  fields = ref<Record<string, string>>({}),
  categories = ref<any[]>([]);
onMounted(async () => {
  if (auth.readField("submissions", "category_id")) {
    try {
      categories.value = await api("/admin/api/lookups/categories");
    } catch (e) {
      error.value = (e as Error).message;
    }
  }
});
async function save() {
  saving.value = true;
  error.value = "";
  fields.value = {};
  try {
    const values = Object.fromEntries(
      ["title", "url", "type", "description", "category_id", "user_id"].map(
        (key) => [key, form[key] ?? null],
      ),
    );
    await api(
      `/admin/api/submissions${props.submission ? `/${props.submission.id}` : ""}`,
      auth.writable("submissions", values),
    );
    ElMessage.success("待审核申请已保存");
    emit("saved");
  } catch (e) {
    error.value = (e as Error).message;
    if (e instanceof ApiError) fields.value = e.fields;
  } finally {
    saving.value = false;
  }
}
</script>
<template>
  <el-drawer
    :model-value="true"
    :title="submission ? `编辑申请 #${submission.id}` : '新增待审核申请'"
    size="560px"
    class="form-drawer"
    :close-on-click-modal="false"
    @close="emit('close')"
  >
    <div v-if="error" role="alert" class="form-alert">{{ error }}</div>
    <el-form label-position="top" @submit.prevent>
      <el-form-item
        v-if="auth.readField('submissions', 'user_id')"
        label="提交用户 ID"
        required
        :error="fields.user_id"
      >
        <el-input-number
          v-model="form.user_id"
          :min="1"
          :precision="0"
          :disabled="!!submission || !auth.writeField('submissions', 'user_id')"
          placeholder="填写用户管理中的数据库 ID"
        />
        <p class="field-help">
          申请会关联此用户，并保留其投稿限制和链接去重检查。
        </p>
      </el-form-item>
    </el-form>
    <ResourceForm
      :model="form"
      :categories="categories"
      :errors="fields"
      permission-module="submissions"
      admin
    />
    <template #footer
      ><el-button :disabled="saving" @click="emit('close')">取消</el-button
      ><el-button type="primary" :loading="saving" @click="save"
        >保存申请</el-button
      ></template
    >
  </el-drawer>
</template>
