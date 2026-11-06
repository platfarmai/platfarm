<script setup>
import { ref, onMounted } from "vue";
import { api } from "../api.js";
import { yuan, when, stateTone } from "../utils.js";

const err = ref("");
const msg = ref("");
const merchants = ref([]);
const mchNo = ref("");
const receivers = ref([]);
const divisions = ref([]);
const ledger = ref({ items: [], total: 0 });
const form = ref({ type: "MERCHANT_ID", account: "", name: "", relationType: "PARTNER", maxRatioBps: 3000, defaultRatioBps: 0 });

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
    err.value = e.message || String(e);
  }
}

async function bind() {
  err.value = "";
  msg.value = "";
  try {
    await api("POST", "/admin/division/receivers", { mchNo: mchNo.value, ifCode: "wxpay_partner", ...form.value });
    msg.value = "接收方已绑定";
    form.value.account = "";
    form.value.name = "";
    await load();
  } catch (e) {
    err.value = e.message || String(e);
  }
}

async function sync(no) {
  err.value = "";
  try {
    await api("POST", `/admin/divisions/${no}/sync`);
    await load();
  } catch (e) {
    err.value = e.message || String(e);
  }
}

onMounted(load);
</script>

<template>
  <div class="flex flex-col gap-3">
    <pf-alert v-if="err" tone="danger">{{ err }}</pf-alert>
    <pf-alert v-if="msg" tone="ok">{{ msg }}</pf-alert>

    <div class="flex gap-2 items-center">
      <select v-model="mchNo" class="pf-input" @change="load">
        <option v-for="m in merchants" :key="m.mchNo" :value="m.mchNo">{{ m.mchName }}（{{ m.mchNo }}）</option>
      </select>
    </div>

    <div class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">分账接收方</h2>
      <table class="pf-table">
        <thead><tr><th>类型</th><th>账号</th><th>名称</th><th>关系</th><th>上限‰</th><th>默认‰</th><th>绑定</th></tr></thead>
        <tbody>
          <tr v-for="r in receivers" :key="r.id">
            <td>{{ r.type }}</td><td><span class="pf-code">{{ r.account }}</span></td><td>{{ r.name }}</td><td>{{ r.relationType }}</td>
            <td>{{ r.maxRatioBps / 10 }}</td><td>{{ r.defaultRatioBps / 10 }}</td>
            <td><span class="pf-dot" :data-tone="r.bindState === 'bound' ? 'ok' : 'danger'">{{ r.bindState }}</span> <span class="pf-muted">{{ r.bindMsg }}</span></td>
          </tr>
          <tr v-if="!receivers.length"><td colspan="7"><span class="pf-muted">尚未绑定接收方</span></td></tr>
        </tbody>
      </table>
      <div class="flex flex-wrap gap-2 items-end">
        <select v-model="form.type" class="pf-input"><option>MERCHANT_ID</option><option>PERSONAL_OPENID</option></select>
        <input v-model="form.account" class="pf-input" placeholder="账号（商户号 / openid）" />
        <input v-model="form.name" class="pf-input" placeholder="名称（商户类型必填）" />
        <select v-model="form.relationType" class="pf-input">
          <option v-for="t in ['PARTNER','SUPPLIER','STAFF','STORE','STORE_OWNER','SERVICE_PROVIDER','HEADQUARTER','BRAND','DISTRIBUTOR','USER','CUSTOM']" :key="t">{{ t }}</option>
        </select>
        <input v-model.number="form.maxRatioBps" type="number" class="pf-input w-28" placeholder="上限(万分)" />
        <input v-model.number="form.defaultRatioBps" type="number" class="pf-input w-28" placeholder="默认(万分)" />
        <pf-button size="sm" variant="primary" @click="bind">绑定到微信</pf-button>
      </div>
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
            <td><span class="pf-dot" :data-tone="stateTone(d.state)">{{ d.state }}</span> <span class="pf-muted">{{ d.errMsg }}</span></td>
            <td>{{ when(d.createdAt) }}</td>
            <td><pf-button v-if="d.state === 'accepted' || d.state === 'pending'" size="sm" variant="ghost" @click="sync(d.divisionNo)">同步</pf-button></td>
          </tr>
          <tr v-if="!divisions.length"><td colspan="8"><span class="pf-muted">暂无分账单（由商户系统经开放 API POST /v1/divisions 发起）</span></td></tr>
        </tbody>
      </table>
    </div>

    <div class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">台账 <span class="pf-muted">合计 {{ yuan(ledger.total) }}</span></h2>
      <table class="pf-table">
        <thead><tr><th>时间</th><th>类型</th><th>引用</th><th>金额</th><th>备注</th></tr></thead>
        <tbody>
          <tr v-for="e in ledger.items" :key="e.id">
            <td>{{ when(e.createdAt) }}</td><td>{{ e.kind }}</td>
            <td><span class="pf-code">{{ e.refType }}:{{ e.refId }}{{ e.refSub ? "/" + e.refSub : "" }}</span></td>
            <td :class="e.amount < 0 ? 'text-danger' : ''">{{ yuan(e.amount) }}</td><td>{{ e.memo }}</td>
          </tr>
          <tr v-if="!ledger.items.length"><td colspan="5"><span class="pf-muted">暂无台账</span></td></tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
