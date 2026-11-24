<script setup lang="ts">
import { computed } from "vue";
import { types } from "../lib/api";
import { useAuth } from "../lib/auth";
const auth = useAuth();
function readable(field: string) {
  return auth.readField(props.permissionModule, field);
}
function writable(field: string) {
  return auth.writeField(props.permissionModule, field);
}
const props = withDefaults(
  defineProps<{
    model: Record<string, any>;
    categories: any[];
    errors?: Record<string, string>;
    admin?: boolean;
    permissionModule?: string;
  }>(),
  { errors: () => ({}), admin: false, permissionModule: "resources" },
);
const linkPlaceholder = computed(() =>
  props.model.type === "website"
    ? "https://example.com/"
    : "https://t.me/username",
);
</script>
<template>
  <el-form label-position="top" class="resource-form" @submit.prevent>
    <el-form-item
      v-if="!admin || readable('title')"
      label="资源名称"
      required
      :error="errors.title"
      ><el-input
        :disabled="admin && !writable('title')"
        v-model="model.title"
        placeholder="为资源起一个清晰的名字"
        maxlength="180"
        show-word-limit
    /></el-form-item>
    <el-form-item
      v-if="!admin || readable('url')"
      :label="model.type === 'website' ? '网站地址' : 'Telegram 链接'"
      required
      :error="errors.url"
      ><el-input
        :disabled="admin && !writable('url')"
        v-model="model.url"
        :placeholder="linkPlaceholder"
        maxlength="500"
      />
      <div class="field-help">
        {{
          model.type === "website"
            ? "支持公开的 http/https 网站地址；已收录或正在审核的链接不能重复提交。"
            : "支持公开频道、群组、机器人或帖子链接。普通网址请选择「网站」类型。"
        }}
      </div></el-form-item
    >
    <div class="form-grid">
      <el-form-item v-if="!admin || readable('type')" label="资源类型" required
        ><el-select v-model="model.type" :disabled="admin && !writable('type')"
          ><el-option
            v-for="(name, key) in types"
            :key="key"
            :label="name"
            :value="key" /></el-select></el-form-item
      ><el-form-item v-if="!admin || readable('category_id')" label="所属分类"
        ><el-select
          v-model="model.category_id"
          :disabled="admin && !writable('category_id')"
          clearable
          placeholder="选择分类"
          ><el-option
            v-for="c in categories"
            :key="c.id"
            :label="c.name"
            :value="Number(c.id)" /></el-select
      ></el-form-item>
    </div>
    <el-form-item v-if="!admin || readable('description')" label="资源简介"
      ><el-input
        :disabled="admin && !writable('description')"
        v-model="model.description"
        type="textarea"
        :rows="4"
        maxlength="5000"
        show-word-limit
        placeholder="介绍内容、用途和适合的人群"
    /></el-form-item>
    <template v-if="admin && permissionModule === 'resources'"
      ><el-form-item v-if="readable('tags')" label="关键词标签"
        ><el-input
          :disabled="!writable('tags')"
          v-model="model.tags"
          placeholder="多个标签用逗号分隔"
          maxlength="255"
      /></el-form-item>
      <div class="form-grid">
        <el-form-item v-if="readable('status')" label="发布状态"
          ><el-select v-model="model.status" :disabled="!writable('status')"
            ><el-option label="已发布" value="published" /><el-option
              label="已下架"
              value="hidden" /><el-option
              label="已归档"
              value="archived" /><el-option
              label="已封禁"
              value="blocked" /><el-option
              label="已失效"
              value="invalid" /></el-select></el-form-item
        ><el-form-item v-if="readable('members')" label="成员数量"
          ><el-input-number
            :disabled="!writable('members')"
            v-model="model.members"
            :min="0"
            :precision="0"
            placeholder="未知可留空"
            controls-position="right"
        /></el-form-item>
      </div>
      <el-form-item
        v-if="readable('status_reason')"
        label="状态说明"
        :error="errors.status_reason"
      >
        <el-input
          v-model="model.status_reason"
          :disabled="!writable('status_reason')"
          type="textarea"
          :rows="2"
          maxlength="500"
          show-word-limit
          placeholder="例如：因违规已封禁，暂不接受重新收录"
        />
        <p class="field-help">
          资源非正常发布状态时，这段说明会展示给重复提交该链接的用户。
        </p>
      </el-form-item>
      <el-form-item v-if="readable('featured')" label="精选资源"
        ><el-switch
          :disabled="!writable('featured')"
          v-model="model.featured"
          :active-value="1"
          :inactive-value="0"
          active-text="精选展示" /></el-form-item
    ></template>
  </el-form>
</template>
