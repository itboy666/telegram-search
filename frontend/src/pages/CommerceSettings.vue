<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { ElMessage } from "element-plus";
import { api } from "../lib/api";
import { useAuth } from "../lib/auth";
import LoadError from "../components/LoadError.vue";
const auth = useAuth(),
  form = reactive<Record<string, any>>({}),
  loading = ref(false),
  saving = ref(false),
  checking = ref(false),
  error = ref("");
const read = (field: string) => auth.readField("commerce_settings", field);
const write = (field: string) => auth.writeField("commerce_settings", field);
async function load() {
  loading.value = true;
  error.value = "";
  try {
    Object.assign(form, await api("/admin/api/commerce/settings"), {
      api_key: "",
    });
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    loading.value = false;
  }
}
async function save() {
  saving.value = true;
  error.value = "";
  try {
    const values = auth.writable("commerce_settings", form);
    if (!values.api_key) delete values.api_key;
    Object.assign(form, await api("/admin/api/commerce/settings", values), {
      api_key: "",
    });
    ElMessage.success("支付与奖励设置已保存");
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    saving.value = false;
  }
}
async function check() {
  checking.value = true;
  try {
    const r = await api("/admin/api/commerce/connection", {});
    ElMessage.success(r.message);
  } catch (e) {
    ElMessage.error((e as Error).message);
  } finally {
    checking.value = false;
  }
}
onMounted(load);
</script>
<template>
  <div class="page-heading">
    <div>
      <span class="eyebrow">PAYMENTS & REWARDS</span>
      <h1>支付与奖励设置</h1>
      <p>统一使用 USDT 记账，奖励按北京时间自然日结算。</p>
    </div>
    <el-button
      v-if="auth.can('commerce_settings.update')"
      type="primary"
      :loading="saving"
      :disabled="loading"
      @click="save"
      >保存设置</el-button
    >
  </div>
  <LoadError :message="error" @retry="load" />
  <el-skeleton v-if="loading" :rows="8" animated />
  <el-form
    v-else
    label-position="top"
    class="commerce-settings"
    @submit.prevent="save"
  >
    <section class="panel">
      <div class="commerce-section-heading">
        <div>
          <h2>USDT-TRC20 充值</h2>
          <p class="muted">生成专属应付金额，Tronscan 确认成功后自动入账。</p>
        </div>
        <el-switch
          v-if="read('trc20_enabled')"
          v-model="form.trc20_enabled"
          :disabled="!write('trc20_enabled')"
          active-text="启用"
        />
      </div>
      <el-form-item v-if="read('receive_address')" label="TRON 主网收款地址"
        ><el-input
          v-model="form.receive_address"
          maxlength="34"
          :disabled="!write('receive_address')"
          placeholder="T 开头的 TRC20 地址"
      /></el-form-item>
      <el-form-item v-if="read('api_key')" label="Tronscan API Key"
        ><el-input
          v-model="form.api_key"
          type="password"
          autocomplete="new-password"
          :disabled="!write('api_key')"
          :placeholder="
            form.key_configured
              ? '已配置。留空保留原密钥，输入内容以替换。'
              : '输入 Tronscan API Key'
          "
        />
        <p class="field-help">
          密钥加密保存，保存后不回显。系统仅查询转账，不需要钱包私钥。
        </p></el-form-item
      >
      <div class="commerce-connection">
        <span v-if="read('key_configured')" class="muted">{{
          form.key_configured ? "API Key 已配置" : "尚未配置 API Key"
        }}</span
        ><el-button
          v-if="auth.can('commerce_settings.update')"
          :loading="checking"
          @click="check"
          >测试已保存的连接</el-button
        >
      </div>
    </section>
    <section class="panel">
      <div class="commerce-section-heading">
        <div>
          <h2>线下转账</h2>
          <p class="muted">用户提交转账编号，管理员核实到账后批准入账。</p>
        </div>
        <el-switch
          v-if="read('offline_enabled')"
          v-model="form.offline_enabled"
          :disabled="!write('offline_enabled')"
          active-text="启用"
        />
      </div>
      <el-form-item v-if="read('offline_instructions')" label="收款与转账说明"
        ><el-input
          v-model="form.offline_instructions"
          type="textarea"
          :rows="5"
          maxlength="2000"
          show-word-limit
          :disabled="!write('offline_instructions')"
          placeholder="填写收款账户、币种、换算规则与用户需要提供的付款凭证。"
      /></el-form-item>
    </section>
    <section class="panel">
      <div class="commerce-section-heading">
        <div>
          <h2>每日搜索奖励</h2>
          <p class="muted">
            登录用户的首次有效关键词搜索自动发放，奖励预算不足时停止发放。
          </p>
        </div>
        <el-switch
          v-if="read('reward_enabled')"
          v-model="form.reward_enabled"
          :disabled="!write('reward_enabled')"
          active-text="启用"
        />
      </div>
      <div class="commerce-form-grid">
        <el-form-item v-if="read('reward_amount')" label="首次搜索奖励 / USDT"
          ><el-input
            v-model="form.reward_amount"
            inputmode="decimal"
            :disabled="!write('reward_amount')"
        /></el-form-item>
        <el-form-item
          v-if="read('reward_daily_cap')"
          label="全站每日上限 / USDT"
          ><el-input
            v-model="form.reward_daily_cap"
            inputmode="decimal"
            :disabled="!write('reward_daily_cap')"
        /></el-form-item>
        <el-form-item
          v-if="read('reward_user_cap')"
          label="单用户累计上限 / USDT"
          ><el-input
            v-model="form.reward_user_cap"
            inputmode="decimal"
            :disabled="!write('reward_user_cap')"
        /></el-form-item>
      </div>
      <p class="field-help">
        同一用户每天最多奖励一次。无结果、发现资源、翻页和开放 API
        不参与。设置默认关闭，启用前请确认预算。
      </p>
    </section>
    <section v-if="read('public_url')" class="panel">
      <h2>用户中心地址</h2>
      <p class="muted">
        机器人将此地址发送给用户。上线后应改为用户可访问的 HTTPS 域名。
      </p>
      <el-form-item label="网站根地址"
        ><el-input
          v-model="form.public_url"
          :disabled="!write('public_url')"
          placeholder="https://example.com"
      /></el-form-item>
    </section>
  </el-form>
</template>
