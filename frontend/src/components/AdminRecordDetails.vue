<script setup lang="ts">
import { computed } from "vue";
import { types, statuses, dateTime } from "../lib/api";
const props = defineProps<{
  record: Record<string, any> | null;
  title?: string;
}>();
defineEmits<{ close: [] }>();
const labels: Record<string, string> = {
  id: "数据库 ID",
  title: "名称",
  name: "名称",
  username: "账号",
  display_name: "昵称",
  url: "资源链接",
  description: "说明",
  type: "类型",
  status: "状态",
  status_reason: "状态说明",
  category_id: "分类 ID",
  category_name: "分类名称",
  tags: "关键词",
  members: "成员数量",
  featured: "精选",
  source: "来源",
  owner_id: "所属用户 ID",
  user_id: "提交用户 ID",
  submitter: "提交人",
  resource_id: "资源 ID",
  reviewer_id: "审核管理员 ID",
  review_note: "审核备注",
  telegram_id: "Telegram ID",
  submission_allowed: "允许投稿",
  submission_note: "投稿权限说明",
  sort_order: "排序",
  resource_count: "资源数量",
  prefix: "密钥前缀",
  daily_limit: "每日额度",
  is_super: "超级管理员",
  role_ids: "角色 ID",
  permissions: "操作权限",
  fields: "字段授权",
  admin_count: "关联管理员",
  action: "操作",
  detail: "详情",
  actor_id: "管理员 ID",
  created_at: "创建时间",
  updated_at: "更新时间",
  last_login_at: "最近登录",
  last_used_at: "最近使用",
  reviewed_at: "审核时间",
};
const entries = computed(() =>
  Object.entries(labels).filter(([key]) => props.record && key in props.record),
);
function value(key: string) {
  const v = props.record?.[key];
  if (v === null || v === undefined || v === "") return "—";
  if (key === "type") return types[v] || v;
  if (key === "status")
    return statuses[v] || (Number(v) === 1 ? "启用" : "停用");
  if (["featured", "is_super", "submission_allowed"].includes(key))
    return Number(v) === 1 ? "是" : "否";
  if (key.endsWith("_at")) return dateTime(v);
  return typeof v === "object" ? JSON.stringify(v, null, 2) : String(v);
}
</script>
<template>
  <el-drawer
    :model-value="!!record"
    :title="title || '记录详情'"
    size="560px"
    class="form-drawer"
    @close="$emit('close')"
  >
    <el-descriptions v-if="record" :column="1" border class="record-details">
      <el-descriptions-item
        v-for="[key, label] in entries"
        :key="key"
        :label="label"
      >
        <a
          v-if="key === 'url' && record.url"
          :href="record.url"
          target="_blank"
          rel="noopener noreferrer"
          class="break-word"
          >{{ record.url }}</a
        >
        <span v-else class="record-detail-value">{{ value(key) }}</span>
      </el-descriptions-item>
    </el-descriptions>
    <template #footer
      ><el-button @click="$emit('close')">关闭</el-button></template
    >
  </el-drawer>
</template>
