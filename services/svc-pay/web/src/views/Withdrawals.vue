<script setup>
import { ref, onMounted } from "vue";
import { api } from "../api.js";
import { yuan, when, stateTone } from "../utils.js";
import Field from "../components/Field.vue";

const err = ref("");
const msg = ref("");
const merchants = ref([]);
const mchNo = ref("");
// 只有收付通有平台侧可提现余额；直付通确认结算即到二级商户结算账户，没有提现。
const ifCode = ref("wxpay_ecommerce");
const channels = [["wxpay_ecommerce", "微信支付 · 收付通（wxpay_ecommerce）"]];
const balance = ref(null);
const withdrawals = ref([]);
const withdrawStates = { pending: "提交中", accepted: "渠道处理中", success: "成功", failed: "失败" };

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
    msg.value = `提现单 ${no} → ${withdrawStates[r.withdrawal.state] || r.withdrawal.state}`;
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

    <div class="pf-card flex flex-col gap-2">
      <h2 class="m-0 text-[16px] font-semibold">这一页什么时候用</h2>
      <p class="pf-muted m-0">
        只有走<b>微信收付通</b>（归集模式）的商户才用得上：它的钱先停在微信的二级商户账户里，要由平台「确认结算」后才变成可提现余额，再由商家系统通过开放 API 发起提现，钱才到商户的银行卡。
        走<b>微信普通服务商</b>（直清）的商户，钱由微信按结算周期自动打到商户银行卡；走<b>支付宝直付通</b>的，确认结算后直接到结算账户。这两种都没有「提现」这一步，本页对它们是空的，属于正常情况。
      </p>
      <p class="pf-muted m-0">本页只能查看余额和提现单、手动同步状态，不能在这里发起提现。</p>
    </div>

    <div class="grid grid-cols-2 gap-3">
      <Field label="商户" hint="选要查看的商户。下面的余额和提现单都是这个商户的。">
        <select v-model="mchNo" class="pf-input" @change="load">
          <option v-for="m in merchants" :key="m.mchNo" :value="m.mchNo">{{ m.mchName }}（{{ m.mchNo }}）</option>
        </select>
      </Field>
      <Field label="渠道" hint="查哪个渠道的余额。目前只有微信收付通有平台侧余额，所以只有这一项。">
        <select v-model="ifCode" class="pf-input" @change="load">
          <option v-for="[code, name] in channels" :key="code" :value="code">{{ name }}</option>
        </select>
      </Field>
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
        <p class="pf-muted m-0">
          单位都是元。<b>台账</b> = 本系统按「确认结算 − 退款 − 已发起的提现」算出来的可提现余额；<b>渠道可用</b> = 微信那边实时查到的可用余额；
          <b>渠道冻结</b> = 微信那边暂时不能动的钱（还没确认结算或处于分账冻结）；<b>提交中</b> = 已经发起、微信还没确认受理的提现。
          <b>本次最多可提</b> = 台账和「渠道可用 − 提交中」两个数里较小的那个，两边对不上时按少的放行，保证不会多提。
        </p>
        <p class="pf-muted m-0">
          两边不一致不一定是出错：刚确认结算、微信还没入账时会短暂有差额。持续不一致时，到「对账」页跑一次对账，再对照「分账·台账」页的流水查原因。
        </p>
      </div>
      <span v-else class="pf-muted">
        查不到余额。可能的原因：这个商户走的是直清或直付通（它们本来就没有平台侧余额）；或者收付通的渠道参数还没配（到「应用·通道」页给服务商和这个商户配 wxpay_ecommerce 的参数）。具体原因见页面顶部的红色提示。
      </span>
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
            <td><span class="pf-dot" :data-tone="stateTone(w.state)">{{ withdrawStates[w.state] || w.state }}</span> <span class="pf-muted">{{ w.errMsg }}</span></td>
            <td>{{ when(w.createdAt) }}</td>
            <td><pf-button v-if="w.state === 'accepted' || w.state === 'pending'" size="sm" variant="ghost" @click="sync(w.withdrawNo)">同步</pf-button></td>
          </tr>
          <tr v-if="!withdrawals.length"><td colspan="7"><span class="pf-muted">暂无提现单（由商户系统经开放 API POST /v1/withdrawals 发起）</span></td></tr>
        </tbody>
      </table>
      <p class="pf-muted m-0">
        「提现单」是本系统的单号，「商户单号」是商家系统发起时自己带的单号，用它可以和商家那边对上。
        状态：提交中 = 已记账、正在发给微信；渠道处理中 = 微信已受理，银行打款中（一般当天到，最晚下一个工作日）；成功 = 钱已到商户银行卡；失败 = 没打成，金额已自动退回可提现余额，后面是原因（常见的是银行卡信息有误）。
        前两种状态系统会自动跟进，着急的话点「同步」立即向微信查一次。
      </p>
    </div>
  </div>
</template>
