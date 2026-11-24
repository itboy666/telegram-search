<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from "vue";

const props = withDefaults(
  defineProps<{
    scope: "admin" | "user";
    rechargeId: number | string;
    hasImage: boolean;
    active?: boolean;
  }>(),
  { active: true },
);
const loading = ref(false),
  imageUrl = ref(""),
  error = ref("");
let generation = 0;
let controller: AbortController | null = null;
const maxBytes = 5 * 1024 * 1024;

function reset() {
  generation++;
  controller?.abort();
  controller = null;
  loading.value = false;
  error.value = "";
  if (imageUrl.value) URL.revokeObjectURL(imageUrl.value);
  imageUrl.value = "";
}

watch(
  () => [props.scope, props.rechargeId, props.hasImage, props.active],
  reset,
);
onBeforeUnmount(reset);

async function loadImage() {
  if (!props.active || !props.hasImage || loading.value) return;
  reset();
  const id = String(props.rechargeId);
  if (!/^[1-9]\d*$/.test(id)) {
    error.value = "订单信息不完整，请刷新订单后重试。";
    return;
  }
  const version = generation;
  const request = new AbortController();
  controller = request;
  loading.value = true;
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    request.abort();
  }, 20000);
  try {
    // Construct the same-origin endpoint instead of trusting an attachment URL.
    const response = await fetch(
      `/${props.scope}/api/commerce/recharges/${id}/proof-image`,
      {
        credentials: "same-origin",
        cache: "no-store",
        redirect: "error",
        headers: { Accept: "image/png" },
        signal: request.signal,
      },
    );
    if (!response.ok) {
      const messages: Record<number, string> = {
        401: "登录已过期，请重新登录后查看截图。",
        403: "你没有查看这笔订单凭证的权限。",
        404: "这笔订单暂无可查看的截图，请刷新订单确认。",
      };
      throw new Error(
        messages[response.status] || "截图暂时无法加载，请稍后重试。",
      );
    }
    const type = response.headers.get("Content-Type")?.split(";")[0]?.trim();
    if (type !== "image/png") {
      throw new Error("截图格式暂不支持预览，请联系管理员核对。");
    }
    if (Number(response.headers.get("Content-Length")) > maxBytes) {
      throw new Error("截图文件过大，请重新提交小于 5 MB 的截图。");
    }
    const image = await response.blob();
    if (version !== generation) return;
    if (!image.size || image.size > maxBytes) {
      throw new Error("截图内容不完整或过大，请重新提交截图。");
    }
    imageUrl.value = URL.createObjectURL(image);
  } catch (e) {
    if (version !== generation) return;
    error.value = timedOut
      ? "截图加载超时，请检查网络后重试。"
      : e instanceof TypeError
        ? "连接失败，请检查网络后重试。"
        : e instanceof Error
          ? e.message
          : "截图暂时无法加载，请稍后重试。";
  } finally {
    clearTimeout(timer);
    if (version === generation) {
      loading.value = false;
      controller = null;
    }
  }
}

function imageFailed(event: Event) {
  if ((event.target as HTMLImageElement).src !== imageUrl.value) return;
  reset();
  error.value = "截图无法显示，请重试或联系管理员核对凭证。";
}
</script>

<template>
  <section
    class="recharge-proof"
    aria-label="转账凭证截图"
    :aria-busy="loading"
  >
    <div class="recharge-proof-heading">
      <strong>转账凭证截图</strong>
      <el-button
        v-if="hasImage && !imageUrl"
        :loading="loading"
        :disabled="!active"
        @click="loadImage"
        >{{ error ? "重新加载" : "查看截图" }}</el-button
      >
      <el-button v-else-if="imageUrl" @click="reset">收起截图</el-button>
    </div>
    <p v-if="error" class="recharge-proof-error" role="alert">{{ error }}</p>
    <p v-else-if="!hasImage" class="recharge-proof-status">
      暂无上传的凭证截图。
    </p>
    <p v-else-if="!imageUrl" class="recharge-proof-status" role="status">
      {{ loading ? "正在加载凭证截图…" : "已上传截图，点击后查看。" }}
    </p>
    <figure v-if="imageUrl" class="recharge-proof-preview">
      <img
        :src="imageUrl"
        :alt="`充值订单 #${rechargeId} 的转账凭证截图`"
        @error="imageFailed"
      />
      <figcaption>
        <a :href="imageUrl" target="_blank" rel="noopener noreferrer"
          >在新窗口查看大图</a
        >
      </figcaption>
    </figure>
    <p v-if="$slots.default" class="recharge-proof-help"><slot /></p>
  </section>
</template>

<style scoped>
.recharge-proof {
  padding: 16px;
  margin: 20px 0 24px;
  border: 1px solid var(--line);
  border-radius: 12px;
  background: var(--surface);
}
.recharge-proof-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
}
.recharge-proof-heading strong {
  color: var(--ink);
  font-size: 14px;
}
.recharge-proof-status,
.recharge-proof-error,
.recharge-proof-help {
  margin: 12px 0 0;
  color: var(--muted);
  font-size: 13px;
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.recharge-proof-error {
  color: var(--danger);
}
.recharge-proof-preview {
  margin: 16px 0 0;
}
.recharge-proof-preview img {
  display: block;
  width: 100%;
  max-height: 360px;
  min-height: 120px;
  object-fit: contain;
  border-radius: 8px;
  background: var(--canvas);
}
.recharge-proof-preview figcaption {
  margin-top: 8px;
  text-align: center;
}
.recharge-proof-preview a {
  display: inline-flex;
  align-items: center;
  min-height: 36px;
  color: var(--primary);
  text-decoration: underline;
  text-underline-offset: 3px;
}
.recharge-proof-preview a:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 4px;
  border-radius: 4px;
}
@media (max-width: 600px) {
  .recharge-proof-heading :deep(.el-button),
  .recharge-proof-preview a {
    min-height: 44px;
  }
}
</style>
