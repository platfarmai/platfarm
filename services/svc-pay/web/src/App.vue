<script setup>
import { ref, computed, watch, onMounted, onUnmounted } from "vue";
import { api, viewAs } from "./api.js";
import Orders from "./views/Orders.vue";
import Merchants from "./views/Merchants.vue";
import Apps from "./views/Apps.vue";
import Applyments from "./views/Applyments.vue";
import Divisions from "./views/Divisions.vue";
import Withdrawals from "./views/Withdrawals.vue";
import Recon from "./views/Recon.vue";
import Agents from "./views/Agents.vue";
import PortalHome from "./views/PortalHome.vue";
import PortalOrders from "./views/PortalOrders.vue";
import Playground from "./views/Playground.vue";

// hash 路由（docs/ui-frameworks.md：不用 browser history）
// 运营：#orders | #merchants | #agents | #apps | #applyments | #divisions | #withdrawals | #recon
// 商户：#home | #my-orders | #playground
const operatorTabs = [
  { key: "orders", label: "订单", comp: Orders },
  { key: "merchants", label: "商户", comp: Merchants },
  { key: "agents", label: "代理商·分润", comp: Agents },
  { key: "apps", label: "应用·通道", comp: Apps },
  { key: "applyments", label: "进件审核", comp: Applyments },
  { key: "divisions", label: "分账·台账", comp: Divisions },
  { key: "withdrawals", label: "提现·余额", comp: Withdrawals },
  { key: "recon", label: "对账", comp: Recon },
];
const merchantTabs = [
  { key: "home", label: "概览·接入凭据", comp: PortalHome },
  { key: "my-orders", label: "订单", comp: PortalOrders },
  { key: "playground", label: "接口调试", comp: Playground },
];

// 登录的是谁：绑定了商户的账号只看商户中心；其余账号是运营工作台。
// loading → operator | merchant | disabled（商户登录被停用）
const mode = ref("loading");
const asMerchant = computed(() => mode.value === "merchant" || !!viewAs.value);
const tabs = computed(() => (asMerchant.value ? merchantTabs : operatorTabs));
const hashKey = () => location.hash.replace(/^#/, "");
const tab = ref(hashKey());
const current = computed(() => tabs.value.find((t) => t.key === tab.value) || tabs.value[0]);
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
  tab.value = hashKey();
}
async function loadMe() {
  try {
    const me = await api("GET", "/me");
    username.value = me.username || "";
  } catch {
    username.value = "";
  }
}
async function detectMode() {
  try {
    await api("GET", "/portal/me");
    mode.value = "merchant";
  } catch (e) {
    mode.value = e.code === "portal_disabled" ? "disabled" : "operator";
  }
}
function leaveViewAs() {
  viewAs.value = "";
  show("merchants");
}
// 运营在「商户」页点了「商户视角」：切到商户中心的页签。
watch(viewAs, (mchNo) => {
  if (mchNo) show("home");
});
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
  detectMode();
});
onUnmounted(() => window.removeEventListener("hashchange", onHash));
</script>

<template>
  <div>
    <pf-topbar :title="mode === 'merchant' ? '商户中心' : '支付中心'">
      <span v-show="username" class="pf-muted">{{ username }}</span>
      <pf-theme-toggle></pf-theme-toggle>
      <pf-button variant="default" size="sm" @click="reloadKey++">刷新</pf-button>
      <pf-button v-show="!embedded" variant="ghost" size="sm" @click="goHome">返回首页</pf-button>
      <pf-button variant="default" size="sm" :loading="loggingOut" @click="logout">退出登录</pf-button>
    </pf-topbar>

    <div class="pf-container flex flex-col gap-3">
      <pf-spinner v-if="mode === 'loading'"></pf-spinner>
      <pf-alert v-else-if="mode === 'disabled'" tone="warn">
        这个账号的商户登录已被停用，暂时无法查看商户中心。需要恢复请联系为你开户的服务商或平台运营。
        停用登录不影响你的业务系统用 appKey 和签名密钥调用支付接口。
      </pf-alert>
      <template v-else>
        <pf-alert v-if="viewAs" tone="info">
          <div class="flex items-center justify-between gap-3 flex-wrap">
            <span>正在以运营身份查看商户 <span class="pf-code">{{ viewAs }}</span> 的商户中心：看到的内容、能做的操作和商户本人登录后一致。</span>
            <pf-button size="sm" @click="leaveViewAs">返回运营工作台</pf-button>
          </div>
        </pf-alert>
        <div class="flex gap-2 flex-wrap">
          <pf-button v-for="t in tabs" :key="t.key" size="sm" :variant="current.key === t.key ? 'primary' : 'default'" @click="show(t.key)">
            {{ t.label }}
          </pf-button>
        </div>
        <component :is="current.comp" :key="current.key + ':' + viewAs + ':' + reloadKey" />
      </template>
    </div>
  </div>
</template>
