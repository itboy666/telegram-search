<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { ElMessage, ElMessageBox } from "element-plus";
import { api, dateTime } from "../lib/api";
import { useAuth } from "../lib/auth";
import {
  adKinds,
  commerceStatuses,
  entryKinds,
  requestKey,
} from "../lib/commerce";
import {
  Plus,
  Collection,
  Promotion,
  Refresh,
  SwitchButton,
} from "../lib/icons";
import IosIcon from "../components/IosIcon.vue";
import LoadError from "../components/LoadError.vue";
import RechargeProofImage from "../components/RechargeProofImage.vue";

const auth = useAuth(),
  route = useRoute(),
  router = useRouter();
const summary = ref<any>(null),
  slots = ref<any[]>([]),
  rows = ref<any[]>([]),
  total = ref(0),
  page = ref(1);
const tab = ref(
  ["marketing", "recharges", "ads", "ledger"].includes(String(route.query.tab))
    ? String(route.query.tab)
    : "marketing",
);
const loading = ref(false),
  error = ref(""),
  busy = ref(false),
  formError = ref("");
const rechargeOpen = ref(false),
  order = ref<any>(null),
  buySlot = ref<any>(null),
  details = ref<any>(null);
const payment = reactive({
  method: "trc20",
  amount: "10",
  request_key: "",
  txid: "",
  proof: "",
});
const ad = reactive({
  title: "",
  url: "",
  content: "",
  keyword: "",
  request_key: "",
});
const config = computed(() => summary.value?.config || {});
const canRecharge = computed(
  () => config.value.trc20_enabled || config.value.offline_enabled,
);
const tabNames: Record<string, string> = {
  marketing: "营销中心",
  recharges: "充值订单",
  ads: "我的广告",
  ledger: "历史账单",
};
const titles: Record<string, string> = {
  keyword: "让搜索找到你",
  brand: "让品牌更清晰",
  search_top: "获得首屏关注",
  search_bottom: "一步直达链接",
  group_pin: "留在群组顶部",
  channel: "向订阅者展示",
};
const icons: Record<string, string> = {
  keyword: "search",
  brand: "star",
  search_top: "up",
  search_bottom: "link",
  group_pin: "group",
  channel: "channel",
};
let generation = 0;
async function load() {
  const id = ++generation;
  loading.value = true;
  error.value = "";
  try {
    const [s, catalog, list] = await Promise.all([
      api("/user/api/commerce/summary"),
      api("/user/api/commerce/slots"),
      tab.value === "marketing"
        ? Promise.resolve({ items: [], total: 0 })
        : api(`/user/api/commerce/records/${tab.value}?page=${page.value}`),
    ]);
    if (id !== generation) return;
    summary.value = s;
    slots.value = catalog.items;
    rows.value = list.items;
    total.value = list.total;
  } catch (e) {
    if (id === generation) error.value = (e as Error).message;
  } finally {
    if (id === generation) loading.value = false;
  }
}
watch(tab, () => {
  page.value = 1;
  router.replace({ query: { tab: tab.value } });
  load();
});
onMounted(load);
function openRecharge(existing?: any) {
  order.value = existing || null;
  formError.value = "";
  Object.assign(payment, {
    method: config.value.trc20_enabled ? "trc20" : "offline",
    amount: "10",
    request_key: requestKey(),
    txid: "",
    proof: existing?.proof || "",
  });
  rechargeOpen.value = true;
}
async function mutate(fn: () => Promise<any>, success: string) {
  busy.value = true;
  formError.value = "";
  try {
    await fn();
    ElMessage.success(success);
    await load();
  } catch (e) {
    formError.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
function createRecharge() {
  return mutate(async () => {
    order.value = await api("/user/api/commerce/recharges", {
      method: payment.method,
      amount: payment.amount,
      request_key: payment.request_key,
    });
  }, "订单已创建，请按应付金额付款");
}
function checkPayment() {
  return mutate(async () => {
    order.value = await api(
      `/user/api/commerce/recharges/${order.value.id}/check`,
      { txid: payment.txid },
    );
  }, "充值已确认入账");
}
function submitProof() {
  return mutate(async () => {
    order.value = await api(
      `/user/api/commerce/recharges/${order.value.id}/proof`,
      { proof: payment.proof },
    );
  }, "凭证已提交，等待核账");
}
function openBuy(slot: any) {
  buySlot.value = slot;
  formError.value = "";
  Object.assign(ad, {
    title: "",
    url: "",
    content: "",
    keyword: "",
    request_key: requestKey(),
  });
}
async function purchase() {
  try {
    await ElMessageBox.confirm(
      `将从余额扣除 ${buySlot.value.price} USDT，审核通过后开始计算 ${buySlot.value.duration_days} 天，拒绝审核会全额退回余额。`,
      "确认购买",
      { confirmButtonText: "确认扣款", cancelButtonText: "再检查一下" },
    );
  } catch {
    return;
  }
  await mutate(async () => {
    await api("/user/api/commerce/ads", {
      ...ad,
      slot_id: buySlot.value.id,
      quoted_price: buySlot.value.price,
      quoted_days: buySlot.value.duration_days,
    });
    buySlot.value = null;
    tab.value = "ads";
  }, "购买成功，广告等待审核");
}
async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text);
    ElMessage.success("已复制");
  } catch {
    ElMessage.error("复制失败，请手动选中文字复制");
  }
}
async function logout() {
  try {
    await auth.logout("user");
    router.push("/");
  } catch (e) {
    ElMessage.error((e as Error).message);
  }
}
</script>
<template>
  <div class="user-center commerce-space">
    <div class="page-heading">
      <div>
        <span class="eyebrow">PERSONAL WORKSPACE</span>
        <h1>你好，{{ auth.users.user?.display_name }}</h1>
        <p>你的发现、余额与推广，在这里有序连接。</p>
      </div>
      <div class="heading-actions">
        <router-link to="/user/security" class="commerce-link"
          ><IosIcon name="lock" />账户与安全</router-link
        >
        <router-link to="/user/collections" class="commerce-link"
          ><el-icon><Collection /></el-icon>收藏与收录</router-link
        >
        <el-button :icon="SwitchButton" @click="logout">退出</el-button>
        <el-button
          type="primary"
          :icon="Plus"
          :disabled="!canRecharge"
          @click="openRecharge()"
          >充值余额</el-button
        >
      </div>
    </div>
    <LoadError :message="error" @retry="load" />
    <div v-if="summary" class="commerce-stats">
      <article class="panel balance-card">
        <div class="stat-label">可用余额 <span>USDT</span></div>
        <strong>{{ summary.balance }}</strong>
        <p>账户 #{{ auth.users.user?.id }} · 充值与奖励可用于广告购买</p>
      </article>
      <article class="panel">
        <div class="stat-label">今日搜索奖励 <IosIcon name="star" /></div>
        <strong>{{ summary.reward_today }} <small>USDT</small></strong>
        <p>
          {{
            config.reward_enabled
              ? `首次有效搜索奖励 ${config.reward_amount} USDT`
              : "当前未开启，开启后首次有效搜索自动入账"
          }}
        </p>
      </article>
      <article class="panel">
        <div class="stat-label">
          正在投放 <el-icon><Promotion /></el-icon>
        </div>
        <strong>{{ summary.active_ads }} <small>条广告</small></strong>
        <p>审核通过开始计时，投放进度随时可查</p>
      </article>
    </div>
    <div class="commerce-tabs" aria-label="用户中心栏目">
      <button
        v-for="(label, key) in tabNames"
        :key="key"
        :class="{ active: tab === key }"
        :aria-current="tab === key ? 'page' : undefined"
        @click="tab = String(key)"
      >
        {{ label }}
      </button>
      <el-button text :icon="Refresh" :loading="loading" @click="load"
        >刷新</el-button
      >
    </div>
    <el-skeleton v-if="loading" :rows="5" animated />
    <template v-else-if="!error">
      <section v-if="tab === 'marketing'">
        <div class="commerce-section-heading">
          <div>
            <h2>选择适合你的推广方式</h2>
            <p class="muted">
              先提交内容，审核通过后展示。价格与可售名额以当前广告位为准。
            </p>
          </div>
        </div>
        <div class="ad-slot-grid">
          <article
            v-for="slot in slots"
            :key="slot.id"
            class="panel ad-slot-card"
          >
            <div class="ad-slot-top">
              <span class="commerce-icon"
                ><IosIcon :name="icons[slot.kind] || 'link'" /></span
              ><el-tag
                :type="slot.enabled ? 'success' : 'info'"
                effect="plain"
                >{{ slot.enabled ? "开放购买" : "暂未开放" }}</el-tag
              >
            </div>
            <h3>{{ slot.name }}</h3>
            <span class="muted">{{ titles[slot.kind] }}</span>
            <p>{{ slot.description }}</p>
            <div class="ad-slot-bottom">
              <div>
                <strong>{{ slot.price }}</strong><small>
                  USDT / {{ slot.duration_days }} 天</small
                >
              </div>
              <el-button :disabled="!slot.enabled" @click="openBuy(slot)"
                >查看并购买</el-button
              >
            </div>
          </article>
        </div>
        <el-empty
          v-if="!slots.length"
          description="暂时没有广告位，请稍后再来。"
        />
        <div class="commerce-note">
          <IosIcon name="info" />品牌展示为本站广告服务，不代表 Telegram
          官方认证。自然搜索结果不受广告订单影响。
        </div>
      </section>
      <section v-else class="panel commerce-records">
        <div class="commerce-section-heading">
          <h2>{{ tabNames[tab] }}</h2>
          <span class="muted">共 {{ total }} 条 · 金额单位 USDT</span>
        </div>
        <el-table :data="rows" empty-text="还没有记录" table-layout="fixed">
          <el-table-column prop="id" label="ID" width="88" />
          <template v-if="tab === 'ledger'">
            <el-table-column label="类型" width="130"
              ><template #default="{ row }">{{
                entryKinds[row.kind] || row.kind
              }}</template></el-table-column
            >
            <el-table-column prop="amount" label="变动金额" width="145" />
            <el-table-column
              prop="balance_after"
              label="变动后余额"
              width="145"
            />
            <el-table-column
              prop="note"
              label="说明"
              min-width="200"
              show-overflow-tooltip
            />
          </template>
          <template v-else>
            <el-table-column
              :label="tab === 'ads' ? '广告' : '充值方式'"
              min-width="180"
              show-overflow-tooltip
              ><template #default="{ row }">{{
                tab === "ads"
                  ? row.title
                  : row.method === "trc20"
                    ? "USDT-TRC20"
                    : "线下转账"
              }}</template></el-table-column
            >
            <el-table-column
              :prop="tab === 'ads' ? 'price' : 'amount'"
              label="金额"
              width="150"
            />
            <el-table-column label="状态" width="150"
              ><template #default="{ row }"
                ><el-tag
                  :type="
                    ['paid', 'active'].includes(row.status) ? 'success' : 'info'
                  "
                  >{{ commerceStatuses[row.status] || row.status }}</el-tag
                ></template
              ></el-table-column
            >
          </template>
          <el-table-column prop="created_at" label="创建时间" width="175" />
          <el-table-column
            v-if="tab !== 'ledger'"
            label="操作"
            width="130"
            fixed="right"
            ><template #default="{ row }">
              <el-button
                text
                type="primary"
                @click="
                  tab === 'recharges' ? openRecharge(row) : (details = row)
                "
                >查看详情</el-button
              >
            </template></el-table-column
          >
        </el-table>
        <div class="pagination">
          <span>第 {{ page }} 页</span
          ><el-pagination
            v-model:current-page="page"
            :total="total"
            :page-size="20"
            :pager-count="5"
            layout="prev,pager,next"
            @current-change="load"
          />
        </div>
      </section>
    </template>
    <p v-if="summary" class="muted commerce-reward-hint">
      累计搜索奖励
      {{ summary.reward_total }}
      USDT。每日按北京时间统计，仅有结果的关键词搜索参与；翻页、筛选、开放 API
      不重复奖励。单用户累计上限 {{ config.reward_user_cap }} USDT，全站每日上限
      {{ config.reward_daily_cap }} USDT。
    </p>
    <el-dialog
      v-model="rechargeOpen"
      :title="order ? `充值订单 #${order.id}` : '充值余额'"
      width="520px"
      class="commerce-dialog"
      :close-on-click-modal="false"
    >
      <div v-if="formError" class="form-alert" role="alert">
        {{ formError }}
      </div>
      <el-form
        v-if="!order"
        label-position="top"
        @submit.prevent="createRecharge"
      >
        <el-form-item label="充值方式"
          ><el-radio-group v-model="payment.method"
            ><el-radio-button v-if="config.trc20_enabled" value="trc20"
              >USDT-TRC20</el-radio-button
            ><el-radio-button v-if="config.offline_enabled" value="offline"
              >线下转账</el-radio-button
            ></el-radio-group
          ></el-form-item
        >
        <el-form-item label="计划充值金额 / USDT"
          ><el-input
            v-model="payment.amount"
            inputmode="decimal"
            maxlength="14"
          />
          <p class="field-help">
            {{
              payment.method === "trc20"
                ? "请输入整数金额。创建后系统分配小数尾款，请支付完整应付金额；全部金额计入余额。"
                : "线下转账完成后提交付款人、转账时间和凭证编号；也可在机器人私聊中发送截图，管理员核账后入账。"
            }}
          </p></el-form-item
        >
        <el-button native-type="submit" type="primary" :loading="busy"
          >创建充值订单</el-button
        >
      </el-form>
      <div v-else class="payment-detail">
        <el-tag>{{ commerceStatuses[order.status] || order.status }}</el-tag>
        <div class="payment-total">
          <span>应付金额 / USDT</span><strong>{{ order.amount }}</strong
          ><el-button text @click="copy(order.amount)">复制完整金额</el-button>
        </div>
        <template v-if="order.method === 'trc20'">
          <label>TRC20 收款地址</label>
          <div class="address-box">
            <code>{{ order.receive_address }}</code><el-button
              text
              @click="copy(order.receive_address)"
              >复制</el-button
            >
          </div>
          <p class="field-help">
            仅转入 TRON 主网 USDT，完整保留六位小数。请在
            {{ dateTime(order.expires_at) }}
            前转账。过期后不要付款；此前已转出的交易仍会继续核查。
          </p>
          <el-form
            v-if="order.status !== 'paid'"
            label-position="top"
            @submit.prevent="checkPayment"
            ><el-form-item label="交易哈希（可选，填写可加快核对）"
              ><el-input
                v-model="payment.txid"
                maxlength="64"
                placeholder="64 位交易哈希" /></el-form-item
            ><el-button type="primary" native-type="submit" :loading="busy"
              >核查到账</el-button
            ></el-form
          >
          <p v-if="order.txid" class="break-word">
            交易哈希：<code>{{ order.txid }}</code>
          </p>
        </template>
        <template v-else>
          <div class="offline-instructions">
            {{
              config.offline_instructions || "请联系管理员获取线下收款说明。"
            }}
          </div>
          <RechargeProofImage
            scope="user"
            :recharge-id="order.id"
            :has-image="!!order.has_proof_image"
            :active="rechargeOpen"
          >
            可在机器人私聊中打开这笔充值订单，按提示发送转账截图。
          </RechargeProofImage>
          <p
            v-if="order.proof && !['pending', 'review'].includes(order.status)"
            class="offline-instructions"
          >
            {{ order.proof }}
          </p>
          <el-form
            v-if="['pending', 'review'].includes(order.status)"
            label-position="top"
            @submit.prevent="submitProof"
            ><el-form-item label="转账凭证说明"
              ><el-input
                v-model="payment.proof"
                type="textarea"
                :rows="4"
                maxlength="500"
                show-word-limit
                placeholder="付款人、转账时间、转账编号" /></el-form-item
            ><el-button type="primary" native-type="submit" :loading="busy"
              >提交核账</el-button
            ></el-form
          >
        </template>
        <p v-if="order.review_note" class="review-note">
          {{ order.review_note }}
        </p>
      </div>
    </el-dialog>
    <el-drawer
      :model-value="!!buySlot"
      title="购买广告位"
      size="540px"
      class="form-drawer"
      :close-on-click-modal="false"
      @close="buySlot = null"
    >
      <template v-if="buySlot">
        <div class="commerce-purchase-summary">
          <h2>{{ buySlot.name }}</h2>
          <p>{{ buySlot.description }}</p>
          <strong
            >{{ buySlot.price }} USDT / {{ buySlot.duration_days }} 天</strong
          >
        </div>
        <div v-if="formError" class="form-alert" role="alert">
          {{ formError }}
        </div>
        <el-form label-position="top" @submit.prevent="purchase">
          <el-form-item
            v-if="['keyword', 'brand'].includes(buySlot.kind)"
            label="精确匹配词"
            ><el-input
              v-model="ad.keyword"
              maxlength="80"
              placeholder="例如：设计工具"
          /></el-form-item>
          <el-form-item label="广告标题"
            ><el-input v-model="ad.title" maxlength="60" show-word-limit
          /></el-form-item>
          <el-form-item label="推广链接"
            ><el-input
              v-model="ad.url"
              maxlength="500"
              placeholder="https://t.me/your_channel"
          /></el-form-item>
          <el-form-item label="广告正文"
            ><el-input
              v-model="ad.content"
              type="textarea"
              :rows="4"
              maxlength="500"
              show-word-limit
            />
            <p class="field-help">
              群置顶与频道广告展示正文；搜索列表仅展示标题和链接。
            </p></el-form-item
          >
          <p class="muted">
            提交时从余额扣款。审核通过后开始计时，拒绝将全额退款。
          </p>
          <el-button type="primary" native-type="submit" :loading="busy"
            >确认内容并购买</el-button
          >
        </el-form>
      </template>
    </el-drawer>
    <el-drawer
      :model-value="!!details"
      title="广告订单详情"
      size="520px"
      class="form-drawer"
      @close="details = null"
    >
      <template v-if="details"
        ><h2>{{ details.title }}</h2>
        <p>{{ adKinds[details.kind] }} · 订单 #{{ details.id }}</p>
        <el-tag>{{ commerceStatuses[details.status] }}</el-tag>
        <p class="commerce-detail-copy">{{ details.content }}</p>
        <a
          :href="details.url"
          target="_blank"
          rel="noopener noreferrer"
          class="break-word"
          >{{ details.url }}</a><el-descriptions
          :column="1"
          border
          class="commerce-detail-copy"
          ><el-descriptions-item label="实付"
            >{{ details.price }} USDT</el-descriptions-item
          ><el-descriptions-item label="开始">{{
            dateTime(details.starts_at)
          }}</el-descriptions-item
          ><el-descriptions-item label="结束">{{
            dateTime(details.ends_at)
          }}</el-descriptions-item
          ><el-descriptions-item label="审核说明">{{
            details.review_note || "等待审核"
          }}</el-descriptions-item></el-descriptions
        ></template
      >
    </el-drawer>
  </div>
</template>
