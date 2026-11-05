<script setup>
import { ref, onMounted, computed } from "vue";
import { api, toastErr } from "./api.js";
import Providers from "./views/Providers.vue";
import Aliases from "./views/Aliases.vue";
import Keys from "./views/Keys.vue";
import Usage from "./views/Usage.vue";

const TABS = [
  { id: "providers", label: "上游 Provider" },
  { id: "aliases", label: "模型别名" },
  { id: "keys", label: "虚拟 Key" },
  { id: "usage", label: "用量" },
];

const view = ref("providers");
const me = ref(null);
const bootLoading = ref(true);
const bootError = ref("");

const isAdmin = computed(() => me.value?.role === "admin");

async function boot() {
  bootLoading.value = true;
  bootError.value = "";
  try {
    me.value = await api("GET", "/me");
  } catch (e) {
    bootError.value = e?.message || String(e);
    toastErr(e);
  } finally {
    bootLoading.value = false;
  }
}

function onTab(id) {
  view.value = id;
}

onMounted(boot);
</script>

<template>
  <div>
    <pf-topbar title="AI 中心">
      <span v-if="me" class="pf-muted text-[13px] mr-2">
        {{ me.username }}
        <pf-badge v-if="isAdmin" tone="info">admin</pf-badge>
      </span>
      <pf-theme-toggle></pf-theme-toggle>
      <pf-button variant="default" size="sm" @click="boot">刷新</pf-button>
    </pf-topbar>

    <div class="pf-container">
      <div v-if="bootLoading" class="pf-card flex items-center gap-3 mt-4">
        <pf-spinner></pf-spinner>
        <span class="pf-muted">初始化…</span>
      </div>

      <div v-else-if="bootError" class="pf-card mt-4">
        <pf-alert tone="danger">{{ bootError }}</pf-alert>
      </div>

      <div v-else-if="!isAdmin" class="ai-denied">
        <p class="ai-denied-title">需要 admin 角色</p>
        <p class="pf-muted m-0">当前账号 {{ me?.username || "—" }}（{{ me?.role || "未知" }}）无权管理 AI 网关。</p>
      </div>

      <template v-else>
        <nav class="ai-nav" role="tablist">
          <button
            v-for="t in TABS"
            :key="t.id"
            type="button"
            class="ai-nav-btn"
            :class="{ active: view === t.id }"
            role="tab"
            :aria-selected="view === t.id"
            @click="onTab(t.id)"
          >{{ t.label }}</button>
        </nav>

        <div class="mt-4">
          <Providers v-if="view === 'providers'" />
          <Aliases v-else-if="view === 'aliases'" />
          <Keys v-else-if="view === 'keys'" />
          <Usage v-else-if="view === 'usage'" />
        </div>
      </template>
    </div>
  </div>
</template>

<style>
.ai-nav {
  display: flex;
  gap: 4px;
  border-bottom: 1px solid var(--pf-border);
  padding-bottom: 0;
  overflow-x: auto;
}
.ai-nav-btn {
  appearance: none;
  background: transparent;
  border: 0;
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  padding: 10px 14px;
  font: inherit;
  font-size: var(--pf-fs-14);
  color: var(--pf-text-muted);
  cursor: pointer;
  white-space: nowrap;
}
.ai-nav-btn:hover { color: var(--pf-text); }
.ai-nav-btn.active {
  color: var(--pf-accent);
  border-bottom-color: var(--pf-accent);
  font-weight: 600;
}
.ai-nav-btn:focus-visible {
  outline: 2px solid var(--pf-accent);
  outline-offset: 2px;
}
.ai-denied {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 280px;
  text-align: center;
  gap: 8px;
}
.ai-denied-title {
  margin: 0;
  font-size: var(--pf-fs-20);
  font-weight: 600;
}
.ai-mono {
  font-family: var(--pf-font-mono);
  font-size: var(--pf-fs-13);
}
.ai-form-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 12px;
}
.ai-key-box {
  border: 1px solid var(--pf-warning);
  background: var(--pf-accent-weak);
  border-radius: var(--pf-radius-lg);
  padding: 14px 16px;
}
.ai-key-value {
  font-family: var(--pf-font-mono);
  font-size: var(--pf-fs-14);
  word-break: break-all;
  margin: 8px 0 12px;
  padding: 10px 12px;
  background: var(--pf-surface);
  border: 1px solid var(--pf-border);
  border-radius: var(--pf-radius);
}
.ai-hint {
  font-size: var(--pf-fs-13);
  color: var(--pf-text-muted);
  margin: 4px 0 0;
}
.ai-err {
  color: var(--pf-danger);
  font-weight: 600;
}
</style>
