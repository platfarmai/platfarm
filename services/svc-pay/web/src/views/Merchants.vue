<script setup>
import { ref, onMounted } from "vue";
import { api } from "../api.js";

const err = ref("");
const merchants = ref([]);

async function load() {
  err.value = "";
  try {
    merchants.value = (await api("GET", "/admin/merchants")).items || [];
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
</template>
