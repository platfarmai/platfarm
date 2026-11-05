<script setup>
import { ref, watch, onMounted, computed } from "vue";
import { api, toastErr } from "../api.js";
import {
  shortSha,
  firstLine,
  formatAbs,
  formatRel,
  renderMarkdown,
} from "../utils.js";

const props = defineProps({
  repos: { type: Array, default: () => [] },
});

const repoId = ref("");
const commits = ref([]);
const total = ref(0);
const offset = ref(0);
const limit = 50;
const loading = ref(false);
const loadingMore = ref(false);
const expanded = ref({});
const daily = ref(null);
const weekly = ref(null);

const selected = computed(() => props.repos.find((r) => String(r.id) === String(repoId.value)));

watch(
  () => props.repos,
  (list) => {
    if (!repoId.value && list?.length) repoId.value = String(list[0].id);
  },
  { immediate: true }
);

watch(repoId, () => {
  if (repoId.value) resetAndLoad();
});

async function resetAndLoad() {
  offset.value = 0;
  commits.value = [];
  await Promise.all([loadCommits(false), loadAnalyses()]);
}

async function loadAnalyses() {
  if (!repoId.value) return;
  try {
    const [d, w] = await Promise.all([
      api("GET", `/repos/${repoId.value}/analyses?kind=daily_digest&limit=1`),
      api("GET", `/repos/${repoId.value}/analyses?kind=weekly_trend&limit=1`),
    ]);
    daily.value = (d.analyses || [])[0] || null;
    weekly.value = (w.analyses || [])[0] || null;
  } catch (e) {
    toastErr(e);
  }
}

async function loadCommits(more) {
  if (!repoId.value) return;
  if (more) loadingMore.value = true;
  else loading.value = true;
  try {
    const data = await api(
      "GET",
      `/repos/${repoId.value}/commits?limit=${limit}&offset=${offset.value}`
    );
    const batch = data.commits || [];
    commits.value = more ? commits.value.concat(batch) : batch;
    total.value = data.total ?? commits.value.length;
  } catch (e) {
    toastErr(e);
  } finally {
    loading.value = false;
    loadingMore.value = false;
  }
}

function loadMore() {
  offset.value += limit;
  loadCommits(true);
}

function toggleMsg(id) {
  expanded.value = { ...expanded.value, [id]: !expanded.value[id] };
}

function conventionBadge(c) {
  if (c.conventionOk === true) return { tone: "ok", text: "✓ 规范" };
  if (c.conventionOk === false) return { tone: "danger", text: "✗ 不规范" };
  return { tone: "neutral", text: "未检" };
}

onMounted(() => {
  if (repoId.value) resetAndLoad();
});
</script>

<template>
  <div>
    <div class="pf-toolbar mb-4 flex-wrap gap-2">
      <div class="pf-field w-64 m-0">
        <label class="sr-only">仓库</label>
        <select v-model="repoId">
          <option disabled value="">选择仓库</option>
          <option v-for="r in repos" :key="r.id" :value="String(r.id)">
            {{ r.owner }}/{{ r.name }}
          </option>
        </select>
      </div>
      <span v-if="selected" class="pf-muted text-[13px]">
        共 {{ total }} 条提交
      </span>
    </div>

    <pf-empty v-if="!repos.length">暂无仓库，请先在「总览」添加。</pf-empty>

    <template v-else-if="repoId">
      <div class="grid gap-3 mb-4" style="grid-template-columns: repeat(auto-fit, minmax(280px, 1fr))">
        <section class="pf-card">
          <h3 class="m-0 mb-2 text-[14px] font-semibold">最新日报</h3>
          <div v-if="daily" class="gp-md text-[13px]" v-html="renderMarkdown(daily.summary)"></div>
          <p v-else class="m-0 pf-muted text-[13px]">暂无日报</p>
          <div v-if="daily" class="pf-muted text-[12px] mt-2">{{ formatAbs(daily.createdAt) }}</div>
        </section>
        <section class="pf-card">
          <h3 class="m-0 mb-2 text-[14px] font-semibold">最新周报</h3>
          <div v-if="weekly" class="gp-md text-[13px]" v-html="renderMarkdown(weekly.summary)"></div>
          <p v-else class="m-0 pf-muted text-[13px]">暂无周报</p>
          <div v-if="weekly" class="pf-muted text-[12px] mt-2">{{ formatAbs(weekly.createdAt) }}</div>
        </section>
      </div>

      <div v-if="loading" class="pf-card flex items-center gap-3">
        <pf-spinner></pf-spinner>
        <span class="pf-muted">加载提交…</span>
      </div>

      <pf-empty v-else-if="!commits.length">等待首次轮询</pf-empty>

      <ol v-else class="gp-timeline m-0 p-0 list-none">
        <li v-for="c in commits" :key="c.id" class="gp-tl-item">
          <div class="gp-tl-dot"></div>
          <div class="pf-card gp-tl-card">
            <div class="flex flex-wrap items-center gap-2 mb-1">
              <span class="pf-code">{{ shortSha(c.sha) }}</span>
              <span class="text-[13px] font-medium">{{ c.authorName || c.authorLogin || "—" }}</span>
              <span class="pf-muted text-[12px]">{{ formatRel(c.committedAt) }} · {{ formatAbs(c.committedAt) }}</span>
              <pf-badge
                :tone="conventionBadge(c).tone"
                :title="c.conventionNote || ''"
              >{{ conventionBadge(c).text }}</pf-badge>
            </div>
            <p class="m-0 text-[14px] leading-snug">
              {{ expanded[c.id] ? c.message : firstLine(c.message) }}
            </p>
            <button
              v-if="(c.message || '').includes('\n')"
              type="button"
              class="pf-link text-[12px] mt-1 bg-transparent border-0 p-0 cursor-pointer"
              @click="toggleMsg(c.id)"
            >{{ expanded[c.id] ? "收起" : "全文" }}</button>
            <div class="flex flex-wrap gap-3 mt-2 text-[12px]">
              <span style="color: var(--pf-success)">+{{ c.additions ?? 0 }}</span>
              <span style="color: var(--pf-danger)">−{{ c.deletions ?? 0 }}</span>
              <span class="pf-muted">{{ c.filesChanged ?? 0 }} 文件</span>
            </div>
            <div v-if="c.aiNote" class="gp-ai-note mt-2">
              <span class="gp-ai-label">AI</span>
              {{ c.aiNote }}
            </div>
          </div>
        </li>
      </ol>

      <div v-if="commits.length < total" class="pf-toolbar mt-4 justify-center">
        <pf-button variant="default" :loading="loadingMore" @click="loadMore">加载更多</pf-button>
      </div>
    </template>
  </div>
</template>