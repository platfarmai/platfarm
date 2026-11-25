<script setup>
import { ref, computed, onMounted } from "vue";
import { api } from "../api.js";
import { yuan, when, stateTone } from "../utils.js";
import Field from "../components/Field.vue";

const err = ref("");
const msg = ref("");
const merchants = ref([]);
const mchNo = ref("");
const receivers = ref([]);
const divisions = ref([]);
const ledger = ref({ items: [], total: 0 });
// 表单里比例按百分数填，提交时换成万分位。
const blank = () => ({ type: "MERCHANT_ID", account: "", name: "", relationType: "PARTNER", maxPct: "30", defaultPct: "0" });
const form = ref(blank());

const pct = (bps) => (Number(bps || 0) / 100).toFixed(2) + "%";
const toBps = (v) => Math.round(Number(v) * 100);

const typeNames = { MERCHANT_ID: "商户号", PERSONAL_OPENID: "个人微信" };
// 微信分账规定的「与分账方的关系」。
const relations = [
  ["PARTNER", "合作伙伴"],
  ["SUPPLIER", "供应商"],
  ["SERVICE_PROVIDER", "服务商"],
  ["STORE", "门店"],
  ["STORE_OWNER", "店主"],
  ["STAFF", "员工"],
  ["HEADQUARTER", "总部"],
  ["BRAND", "品牌方"],
  ["DISTRIBUTOR", "分销商"],
  ["USER", "用户"],
  ["CUSTOM", "自定义"],
];
const relationName = (code) => relations.find(([c]) => c === code)?.[1] || code;
const bindNames = { bound: "已绑定", failed: "绑定失败" };
const divisionStates = { pending: "待提交", accepted: "渠道处理中", success: "成功", failed: "失败" };
const ledgerKinds = {
  fee: "渠道手续费",
  fee_refund: "手续费退回",
  rebate: "服务商返佣",
  rebate_refund: "返佣冲回",
  sharing: "分账",
  settle: "确认结算",
  refund: "退款",
  withdrawal: "提现",
  withdrawal_reversal: "提现失败退回",
  agent_share: "代理商分润",
  agent_share_refund: "代理商分润冲回",
  agent_payout: "代理商结算",
};
const isMerchantType = computed(() => form.value.type === "MERCHANT_ID");

function fail(e) {
  err.value = e.message || String(e);
}

async function loadMerchants() {
  merchants.value = (await api("GET", "/admin/merchants")).items || [];
  if (!mchNo.value && merchants.value.length) mchNo.value = merchants.value[0].mchNo;
}

async function load() {
  err.value = "";
  try {
    if (!merchants.value.length) await loadMerchants();
    if (!mchNo.value) return;
    const [r, d, l] = await Promise.all([
      api("GET", "/admin/division/receivers?mchNo=" + encodeURIComponent(mchNo.value)),
      api("GET", "/admin/divisions?mchNo=" + encodeURIComponent(mchNo.value)),
      api("GET", "/admin/ledger?mchNo=" + encodeURIComponent(mchNo.value)),
    ]);
    receivers.value = r.items || [];
    divisions.value = d.items || [];
    ledger.value = { items: l.items || [], total: l.total || 0 };
  } catch (e) {
    fail(e);
  }
}

async function bind() {
  err.value = "";
  msg.value = "";
  const f = form.value;
  const account = f.account.trim();
  const name = f.name.trim();
  if (!account) return fail(new Error("接收方账号必填"));
  if (isMerchantType.value && !name) return fail(new Error("接收方是商户号时，「商户全称」必填，且要和该商户号在微信登记的名称完全一致"));
  const maxRatioBps = toBps(f.maxPct);
  const defaultRatioBps = toBps(f.defaultPct || 0);
  if (!Number.isFinite(maxRatioBps) || maxRatioBps <= 0 || maxRatioBps > 10000) return fail(new Error("比例上限须大于 0%、不超过 100%"));
  if (!Number.isFinite(defaultRatioBps) || defaultRatioBps < 0 || defaultRatioBps > maxRatioBps) return fail(new Error("默认比例须在 0% 和比例上限之间"));
  try {
    await api("POST", "/admin/division/receivers", {
      mchNo: mchNo.value,
      ifCode: "wxpay_partner",
      type: f.type,
      account,
      name,
      relationType: f.relationType,
      maxRatioBps,
      defaultRatioBps,
    });
    msg.value = "接收方已绑定到微信，可以用于分账了。";
    form.value = { ...blank(), type: f.type, relationType: f.relationType };
    await load();
  } catch (e) {
    // 渠道绑定失败时这条记录仍会落库（状态为「绑定失败」）：先刷新列表，再显示错误（load 会清空 err）。
    await load();
    fail(e);
  }
}

async function sync(no) {
  err.value = "";
  try {
    await api("POST", `/admin/divisions/${no}/sync`);
    await load();
  } catch (e) {
    fail(e);
  }
}

onMounted(load);
</script>

<template>
  <div class="flex flex-col gap-3">
    <pf-alert v-if="err" tone="danger">{{ err }}</pf-alert>
    <pf-alert v-if="msg" tone="ok">{{ msg }}</pf-alert>

    <div class="pf-card flex flex-col gap-2">
      <h2 class="m-0 text-[16px] font-semibold">分账是什么、怎么用</h2>
      <p class="pf-muted m-0">
        分账 = 商户收到一笔款之后，把其中一部分直接分给别人（平台抽成、供应商货款、门店或员工提成……），钱由微信从商户的账户划到对方账户，不经过你手。
        步骤：① 在下面把收钱的一方「绑定」成这个商户的分账接收方（只需做一次）→ ② 商家系统下单时声明这笔订单要分账（或给商户配上「默认分账冻结」）→
        ③ 顾客付款后，商家系统通过开放 API 发起分账 → ④ 在本页的「分账单」里看结果。本页只负责第 ① 步和查看，不能在这里手工发起分账。
      </p>
      <p class="pf-muted m-0">
        前提：商户要先在微信商户平台开通「分账」产品，并授权服务商分账及分账比例上限（默认最高 30%）；没开通时绑定会被微信拒绝。不需要分账的商户可以完全不管这一页。
      </p>
    </div>

    <div class="grid grid-cols-2 gap-3">
      <Field label="商户" hint="先选要查看或配置的商户。下面的接收方、分账单、台账都只显示这个商户的。">
        <select v-model="mchNo" class="pf-input" @change="load">
          <option v-for="m in merchants" :key="m.mchNo" :value="m.mchNo">{{ m.mchName }}（{{ m.mchNo }}）</option>
        </select>
      </Field>
    </div>
    <pf-alert v-if="!merchants.length" tone="info">还没有商户。先到「商户」页创建。</pf-alert>

    <div class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">分账接收方</h2>
      <table class="pf-table">
        <thead><tr><th>类型</th><th>账号</th><th>名称</th><th>关系</th><th>比例上限</th><th>默认比例</th><th>绑定</th></tr></thead>
        <tbody>
          <tr v-for="r in receivers" :key="r.id">
            <td>{{ typeNames[r.type] || r.type }}</td><td><span class="pf-code">{{ r.account }}</span></td><td>{{ r.name }}</td><td>{{ relationName(r.relationType) }}</td>
            <td>{{ pct(r.maxRatioBps) }}</td><td>{{ r.defaultRatioBps ? pct(r.defaultRatioBps) : "—" }}</td>
            <td><span class="pf-dot" :data-tone="r.bindState === 'bound' ? 'ok' : 'danger'">{{ bindNames[r.bindState] || r.bindState }}</span> <span class="pf-muted">{{ r.bindMsg }}</span></td>
          </tr>
          <tr v-if="!receivers.length"><td colspan="7"><span class="pf-muted">尚未绑定接收方。用下面的表单添加。</span></td></tr>
        </tbody>
      </table>
      <p class="pf-muted m-0">
        名称是打码显示的。「绑定失败」的接收方不能用于分账：按后面的原因改好后，用同一个账号在下面重新绑定一次即可覆盖。
      </p>

      <h3 class="m-0 text-[14px] font-semibold">添加 / 更新接收方</h3>
      <p class="pf-muted m-0">
        点「绑定到微信」会立即调用微信接口把对方登记为这个商户的分账接收方，成功后才能分钱给它。同一个账号再绑一次 = 更新它的关系和比例。目前只支持微信普通服务商渠道。
      </p>
      <div class="grid grid-cols-2 gap-3">
        <Field label="接收方类型" required>
          <select v-model="form.type" class="pf-input">
            <option value="MERCHANT_ID">商户号（对方是一个微信支付商户）</option>
            <option value="PERSONAL_OPENID">个人微信（对方是个人，钱进他的微信零钱）</option>
          </select>
          <template #hint>
            对方用什么收钱。分给公司、平台、供应商，选<b>商户号</b>，钱进对方的微信支付商户账户；分给个人（店员、达人），选<b>个人微信</b>，钱进他的微信零钱。
          </template>
        </Field>
        <Field label="接收方账号" required :hint="isMerchantType ? '对方的微信支付商户号，10 位数字，例如 1900000109。分给你自己（平台抽成）就填你的服务商商户号。' : '对方在服务商 AppID（渠道参数里的 sp_appid）下的 openid，28 位左右、通常以 o 开头的一串字符。注意必须是这个 AppID 下的 openid，同一个人在别的公众号 / 小程序下的 openid 不一样，不能混用。'">
          <input v-model="form.account" class="pf-input" :placeholder="isMerchantType ? '例如 1900000109' : '例如 oUpF8uMuAJO_M2pxb1Q9zNjWeS6o'" autocomplete="off">
        </Field>
        <Field :label="isMerchantType ? '商户全称' : '姓名'" :required="isMerchantType" :hint="isMerchantType ? '必填。对方商户号在微信支付登记的商户全称（一般就是营业执照上的公司名），必须一字不差，否则微信会拒绝绑定。加密传给微信，在本系统里密封存储、打码显示。' : '选填。填了的话微信会校验姓名和这个 openid 的实名是否一致，不一致就绑定失败；拿不准就留空。'">
          <input v-model="form.name" class="pf-input" :placeholder="isMerchantType ? '例如 成都某某商贸有限公司' : '例如 张三（可留空）'" autocomplete="off">
        </Field>
        <Field label="与商户的关系" required hint="接收方和这个商户是什么关系，微信要求如实填写，会显示在商户平台的分账记录里。平台抽成一般选「服务商」或「合作伙伴」，货款选「供应商」，提成选「员工」或「店主」。拿不准选「合作伙伴」。">
          <select v-model="form.relationType" class="pf-input">
            <option v-for="[code, name] in relations" :key="code" :value="code">{{ name }}（{{ code }}）</option>
          </select>
        </Field>
        <Field label="比例上限（%）" required hint="一笔订单最多能分给这个接收方百分之几，填百分数：30 表示最多分订单金额的 30%。商家系统发起分账时，给它的比例超过这个数会被拒绝，用来防止误操作把钱分多了。范围大于 0、不超过 100，最多两位小数；并且不能超过商户在微信授权的分账比例上限（默认 30%）。">
          <input v-model="form.maxPct" type="number" class="pf-input" min="0.01" max="100" step="0.01" placeholder="例如 30">
        </Field>
        <Field label="默认比例（%）" hint="选填，填 0 表示不设默认。商家系统发起分账时如果没有指明分给谁、分多少，系统就按这里的默认比例分给所有设了默认比例的接收方：填 5 表示每笔默认分 5%。不能超过左边的比例上限。每次都由商家系统明确指定比例的，填 0 即可。">
          <input v-model="form.defaultPct" type="number" class="pf-input" min="0" max="100" step="0.01" placeholder="例如 0">
        </Field>
      </div>
      <div><pf-button size="sm" variant="primary" :disabled="!mchNo" @click="bind">绑定到微信</pf-button></div>
    </div>

    <div class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">分账单</h2>
      <table class="pf-table">
        <thead><tr><th>分账单</th><th>支付单</th><th>订单金额</th><th>分账金额</th><th>接收方</th><th>状态</th><th>时间</th><th></th></tr></thead>
        <tbody>
          <tr v-for="d in divisions" :key="d.divisionNo">
            <td><span class="pf-code">{{ d.divisionNo }}</span></td>
            <td><span class="pf-code">{{ d.payOrderId }}</span></td>
            <td>{{ yuan(d.orderAmount) }}</td><td>{{ yuan(d.divisionAmount) }}</td>
            <td><span v-for="r in d.receivers" :key="r.account" class="pf-muted">{{ r.account }}: {{ yuan(r.amount) }} {{ r.result || "" }}<br /></span></td>
            <td><span class="pf-dot" :data-tone="stateTone(d.state)">{{ divisionStates[d.state] || d.state }}</span> <span class="pf-muted">{{ d.errMsg }}</span></td>
            <td>{{ when(d.createdAt) }}</td>
            <td><pf-button v-if="d.state === 'accepted' || d.state === 'pending'" size="sm" variant="ghost" @click="sync(d.divisionNo)">同步</pf-button></td>
          </tr>
          <tr v-if="!divisions.length"><td colspan="8"><span class="pf-muted">暂无分账单（由商户系统经开放 API POST /v1/divisions 发起）</span></td></tr>
        </tbody>
      </table>
      <p class="pf-muted m-0">
        金额单位都是元。分账单由商家系统发起，这里只能看。状态「待提交 / 渠道处理中」表示微信还没给出最终结果，系统会自动跟进；着急的话点「同步」立即向微信查一次。
        「失败」后面会带微信返回的原因，常见的是接收方没绑定、订单下单时没有声明分账、或可分金额不足。
      </p>
    </div>

    <div class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">台账 <span class="pf-muted">合计 {{ yuan(ledger.total) }}</span></h2>
      <table class="pf-table">
        <thead><tr><th>时间</th><th>类型</th><th>引用</th><th>金额</th><th>备注</th></tr></thead>
        <tbody>
          <tr v-for="e in ledger.items" :key="e.id">
            <td>{{ when(e.createdAt) }}</td><td>{{ ledgerKinds[e.kind] || e.kind }}</td>
            <td><span class="pf-code">{{ e.refType }}:{{ e.refId }}{{ e.refSub ? "/" + e.refSub : "" }}</span></td>
            <td :class="e.amount < 0 ? 'text-danger' : ''">{{ yuan(e.amount) }}</td><td>{{ e.memo }}</td>
          </tr>
          <tr v-if="!ledger.items.length"><td colspan="5"><span class="pf-muted">暂无台账</span></td></tr>
        </tbody>
      </table>
      <p class="pf-muted m-0">
        台账是本系统给这个商户记的资金流水，每一行是一次资金变动：正数是记入，负数（红色）是扣减，例如手续费、分账、退款、提现。
        「引用」是产生这笔流水的单据（订单、退款单、分账单……）。合计是所列流水相加，仅供核对，实际到账以微信 / 支付宝为准。
      </p>
    </div>
  </div>
</template>
