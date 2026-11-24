<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { newPasswordError } from "../lib/passwordValidation";
import IosIcon from "../components/IosIcon.vue";
import LoadError from "../components/LoadError.vue";
import TelegramAuthPanel from "../components/TelegramAuthPanel.vue";

const auth = useAuth();
const connection = ref<any>(null);
const loading = ref(false);
const error = ref("");
const busy = ref(false);
const passwordError = ref("");
const passwordFields = ref<Record<string, string>>({});
const password = reactive({
  current_password: "",
  new_password: "",
  confirmation: "",
});
const unbindOpen = ref(false);
const unbindPassword = ref("");
const unbindError = ref("");
const hasPassword = computed(() =>
  Boolean(connection.value?.has_password ?? auth.users.user?.has_password),
);

async function load() {
  loading.value = true;
  error.value = "";
  try {
    connection.value = await api("/user/api/telegram");
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    loading.value = false;
  }
}
async function bound() {
  ElMessage.success("Telegram 绑定成功");
  await auth.ensure("user");
  await load();
}
async function savePassword() {
  passwordError.value = "";
  passwordFields.value = {};
  const invalidPassword = newPasswordError(password.new_password);
  if (invalidPassword) {
    passwordFields.value.new_password = invalidPassword;
    return;
  }
  if (password.new_password !== password.confirmation) {
    passwordFields.value.confirmation = "两次输入的密码不一致";
    return;
  }
  busy.value = true;
  try {
    await api("/user/api/password", {
      ...(hasPassword.value
        ? { current_password: password.current_password }
        : {}),
      new_password: password.new_password,
    });
    Object.assign(password, {
      current_password: "",
      new_password: "",
      confirmation: "",
    });
    ElMessage.success("登录密码已更新");
    await auth.ensure("user");
    await load();
  } catch (e) {
    passwordError.value = (e as Error).message;
    if (e instanceof ApiError) passwordFields.value = e.fields;
  } finally {
    busy.value = false;
  }
}
async function openUnbind() {
  try {
    await ElMessageBox.confirm(
      "解除后，Telegram 将无法登录此网站账户。账户余额、广告订单和收藏仍保留，请使用网站账号和密码登录。",
      "解除 Telegram 绑定",
      {
        confirmButtonText: "继续验证密码",
        cancelButtonText: "保留绑定",
        type: "warning",
      },
    );
    unbindPassword.value = "";
    unbindError.value = "";
    unbindOpen.value = true;
  } catch {}
}
async function unbind() {
  busy.value = true;
  unbindError.value = "";
  try {
    await api("/user/api/telegram/unbind", { password: unbindPassword.value });
    unbindOpen.value = false;
    unbindPassword.value = "";
    ElMessage.success("Telegram 已解除绑定");
    await auth.ensure("user");
    await load();
  } catch (e) {
    unbindError.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
onMounted(load);
</script>

<template>
  <div class="user-center account-security-page">
    <div class="page-heading">
      <div>
        <span class="eyebrow">ACCOUNT & SECURITY</span>
        <h1>账户与安全</h1>
        <p>让网站与 Telegram 连接起来，登录和管理更加从容。</p>
      </div>
      <router-link to="/user" class="el-button"
        ><IosIcon name="left" />返回用户中心</router-link
      >
    </div>
    <LoadError :message="error" @retry="load" />
    <el-skeleton v-if="loading && !connection" :rows="8" animated />
    <div v-if="connection" class="security-grid">
      <section class="panel security-identity">
        <span class="security-avatar"><IosIcon name="user" /></span>
        <h2>{{ auth.users.user?.display_name }}</h2>
        <p class="muted">@{{ auth.users.user?.username }}</p>
        <dl class="account-facts">
          <div>
            <dt>账户 ID</dt>
            <dd>#{{ auth.users.user?.id }}</dd>
          </div>
          <div>
            <dt>Telegram</dt>
            <dd>{{ connection.bound ? "已绑定" : "未绑定" }}</dd>
          </div>
          <div>
            <dt>登录密码</dt>
            <dd>{{ hasPassword ? "已设置" : "尚未设置" }}</dd>
          </div>
        </dl>
        <div class="security-tip">
          <IosIcon name="lock" />
          <p>网站账户与管理员账户独立。绑定 Telegram 不会赋予后台访问权限。</p>
        </div>
      </section>
      <div class="security-sections">
        <section class="panel">
          <div class="panel-heading">
            <div>
              <h2>Telegram 身份</h2>
              <p>同一个账户，连接两种使用方式。</p>
            </div>
            <el-tag :type="connection.bound ? 'success' : 'info'">{{
              connection.bound ? "已绑定" : "未绑定"
            }}</el-tag>
          </div>
          <template v-if="connection.bound">
            <div class="bound-telegram">
              <span class="telegram-auth-icon"
                ><IosIcon name="telegram"
              /></span>
              <div>
                <strong>{{
                  connection.display_name || "Telegram 用户"
                }}</strong>
                <p>
                  {{
                    connection.username ? `@${connection.username} · ` : ""
                  }}ID {{ connection.telegram_id }}
                </p>
              </div>
              <span class="bound-check"><IosIcon name="check" /></span>
            </div>
            <p class="field-help">
              可通过 Telegram
              登录，也可在机器人中使用当前账户的余额、广告订单与搜索奖励。
            </p>
            <div class="security-section-actions">
              <el-button :disabled="!connection.can_unbind" @click="openUnbind"
                >解除绑定</el-button
              >
              <span v-if="!connection.can_unbind" class="muted"
                >先设置网站登录密码，确保解除后仍可登录。</span
              >
            </div>
          </template>
          <TelegramAuthPanel v-else purpose="bind" @complete="bound" />
        </section>
        <section class="panel">
          <div class="panel-heading">
            <div>
              <h2>{{ hasPassword ? "修改登录密码" : "设置登录密码" }}</h2>
              <p>用于使用网站账号登录，建议使用唯一的长密码。</p>
            </div>
            <IosIcon name="lock" />
          </div>
          <p v-if="!hasPassword" class="field-help security-password-help">
            首次设置需在登录后 15 分钟内完成；若登录已过期，请通过 Telegram
            重新登录后再设置。
          </p>
          <div v-if="passwordError" class="form-alert" role="alert">
            {{ passwordError }}
          </div>
          <el-form
            label-position="top"
            :disabled="busy"
            @submit.prevent="savePassword"
          >
            <el-form-item
              v-if="hasPassword"
              label="当前密码"
              required
              :error="passwordFields.current_password"
              ><el-input
                v-model="password.current_password"
                type="password"
                show-password
                autocomplete="current-password"
                maxlength="128"
            /></el-form-item>
            <div class="form-grid">
              <el-form-item
                label="新密码"
                required
                :error="passwordFields.new_password"
                ><el-input
                  v-model="password.new_password"
                  type="password"
                  show-password
                  autocomplete="new-password"
                  maxlength="72"
                  placeholder="10–72 字节密码"
              /></el-form-item>
              <el-form-item
                label="确认新密码"
                required
                :error="passwordFields.confirmation"
                ><el-input
                  v-model="password.confirmation"
                  type="password"
                  show-password
                  autocomplete="new-password"
                  maxlength="72"
                  placeholder="再次输入新密码"
              /></el-form-item>
            </div>
            <el-button type="primary" native-type="submit" :loading="busy">{{
              hasPassword ? "保存新密码" : "设置密码"
            }}</el-button>
          </el-form>
        </section>
      </div>
    </div>
    <el-dialog
      v-model="unbindOpen"
      title="验证密码并解除绑定"
      width="440px"
      :close-on-click-modal="false"
    >
      <div v-if="unbindError" class="form-alert" role="alert">
        {{ unbindError }}
      </div>
      <el-form label-position="top" @submit.prevent="unbind">
        <el-form-item label="网站登录密码" required
          ><el-input
            v-model="unbindPassword"
            type="password"
            show-password
            autocomplete="current-password"
            maxlength="128"
        /></el-form-item>
        <div class="security-dialog-actions">
          <el-button @click="unbindOpen = false">取消</el-button
          ><el-button type="danger" :loading="busy" native-type="submit"
            >解除绑定</el-button
          >
        </div>
      </el-form>
    </el-dialog>
  </div>
</template>
