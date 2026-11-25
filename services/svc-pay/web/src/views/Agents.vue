<script setup>
import { ref, computed, onMounted } from "vue";
import { api } from "../api.js";
import { yuan, when } from "../utils.js";

const MAX_LEVEL = 5;

const err = ref("");
const msg = ref("");
const isvs = ref([]);
const isvNo = ref("");
const agents = ref([]);
const merchants = ref([]);

const showCreate = ref(false);
const blank = () => ({ agentNo: "", agentName: "", parentAgentNo: "", rateBps: 0, contactName: "", contactPhone: "", remark: "" });
const form = ref(blank());

const editing = ref(null); // { agentNo, agentName, rateBps, contactName, contactPhone, remark }
const paying = ref(null); // { agentNo, agentName, balance, amount, payoutNo, memo }
const detail = ref(null); // { agentNo, agentName, items, total }
const assign = ref({}); // mchNo → 选中的 agentNo

const isv = computed(() => isvs.value.find((i) => i.isvNo === isvNo.value));
const byNo = computed(() => Object.fromEntries(agents.value.map((a) => [a.agentNo, a])));

// 按树的顺序排：一级在前，下级紧跟在自己的上级后面。
const tree = computed(() => {
  const kids = {};
  for (const a of agents.value) (kids[a.parentAgentNo] ||= []).push(a);
  const out = [];
  const walk = (parent) => {
    for (const a of kids[parent] || []) {
      out.push(a);
      walk(a.agentNo);
    }
  };
  walk("");
  return out;
});

// 新代理商的费率上限：一级看服务商返佣，其余看上级。
const parentLimit = computed(() => {
  const p = byNo.value[form.value.parentAgentNo];
  return p ? p.rateBps : isv.value?.rebateRateBps || 0;
});
const parents = computed(() => tree.value.filter((a) => a.state === "active" && a.level < MAX_LEVEL));
const activeAgents = computed(() => tree.value.filter((a) => a.state === "active"));
const isvMerchants = computed(() => merchants.value.filter((m) => m.isvNo === isvNo.value));

// 本级实得费率 = 自己的费率 − 下级里最高的那个（按商户所在的那条线算，这里给的是最少能留下的）。
function keeps(a) {
  const below = agents.value.filter((x) => x.parentAgentNo === a.agentNo).map((x) => x.rateBps);
  return below.length ? `${a.rateBps - Math.max(...below)}–${a.rateBps}` : String(a.rateBps);
}
const label = (a) => `${"　".repeat(a.level - 1)}${a.agentName}（${a.level} 级 · ${a.rateBps}）`;
const fen = (v) => Math.round(Number(v) * 100);

function fail(e) {
  err.value = e.message || String(e);
}
function reset() {
  err.value = "";
  msg.value = "";
}

async function load() {
  err.value = "";
  try {
    if (!isvs.value.length) {
      isvs.value = (await api("GET", "/admin/isv")).items || [];
      if (!isvNo.value && isvs.value.length) isvNo.value = isvs.value[0].isvNo;
    }
    if (!isvNo.value) return;
    const [a, m] = await Promise.all([
      api("GET", `/admin/agents?isvNo=${encodeURIComponent(isvNo.value)}`),
      api("GET", "/admin/merchants"),
    ]);
    agents.value = a.items || [];
    merchants.value = m.items || [];
    assign.value = Object.fromEntries(merchants.value.map((x) => [x.mchNo, x.agentNo || ""]));
  } catch (e) {
    fail(e);
  }
}

function switchISV() {
  showCreate.value = false;
  editing.value = paying.value = detail.value = null;
  form.value = blank();
  load();
}

async function create() {
  reset();
  try {
    const body = { ...form.value, rateBps: Number(form.value.rateBps) || 0, isvNo: isvNo.value };
    const r = await api("POST", "/admin/agents", body);
    msg.value = `代理商已创建：${r.agent.agentName}（${r.agent.agentNo}，${r.agent.level} 级）`;
    form.value = blank();
    showCreate.value = false;
    await load();
  } catch (e) {
    fail(e);
  }
}

function edit(a) {
  paying.value = detail.value = null;
  editing.value = { agentNo: a.agentNo, agentName: a.agentName, rateBps: a.rateBps, contactName: a.contactName, contactPhone: a.contactPhone, remark: a.remark };
}
async function save() {
  reset();
  try {
    const { agentNo, ...body } = editing.value;
    await api("PUT", `/admin/agents/${encodeURIComponent(agentNo)}`, { ...body, rateBps: Number(body.rateBps) || 0 });
    msg.value = "已保存。费率只对之后的新订单生效。";
    editing.value = null;
    await load();
  } catch (e) {
    fail(e);
  }
}

async function setState(a, state) {
  reset();
  try {
    await api("PUT", `/admin/agents/${encodeURIComponent(a.agentNo)}`, { state });
    msg.value = state === "active" ? `${a.agentName} 已启用` : `${a.agentName} 已停用：之后的新订单不再给它计分润，已有的分润和余额不变。`;
    await load();
  } catch (e) {
    fail(e);
  }
}

function pay(a) {
  editing.value = detail.value = null;
  paying.value = { agentNo: a.agentNo, agentName: a.agentName, balance: a.balance, amount: yuan(a.balance), payoutNo: "", memo: "" };
}
async function payout() {
  reset();
  const p = paying.value;
  const amount = fen(p.amount);
  if (!(amount > 0)) {
    err.value = "结算金额必须大于 0";
    return;
  }
  try {
    const r = await api("POST", `/admin/agents/${encodeURIComponent(p.agentNo)}/payouts`, { amount, payoutNo: p.payoutNo, memo: p.memo });
    msg.value = r.idempotent
      ? `结算单 ${r.payoutNo} 之前已登记过，没有重复记账。`
      : `已登记 ${p.agentName} 结算 ${yuan(r.amount)} 元（单号 ${r.payoutNo}），待结余额 ${yuan(r.balance)} 元。`;
    paying.value = null;
    await load();
  } catch (e) {
    fail(e);
  }
}

async function showDetail(a) {
  reset();
  editing.value = paying.value = null;
  try {
    const r = await api("GET", `/admin/ledger?agentNo=${encodeURIComponent(a.agentNo)}`);
    detail.value = { agentNo: a.agentNo, agentName: a.agentName, items: r.items || [], total: r.total || 0 };
  } catch (e) {
    fail(e);
  }
}

async function saveAssign(m) {
  reset();
  try {
    const agentNo = assign.value[m.mchNo] || "";
    await api("PUT", `/admin/merchants/${encodeURIComponent(m.mchNo)}/agent`, { agentNo });
    msg.value = agentNo ? `${m.mchName} 已归到 ${byNo.value[agentNo]?.agentName || agentNo} 名下，对之后的新订单生效。` : `${m.mchName} 已解除代理商归属。`;
    await load();
  } catch (e) {
    fail(e);
  }
}

const kinds = { agent_share: "订单分润", agent_share_refund: "退款冲回", agent_payout: "结算" };

onMounted(load);
</script>

<template>
  <div class="flex flex-col gap-3">
    <pf-alert v-if="err" tone="danger">{{ err }}</pf-alert>
    <pf-alert v-if="msg" tone="ok">{{ msg }}</pf-alert>

    <div class="flex gap-2 items-center">
      <select v-model="isvNo" class="pf-input" @change="switchISV">
        <option v-for="i in isvs" :key="i.isvNo" :value="i.isvNo">{{ i.isvName }}（{{ i.isvNo }}）</option>
      </select>
      <span v-if="isv" class="pf-muted">返佣费率 {{ isv.rebateRateBps }}（万分位）</span>
      <pf-button v-if="isv" size="sm" variant="primary" @click="showCreate = !showCreate">新建代理商</pf-button>
    </div>

    <pf-alert v-if="!isvs.length" tone="info">还没有服务商。先到「商户」里登记服务商并设置返佣费率，代理商的分润从返佣里让出。</pf-alert>
    <pf-alert v-else-if="isv && !isv.rebateRateBps" tone="warn">这个服务商的返佣费率是 0，没有可以让给代理商的分润。先到「商户」里把返佣费率设上。</pf-alert>

    <div v-if="showCreate" class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">新建代理商</h2>
      <p class="pf-muted m-0">
        费率是让给这个代理商整条线（它自己加上所有下级）的分润，万分位，不能超过上一级。
        它自己实得的是和下级之间的差额。最多 {{ MAX_LEVEL }} 级，建好后不能换上级。
      </p>
      <div class="grid grid-cols-2 gap-3">
        <label class="flex flex-col gap-1 text-[13px]">名称
          <input v-model="form.agentName" class="pf-input">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">上级
          <select v-model="form.parentAgentNo" class="pf-input">
            <option value="">无（一级代理商，直接挂在服务商下）</option>
            <option v-for="a in parents" :key="a.agentNo" :value="a.agentNo">{{ label(a) }}</option>
          </select>
        </label>
        <label class="flex flex-col gap-1 text-[13px]">分润费率（万分位，最高 {{ parentLimit }}）
          <input v-model="form.rateBps" class="pf-input" type="number" min="0" :max="parentLimit">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">代理商号（可空）
          <input v-model="form.agentNo" class="pf-input" placeholder="留空自动生成">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">联系人
          <input v-model="form.contactName" class="pf-input">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">联系电话
          <input v-model="form.contactPhone" class="pf-input">
        </label>
        <label class="flex flex-col gap-1 text-[13px] col-span-2">备注
          <input v-model="form.remark" class="pf-input">
        </label>
      </div>
      <div class="flex gap-2">
        <pf-button variant="primary" size="sm" @click="create">保存</pf-button>
        <pf-button size="sm" @click="showCreate = false">取消</pf-button>
      </div>
    </div>

    <div v-if="isv" class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[14px] font-semibold">代理商</h2>
      <table class="pf-table">
        <thead>
          <tr>
            <th>代理商</th><th>费率</th><th>本级实得</th><th>商户</th><th>累计分润</th><th>退款冲回</th><th>已结算</th><th>待结余额</th><th>状态</th><th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="a in tree" :key="a.agentNo">
            <td>
              <div :style="{ paddingLeft: (a.level - 1) * 18 + 'px' }">
                {{ a.agentName }} <span class="pf-muted">{{ a.level }} 级</span><br>
                <span class="pf-code">{{ a.agentNo }}</span>
              </div>
            </td>
            <td>
              {{ a.rateBps }}
              <span v-if="a.level === 1 && a.rateBps > isv.rebateRateBps" class="pf-dot" data-tone="warn">高于返佣，按 {{ isv.rebateRateBps }} 封顶</span>
            </td>
            <td>{{ keeps(a) }}</td>
            <td>{{ a.merchantCount }}</td>
            <td>{{ yuan(a.earned) }}</td>
            <td>{{ yuan(a.refunded) }}</td>
            <td>{{ yuan(a.paid) }}</td>
            <td>
              <span class="pf-dot" :data-tone="a.balance < 0 ? 'danger' : a.balance > 0 ? 'info' : 'neutral'">{{ yuan(a.balance) }}</span>
            </td>
            <td><span class="pf-dot" :data-tone="a.state === 'active' ? 'ok' : 'neutral'">{{ a.state === "active" ? "启用" : "停用" }}</span></td>
            <td>
              <div class="flex gap-1 flex-wrap">
                <pf-button size="sm" variant="ghost" @click="edit(a)">编辑</pf-button>
                <pf-button v-if="a.balance > 0" size="sm" variant="ghost" @click="pay(a)">登记结算</pf-button>
                <pf-button size="sm" variant="ghost" @click="showDetail(a)">明细</pf-button>
                <pf-button v-if="a.state === 'active'" size="sm" variant="ghost" @click="setState(a, 'disabled')">停用</pf-button>
                <pf-button v-else size="sm" variant="ghost" @click="setState(a, 'active')">启用</pf-button>
              </div>
            </td>
          </tr>
          <tr v-if="!tree.length">
            <td colspan="10"><span class="pf-muted">这个服务商下还没有代理商</span></td>
          </tr>
        </tbody>
      </table>
      <p class="pf-muted m-0">
        费率都是万分位。待结余额 = 累计分润 − 退款冲回 − 已结算；为负表示已经结算出去的分润后来被退款冲回，下一期结算时抵扣。
      </p>
    </div>

    <div v-if="editing" class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">编辑代理商 <span class="pf-code">{{ editing.agentNo }}</span></h2>
      <div class="grid grid-cols-2 gap-3">
        <label class="flex flex-col gap-1 text-[13px]">名称
          <input v-model="editing.agentName" class="pf-input">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">分润费率（万分位，介于下级和上级之间）
          <input v-model="editing.rateBps" class="pf-input" type="number" min="0">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">联系人
          <input v-model="editing.contactName" class="pf-input">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">联系电话
          <input v-model="editing.contactPhone" class="pf-input">
        </label>
        <label class="flex flex-col gap-1 text-[13px] col-span-2">备注
          <input v-model="editing.remark" class="pf-input">
        </label>
      </div>
      <div class="flex gap-2">
        <pf-button variant="primary" size="sm" @click="save">保存</pf-button>
        <pf-button size="sm" @click="editing = null">取消</pf-button>
      </div>
    </div>

    <div v-if="paying" class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">登记结算：{{ paying.agentName }}</h2>
      <p class="pf-muted m-0">
        这里只记账，不会打款。先在线下把钱付给代理商，再回来登记。待结余额 {{ yuan(paying.balance) }} 元，登记金额不能超过它。
      </p>
      <div class="grid grid-cols-2 gap-3">
        <label class="flex flex-col gap-1 text-[13px]">结算金额（元）
          <input v-model="paying.amount" class="pf-input" type="number" min="0.01" step="0.01">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">结算单号（可空，填银行流水号便于核对）
          <input v-model="paying.payoutNo" class="pf-input" placeholder="留空自动生成" maxlength="64">
        </label>
        <label class="flex flex-col gap-1 text-[13px] col-span-2">备注
          <input v-model="paying.memo" class="pf-input" placeholder="例如 2026 年 11 月分润" maxlength="200">
        </label>
      </div>
      <div class="flex gap-2">
        <pf-button variant="primary" size="sm" @click="payout">登记</pf-button>
        <pf-button size="sm" @click="paying = null">取消</pf-button>
      </div>
    </div>

    <div v-if="detail" class="pf-card flex flex-col gap-3">
      <div class="flex gap-2 items-center">
        <h2 class="m-0 text-[14px] font-semibold">{{ detail.agentName }} 的分润明细</h2>
        <span class="pf-muted">最近 500 条，合计 {{ yuan(detail.total) }} 元</span>
        <pf-button size="sm" variant="ghost" @click="detail = null">收起</pf-button>
      </div>
      <table class="pf-table">
        <thead><tr><th>时间</th><th>类型</th><th>金额</th><th>关联单号</th><th>备注</th></tr></thead>
        <tbody>
          <tr v-for="e in detail.items" :key="e.id">
            <td>{{ when(e.createdAt) }}</td>
            <td>{{ kinds[e.kind] || e.kind }}</td>
            <td>{{ yuan(e.amount) }}</td>
            <td><span class="pf-code">{{ e.refId }}</span></td>
            <td>{{ e.memo }}</td>
          </tr>
          <tr v-if="!detail.items.length"><td colspan="5"><span class="pf-muted">还没有分润记录</span></td></tr>
        </tbody>
      </table>
    </div>

    <div v-if="isv" class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[14px] font-semibold">商户归属</h2>
      <p class="pf-muted m-0">
        把商户归到直接发展它的那个代理商名下，它的上级会按级差自动分到。改归属只影响之后的新订单，已有订单按下单时的归属结算。
      </p>
      <table class="pf-table">
        <thead><tr><th>商户</th><th>当前归属</th><th>改为</th><th></th></tr></thead>
        <tbody>
          <tr v-for="m in isvMerchants" :key="m.mchNo">
            <td>{{ m.mchName }} <span class="pf-code">{{ m.mchNo }}</span></td>
            <td>{{ m.agentNo ? byNo[m.agentNo]?.agentName || m.agentNo : "—" }}</td>
            <td>
              <select v-model="assign[m.mchNo]" class="pf-input">
                <option value="">不归属代理商</option>
                <option v-for="a in activeAgents" :key="a.agentNo" :value="a.agentNo">{{ label(a) }}</option>
              </select>
            </td>
            <td>
              <pf-button v-if="(assign[m.mchNo] || '') !== (m.agentNo || '')" size="sm" variant="primary" @click="saveAssign(m)">保存</pf-button>
            </td>
          </tr>
          <tr v-if="!isvMerchants.length"><td colspan="4"><span class="pf-muted">这个服务商下还没有商户</span></td></tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
