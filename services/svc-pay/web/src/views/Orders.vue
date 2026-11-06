<script setup>
import { ref, onMounted } from "vue";
import { api } from "../api.js";
import { yuan, when } from "../utils.js";

const err = ref("");
const orders = ref([]);

async function load() {
  err.value = "";
  try {
    orders.value = (await api("GET", "/admin/orders")).items || [];
  } catch (e) {
    err.value = e.message || String(e);
  }
}
onMounted(load);
</script>

<template>
  <div class="flex flex-col gap-3">
    <pf-alert v-if="err" tone="danger">{{ err }}</pf-alert>
    <div class="pf-card">
      <table class="pf-table">
        <thead>
          <tr>
            <th>商户订单号</th><th>商户</th><th>金额</th><th>方式</th><th>状态</th><th>退款</th><th>创建时间</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="o in orders" :key="o.payOrderId">
            <td><span class="pf-code">{{ o.mchOrderNo }}</span></td>
            <td><span class="pf-code">{{ o.mchNo }}</span></td>
            <td>{{ yuan(o.amount) }}</td>
            <td>{{ o.wayCode }}</td>
            <td>{{ o.state }}</td>
            <td>{{ o.refundState }}</td>
            <td>{{ when(o.createdAt) }}</td>
          </tr>
          <tr v-if="!orders.length">
            <td colspan="7"><span class="pf-muted">暂无订单</span></td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
