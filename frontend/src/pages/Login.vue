<script setup lang="ts">
import { computed, reactive, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { Search, Collection, Connection, ArrowRight } from "../lib/icons";
import Brand from "../components/Brand.vue";
import IosIcon from "../components/IosIcon.vue";
import { useAuth } from "../lib/auth";
import { newPasswordError } from "../lib/passwordValidation";
const route = useRoute(),
  router = useRouter(),
  auth = useAuth();
const admin = computed(() => route.meta.scope === "admin");
const form = reactive({ username: "", password: "" }),
  register = ref(false),
  loading = ref(false),
  error = ref("");
async function submit() {
  error.value = "";
  if (!form.username || !form.password) {
    error.value = "请填写账号和密码。";
    return;
  }
  if (register.value) {
    error.value = newPasswordError(form.password);
    if (error.value) return;
  }
  loading.value = true;
  try {
    const scope = admin.value ? "admin" : "user";
    await auth.login(scope, form, register.value);
    const next = String(route.query.next || "");
    router.push(
      next.startsWith(`/${scope}`) && !next.includes("login")
        ? next
        : `/${scope}`,
    );
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    loading.value = false;
  }
}
</script>
<template>
  <div class="login-page" :class="{ standalone: admin }">
    <div v-if="admin" class="login-brand"><Brand /></div>
    <div class="login-wrap">
      <section class="login-intro">
        <span class="eyebrow">{{
          admin
            ? "STARINDEX · CONTROL CENTER"
            : "ONE ACCOUNT. MORE CONNECTIONS."
        }}</span>
        <h1>
          {{ admin ? "掌控每一次" : "连接你的" }}<br /><span>{{
            admin ? "有价值的连接。" : "发现与可能。"
          }}</span>
        </h1>
        <p>
          {{
            admin
              ? "内容、用户、机器人与业务，在一个清晰有序的工作空间协同运转。"
              : "发现资源、收藏灵感、管理推广。使用一个账户，让网站与 Telegram 无缝连接。"
          }}
        </p>
        <div class="intro-points">
          <span
            ><el-icon><Search /></el-icon>发现优质内容</span
          ><span
            ><el-icon><Collection /></el-icon>有序管理资源</span
          ><span
            ><el-icon><Connection /></el-icon>连接更多可能</span
          >
        </div>
        <div class="login-orbit" aria-hidden="true">
          <span class="orbit-core"
            ><el-icon><Search /></el-icon></span
          ><span class="orbit-label label-one"
            ><IosIcon name="telegram" />TELEGRAM</span
          ><span class="orbit-label label-two"
            ><IosIcon name="link" />CONNECTED</span
          >
        </div>
      </section>
      <section class="login-card">
        <span class="eyebrow">{{
          admin ? "ADMIN CONSOLE" : "WELCOME TO STARINDEX"
        }}</span>
        <h2>
          {{
            register ? "创建你的账号" : admin ? "登录管理工作台" : "欢迎回来"
          }}
        </h2>
        <p class="muted">
          {{
            register
              ? "从收藏第一个好资源开始。"
              : admin
                ? "使用独立的管理员账号登录。"
                : "使用普通用户账号登录，收藏与提交资源。"
          }}
        </p>
        <template v-if="!admin">
          <router-link
            :to="{ path: '/user/telegram-login', query: route.query }"
            class="el-button telegram-login-link"
            ><IosIcon name="telegram" />使用 Telegram 登录<IosIcon name="right"
          /></router-link>
          <div class="login-divider"><span>或使用网站账号</span></div>
        </template>
        <div>
          <div v-if="error" class="form-alert" role="alert">{{ error }}</div>
          <el-form label-position="top" @submit.prevent="submit"
            ><el-form-item label="账号" required
              ><el-input
                v-model="form.username"
                autocomplete="username"
                :maxlength="32"
                placeholder="请输入账号" /></el-form-item
            ><el-form-item label="密码" required
              ><el-input
                v-model="form.password"
                type="password"
                show-password
                :autocomplete="register ? 'new-password' : 'current-password'"
                :maxlength="register ? 72 : 128"
                placeholder="请输入密码"
              />
              <div v-if="register" class="field-help">
                账号 4–32 位字母、数字或下划线；密码 10–72 字节。
              </div></el-form-item
            ><el-button
              type="primary"
              native-type="submit"
              :loading="loading"
              class="login-submit"
              >{{ register ? "创建账号" : admin ? "进入工作台" : "登录"
              }}<el-icon><ArrowRight /></el-icon></el-button
          ></el-form>
        </div>
        <p v-if="admin" class="login-note">
          仅限授权管理员访问，普通用户账号无法登录后台。<br />
          <router-link to="/user/login">前往普通用户登录</router-link>
        </p>
        <el-button
          v-else
          text
          class="register-toggle"
          @click="
            register = !register;
            error = '';
          "
          >{{
            register ? "已有账号？返回登录" : "还没有账号？立即注册"
          }}</el-button
        >
      </section>
    </div>
    <p v-if="admin" class="login-footer">星索 StarIndex · 资源与发现的连接点</p>
  </div>
</template>
