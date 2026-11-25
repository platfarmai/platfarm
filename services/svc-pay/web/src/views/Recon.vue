<script setup>
import { ref, onMounted } from "vue";
import { api } from "../api.js";
import { yuan } from "../utils.js";
import Field from "../components/Field.vue";

const err = ref("");
const msg = ref("");
const isvs = ref([]);
const batches = ref([]);
const diffs = ref([]);
const current = ref(null);
const resolving = ref(null); // { id, localId, channelOrderNo, diffType, note }

// 账单按北京时间切日（00:00–24:00），默认填北京时间的昨天。
const yesterday = new Date(Date.now() + 8 * 3600000 - 86400000).toISOString().slice(0, 10);
const form = ref({ ifCode: "wxpay_partner", isvNo: "", billDate: yesterday, billType: "ALL", force: false });

const channels = [
  ["wxpay_partner", "微信支付 · 普通服务商（wxpay_partner）"],
  ["wxpay_ecommerce", "微信支付 · 收付通（wxpay_ecommerce）"],
  ["alipay_zft", "支付宝 · 直付通（alipay_zft）"],
];
const channelName = (code) => channels.find(([c]) => c === code)?.[1] || code;
const billTypes = [
  ["ALL", "ALL（全部：支付 + 退款，推荐）"],
  ["SUCCESS", "SUCCESS（仅成功支付单）"],
  ["REFUND", "REFUND（仅退款单）"],
];
const batchStates = { pending: "执行中", done: "已完成", no_bill: "当日无账单", failed: "失败" };
const kindNames = { pay: "支付", refund: "退款" };
const diffLabel = {
  missing_local: "渠道有 · 本地无",
  missing_channel: "本地有 · 渠道无",
  amount_mismatch: "金额不一致",
  state_mismatch: "状态不一致",
};

function batchTone(b) {
  if (b.state === "failed") return "danger";
  if (b.state === "done") return b.diffCount ? "danger" : "ok";
  if (b.state === "no_bill") return b.diffCount ? "danger" : "ok";
  return "neutral";
}

async function load() {
  err.value = "";
  try {
    const [isvRes, batchRes] = await Promise.all([api("GET", "/admin/isvs"), api("GET", "/admin/recon/batches")]);
    isvs.value = isvRes.items || [];
    batches.value = batchRes.items || [];
    if (!form.value.isvNo && isvs.value.length) form.value.isvNo = isvs.value[0].isvNo;
  } catch (e) {
    err.value = e.message || String(e);
  }
}

async function run() {
  err.value = "";
  msg.value = "";
  if (!form.value.isvNo) {
    err.value = "请先选择服务商";
    return;
  }
  if (!form.value.billDate) {
    err.value = "请选择账单日";
    return;
  }
  try {
    const r = await api("POST", "/admin/recon/run", form.value);
    const b = r.batch;
    msg.value = `批次 #${b.id}（${batchStates[b.state] || b.state}）：渠道 ${b.channelCount} 笔 / 本地 ${b.localCount} 笔 / 差异 ${b.diffCount} 条`;
    await load();
    await open(b);
  } catch (e) {
    err.value = e.message || String(e);
  }
}

async function open(b) {
  current.value = b;
  resolving.value = null;
  err.value = "";
  try {
    diffs.value = (await api("GET", `/admin/recon/batches/${b.id}/diffs`)).items || [];
  } catch (e) {
    err.value = e.message || String(e);
  }
}

function startResolve(d) {
  resolving.value = { id: d.id, localId: d.localId, channelOrderNo: d.channelOrderNo, diffType: d.diffType, note: "" };
}

async function confirmResolve() {
  if (!resolving.value) return;
  err.value = "";
  msg.value = "";
  try {
    await api("POST", `/admin/recon/diffs/${resolving.value.id}/resolve`, { note: resolving.value.note.trim() });
    msg.value = "已标记为处理完成。";
    resolving.value = null;
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
    msg.value = r.existing ? `每日自动对账计划已存在（#${r.scheduleId}），无需重复创建。` : `已在 svc-jobs 创建每日自动对账计划（#${r.id}）。`;
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
      <h2 class="m-0 text-[16px] font-semibold">对账是做什么的</h2>
      <p class="pf-muted m-0">
        对账 = 把微信 / 支付宝官方生成的某一天「交易账单」下载下来，和本系统里同一天成功的支付单、退款单逐笔核对，看有没有漏单、多单或金额不一致。
        渠道账单按<b>服务商</b>出，跑一次就覆盖这个服务商名下所有子商户当天的交易。
      </p>
      <p class="pf-muted m-0">
        平时不用每天手点：由平台管理员点一次下面的「注册每日自动对账」，之后每天下午系统会自动把昨天所有已配好渠道参数的服务商对一遍；你只要偶尔回来看「差异」列是不是 0 就行。
        注意：微信和支付宝当天的账单要到<b>次日上午 9–10 点之后</b>才生成，不能对今天，也不要在早上 9 点前对昨天。
      </p>
    </div>

    <div class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">手动发起对账</h2>
      <pf-alert v-if="!isvs.length" tone="info">还没有服务商。先到「商户」页新建服务商，再到「应用·通道」页配好渠道参数，才能下载账单。</pf-alert>
      <div class="grid grid-cols-2 gap-3">
        <Field label="渠道" required hint="选要下载哪家渠道的账单。前提是下面选的服务商已经在「应用·通道」页配好了这个渠道的证书和密钥，否则无法向渠道拉账单。">
          <select v-model="form.ifCode" class="pf-input">
            <option v-for="[code, name] in channels" :key="code" :value="code">{{ name }}</option>
          </select>
        </Field>
        <Field label="服务商" required hint="渠道账单是按服务商出的，选哪个服务商，就核对它名下所有特约商户 / 二级商户那一天的交易。">
          <select v-model="form.isvNo" class="pf-input">
            <option v-for="i in isvs" :key="i.isvNo" :value="i.isvNo">{{ i.isvName }}（{{ i.isvNo }}）</option>
          </select>
        </Field>
        <Field label="账单日" required hint="核对哪一天（北京时间 00:00–24:00）的交易，默认是昨天。只能选今天之前的日期：微信账单一般在次日 9:00–10:00 生成，而且微信只保留最近 3 个月的账单。">
          <input v-model="form.billDate" type="date" class="pf-input" :max="yesterday" />
        </Field>
        <Field label="账单类型" required hint="日常对账保持默认的 ALL（支付和退款一起核对）。本系统本地是把当天成功的支付单和退款单一起拿出来比对的，如果只选 SUCCESS 或 REFUND，另一类交易在渠道账单里没有，会被标成「本地有 · 渠道无」，仅供排查单项时使用。">
          <select v-model="form.billType" class="pf-input">
            <option v-for="[code, name] in billTypes" :key="code" :value="code">{{ name }}</option>
          </select>
        </Field>
        <Field class="col-span-2" label="重跑选项" hint="同一个「渠道 + 服务商 + 账单日 + 账单类型」算一个批次。状态已经是「已完成」或「当日无账单」的批次，默认点手动对账不会重复下载；勾选「强制重跑」会清掉这一批旧的差异记录（含已写的处理说明），重新去渠道拉账单再比一遍。适合补完漏单或修完数据后复核。">
          <label class="flex items-center gap-2 text-[13px]">
            <input v-model="form.force" type="checkbox" />
            <span>强制重跑（清空该批次旧差异并重新拉取渠道账单）</span>
          </label>
        </Field>
      </div>
      <div class="flex gap-2 items-center flex-wrap">
        <pf-button size="sm" variant="primary" :disabled="!form.isvNo" @click="run">手动对账</pf-button>
        <pf-button size="sm" @click="schedule">注册每日自动对账（svc-jobs）</pf-button>
        <span class="pf-muted">「注册每日自动对账」只需平台管理员点一次（默认每天 17:30 自动跑昨天的 ALL 账单，已注册过再点不会重复建）。</span>
      </div>
    </div>

    <div class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">对账批次</h2>
      <table class="pf-table">
        <thead><tr><th>#</th><th>渠道</th><th>服务商</th><th>账单日</th><th>类型</th><th>状态</th><th>渠道笔数 / 净额</th><th>本地笔数 / 净额</th><th>差异</th><th></th></tr></thead>
        <tbody>
          <tr v-for="b in batches" :key="b.id">
            <td>{{ b.id }}</td><td>{{ channelName(b.ifCode) }}</td><td><span class="pf-code">{{ b.isvNo }}</span></td><td>{{ b.billDate }}</td><td>{{ b.billType }}</td>
            <td><span class="pf-dot" :data-tone="batchTone(b)">{{ batchStates[b.state] || b.state }}</span> <span class="pf-muted">{{ b.errMsg }}</span></td>
            <td>{{ b.channelCount }} / {{ yuan(b.channelAmount) }}</td>
            <td>{{ b.localCount }} / {{ yuan(b.localAmount) }}</td>
            <td><span :class="b.diffCount ? 'text-danger font-semibold' : ''">{{ b.diffCount }}</span></td>
            <td><pf-button size="sm" variant="ghost" @click="open(b)">查看差异</pf-button></td>
          </tr>
          <tr v-if="!batches.length"><td colspan="10"><span class="pf-muted">暂无对账批次。在上面选好服务商和账单日，点「手动对账」跑第一批。</span></td></tr>
        </tbody>
      </table>
      <p class="pf-muted m-0">
        净额单位是元，按「成功支付金额 − 退款金额」计算。状态：<b>已完成</b>且差异为 0（绿点）= 两边完全对上；<b>当日无账单</b> = 那天渠道没有任何交易（若本地也没交易就是绿点）；
        红点表示有差异或拉账单失败，点右边的「查看差异」看具体是哪几笔。
      </p>
    </div>

    <div v-if="current" class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">批次 #{{ current.id }} 差异明细 <span class="pf-muted">{{ current.billDate }} · {{ current.isvNo }}</span></h2>
      <table class="pf-table">
        <thead><tr><th>差异类型</th><th>种类</th><th>本地单号</th><th>渠道单号</th><th>子商户号</th><th>本地金额 / 状态</th><th>渠道金额 / 状态</th><th>处理</th></tr></thead>
        <tbody>
          <tr v-for="d in diffs" :key="d.id">
            <td>{{ diffLabel[d.diffType] || d.diffType }}</td>
            <td>{{ kindNames[d.kind] || d.kind }}</td>
            <td><span class="pf-code">{{ d.localId || "—" }}</span></td>
            <td><span class="pf-code">{{ d.channelOrderNo || "—" }}</span></td>
            <td><span class="pf-code">{{ d.subMchid || "—" }}</span></td>
            <td>{{ d.localState ? `${yuan(d.localAmount)}（${d.localState}）` : "—" }}</td>
            <td>{{ d.channelState ? `${yuan(d.channelAmount)}（${d.channelState}）` : "—" }}</td>
            <td>
              <span v-if="d.resolved" class="pf-muted">已处理：{{ d.note }}</span>
              <pf-button v-else size="sm" variant="ghost" @click="startResolve(d)">标记处理</pf-button>
            </td>
          </tr>
          <tr v-if="!diffs.length"><td colspan="8"><span class="pf-muted">这一批没有差异，两边账目完全一致。</span></td></tr>
        </tbody>
      </table>
      <p class="pf-muted m-0">
        四种差异怎么查：<b>渠道有 · 本地无</b> = 微信/支付宝收了钱或退了款，但本系统没有成功记录（可能是回调漏了，或者商户绕开本系统直接用同一个服务商号收了款）；
        <b>本地有 · 渠道无</b> = 本系统记了成功，当天渠道账单里没有（最常见是 23:59 前后跨零点的交易，落到了前一天或后一天的渠道账单里，对比相邻一天即可确认）；
        <b>金额不一致 / 状态不一致</b> = 单号对上了但金额或状态不同，到「订单」页搜本地单号核查。排查清楚后点「标记处理」留一条说明，方便财务留档（标记不会自动改订单数据）。
      </p>

      <div v-if="resolving" class="pf-card flex flex-col gap-3">
        <h3 class="m-0 text-[14px] font-semibold">
          标记差异已处理 <span class="pf-code">{{ resolving.localId || resolving.channelOrderNo }}</span>
          <span class="pf-muted">（{{ diffLabel[resolving.diffType] || resolving.diffType }}）</span>
        </h3>
        <Field label="处理说明" hint="写清楚核查结论和处理方式，例如「23:59:58 跨零点支付，已见于次日微信账单」「商户旧收银台直连交易，非本系统订单」。系统会自动在前面加上你的登录账号。">
          <input v-model="resolving.note" class="pf-input" placeholder="例如 跨零点交易，已核对在次日账单中" maxlength="450" />
        </Field>
        <div class="flex gap-2">
          <pf-button size="sm" variant="primary" @click="confirmResolve">确认标记</pf-button>
          <pf-button size="sm" variant="ghost" @click="resolving = null">取消</pf-button>
        </div>
      </div>
    </div>
  </div>
</template>
