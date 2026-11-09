<script setup>
import { ref, onMounted } from "vue";
import { api } from "../api.js";
import { yuan, when, stateTone } from "../utils.js";

const err = ref("");
const msg = ref("");
const merchants = ref([]);
const mchNo = ref("");
// 只有收付通有平台侧可提现余额；直付通确认结算即到二级商户结算账户，没有提现。
const ifCode = ref("wxpay_ecommerce");
const channels = ["wxpay_ecommerce"];
const balance = ref(null);
const withdrawals = ref([]);

async function loadMerchants() {
  merchants.value = (await api("GET", "/admin/merchants")).items || [];
  if (!mchNo.value && merchants.value.length) mchNo.value = merchants.value[0].mchNo;
}

async function load() {
  err.value = "";
  balance.value = null;
  try {
    if (!merchants.value.length) await loadMerchants();
    if (!mchNo.value) return;
    const [b, w] = await Promise.allSettled([
      api("GET", `/admin/balance?mchNo=${encodeURIComponent(mchNo.value)}&ifCode=${encodeURIComponent(ifCode.value)}`),
      api("GET", `/admin/withdrawals?mchNo=${encodeURIComponent(mchNo.value)}`),
    ]);
    if (b.status === "fulfilled") balance.value = b.value;
    else err.value = b.reason?.message || String(b.reason);
    withdrawals.value = w.status === "fulfilled" ? w.value.items || [] : [];
  } catch (e) {
    err.value = e.message || String(e);
  }
}

async function sync(no) {
  err.value = "";
  msg.value = "";
  try {
    const r = await api("POST", `/admin/withdrawals/${no}/sync`);
    msg.value = `提现单 ${no} → ${r.withdrawal.state}`;
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
      <select v-model="ifCode" class="pf-input" @change="load">
        <option v-for="c in channels" :key="c">{{ c }}</option>
      </select>
    </div>

    <div class="pf-card flex flex-col gap-2">
      <h2 class="m-0 text-[16px] font-semibold">可提现余额（收付通）</h2>
      <div v-if="balance" class="flex flex-col gap-2">
        <div class="flex gap-6 flex-wrap">
          <div>本次最多可提 <span class="text-[20px] font-semibold">{{ yuan(balance.withdrawable) }}</span></div>
          <div>台账 <span class="text-[20px] font-semibold">{{ yuan(balance.ledger) }}</span></div>
          <div>渠道可用 <span class="text-[20px] font-semibold">{{ yuan(balance.available) }}</span></div>
          <div>渠道冻结 <span class="text-[20px] font-semibold">{{ yuan(balance.pending) }}</span></div>
          <div>提交中 <span class="text-[20px] font-semibold">{{ yuan(balance.submitting) }}</span></div>
        </div>
        <div>
          <span class="pf-dot" :data-tone="balance.drift === 0 ? 'ok' : 'info'">
            {{ balance.drift === 0 ? "台账与渠道一致" : `台账与渠道相差 ${yuan(balance.drift)}（需对账核查；提现按两者较小值放行）` }}
          </span>
        </div>
      </div>
      <span v-else class="pf-muted">直清与直付通没有平台侧余额；收付通需先配置渠道参数。</span>
    </div>

    <div class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">提现单</h2>
      <table class="pf-table">
        <thead><tr><th>提现单</th><th>商户单号</th><th>渠道</th><th>金额</th><th>状态</th><th>时间</th><th></th></tr></thead>
        <tbody>
          <tr v-for="w in withdrawals" :key="w.withdrawNo">
            <td><span class="pf-code">{{ w.withdrawNo }}</span></td>
            <td><span class="pf-code">{{ w.mchWithdrawNo }}</span></td>
            <td>{{ w.ifCode }}</td>
            <td>{{ yuan(w.amount) }}</td>
            <td><span class="pf-dot" :data-tone="stateTone(w.state)">{{ w.state }}</span> <span class="pf-muted">{{ w.errMsg }}</span></td>
            <td>{{ when(w.createdAt) }}</td>
            <td><pf-button v-if="w.state === 'accepted' || w.state === 'pending'" size="sm" variant="ghost" @click="sync(w.withdrawNo)">同步</pf-button></td>
          </tr>
          <tr v-if="!withdrawals.length"><td colspan="7"><span class="pf-muted">暂无提现单（由商户系统经开放 API POST /v1/withdrawals 发起）</span></td></tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
