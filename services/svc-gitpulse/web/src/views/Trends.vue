<script setup>
import { ref, watch, onMounted, computed } from "vue";
import { api, toastErr } from "../api.js";
import {
  formatRate,
  rateTone,
  humanizeHours,
  formatAbs,
  renderMarkdown,
  excerpt,
} from "../utils.js";

const props = defineProps({
  repos: { type: Array, default: () => [] },
});

const repoId = ref("");
const weeks = ref([]);
const hotspots = ref([]);
const analyses = ref([]);
const loading = ref(false);
const expanded = ref({});

watch(
  () => props.repos,
  (list) => {
    if (!repoId.value && list?.length) repoId.value = String(list[0].id);
  },
  { immediate: true }
);

watch(repoId, () => {
  if (repoId.value) load();
});

async function load() {
  if (!repoId.value) return;
  loading.value = true;
  try {
    const [t, a] = await Promise.all([
      api("GET", `/repos/${repoId.value}/trends`),
      api("GET", `/repos/${repoId.value}/analyses?kind=weekly_trend&limit=10`),
    ]);
    weeks.value = t.weeks || [];
    hotspots.value = (t.hotspots || []).slice(0, 10);
    analyses.value = a.analyses || [];
  } catch (e) {
    toastErr(e);
  } finally {
    loading.value = false;
  }
}

const maxCommits = computed(() =>
  Math.max(1, ...weeks.value.map((w) => w.commits || 0))
);
const maxChanges = computed(() =>
  Math.max(1, ...hotspots.value.map((h) => h.changes || 0))
);

const stats = computed(() => {
  const list = weeks.value;
  if (!list.length) return null;
  const commits = list.reduce((s, w) => s + (w.commits || 0), 0);
  const rates = list.map((w) => w.conventionRate).filter((x) => x != null);
  const leads = list.map((w) => w.avgLeadTimeHours).filter((x) => x != null);
  const verified = list.reduce((s, w) => s + (w.verifiedIssues || 0), 0);
  return {
    commits,
    avgRate: rates.length ? rates.reduce((a, b) => a + b, 0) / rates.length : null,
    avgLead: leads.length ? leads.reduce((a, b) => a + b, 0) / leads.length : null,
    verified,
  };
});

function barH(n) {
  return Math.max(4, Math.round(((n || 0) / maxCommits.value) * 120));
}

function hotspotW(n) {
  return Math.max(4, Math.round(((n || 0) / maxChanges.value) * 100));
}

function weekLabel(ws) {
  if (!ws) return "";
  const d = new Date(ws);
  if (Number.isNaN(d.getTime())) return String(ws).slice(0, 10);
  return `${d.getMonth() + 1}/${d.getDate()}`;
}

function toggleA(id) {
  expanded.value = { ...expanded.value, [id]: !expanded.value[id] };
}

onMounted(() => {
  if (repoId.value) load();
});
</script>

<template>
  <div>
    <div class="pf-toolbar mb-4 flex-wrap gap-2">
      <div class="pf-field w-64 m-0">
        <select v-model="repoId">
          <option disabled value="">选择仓库</option>
          <option v-for="r in repos" :key="r.id" :value="String(r.id)">
            {{ r.owner }}/{{ r.name }}
          </option>
        </select>
      </div>
      <pf-button variant="default" size="sm" @click="load">刷新</pf-button>
    </div>

    <pf-empty v-if="!repos.length">暂无仓库，请先在「总览」添加。</pf-empty>

    <div v-else-if="loading" class="pf-card flex items-center gap-3">
      <pf-spinner></pf-spinner>
      <span class="pf-muted">加载趋势…</span>
    </div>

    <template v-else-if="repoId">
      <div v-if="stats" class="gp-metrics mb-4">
        <div class="pf-card">
          <div class="gp-metric-label">区间提交</div>
          <div class="gp-metric-val">{{ stats.commits }}</div>
        </div>
        <div class="pf-card">
          <div class="gp-metric-label">平均规范率</div>
          <div class="gp-metric-val">
            <pf-badge :tone="rateTone(stats.avgRate)">
              {{ formatRate(stats.avgRate) == null ? "—" : formatRate(stats.avgRate) + "%" }}
            </pf-badge>
          </div>
        </div>
        <div class="pf-card">
          <div class="gp-metric-label">平均修复时长</div>
          <div class="gp-metric-val text-[14px]">{{ humanizeHours(stats.avgLead) }}</div>
        </div>
        <div class="pf-card">
          <div class="gp-metric-label">已核验问题</div>
          <div class="gp-metric-val">{{ stats.verified }}</div>
        </div>
      </div>

      <section class="pf-card mb-4">
        <h3 class="m-0 mb-3 text-[14px] font-semibold">周提交量 / 规范率</h3>
        <pf-empty v-if="!weeks.length">暂无趋势数据，等待首次轮询与分析。</pf-empty>
        <div v-else class="gp-chart">
          <div
            v-for="(w, i) in weeks"
            :key="w.weekStart || i"
            class="gp-chart-col"
            :title="`${weekLabel(w.weekStart)}: ${w.commits} 提交, 规范率 ${formatRate(w.conventionRate) ?? '—'}%`"
          >
            <div class="gp-chart-rate">
              <span
                class="gp-rate-dot"
                :style="{
                  bottom: (formatRate(w.conventionRate) ?? 0) + '%',
                  background:
                    rateTone(w.conventionRate) === 'ok'
                      ? 'var(--pf-success)'
                      : rateTone(w.conventionRate) === 'warn'
                        ? 'var(--pf-warning)'
                        : rateTone(w.conventionRate) === 'danger'
                          ? 'var(--pf-danger)'
                          : 'var(--pf-text-muted)',
                }"
              ></span>
            </div>
            <div class="gp-chart-bar-wrap">
              <div
                class="gp-chart-bar"
                :style="{ height: barH(w.commits) + 'px' }"
              ></div>
            </div>
            <div class="gp-chart-label">{{ weekLabel(w.weekStart) }}</div>
            <div class="gp-chart-n">{{ w.commits }}</div>
          </div>
        </div>
        <p class="pf-muted text-[12px] m-0 mt-2">柱高 = 提交数；圆点高度 = 规范率（0–100%）</p>
      </section>

      <div class="grid gap-3 mb-4" style="grid-template-columns: repeat(auto-fit, minmax(280px, 1fr))">
        <section class="pf-card">
          <h3 class="m-0 mb-3 text-[14px] font-semibold">热点文件 Top 10</h3>
          <pf-empty v-if="!hotspots.length">暂无热点文件</pf-empty>
          <ul v-else class="m-0 p-0 list-none flex flex-col gap-2">
            <li v-for="h in hotspots" :key="h.path" class="gp-hotspot">
              <div class="flex justify-between gap-2 text-[12px] mb-1">
                <span class="pf-code truncate" :title="h.path">{{ h.path }}</span>
                <span class="pf-muted shrink-0">{{ h.changes }}</span>
              </div>
              <div class="gp-hotspot-track">
                <div class="gp-hotspot-fill" :style="{ width: hotspotW(h.changes) + '%' }"></div>
              </div>
            </li>
          </ul>
        </section>

        <section class="pf-card">
          <h3 class="m-0 mb-3 text-[14px] font-semibold">近期周报</h3>
          <pf-empty v-if="!analyses.length">暂无周报分析</pf-empty>
          <ul v-else class="m-0 p-0 list-none flex flex-col gap-3">
            <li v-for="a in analyses" :key="a.id">
              <div class="pf-muted text-[12px] mb-1">
                {{ formatAbs(a.periodStart) }} → {{ formatAbs(a.periodEnd) }}
                · {{ formatAbs(a.createdAt) }}
              </div>
              <div
                v-if="expanded[a.id]"
                class="gp-md text-[13px]"
                v-html="renderMarkdown(a.summary)"
              ></div>
              <p v-else class="m-0 text-[13px]">{{ excerpt(a.summary, 160) }}</p>
              <button
                v-if="(a.summary || '').length > 160"
                type="button"
                class="pf-link text-[12px] mt-1 bg-transparent border-0 p-0 cursor-pointer"
                @click="toggleA(a.id)"
              >{{ expanded[a.id] ? "收起" : "展开全文" }}</button>
            </li>
          </ul>
        </section>
      </div>
    </template>
  </div>
</template>