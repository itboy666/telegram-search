<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import { useRoute } from "vue-router";
import { ElMessage, ElMessageBox } from "element-plus";
import { api, query } from "../lib/api";
import { useAuth } from "../lib/auth";
import {
  adKinds,
  commerceLabels,
  commerceStatuses,
  commerceValue,
} from "../lib/commerce";
import { Plus, Refresh } from "../lib/icons";
import LoadError from "../components/LoadError.vue";
import RechargeProofImage from "../components/RechargeProofImage.vue";

const route = useRoute(),
  auth = useAuth(),
  module = computed(() => String(route.meta.commerce));
const page = ref(1),
  rows = ref<any[]>([]),
  total = ref(0),
  loading = ref(false),
  error = ref(""),
  busy = ref(false);
const filter = reactive({ id: "", user_id: "", status: "" }),
  edit = ref(false),
  record = ref<any>(null),
  form = reactive<Record<string, any>>({}),
  formError = ref("");
const review = ref<any>(null),
  decision = ref("approve"),
  note = ref(""),
  txid = ref(""),
  messageId = ref("");
const read = (key: string) => auth.readField(module.value, key),
  write = (key: string) => auth.writeField(module.value, key);
const defaults = {
  name: "",
  kind: "keyword",
  description: "",
  price: "10.000000",
  duration_days: 7,
  capacity: 1,
  target_chat: "",
  enabled: false,
};
const fields: Record<string, string[]> = {
  wallets: ["user_id", "balance", "updated_at"],
  wallet_entries: [
    "user_id",
    "kind",
    "amount",
    "balance_after",
    "note",
    "created_at",
  ],
  recharges: ["user_id", "method", "amount", "status", "created_at", "paid_at"],
  ad_slots: [
    "name",
    "kind",
    "price",
    "duration_days",
    "enabled",
    "target_chat",
  ],
  ad_orders: [
    "user_id",
    "title",
    "slot_name",
    "price",
    "status",
    "starts_at",
    "ends_at",
  ],
};
const visible = computed(() => (fields[module.value] || []).filter(read));
const detailKeys = computed(() =>
  Object.keys(commerceLabels).filter((k) => record.value && k in record.value),
);
let generation = 0;
async function load() {
  const id = ++generation;
  loading.value = true;
  error.value = "";
  try {
    const values: Record<string, any> = { page: page.value, id: filter.id };
    if (read("user_id")) values.user_id = filter.user_id;
    if (read("status")) values.status = filter.status;
    const data = await api(
      `/admin/api/commerce/records/${module.value}?${query(values)}`,
    );
    if (id !== generation) return;
    rows.value = data.items;
    total.value = data.total;
  } catch (e) {
    if (id === generation) error.value = (e as Error).message;
  } finally {
    if (id === generation) loading.value = false;
  }
}
watch(module, () => {
  page.value = 1;
  filter.id = filter.user_id = filter.status = "";
  record.value = review.value = null;
  edit.value = false;
  load();
});
onMounted(load);
function openEdit(row?: any) {
  Object.keys(form).forEach((k) => delete form[k]);
  Object.assign(form, row || defaults);
  form.enabled = !!Number(form.enabled);
  formError.value = "";
  edit.value = true;
}
async function save() {
  busy.value = true;
  formError.value = "";
  try {
    await api(
      `/admin/api/commerce/slots${form.id ? "/" + form.id : ""}`,
      auth.writable("ad_slots", form),
    );
    edit.value = false;
    ElMessage.success("广告位已保存");
    await load();
  } catch (e) {
    formError.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function remove(row: any) {
  try {
    await ElMessageBox.confirm(
      `删除广告位 #${row.id}？已有订单的广告位只允许停用。`,
      "删除广告位",
      { type: "warning", confirmButtonText: "删除", cancelButtonText: "取消" },
    );
    await api(`/admin/api/commerce/slots/${row.id}`, {}, "DELETE");
    ElMessage.success("已删除");
    load();
  } catch (e) {
    if (e instanceof Error) ElMessage.error(e.message);
  }
}
function openReview(row: any) {
  review.value = row;
  note.value = txid.value = messageId.value = formError.value = "";
  decision.value =
    row.status === "pending" || row.status === "review" ? "approve" : "refund";
}
async function submitReview(action = "review") {
  busy.value = true;
  formError.value = "";
  try {
    const kind = module.value === "recharges" ? "recharges" : "ads";
    const input =
      action === "check"
        ? { txid: txid.value }
        : action === "resolve"
          ? { message_id: Number(messageId.value) }
          : { decision: decision.value, review_note: note.value };
    await api(
      `/admin/api/commerce/${kind}/${review.value.id}/${action}`,
      input,
    );
    review.value = null;
    ElMessage.success("订单已处理");
    await load();
  } catch (e) {
    formError.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
const canReview = (r: any) =>
  module.value === "recharges"
    ? (r.method === "offline" &&
        r.status === "review" &&
        auth.can("recharges.review") &&
        write("decision") &&
        write("review_note")) ||
      (r.method === "trc20" &&
        r.status !== "paid" &&
        auth.can("recharges.check") &&
        write("txid"))
    : module.value === "ad_orders" &&
      ((["pending", "active", "delivery_issue"].includes(r.status) &&
        auth.can("ad_orders.review") &&
        write("decision") &&
        write("review_note")) ||
        (r.status === "delivery_issue" &&
          auth.can("ad_orders.resolve") &&
          write("message_id")));
</script>
<template>
  <div class="page-heading">
    <div>
      <span class="eyebrow">COMMERCE WORKSPACE</span>
      <h1>{{ route.meta.title }}</h1>
      <p>
        {{
          module === "ad_slots"
            ? "配置展示位置、售价与投放时长，开放后用户即可购买。"
            : "查看真实订单与资金记录，所有金额以 USDT 计价。"
        }}
      </p>
    </div>
    <el-button
      v-if="module === 'ad_slots' && auth.can('ad_slots.create')"
      type="primary"
      :icon="Plus"
      @click="openEdit()"
      >新增广告位</el-button
    ><el-button v-else :icon="Refresh" :loading="loading" @click="load"
      >刷新列表</el-button
    >
  </div>
  <section class="panel">
    <form
      class="commerce-filter"
      @submit.prevent="
        page = 1;
        load();
      "
    >
      <el-input
        v-model="filter.id"
        placeholder="数据库 ID"
        aria-label="数据库 ID"
        inputmode="numeric"
        clearable
      />
      <el-input
        v-if="read('user_id')"
        v-model="filter.user_id"
        placeholder="用户 ID"
        aria-label="用户 ID"
        inputmode="numeric"
        clearable
      />
      <el-select
        v-if="read('status')"
        v-model="filter.status"
        placeholder="全部状态"
        aria-label="订单状态"
        clearable
        ><el-option
          v-for="(label, value) in commerceStatuses"
          :key="value"
          :label="label"
          :value="value"
      /></el-select>
      <el-button native-type="submit">查询</el-button
      ><el-button
        @click="
          filter.id = filter.user_id = filter.status = '';
          page = 1;
          load();
        "
        >重置</el-button
      >
    </form>
    <LoadError :message="error" @retry="load" />
    <el-table
      v-loading="loading"
      :data="rows"
      empty-text="暂无记录"
      table-layout="fixed"
    >
      <el-table-column prop="id" label="ID" width="86" />
      <el-table-column
        v-for="field in visible"
        :key="field"
        :label="commerceLabels[field]"
        :min-width="
          ['name', 'title', 'slot_name', 'note', 'target_chat'].includes(field)
            ? 190
            : field.endsWith('_at')
              ? 175
              : 135
        "
        show-overflow-tooltip
      >
        <template #default="{ row }"
          ><el-tag
            v-if="field === 'status' || field === 'enabled'"
            :type="
              ['paid', 'active', 1, true].includes(row[field])
                ? 'success'
                : 'info'
            "
            >{{ commerceValue(field, row[field]) }}</el-tag
          ><span v-else>{{ commerceValue(field, row[field]) }}</span></template
        >
      </el-table-column>
      <el-table-column
        label="操作"
        :width="module === 'ad_slots' ? 225 : 150"
        fixed="right"
      >
        <template #default="{ row }"
          ><div class="commerce-row-actions">
            <el-button text @click="record = row">详情</el-button>
            <el-button
              v-if="module === 'ad_slots' && auth.can('ad_slots.update')"
              text
              type="primary"
              @click="openEdit(row)"
              >编辑</el-button
            >
            <el-dropdown
              v-if="module === 'ad_slots' && auth.can('ad_slots.delete')"
              trigger="click"
              @command="remove(row)"
              ><el-button text>更多</el-button
              ><template #dropdown
                ><el-dropdown-menu
                  ><el-dropdown-item command="delete"
                    >删除无订单广告位</el-dropdown-item
                  ></el-dropdown-menu
                ></template
              ></el-dropdown
            >
            <el-button
              v-if="canReview(row)"
              text
              type="primary"
              @click="openReview(row)"
              >处理</el-button
            >
          </div></template
        >
      </el-table-column>
    </el-table>
    <div class="pagination">
      <span>共 {{ total }} 条</span
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
  <el-drawer
    v-model="edit"
    :title="form.id ? `编辑广告位 #${form.id}` : '新增广告位'"
    size="560px"
    class="form-drawer"
    :close-on-click-modal="false"
  >
    <div v-if="formError" class="form-alert" role="alert">{{ formError }}</div>
    <el-form label-position="top" @submit.prevent="save">
      <el-form-item v-if="read('name')" label="广告位名称"
        ><el-input
          v-model="form.name"
          maxlength="80"
          :disabled="!write('name')"
      /></el-form-item>
      <el-form-item v-if="read('kind')" label="广告类型"
        ><el-select v-model="form.kind" :disabled="!write('kind')"
          ><el-option
            v-for="(label, value) in adKinds"
            :key="value"
            :value="value"
            :label="label" /></el-select
      ></el-form-item>
      <el-form-item v-if="read('description')" label="展示说明"
        ><el-input
          v-model="form.description"
          type="textarea"
          :rows="3"
          maxlength="500"
          :disabled="!write('description')"
      /></el-form-item>
      <div class="commerce-form-grid">
        <el-form-item v-if="read('price')" label="售价 / USDT"
          ><el-input
            v-model="form.price"
            inputmode="decimal"
            :disabled="!write('price')" /></el-form-item
        ><el-form-item v-if="read('duration_days')" label="投放天数"
          ><el-input-number
            v-model="form.duration_days"
            :min="1"
            :max="365"
            :disabled="!write('duration_days')" /></el-form-item
        ><el-form-item v-if="read('capacity')" label="可同时投放数量"
          ><el-input-number
            v-model="form.capacity"
            :min="1"
            :max="50"
            :disabled="!write('capacity')"
        /></el-form-item>
      </div>
      <p class="field-help">
        关键词与品牌按匹配词分别计容量。群置顶使用独占广告位，容量为
        1。顶部轮播在每次新搜索时选择展示一条。
      </p>
      <el-form-item
        v-if="
          read('target_chat') && ['group_pin', 'channel'].includes(form.kind)
        "
        label="目标群 ID / @频道名"
        ><el-input
          v-model="form.target_chat"
          :disabled="!write('target_chat')"
          placeholder="-100… 或 @channel"
        />
        <p class="field-help">
          请先将机器人设为管理员并授予发帖或置顶权限。批准订单时会检查权限。
        </p></el-form-item
      >
      <el-form-item v-if="read('enabled')" label="开放购买"
        ><el-switch v-model="form.enabled" :disabled="!write('enabled')" />
        <p class="field-help">
          停用后停止接受新订单，已批准的订单按原约定展示至到期。
        </p></el-form-item
      >
      <el-button native-type="submit" type="primary" :loading="busy"
        >保存广告位</el-button
      >
    </el-form>
  </el-drawer>
  <el-drawer
    :model-value="!!record"
    title="记录详情"
    size="560px"
    class="form-drawer"
    @close="record = null"
  >
    <el-descriptions v-if="record" :column="1" border
      ><el-descriptions-item
        v-for="field in detailKeys"
        :key="field"
        :label="commerceLabels[field]"
        ><span class="break-word">{{
          commerceValue(field, record[field])
        }}</span></el-descriptions-item
      ></el-descriptions
    >
    <RechargeProofImage
      v-if="record && module === 'recharges' && read('proof')"
      scope="admin"
      :recharge-id="record.id"
      :has-image="!!record.has_proof_image"
    >
      用户可在机器人私聊中打开充值订单，按提示发送转账截图。
    </RechargeProofImage>
  </el-drawer>
  <el-dialog
    :model-value="!!review"
    :title="`处理订单 #${review?.id || ''}`"
    width="520px"
    class="commerce-dialog"
    :close-on-click-modal="false"
    @close="review = null"
  >
    <template v-if="review">
      <p>
        {{ module === "recharges" ? "充值金额" : "实付金额" }}：<strong
          >{{ review.amount || review.price }} USDT</strong
        >
      </p>
      <p v-if="read('proof') && review.proof" class="offline-instructions">
        {{ review.proof }}
      </p>
      <RechargeProofImage
        v-if="module === 'recharges' && read('proof')"
        scope="admin"
        :recharge-id="review.id"
        :has-image="!!review.has_proof_image"
      >
        用户可在机器人私聊中提交转账截图；请结合实际收款记录核账。
      </RechargeProofImage>
      <p v-if="review.delivery_error" class="form-alert">
        {{ review.delivery_error }}
      </p>
      <div v-if="formError" class="form-alert" role="alert">
        {{ formError }}
      </div>
      <el-form
        v-if="module === 'recharges' && review.method === 'trc20'"
        label-position="top"
        @submit.prevent="submitReview('check')"
        ><el-form-item label="交易哈希（可选）"
          ><el-input v-model="txid" maxlength="64" /></el-form-item
        ><el-button type="primary" native-type="submit" :loading="busy"
          >查询并核账</el-button
        ></el-form
      >
      <el-form
        v-else-if="
          auth.can(module + '.review') &&
          write('decision') &&
          write('review_note')
        "
        label-position="top"
        @submit.prevent="submitReview()"
      >
        <el-form-item label="处理方式"
          ><el-select v-model="decision"
            ><el-option
              v-if="['pending', 'review'].includes(review.status)"
              value="approve"
              label="审核通过" /><el-option
              v-if="['pending', 'review'].includes(review.status)"
              value="reject"
              label="拒绝（广告订单退回余额）" /><el-option
              v-if="
                module === 'ad_orders' &&
                ['active', 'delivery_issue'].includes(review.status)
              "
              value="refund"
              label="停止订单并全额退款" /></el-select
        ></el-form-item>
        <el-form-item label="审核 / 核账说明"
          ><el-input
            v-model="note"
            type="textarea"
            :rows="3"
            maxlength="300"
            placeholder="填写已核实的转账依据或广告审核说明"
        /></el-form-item>
        <el-button type="primary" native-type="submit" :loading="busy"
          >确认处理</el-button
        >
      </el-form>
      <el-form
        v-if="
          review.status === 'delivery_issue' &&
          auth.can('ad_orders.resolve') &&
          write('message_id')
        "
        label-position="top"
        class="commerce-detail-copy"
        @submit.prevent="submitReview('resolve')"
        ><h3>确认已发送的广告</h3>
        <p class="field-help">
          先检查目标聊天，确认消息为本订单广告，再填写实际消息
          ID。此操作不会重复发送。
        </p>
        <el-form-item label="实际投放消息 ID"
          ><el-input v-model="messageId" inputmode="numeric" /></el-form-item
        ><el-button :loading="busy" native-type="submit"
          >确认已投放并开始计时</el-button
        ></el-form
      >
    </template>
  </el-dialog>
</template>
