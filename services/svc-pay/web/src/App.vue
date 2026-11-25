<script setup>
import { ref, onMounted, onUnmounted } from "vue";
import { api } from "./api.js";
import Orders from "./views/Orders.vue";
import Merchants from "./views/Merchants.vue";
import Apps from "./views/Apps.vue";
import Applyments from "./views/Applyments.vue";
import Divisions from "./views/Divisions.vue";
import Withdrawals from "./views/Withdrawals.vue";
import Recon from "./views/Recon.vue";
import Agents from "./views/Agents.vue";

// hash 路由：#orders | #merchants | #agents | #apps | #applyments | #divisions | #withdrawals | #recon（docs/ui-frameworks.md：不用 browser history）
const tabs = [
  { key: "orders", label: "订单", comp: Orders },
  { key: "merchants", label: "商户", comp: Merchants },
  { key: "agents", label: "代理商·分润", comp: Agents },
  { key: "apps", label: "应用·通道", comp: Apps },
  { key: "applyments", label: "进件审核", comp: Applyments },
  { key: "divisions", label: "分账·台账", comp: Divisions },
  { key: "withdrawals", label: "提现·余额", comp: Withdrawals },
  { key: "recon", label: "对账", comp: Recon },
];
const fromHash = () => tabs.find((t) => "#" + t.key === location.hash)?.key || "orders";
const tab = ref(fromHash());
const reloadKey = ref(0);
const username = ref("");
const loggingOut = ref(false);
const embedded = (() => {
  try {
    return window.self !== window.top;
  } catch {
    return true;
  }
})();

function show(key) {
  tab.value = key;
  location.hash = key;
}
function onHash() {
  tab.value = fromHash();
}
async function loadMe() {
  try {
    const me = await api("GET", "/me");
    username.value = me.username || "";
  } catch {
    username.value = "";
  }
}
function goHome() {
  window.location.href = "/";
}
async function logout() {
  if (loggingOut.value) return;
  loggingOut.value = true;
  try {
    await fetch("/auth/logout", { method: "POST", credentials: "same-origin" });
  } catch {
    // 忽略网络错误，照常回入口页重新登录
  }
  if (embedded) {
    try {
      window.top.location.reload();
      return;
    } catch {
      // 跨域嵌套时回退当前窗口跳转
    }
  }
  window.location.href = "/";
}
onMounted(() => {
  window.addEventListener("hashchange", onHash);
  loadMe();
});
onUnmounted(() => window.removeEventListener("hashchange", onHash));
</script>

<template>
  <div>
    <pf-topbar title="支付中心">
      <span v-show="username" class="pf-muted">{{ username }}</span>
      <pf-theme-toggle></pf-theme-toggle>
      <pf-button variant="default" size="sm" @click="reloadKey++">刷新</pf-button>
      <pf-button v-show="!embedded" variant="ghost" size="sm" @click="goHome">返回首页</pf-button>
      <pf-button variant="default" size="sm" :loading="loggingOut" @click="logout">退出登录</pf-button>
    </pf-topbar>

    <div class="pf-container flex flex-col gap-3">
      <div class="flex gap-2 flex-wrap">
        <pf-button v-for="t in tabs" :key="t.key" size="sm" :variant="tab === t.key ? 'primary' : 'default'" @click="show(t.key)">
          {{ t.label }}
        </pf-button>
      </div>
      <component :is="tabs.find((t) => t.key === tab).comp" :key="tab + ':' + reloadKey" />
    </div>
  </div>
</template>
