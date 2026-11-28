<script setup>
import { ref, computed, onMounted } from "vue";
import { portal } from "../api.js";
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
    return [o.payOrderId, o.mchOrderNo, o.channelOrderNo, o.subject].some((v) => (v || "").toLowerCase().includes(q));
  });
});

async function load() {
  err.value = "";
  try {
    orders.value = (await portal("GET", "/orders")).items || [];
  } catch (e) {
    err.value = e.message || String(e);
  }
}
onMounted(load);
</script>

<template>
  <div class="flex flex-col gap-3">
    <pf-alert v-if="err" tone="danger">{{ err }}</pf-alert>

    <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
      <Field label="搜索单号" hint="在最近 100 笔订单里按你的商户订单号、平台支付单号（P 开头）、渠道交易号或商品标题过滤。">
        <input v-model="keyword" class="pf-input" placeholder="输入商户订单号或 P... 支付单号">
      </Field>
      <Field label="订单状态" hint="顾客拉起收银台还没付是「待付款」，收到微信 / 支付宝确认后变成「已支付」。">
        <select v-model="stateFilter" class="pf-input">
          <option value="">全部状态</option>
          <option v-for="(label, code) in orderStates" :key="code" :value="code">{{ label }}（{{ code }}）</option>
        </select>
      </Field>
    </div>

    <div class="pf-card flex flex-col gap-3">
      <div class="overflow-x-auto">
      <table class="pf-table whitespace-nowrap">
        <thead>
          <tr>
            <th>商户订单号</th><th>支付单号</th><th>商品</th><th>金额</th><th>手续费</th><th>方式</th><th>状态</th><th>退款</th><th>创建时间</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="o in filtered" :key="o.payOrderId">
            <td>
              <span class="pf-code">{{ o.mchOrderNo }}</span>
              <pf-badge v-if="o.ifCode === 'sandbox'" tone="warn">沙箱</pf-badge>
            </td>
            <td><span class="pf-code">{{ o.payOrderId }}</span></td>
            <td>{{ o.subject }}</td>
            <td>{{ yuan(o.amount) }}</td>
            <td>{{ yuan(o.feeAmount) }}</td>
            <td>{{ wayNames[o.wayCode] || o.wayCode }}</td>
            <td><span class="pf-dot" :data-tone="stateTone(o.state)">{{ orderStates[o.state] || o.state }}</span></td>
            <td>{{ o.refundState === "partial" ? `已退 ${yuan(o.refundAmount)}` : (refundStates[o.refundState] || o.refundState) }}</td>
            <td>{{ when(o.createdAt) }}</td>
          </tr>
          <tr v-if="!filtered.length">
            <td colspan="9"><span class="pf-muted">{{ orders.length ? "没有匹配的订单" : "还没有订单。可以先到「接口调试」页下一笔调试单看看效果。" }}</span></td>
          </tr>
        </tbody>
      </table>
      </div>
      <p class="pf-muted m-0">
        这里显示最近 100 笔，金额单位是元。<b>商户订单号</b>是你的业务系统下单时传的单号（DBG 开头的是「接口调试」页下的调试单）；<b>支付单号</b>是平台生成的唯一单号，找服务商排查问题时报这个。
        「沙箱」订单不动真钱，没有资金入账。完整的流水和按日汇总请用查账接口（<a class="pf-link" href="/docs" target="_blank" rel="noopener">接入指南</a>「查账」一节）拉取。
      </p>
    </div>
  </div>
</template>
