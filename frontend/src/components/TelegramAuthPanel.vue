<script setup lang="ts">
import { computed } from "vue";
import IosIcon from "./IosIcon.vue";
import { useTelegramChallenge } from "../lib/telegramAuth";

const props = withDefaults(defineProps<{ purpose?: "login" | "bind" }>(), {
  purpose: "login",
});
const emit = defineEmits<{ complete: [result: any] }>();
const { challenge, state, error, checking, start, check, cancel } =
  useTelegramChallenge(props.purpose, (result) => emit("complete", result));
const binding = computed(() => props.purpose === "bind");
</script>

<template>
  <div class="telegram-auth-panel">
    <div class="telegram-auth-heading">
      <span class="telegram-auth-icon"><IosIcon name="telegram" /></span>
      <div>
        <h3>{{ binding ? "连接你的 Telegram" : "用 Telegram 安全登录" }}</h3>
        <p>
          {{
            binding
              ? "绑定后，网站和机器人使用同一个账户。"
              : "在机器人中确认身份，无需输入 Telegram 密码。"
          }}
        </p>
      </div>
    </div>
    <div v-if="error" class="form-alert" role="alert">{{ error }}</div>
    <template
      v-if="(state === 'pending' || state === 'consuming') && challenge"
    >
      <ol class="telegram-auth-steps">
        <li><span>01</span>打开下方链接，进入 Telegram 机器人。</li>
        <li><span>02</span>核对请求，点击机器人的确认按钮。</li>
        <li>
          <span>03</span>返回本页，系统会自动完成{{
            binding ? "绑定" : "登录"
          }}。
        </li>
      </ol>
      <a
        :href="challenge.url"
        target="_blank"
        rel="noopener noreferrer"
        class="el-button el-button--primary telegram-auth-launch"
      >
        <IosIcon name="telegram" /> 打开 Telegram 确认
        <IosIcon name="right" />
      </a>
      <div class="telegram-auth-wait" role="status" aria-live="polite">
        <span class="status-dot"></span
        >{{
          state === "consuming"
            ? "已确认，正在完成请求…"
            : "等待你在机器人中确认"
        }}
      </div>
      <div class="telegram-auth-options">
        <el-button text :loading="checking" @click="check()"
          >我已确认，检查状态</el-button
        >
        <el-button text :disabled="state === 'consuming'" @click="cancel"
          >取消</el-button
        >
      </div>
    </template>
    <template v-else>
      <p v-if="state === 'expired'" class="field-help" role="status">
        本次请求已失效，请重新发起。
      </p>
      <p v-if="state === 'cancelled'" class="field-help" role="status">
        你已在 Telegram 中取消授权。如需继续，请重新发起。
      </p>
      <el-button
        type="primary"
        :loading="state === 'creating'"
        class="telegram-auth-launch"
        @click="start"
      >
        <IosIcon name="telegram" />
        {{ binding ? "通过 Telegram 绑定" : "使用 Telegram 登录" }}
      </el-button>
      <p class="telegram-auth-note">
        {{
          binding
            ? "已关联其他网站账户的 Telegram 无法重复绑定，已有余额和订单不会自动合并。"
            : "仅在你确认后登录。首次使用将创建账户，请勿确认非本人发起的请求。"
        }}
      </p>
    </template>
  </div>
</template>
