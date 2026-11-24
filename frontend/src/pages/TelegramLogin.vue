<script setup lang="ts">
import { ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { api, csrf } from "../lib/api";
import { useAuth } from "../lib/auth";
import TelegramAuthPanel from "../components/TelegramAuthPanel.vue";
import IosIcon from "../components/IosIcon.vue";
const code = ref(""),
  useCode = ref(false),
  error = ref(""),
  busy = ref(false),
  router = useRouter(),
  auth = useAuth(),
  route = useRoute();
async function complete(result: any) {
  csrf.user = result.csrf_token;
  auth.users.user = result.user;
  code.value = "";
  const next = String(route.query.next || "");
  if (next.startsWith("/user") && !next.includes("login")) {
    await router.replace(next);
    return;
  }
  const tab = String(route.query.tab || "marketing");
  await router.replace({
    path: "/user",
    query: {
      tab: ["marketing", "recharges", "ledger", "ads"].includes(tab)
        ? tab
        : "marketing",
    },
  });
}
async function submit() {
  busy.value = true;
  error.value = "";
  try {
    const result = await api("/user/api/bot-login", { code: code.value });
    await complete(result);
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <div class="telegram-login-page">
    <section class="telegram-login-story">
      <span class="eyebrow">CONNECTED WITH TELEGRAM</span>
      <h1>你的账户，<br /><span>一键连接。</span></h1>
      <p>在熟悉的 Telegram 中确认身份。回来后，收藏、余额与推广都在这里。</p>
      <div class="telegram-connect-visual" aria-hidden="true">
        <span><IosIcon name="telegram" /></span><i></i
        ><span><IosIcon name="user" /></span>
      </div>
      <div class="telegram-login-trust">
        <IosIcon name="lock" /><span>无需提供 Telegram 密码或短信验证码</span>
      </div>
    </section>
    <section class="panel telegram-login">
      <TelegramAuthPanel v-if="!useCode" @complete="complete" />
      <template v-else>
        <span class="eyebrow">ONE-TIME CODE</span>
        <h2>使用一次性登录码</h2>
        <p class="muted">
          在机器人私聊发送
          <code>/account</code>，将收到的登录码粘贴在下方。有效期 5
          分钟，仅能使用一次。
        </p>
        <div v-if="error" class="form-alert" role="alert">{{ error }}</div>
        <el-form label-position="top" @submit.prevent="submit"
          ><el-form-item label="一次性登录码"
            ><el-input
              v-model="code"
              maxlength="20"
              autocomplete="one-time-code"
              placeholder="粘贴机器人提供的 20 位登录码" /></el-form-item
          ><el-button type="primary" :loading="busy" native-type="submit"
            >登录用户中心</el-button
          ></el-form
        >
      </template>
      <div class="telegram-login-alternatives">
        <el-button text @click="useCode = !useCode">{{
          useCode ? "返回 Telegram 确认登录" : "已有机器人登录码？"
        }}</el-button>
        <router-link to="/user/login">使用网站账号</router-link>
      </div>
      <p class="telegram-login-binding-note">
        已有网站账户？先登录网站，再到「账户与安全」绑定
        Telegram，即可共用同一个账户。
      </p>
    </section>
  </div>
</template>
