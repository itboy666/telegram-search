<script setup lang="ts">
import {
  computed,
  nextTick,
  onMounted,
  onBeforeUnmount,
  reactive,
  ref,
} from "vue";
import type { FormInstance, FormRules } from "element-plus";
import Brand from "../components/Brand.vue";
import IosIcon from "../components/IosIcon.vue";
import { ApiError } from "../lib/api";
import { newPasswordError } from "../lib/passwordValidation";

type Check = { key: string; label: string; ok: boolean; detail: string };
const steps = ["环境检查", "连接数据库", "站点与管理员", "安装完成"];
const step = ref(0),
  busy = ref(false),
  error = ref(""),
  loading = ref(true);
const token = ref(""),
  checks = ref<Check[]>([]),
  ready = ref(false),
  resume = ref(false);
const databaseResult = ref<{ version: string; message: string }>();
const locked = ref(false),
  formRef = ref<FormInstance>(),
  errorBox = ref<HTMLElement>();
const fields = ref<Record<string, string>>({});
const form = reactive({
  database: {
    host: "127.0.0.1",
    port: 3306,
    name: "starindex",
    username: "",
    password: "",
    tls: false,
    create_database: false,
  },
  site_name: "星索",
  public_url: window.location.origin,
  admin_username: "",
  admin_password: "",
  confirm_password: "",
});
const rules: FormRules = {
  "database.host": [
    { required: true, message: "请输入数据库地址", trigger: "blur" },
  ],
  "database.name": [
    {
      required: true,
      pattern: /^[a-zA-Z][a-zA-Z0-9_-]{0,63}$/,
      message: "以字母开头，支持字母、数字、下划线或短横线",
      trigger: "blur",
    },
  ],
  "database.username": [
    { required: true, message: "请输入数据库用户", trigger: "blur" },
  ],
  site_name: [{ required: true, message: "请输入站点名称", trigger: "blur" }],
  admin_username: [
    {
      required: true,
      pattern: /^[a-zA-Z0-9_]{4,32}$/,
      message: "4–32 位字母、数字或下划线",
      trigger: "blur",
    },
  ],
  admin_password: [
    {
      validator: (_, value, callback) => {
        const e = newPasswordError(value);
        callback(e ? new Error(e) : undefined);
      },
      trigger: "blur",
    },
  ],
  confirm_password: [
    {
      validator: (_, value, callback) =>
        callback(
          value === form.admin_password && value
            ? undefined
            : new Error("两次密码不一致"),
        ),
      trigger: "blur",
    },
  ],
};
const title = computed(() =>
  locked.value
    ? "系统已经安装"
    : [
        "准备好，开启星索。",
        "连接你的数据库",
        "建立你的管理空间",
        "一切就绪，开始探索。",
      ][step.value],
);
const subtitle = computed(
  () =>
    [
      "完成环境检查，开始部署你的资源搜索平台。",
      "使用独立的 MySQL 数据库，承载资源、用户和业务数据。",
      "设置网站名称，创建拥有完整管理权限的管理员账号。",
      "数据库、管理账号和站点配置已准备完成。",
    ][step.value],
);
let controller: AbortController | undefined;
async function request<T = any>(path: string, data?: unknown): Promise<T> {
  controller = new AbortController();
  const timer = window.setTimeout(() => controller?.abort(), 150000);
  try {
    const res = await fetch(`/install/api/${path}`, {
      method: data === undefined ? "GET" : "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: {
        "Content-Type": "application/json",
        ...(data === undefined
          ? {}
          : { "X-Install-Token": token.value.trim() }),
      },
      ...(data === undefined ? {} : { body: JSON.stringify(data) }),
      signal: controller.signal,
    });
    const json = await res.json();
    if (!res.ok || json.code !== 0)
      throw new ApiError(
        json.message || "请求未完成，请重试。",
        res.status,
        json.errors || {},
      );
    return json.data;
  } catch (e) {
    if (e instanceof ApiError) throw e;
    throw new Error(
      "暂时无法取得结果，请检查网络后重试。安装中的任务会保留进度。",
    );
  } finally {
    window.clearTimeout(timer);
  }
}
async function attempt(action: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  error.value = "";
  fields.value = {};
  try {
    await action();
  } catch (e) {
    error.value = (e as Error).message;
    if (e instanceof ApiError) fields.value = e.fields;
    await nextTick();
    errorBox.value?.focus();
  } finally {
    busy.value = false;
  }
}
async function load() {
  loading.value = true;
  await attempt(async () => {
    locked.value = (await request("status")).installed;
  });
  loading.value = false;
}
async function checkEnvironment() {
  await attempt(async () => {
    if (!token.value.trim()) throw new Error("请先输入服务器安装码。");
    const result = await request("environment", {});
    checks.value = result.checks;
    ready.value = result.ready;
    resume.value = result.resumable;
  });
}
async function move(next: number) {
  step.value = next;
  error.value = "";
  fields.value = {};
  await nextTick();
  formRef.value?.clearValidate();
  document.querySelector<HTMLElement>(".install-title")?.focus();
}
async function testDatabase() {
  await attempt(async () => {
    if (!(await formRef.value?.validate().catch(() => false)))
      throw new Error("请检查标记的数据库字段。");
    databaseResult.value = await request("database", {
      database: form.database,
    });
  });
}
async function finish(isResume = false) {
  await attempt(async () => {
    if (!isResume && !(await formRef.value?.validate().catch(() => false)))
      throw new Error("请检查标记的站点与管理员字段。");
    try {
      await request(isResume ? "resume" : "complete", isResume ? {} : form);
    } catch (e) {
      // A lost response may still have completed installation. Inspect status
      // before inviting a retry; never submit a different administrator.
      try {
        if ((await request("status")).installed) {
          await installed();
          return;
        }
        const state = await request("environment", {});
        resume.value = state.resumable;
      } catch {
        /* Preserve the original actionable error. */
      }
      throw e;
    }
    await installed();
  });
}
async function installed() {
  form.database.password = "";
  form.admin_password = "";
  form.confirm_password = "";
  token.value = "";
  await move(3);
}
function leaving(e: BeforeUnloadEvent) {
  if (busy.value || (step.value > 0 && step.value < 3)) {
    e.preventDefault();
    e.returnValue = "";
  }
}
onMounted(() => {
  void load();
  window.addEventListener("beforeunload", leaving);
});
onBeforeUnmount(() => {
  controller?.abort();
  window.removeEventListener("beforeunload", leaving);
});
</script>

<template>
  <div class="install-page">
    <header class="install-header">
      <Brand /><span class="install-edition"
        >INSTALLATION <span>/</span> 初始化安装</span
      >
    </header>
    <main id="main" class="install-shell" tabindex="-1">
      <aside class="install-aside">
        <span class="eyebrow">YOUR NEXT CONNECTION</span>
        <h1>让每一次发现，<br /><span>都有新的可能。</span></h1>
        <p>从一个清晰有序的起点，连接内容、用户与 Telegram。</p>
        <ol class="install-steps" aria-label="安装步骤">
          <li
            v-for="(label, i) in steps"
            :key="label"
            :class="{ current: step === i, complete: step > i || locked }"
            :aria-current="step === i ? 'step' : undefined"
          >
            <span class="step-number"
              ><IosIcon v-if="step > i || locked" name="check" /><template
                v-else
                >0{{ i + 1 }}</template
              ></span
            >
            <span
              >{{ label
              }}<small>{{
                [
                  "检查环境与安装权限",
                  "连接 MySQL 8.0+",
                  "设置站点与管理账号",
                  "进入你的工作台",
                ][i]
              }}</small></span
            >
          </li>
        </ol>
        <div class="install-aside-note">
          <IosIcon name="lock" /><span
            >独立管理员 · 完整角色权限<br />网站与机器人，共用一个工作空间</span
          >
        </div>
      </aside>

      <section class="install-card" :aria-busy="busy || loading">
        <span class="eyebrow">{{
          locked ? "INSTALLATION LOCKED" : `STEP 0${step + 1} / 04`
        }}</span>
        <h2 class="install-title" tabindex="-1">{{ title }}</h2>
        <p class="install-subtitle">
          {{
            locked ? "初始化入口已关闭，可使用管理员账号登录后台。" : subtitle
          }}
        </p>
        <div
          v-if="error"
          ref="errorBox"
          class="form-alert install-error"
          role="alert"
          tabindex="-1"
        >
          {{ error }}
        </div>
        <div v-if="loading" class="install-loading" role="status">
          正在检查安装状态…
        </div>
        <template v-else-if="locked">
          <div class="install-success">
            <IosIcon name="check" />
            <h3>安装保护已生效</h3>
            <p>现有账号和数据将继续保留。</p>
          </div>
          <a
            href="/admin/login"
            class="el-button el-button--primary install-wide"
            >前往管理员登录<IosIcon name="right"
          /></a>
        </template>
        <template v-else-if="step === 0">
          <el-form label-position="top" @submit.prevent="checkEnvironment">
            <el-form-item label="服务器安装码" for="install-token" required>
              <el-input
                id="install-token"
                v-model="token"
                type="password"
                show-password
                autocomplete="off"
                :disabled="busy"
                placeholder="粘贴服务器生成的安装码"
                @input="
                  ready = false;
                  checks = [];
                "
              />
              <div class="field-help">
                在服务器项目根目录读取
                <code>.install-token</code>，用于确认你拥有本次部署的管理权限。
              </div>
            </el-form-item>
            <el-button
              :type="ready ? 'default' : 'primary'"
              native-type="submit"
              :loading="busy"
              >{{ checks.length ? "重新检查" : "验证并检查环境" }}</el-button
            >
          </el-form>
          <ul v-if="checks.length" class="install-checks" aria-live="polite">
            <li v-for="check in checks" :key="check.key">
              <IosIcon
                :name="check.ok ? 'check' : 'close'"
                :class="check.ok ? 'check-ok' : 'check-failed'"
              /><span
                >{{ check.label
                }}<small v-if="!check.ok">{{ check.detail }}</small></span
              ><strong :class="check.ok ? 'check-ok' : 'check-failed'">{{
                check.ok ? "已通过" : "待修复"
              }}</strong>
            </li>
          </ul>
          <div v-if="resume" class="install-note">
            <IosIcon name="clock" /><span
              >检测到未完成的安装任务。继续安装会沿用原数据库与管理员设置，恢复已完成的步骤。</span
            >
          </div>
          <div v-else class="install-note">
            <IosIcon name="help" /><span
              >准备一个空数据库和管理员账号即可开始。机器人
              Token、支付与广告配置可在安装后设置。</span
            >
          </div>
          <div v-if="ready" class="install-actions">
            <span>环境已就绪</span
            ><el-button
              type="primary"
              :loading="busy"
              @click="resume ? finish(true) : move(1)"
              >{{ resume ? "继续未完成安装" : "下一步" }}<IosIcon name="right"
            /></el-button>
          </div>
          <el-button
            v-if="!checks.length && error"
            text
            :disabled="busy"
            @click="load"
            >重新检查安装状态</el-button
          >
        </template>

        <template v-else-if="step === 1">
          <el-form
            ref="formRef"
            :model="form"
            :rules="rules"
            label-position="top"
            :disabled="busy"
            @submit.prevent="testDatabase"
            @change="databaseResult = undefined"
          >
            <div class="install-host-row">
              <el-form-item
                label="数据库地址"
                prop="database.host"
                :error="fields.host"
                for="db-host"
                required
                ><el-input
                  id="db-host"
                  v-model="form.database.host"
                  placeholder="127.0.0.1"
                  autocomplete="off"
                  @input="databaseResult = undefined"
              /></el-form-item>
              <el-form-item
                label="端口"
                for="db-port"
                :error="fields.port"
                required
                ><el-input-number
                  id="db-port"
                  v-model="form.database.port"
                  :min="1"
                  :max="65535"
                  :controls="false"
                  @change="databaseResult = undefined"
              /></el-form-item>
            </div>
            <el-form-item
              label="数据库名称"
              prop="database.name"
              :error="fields.name"
              for="db-name"
              required
              ><el-input
                id="db-name"
                v-model="form.database.name"
                :maxlength="64"
                @input="databaseResult = undefined"
              />
              <div class="field-help">
                使用独立空库；检测到已有数据表时会停止安装。
              </div></el-form-item
            >
            <div class="install-two-columns">
              <el-form-item
                label="数据库用户"
                prop="database.username"
                :error="fields.username"
                for="db-user"
                required
                ><el-input
                  id="db-user"
                  v-model="form.database.username"
                  autocomplete="off"
                  @input="databaseResult = undefined"
              /></el-form-item>
              <el-form-item
                label="数据库密码"
                for="db-password"
                :error="fields.password"
                ><el-input
                  id="db-password"
                  v-model="form.database.password"
                  type="password"
                  show-password
                  autocomplete="off"
                  @input="databaseResult = undefined"
              /></el-form-item>
            </div>
            <div class="install-options">
              <el-checkbox
                v-model="form.database.create_database"
                @change="databaseResult = undefined"
                >数据库不存在时自动创建</el-checkbox
              ><el-checkbox
                v-model="form.database.tls"
                @change="databaseResult = undefined"
                >使用 TLS 连接数据库</el-checkbox
              >
            </div>
            <div class="field-help">
              自动建库需要 CREATE 权限。远程数据库建议启用 TLS 并使用可信证书。
            </div>
            <div v-if="databaseResult" class="install-db-success" role="status">
              <IosIcon name="check" /><span
                >MySQL {{ databaseResult.version
                }}<small>{{ databaseResult.message }}</small></span
              >
            </div>
            <div class="install-actions">
              <el-button @click="move(0)">上一步</el-button
              ><el-button
                v-if="!databaseResult"
                type="primary"
                native-type="submit"
                :loading="busy"
                >测试数据库连接</el-button
              ><el-button v-else type="primary" @click="move(2)"
                >下一步<IosIcon name="right"
              /></el-button>
            </div>
          </el-form>
        </template>

        <template v-else-if="step === 2">
          <el-form
            ref="formRef"
            :model="form"
            :rules="rules"
            label-position="top"
            :disabled="busy"
            @submit.prevent="finish(resume)"
          >
            <el-form-item
              label="站点名称"
              prop="site_name"
              :error="fields.site_name"
              for="site-name"
              required
              ><el-input
                id="site-name"
                v-model="form.site_name"
                :maxlength="80"
            /></el-form-item>
            <el-form-item
              label="网站地址"
              for="site-url"
              :error="fields.public_url"
              ><el-input id="site-url" v-model="form.public_url" readonly />
              <div class="field-help">
                使用当前访问域名，安装后可在后台配置网站公开地址。
              </div></el-form-item
            >
            <div class="install-section-label">
              <IosIcon name="user" /><span>超级管理员</span>
            </div>
            <el-form-item
              label="管理员账号"
              prop="admin_username"
              :error="fields.admin_username"
              for="admin-username"
              required
              ><el-input
                id="admin-username"
                v-model="form.admin_username"
                autocomplete="username"
                :maxlength="32"
                placeholder="4–32 位字母、数字或下划线"
            /></el-form-item>
            <div class="install-two-columns">
              <el-form-item
                label="管理员密码"
                prop="admin_password"
                :error="fields.admin_password"
                for="admin-password"
                required
                ><el-input
                  id="admin-password"
                  v-model="form.admin_password"
                  type="password"
                  show-password
                  autocomplete="new-password"
              /></el-form-item>
              <el-form-item
                label="确认密码"
                prop="confirm_password"
                :error="fields.confirm_password"
                for="confirm-password"
                required
                ><el-input
                  id="confirm-password"
                  v-model="form.confirm_password"
                  type="password"
                  show-password
                  autocomplete="new-password"
              /></el-form-item>
            </div>
            <div class="field-help">
              密码 10–72
              字节。管理员与普通用户独立，可在后台继续配置角色和字段权限。
            </div>
            <div class="install-note">
              <IosIcon name="lock" /><span>{{
                resume
                  ? "上次安装任务已保存，请继续原任务。"
                  : "安装完成后会自动关闭初始化入口，机器人、支付和广告功能由你在后台配置后启用。"
              }}</span>
            </div>
            <div v-if="busy" class="install-progress" role="status">
              正在初始化数据库和配置，通常需要几秒钟，请保留此页面…
            </div>
            <div class="install-actions">
              <el-button :disabled="resume" @click="move(1)">上一步</el-button
              ><el-button type="primary" native-type="submit" :loading="busy"
                >{{ resume ? "继续安装" : "开始安装" }}<IosIcon name="right"
              /></el-button>
            </div>
          </el-form>
        </template>

        <template v-else>
          <div class="install-success">
            <IosIcon name="check" />
            <h3>你的星索已准备就绪</h3>
            <p>安装入口已锁定，凭刚设置的管理员账号登录即可开始。</p>
          </div>
          <div class="install-next">
            <span><IosIcon name="bot" />连接 Telegram 机器人</span
            ><span><IosIcon name="folder" />导入第一批资源</span
            ><span><IosIcon name="user" />配置团队角色与权限</span>
          </div>
          <div class="install-actions">
            <a href="/" class="el-button">查看网站</a
            ><a href="/admin/login" class="el-button el-button--primary"
              >进入管理后台<IosIcon name="right"
            /></a>
          </div>
        </template>
      </section>
    </main>
    <footer class="install-footer">
      星索 StarIndex <span>·</span> 连接内容，发现更多可能。
    </footer>
  </div>
</template>

<style scoped>
.install-page {
  min-height: 100dvh;
  background:
    radial-gradient(ellipse at 10% 20%, #eaf1ff, transparent 48%), var(--canvas);
}
.install-header {
  max-width: 1200px;
  margin: auto;
  padding: 30px 36px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
}
.install-edition {
  color: var(--muted);
  font-size: 12px;
  letter-spacing: 1.2px;
}
.install-edition span {
  margin: 0 12px;
  color: var(--line);
}
.install-shell {
  max-width: 1180px;
  padding: 24px 36px 40px;
  margin: auto;
  display: grid;
  grid-template-columns: minmax(280px, 0.85fr) minmax(0, 1.3fr);
  gap: 72px;
  align-items: start;
}
.install-aside {
  padding: 24px 0;
}
.install-aside h1 {
  margin-top: 20px;
  font-size: clamp(28px, 3vw, 38px);
  line-height: 1.5;
  letter-spacing: -1.2px;
}
.install-aside h1 span {
  color: var(--primary);
}
.install-aside > p {
  color: var(--muted);
  max-width: 310px;
  font-size: 15px;
  margin: 18px 0 32px;
}
.install-steps {
  padding: 0;
  margin: 0;
  list-style: none;
  display: grid;
  gap: 24px;
}
.install-steps li {
  display: flex;
  gap: 16px;
  align-items: center;
  color: var(--muted);
}
.step-number {
  width: 42px;
  height: 42px;
  border: 1px solid var(--line);
  background: var(--surface);
  border-radius: 13px;
  display: grid;
  place-items: center;
  font-size: 13px;
  font-weight: 600;
}
.install-steps .current {
  color: var(--ink);
  font-weight: 600;
}
.current .step-number {
  color: white;
  background: var(--primary);
  border-color: var(--primary);
  box-shadow: 0 6px 18px #2859e82b;
}
.complete .step-number {
  color: var(--success);
  background: #e6f5f0;
  border-color: #c5e9dc;
}
.install-steps small {
  display: block;
  margin-top: 3px;
  color: var(--muted);
  font-size: 12px;
  font-weight: 400;
}
.install-aside-note {
  margin-top: 36px;
  padding-top: 24px;
  border-top: 1px solid var(--line);
  display: flex;
  gap: 12px;
  color: var(--muted);
  font-size: 12px;
  align-items: center;
}
.install-aside-note .tg-ios-icon {
  font-size: 22px;
}
.install-card {
  background: var(--surface);
  padding: 36px;
  border: 1px solid var(--line);
  border-radius: 20px;
  box-shadow: 0 14px 60px #14243d08;
  min-width: 0;
}
.install-title {
  font-size: 26px;
  margin: 12px 0;
  letter-spacing: -0.6px;
  outline: none;
}
.install-subtitle {
  color: var(--muted);
  margin-bottom: 28px;
}
.install-error {
  margin-bottom: 24px;
}
.install-loading {
  color: var(--muted);
  padding: 48px 0;
  text-align: center;
}
.install-card :deep(.el-form-item) {
  margin-bottom: 22px;
}
.install-card :deep(.el-input-number) {
  width: 100%;
}
.install-card :deep(.el-input-number input) {
  text-align: left;
}
.install-card :deep(.el-form-item__error) {
  position: static;
  line-height: 1.5;
  padding-top: 6px;
}
.install-card :deep(.el-form-item__label) {
  font-weight: 500;
}
.install-card :deep(.el-checkbox) {
  height: auto;
  min-height: 32px;
  margin-right: 0;
}
.install-card :deep(.el-checkbox__label) {
  white-space: normal;
}
.install-host-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 110px;
  gap: 16px;
}
.install-two-columns {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 16px;
}
.install-options {
  display: grid;
  gap: 6px;
  margin-bottom: 8px;
}
.install-checks {
  list-style: none;
  padding: 16px 0 0;
  margin: 16px 0 0;
}
.install-checks li {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 0;
  border-bottom: 1px solid var(--line);
}
.install-checks li > span {
  flex: 1;
}
.install-checks small {
  display: block;
  color: var(--muted);
  margin-top: 4px;
}
.install-checks strong {
  font-size: 12px;
  white-space: nowrap;
}
.check-ok {
  color: var(--success);
}
.check-failed {
  color: var(--danger);
}
.install-note {
  display: flex;
  gap: 12px;
  margin-top: 24px;
  padding: 16px;
  border-radius: 10px;
  background: var(--canvas);
  font-size: 13px;
  color: var(--muted);
}
.install-note > .tg-ios-icon {
  font-size: 18px;
  margin-top: 3px;
}
.install-actions {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  border-top: 1px solid var(--line);
  margin-top: 28px;
  padding-top: 24px;
}
.install-actions > span {
  color: var(--success);
  font-size: 13px;
}
.install-actions .el-button {
  margin: 0;
  gap: 10px;
}
.install-wide {
  width: 100%;
  gap: 12px;
}
.install-db-success {
  display: flex;
  gap: 12px;
  padding: 16px;
  border: 1px solid #c5e9dc;
  background: #f2faf6;
  color: var(--success);
  border-radius: 10px;
  margin-top: 20px;
}
.install-db-success .tg-ios-icon {
  margin-top: 3px;
}
.install-db-success small {
  display: block;
  margin-top: 6px;
}
.install-section-label {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-top: 18px;
  margin: 6px 0 20px;
  border-top: 1px solid var(--line);
  font-weight: 600;
}
.install-progress {
  color: var(--primary);
  font-size: 13px;
  margin-top: 16px;
}
.install-success {
  text-align: center;
  padding: 26px 8px;
}
.install-success > .tg-ios-icon {
  font-size: 52px;
  color: var(--success);
  margin-bottom: 20px;
}
.install-success h3 {
  font-size: 20px;
}
.install-success p {
  color: var(--muted);
  margin-bottom: 0;
}
.install-next {
  display: grid;
  gap: 16px;
  padding: 20px;
  background: var(--canvas);
  border-radius: 12px;
}
.install-next span {
  display: flex;
  align-items: center;
  gap: 12px;
  color: var(--muted);
}
.install-next .tg-ios-icon {
  color: var(--primary);
}
.install-footer {
  padding: 0 24px 24px;
  text-align: center;
  font-size: 12px;
  color: var(--muted);
}
.install-footer span {
  margin: 0 8px;
}
@media (max-width: 960px) {
  .install-shell {
    gap: 32px;
    grid-template-columns: minmax(220px, 0.8fr) minmax(0, 1.3fr);
    padding: 16px 24px 32px;
  }
  .install-card {
    padding: 28px;
  }
  .install-aside h1 {
    font-size: 28px;
  }
}
@media (max-width: 720px) {
  .install-header {
    padding: 22px 20px;
  }
  .install-edition {
    display: none;
  }
  .install-shell {
    display: block;
    padding: 0 20px 24px;
  }
  .install-aside {
    padding: 0;
  }
  .install-aside > .eyebrow,
  .install-aside h1,
  .install-aside > p,
  .install-aside-note {
    display: none;
  }
  .install-steps {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 8px;
    margin: 10px 0 24px;
  }
  .install-steps li {
    display: flex;
    flex-direction: column;
    gap: 8px;
    text-align: center;
    font-size: 12px;
  }
  .install-steps small {
    display: none;
  }
  .step-number {
    width: 34px;
    height: 34px;
    border-radius: 10px;
  }
  .install-card {
    padding: 24px 20px;
    border-radius: 16px;
  }
  .install-title {
    font-size: 23px;
  }
  .install-two-columns {
    display: block;
  }
  .install-host-row {
    gap: 12px;
    grid-template-columns: minmax(0, 1fr) 90px;
  }
  .install-card :deep(.el-input__inner) {
    font-size: 16px;
  }
  .install-actions {
    gap: 12px;
  }
  .install-actions .el-button {
    min-height: 44px;
  }
  .install-checks li {
    gap: 8px;
  }
  .install-note {
    padding: 12px;
  }
}
</style>
