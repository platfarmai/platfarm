<script setup>
import { ref, onMounted, onUnmounted } from "vue";
import Orders from "./views/Orders.vue";
import Merchants from "./views/Merchants.vue";
import Applyments from "./views/Applyments.vue";

// hash 路由：#orders | #merchants | #applyments（docs/ui-frameworks.md：不用 browser history）
const tabs = [
  { key: "orders", label: "订单", comp: Orders },
  { key: "merchants", label: "商户", comp: Merchants },
  { key: "applyments", label: "进件审核", comp: Applyments },
];
const fromHash = () => tabs.find((t) => "#" + t.key === location.hash)?.key || "orders";
const tab = ref(fromHash());
const reloadKey = ref(0);

function show(key) {
  tab.value = key;
  location.hash = key;
}
function onHash() {
  tab.value = fromHash();
}
onMounted(() => window.addEventListener("hashchange", onHash));
onUnmounted(() => window.removeEventListener("hashchange", onHash));
</script>

<template>
  <div>
    <pf-topbar title="支付中心">
      <pf-theme-toggle></pf-theme-toggle>
      <pf-button variant="default" size="sm" @click="reloadKey++">刷新</pf-button>
    </pf-topbar>

    <div class="pf-container flex flex-col gap-3">
      <div class="flex gap-2">
        <pf-button v-for="t in tabs" :key="t.key" size="sm" :variant="tab === t.key ? 'primary' : 'default'" @click="show(t.key)">
          {{ t.label }}
        </pf-button>
      </div>
      <component :is="tabs.find((t) => t.key === tab).comp" :key="tab + ':' + reloadKey" />
    </div>
  </div>
</template>
