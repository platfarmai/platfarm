<script setup>
import { ref, onMounted } from "vue";
import { api } from "./api.js";

const tab = ref(location.hash === "#merchants" ? "merchants" : "orders");
const err = ref("");
const orders = ref([]);
const merchants = ref([]);

const yuan = (fen) => (Number(fen || 0) / 100).toFixed(2);
const when = (s) => (s ? String(s).replace("T", " ").slice(0, 19) : "");

function show(name) {
  tab.value = name;
  location.hash = name === "orders" ? "" : name;
}

async function load() {
  err.value = "";
  try {
    const [o, m] = await Promise.all([api("GET", "/admin/orders"), api("GET", "/admin/merchants")]);
    orders.value = o.items || [];
    merchants.value = m.items || [];
  } catch (e) {
    err.value = e.message || String(e);
  }
}

onMounted(load);
window.addEventListener("hashchange", () => show(location.hash === "#merchants" ? "merchants" : "orders"));
</script>

<template>
  <div>
    <pf-topbar title="支付中心">
      <pf-theme-toggle></pf-theme-toggle>
      <pf-button variant="default" size="sm" @click="load">刷新</pf-button>
    </pf-topbar>

    <div class="pf-container flex flex-col gap-3">
      <pf-alert v-if="err" tone="danger">{{ err }}</pf-alert>

      <div class="flex gap-2">
        <pf-button size="sm" :variant="tab === 'orders' ? 'primary' : 'default'" @click="show('orders')">订单</pf-button>
        <pf-button size="sm" :variant="tab === 'merchants' ? 'primary' : 'default'" @click="show('merchants')">商户</pf-button>
      </div>

      <div v-if="tab === 'orders'" class="pf-card">
        <table class="pf-table">
          <thead>
            <tr>
              <th>商户订单号</th><th>金额</th><th>方式</th><th>状态</th><th>退款</th><th>创建时间</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="o in orders" :key="o.payOrderId">
              <td><span class="pf-code">{{ o.mchOrderNo }}</span></td>
              <td>{{ yuan(o.amount) }}</td>
              <td>{{ o.wayCode }}</td>
              <td>{{ o.state }}</td>
              <td>{{ o.refundState }}</td>
              <td>{{ when(o.createdAt) }}</td>
            </tr>
            <tr v-if="!orders.length">
              <td colspan="6"><span class="pf-muted">暂无订单</span></td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-else class="pf-card">
        <table class="pf-table">
          <thead>
            <tr><th>商户号</th><th>名称</th><th>类型</th><th>服务商</th><th>状态</th></tr>
          </thead>
          <tbody>
            <tr v-for="m in merchants" :key="m.mchNo">
              <td><span class="pf-code">{{ m.mchNo }}</span></td>
              <td>{{ m.mchName }}</td>
              <td>{{ m.mchType === 1 ? "普通" : "特约" }}</td>
              <td>{{ m.isvNo || "—" }}</td>
              <td>{{ m.state }}</td>
            </tr>
            <tr v-if="!merchants.length">
              <td colspan="5"><span class="pf-muted">暂无商户</span></td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>
