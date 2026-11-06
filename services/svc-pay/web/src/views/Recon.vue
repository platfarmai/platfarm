<script setup>
import { ref, onMounted } from "vue";
import { api } from "../api.js";
import { yuan, when } from "../utils.js";

const err = ref("");
const msg = ref("");
const batches = ref([]);
const diffs = ref([]);
const current = ref(null);
const yesterday = new Date(Date.now() - 86400000).toISOString().slice(0, 10);
const form = ref({ ifCode: "wxpay_partner", isvNo: "", billDate: yesterday, billType: "ALL", force: false });

const diffLabel = { missing_local: "渠道有·本地无", missing_channel: "本地有·渠道无", amount_mismatch: "金额不一致", state_mismatch: "状态不一致" };

async function load() {
  err.value = "";
  try {
    batches.value = (await api("GET", "/admin/recon/batches")).items || [];
  } catch (e) {
    err.value = e.message || String(e);
  }
}

async function run() {
  err.value = "";
  msg.value = "";
  try {
    const r = await api("POST", "/admin/recon/run", form.value);
    msg.value = `批次 #${r.batch.id} ${r.batch.state}：渠道 ${r.batch.channelCount} 笔 / 本地 ${r.batch.localCount} 笔 / 差异 ${r.batch.diffCount}`;
    await load();
    await open(r.batch);
  } catch (e) {
    err.value = e.message || String(e);
  }
}

async function open(b) {
  current.value = b;
  err.value = "";
  try {
    diffs.value = (await api("GET", `/admin/recon/batches/${b.id}/diffs`)).items || [];
  } catch (e) {
    err.value = e.message || String(e);
  }
}

async function resolve(d) {
  const note = window.prompt("处理说明");
  if (note === null) return;
  try {
    await api("POST", `/admin/recon/diffs/${d.id}/resolve`, { note });
    await open(current.value);
  } catch (e) {
    err.value = e.message || String(e);
  }
}

async function schedule() {
  err.value = "";
  msg.value = "";
  try {
    const r = await api("POST", "/admin/recon/schedule", {});
    msg.value = r.existing ? `每日对账计划已存在（#${r.scheduleId}）` : `已在 svc-jobs 创建每日对账计划（#${r.id}）`;
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

    <div class="pf-card flex flex-wrap gap-2 items-end">
      <select v-model="form.ifCode" class="pf-input"><option value="wxpay_partner">微信普通服务商</option></select>
      <input v-model="form.isvNo" class="pf-input" placeholder="服务商号 isvNo" />
      <input v-model="form.billDate" type="date" class="pf-input" />
      <select v-model="form.billType" class="pf-input"><option>ALL</option><option>SUCCESS</option><option>REFUND</option></select>
      <label class="flex items-center gap-1 text-[13px]"><input v-model="form.force" type="checkbox" /> 强制重跑</label>
      <pf-button size="sm" variant="primary" @click="run">手动对账</pf-button>
      <pf-button size="sm" @click="schedule">注册每日计划（svc-jobs）</pf-button>
    </div>

    <div class="pf-card">
      <table class="pf-table">
        <thead><tr><th>#</th><th>渠道</th><th>服务商</th><th>账单日</th><th>类型</th><th>状态</th><th>渠道笔数/金额</th><th>本地笔数/金额</th><th>差异</th><th></th></tr></thead>
        <tbody>
          <tr v-for="b in batches" :key="b.id">
            <td>{{ b.id }}</td><td>{{ b.ifCode }}</td><td>{{ b.isvNo }}</td><td>{{ b.billDate }}</td><td>{{ b.billType }}</td>
            <td><span class="pf-dot" :data-tone="b.state === 'done' ? (b.diffCount ? 'danger' : 'ok') : 'neutral'">{{ b.state }}</span> <span class="pf-muted">{{ b.errMsg }}</span></td>
            <td>{{ b.channelCount }} / {{ yuan(b.channelAmount) }}</td>
            <td>{{ b.localCount }} / {{ yuan(b.localAmount) }}</td>
            <td>{{ b.diffCount }}</td>
            <td><pf-button size="sm" variant="ghost" @click="open(b)">差异</pf-button></td>
          </tr>
          <tr v-if="!batches.length"><td colspan="10"><span class="pf-muted">暂无对账批次</span></td></tr>
        </tbody>
      </table>
    </div>

    <div v-if="current" class="pf-card flex flex-col gap-2">
      <h2 class="m-0 text-[16px] font-semibold">批次 #{{ current.id }} 差异 <span class="pf-muted">{{ current.billDate }}</span></h2>
      <table class="pf-table">
        <thead><tr><th>类型</th><th>种类</th><th>本地单号</th><th>渠道单号</th><th>子商户</th><th>本地金额/状态</th><th>渠道金额/状态</th><th>处理</th></tr></thead>
        <tbody>
          <tr v-for="d in diffs" :key="d.id">
            <td>{{ diffLabel[d.diffType] || d.diffType }}</td><td>{{ d.kind }}</td>
            <td><span class="pf-code">{{ d.localId }}</span></td><td><span class="pf-code">{{ d.channelOrderNo }}</span></td><td>{{ d.subMchid }}</td>
            <td>{{ yuan(d.localAmount) }} {{ d.localState }}</td><td>{{ yuan(d.channelAmount) }} {{ d.channelState }}</td>
            <td>
              <span v-if="d.resolved" class="pf-muted">已处理：{{ d.note }}</span>
              <pf-button v-else size="sm" variant="ghost" @click="resolve(d)">标记处理</pf-button>
            </td>
          </tr>
          <tr v-if="!diffs.length"><td colspan="8"><span class="pf-muted">无差异</span></td></tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
