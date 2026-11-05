<script setup>
import { ref, onMounted, computed } from "vue";
import { api, toastErr } from "./api.js";
import Overview from "./views/Overview.vue";
import Timeline from "./views/Timeline.vue";
import Issues from "./views/Issues.vue";
import Trends from "./views/Trends.vue";

const TABS = [
  { id: "overview", label: "总览" },
  { id: "timeline", label: "时间线" },
  { id: "issues", label: "问题跟踪" },
  { id: "trends", label: "趋势报告" },
];

const view = ref("overview");
const me = ref(null);
const repos = ref([]);
const bootLoading = ref(true);

const isAdmin = computed(() => me.value?.role === "admin");

async function loadMe() {
  try {
    me.value = await api("GET", "/me");
  } catch (e) {
    toastErr(e);
  }
}

async function loadRepos() {
  try {
    const data = await api("GET", "/repos");
    repos.value = data.repos || [];
  } catch (e) {
    toastErr(e);
  }
}

async function boot() {
  bootLoading.value = true;
  await Promise.all([loadMe(), loadRepos()]);
  bootLoading.value = false;
}

function onTab(name) {
  view.value = name;
  if (name !== "overview") loadRepos();
}

onMounted(boot);
</script>

<template>
  <div>
    <pf-topbar title="GitPulse 控制台">
      <span v-if="me" class="pf-muted text-[13px] mr-2">
        {{ me.username }}
        <pf-badge v-if="isAdmin" tone="info">admin</pf-badge>
      </span>
      <pf-theme-toggle></pf-theme-toggle>
      <pf-button variant="default" size="sm" @click="boot">刷新</pf-button>
    </pf-topbar>

    <div class="pf-container">
      <nav class="gp-nav" role="tablist">
        <button
          v-for="t in TABS"
          :key="t.id"
          type="button"
          class="gp-nav-btn"
          :class="{ active: view === t.id }"
          role="tab"
          :aria-selected="view === t.id"
          @click="onTab(t.id)"
        >{{ t.label }}</button>
      </nav>

      <div v-if="bootLoading" class="pf-card flex items-center gap-3 mt-4">
        <pf-spinner></pf-spinner>
        <span class="pf-muted">初始化…</span>
      </div>

      <div v-else class="mt-4">
        <Overview v-if="view === 'overview'" :is-admin="isAdmin" @changed="loadRepos" />
        <Timeline v-else-if="view === 'timeline'" :repos="repos" />
        <Issues v-else-if="view === 'issues'" :repos="repos" />
        <Trends v-else-if="view === 'trends'" :repos="repos" />
      </div>
    </div>
  </div>
</template>

<style>
.gp-nav {
  display: flex;
  gap: 4px;
  border-bottom: 1px solid var(--pf-border);
  padding-bottom: 0;
  overflow-x: auto;
}
.gp-nav-btn {
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
.gp-nav-btn:hover { color: var(--pf-text); }
.gp-nav-btn.active {
  color: var(--pf-accent);
  border-bottom-color: var(--pf-accent);
  font-weight: 600;
}
.gp-nav-btn:focus-visible {
  outline: 2px solid var(--pf-accent);
  outline-offset: 2px;
}

.gp-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 16px;
}
.gp-repo-card { display: flex; flex-direction: column; }

.gp-metrics {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px;
}
.gp-metric-label {
  font-size: var(--pf-fs-12);
  color: var(--pf-text-muted);
  margin-bottom: 4px;
}
.gp-metric-val {
  font-size: var(--pf-fs-20);
  font-weight: 600;
  line-height: 1.2;
}

.gp-timeline { position: relative; }
.gp-tl-item {
  position: relative;
  padding-left: 28px;
  margin-bottom: 12px;
}
.gp-tl-item::before {
  content: "";
  position: absolute;
  left: 7px;
  top: 16px;
  bottom: -12px;
  width: 2px;
  background: var(--pf-border);
}
.gp-tl-item:last-child::before { display: none; }
.gp-tl-dot {
  position: absolute;
  left: 2px;
  top: 18px;
  width: 12px;
  height: 12px;
  border-radius: 50%;
  background: var(--pf-accent);
  border: 2px solid var(--pf-surface);
  box-shadow: 0 0 0 1px var(--pf-border);
}
.gp-tl-card { margin: 0; }

.gp-ai-note {
  background: var(--pf-accent-weak);
  border-radius: var(--pf-radius);
  padding: 8px 10px;
  font-size: var(--pf-fs-13);
  line-height: 1.45;
}
.gp-ai-label {
  display: inline-block;
  font-size: 11px;
  font-weight: 700;
  color: var(--pf-accent);
  margin-right: 6px;
  letter-spacing: 0.04em;
}

.gp-md h1, .gp-md h2, .gp-md h3 {
  margin: 0.6em 0 0.35em;
  font-size: var(--pf-fs-14);
  font-weight: 600;
}
.gp-md p { margin: 0.35em 0; }
.gp-md ul { margin: 0.35em 0; padding-left: 1.2em; }
.gp-md li { margin: 0.15em 0; }

.gp-issues-layout {
  display: grid;
  grid-template-columns: minmax(240px, 360px) 1fr;
  gap: 16px;
  align-items: start;
}
@media (max-width: 800px) {
  .gp-issues-layout { grid-template-columns: 1fr; }
  .gp-metrics { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
.gp-issue-row {
  padding: 12px 8px;
  cursor: pointer;
  border-radius: var(--pf-radius);
}
.gp-issue-row:hover { background: var(--pf-surface-2); }
.gp-issue-row.active {
  background: var(--pf-accent-weak);
}
.gp-match-card {
  border: 1px solid var(--pf-border);
  border-radius: var(--pf-radius);
  padding: 10px 12px;
  background: var(--pf-surface-2);
}

.gp-chart {
  display: flex;
  align-items: flex-end;
  gap: 8px;
  min-height: 160px;
  overflow-x: auto;
  padding-bottom: 4px;
}
.gp-chart-col {
  flex: 1 0 36px;
  max-width: 56px;
  display: flex;
  flex-direction: column;
  align-items: center;
}
.gp-chart-rate {
  position: relative;
  width: 100%;
  height: 28px;
  margin-bottom: 4px;
}
.gp-rate-dot {
  position: absolute;
  left: 50%;
  width: 8px;
  height: 8px;
  margin-left: -4px;
  border-radius: 50%;
  transform: translateY(50%);
}
.gp-chart-bar-wrap {
  height: 120px;
  width: 100%;
  display: flex;
  align-items: flex-end;
  justify-content: center;
}
.gp-chart-bar {
  width: 70%;
  max-width: 28px;
  border-radius: 4px 4px 0 0;
  background: var(--pf-accent);
  min-height: 4px;
}
.gp-chart-label {
  font-size: 11px;
  color: var(--pf-text-muted);
  margin-top: 4px;
}
.gp-chart-n {
  font-size: 11px;
  font-weight: 600;
}

.gp-hotspot-track {
  height: 6px;
  background: var(--pf-surface-2);
  border-radius: 3px;
  overflow: hidden;
}
.gp-hotspot-fill {
  height: 100%;
  background: var(--pf-accent);
  border-radius: 3px;
}

.gp-modal-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.35);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 50;
  padding: 16px;
}
.gp-modal {
  width: min(420px, 100%);
  margin: 0;
  box-shadow: var(--pf-shadow-2);
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  border: 0;
}
</style>