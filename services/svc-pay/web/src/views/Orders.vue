<script setup>
import { ref, computed, onMounted } from "vue";
import { api } from "../api.js";
import { yuan, when, stateTone } from "../utils.js";
import Field from "../components/Field.vue";

const err = ref("");
const orders = ref([]);
const keyword = ref("");
const stateFilter = ref("");

const orderStates = { created: "待下单", paying: "待付款", success: "已支付", failed: "失败", closed: "已关闭" };
const refundStates = { none: "—", partial: "部分退款", full: "已全额退" };
const wayNames = {
  WX_JSAPI: "微信 JSAPI",
  WX_LITE: "微信小程序",
  WX_NATIVE: "微信扫码",
  WX_H5: "微信 H5",
  ALI_JSAPI: "支付宝生活号",
  ALI_QR: "支付宝扫码",
  ALI_WAP: "支付宝手机网页",
  ALI_PC: "支付宝电脑网页",
};

const filtered = computed(() => {
  const q = keyword.value.trim().toLowerCase();
  return orders.value.filter((o) => {
    if (stateFilter.value && o.state !== stateFilter.value) return false;
    if (!q) return true;
    return [o.payOrderId, o.mchOrderNo, o.mchNo, o.channelOrderNo, o.subject].some((v) => (v || "").toLowerCase().includes(q));
  });
});

async function load() {
  err.value = "";
  try {
    orders.value = (await api("GET", "/admin/orders")).items || [];
  } catch (e) {
    err.value = e.message || String(e);
  }
}
// 悬停看逐级明细（下单时的快照，支付成功后才入账）。
const shares = (o) => (o.agentShares || []).map((s) => `${s.level} 级 ${s.agentNo}：${yuan(s.amount)}`).join("\n");
onMounted(load);
</script>

<template>
  <div class="flex flex-col gap-3">
    <pf-alert v-if="err" tone="danger">{{ err }}</pf-alert>

    <div class="pf-card flex flex-col gap-2">
      <h2 class="m-0 text-[16px] font-semibold">第一次接入按这四步走</h2>
      <p class="pf-muted m-0">
        <b>① 商户</b>：先建一个「服务商」（你自己），再建「商户」（要收款的店家） →
        <b>② 进件审核</b>：没有微信特约商户号的，在这里填资料提交给微信签约拿号；已经有号的跳过 →
        <b>③ 应用·通道</b>：给服务商填一次证书密钥、给商户填特约商户号，再建应用并打开支付方式 →
        <b>④ 下单收款</b>：业务系统调用开放 API 下单，所有交易都会出现在下面的列表里。
      </p>
      <p class="pf-muted m-0">
        其余页签按需启用：有业务员/渠道商返点看<b>代理商·分润</b>；要把一笔钱分给供应商或平台抽成看<b>分账·台账</b>；走微信收付通归集看<b>提现·余额</b>；财务核对每日账单看<b>对账</b>。
      </p>
    </div>

    <div class="grid grid-cols-2 gap-3">
      <Field label="搜索单号 / 商户" hint="在最近 100 笔订单里按支付单号（P 开头）、商户订单号、商户号、渠道交易号或商品标题过滤；对账页查差异时把「本地单号」贴进来即可定位。">
        <input v-model="keyword" class="pf-input" placeholder="输入 P... 支付单号、商户订单号或商户号" />
      </Field>
      <Field label="订单状态" hint="只看某一种状态的订单。顾客刚拉起收银台还没付是「待付款」，付完收到微信/支付宝确认后变成「已支付」。">
        <select v-model="stateFilter" class="pf-input">
          <option value="">全部状态</option>
          <option v-for="(label, code) in orderStates" :key="code" :value="code">{{ label }}（{{ code }}）</option>
        </select>
      </Field>
    </div>

    <div class="pf-card flex flex-col gap-3">
      <table class="pf-table">
        <thead>
          <tr>
            <th>支付单号</th><th>商户订单号</th><th>商户</th><th>金额</th><th>方式</th><th>状态</th><th>退款</th><th>代理分润</th><th>创建时间</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="o in filtered" :key="o.payOrderId">
            <td><span class="pf-code">{{ o.payOrderId }}</span></td>
            <td><span class="pf-code">{{ o.mchOrderNo }}</span></td>
            <td><span class="pf-code">{{ o.mchNo }}</span></td>
            <td>{{ yuan(o.amount) }}</td>
            <td>{{ wayNames[o.wayCode] || o.wayCode }}</td>
            <td><span class="pf-dot" :data-tone="stateTone(o.state)">{{ orderStates[o.state] || o.state }}</span></td>
            <td>{{ o.refundState === "partial" ? `已退 ${yuan(o.refundAmount)}` : (refundStates[o.refundState] || o.refundState) }}</td>
            <td :title="shares(o)">{{ o.agentAmount ? yuan(o.agentAmount) : "—" }}</td>
            <td>{{ when(o.createdAt) }}</td>
          </tr>
          <tr v-if="!filtered.length">
            <td colspan="9"><span class="pf-muted">{{ orders.length ? "没有匹配的订单" : "暂无订单（完成上面三步配置后，由业务系统调用 POST /api/pay/v1/orders 下单）" }}</span></td>
          </tr>
        </tbody>
      </table>
      <p class="pf-muted m-0">
        金额单位是元。<b>支付单号</b>是本系统生成的唯一单号（对账差异里的「本地单号」就是它）；<b>商户订单号</b>是商家业务系统自己传过来的单号。鼠标悬停在「代理分润」数字上可看各级代理商分别分到多少。
      </p>
    </div>
  </div>
</template>
