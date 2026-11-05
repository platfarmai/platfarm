<script setup>
import { ref, onMounted, watch, computed } from "vue";
import { api, toastErr } from "../api.js";
import { formatAbs, formatNum } from "../utils.js";

const DAYS = [7, 30, 90];

const days = ref(7);
const loading = ref(true);
const byKey = ref([]);
const byModel = ref([]);
const recent = ref([]);

const emptyKey = computed(() => !loading.value && !byKey.value.length);
const emptyModel = computed(() => !loading.value && !byModel.value.length);
const emptyRecent = computed(() => !loading.value && !recent.value.length);

async function load() {
  loading.value = true;
  try {
    const data = await api("GET", `/admin/usage?days=${days.value}`);
    byKey.value = data.byKey || [];
    byModel.value = data.byModel || [];
    recent.value = data.recent || [];
  } catch (e) {
    toastErr(e);
  } finally {
    loading.value = false;
  }
}

function statusTone(s) {
  return Number(s) === 200 ? "ok" : "danger";
}

watch(days, load);
onMounted(load);
</script>

<template>
  <div>
    <div class="pf-toolbar mb-4">
      <div class="pf-field">
        <label for="usage-days" class="sr-only">统计天数</label>
        <select id="usage-days" v-model.number="days">
          <option v-for="d in DAYS" :key="d" :value="d">近 {{ d }} 天</option>
        </select>
      </div>
      <pf-button variant="default" size="sm" @click="load">刷新</pf-button>
    </div>

    <div v-if="loading" class="pf-card flex items-center gap-3 mb-4">
      <pf-spinner></pf-spinner>
      <span class="pf-muted">加载用量…</span>
    </div>

    <template v-else>
      <section class="pf-card mb-4">
        <h3 class="m-0 mb-3 text-[16px] font-semibold">按 Key</h3>
        <pf-empty v-if="emptyKey">该时间范围内无调用。</pf-empty>
        <table v-else class="pf-table">
          <thead>
            <tr>
              <th>Key</th>
              <th>调用</th>
              <th>Input Tokens</th>
              <th>Output Tokens</th>
              <th>错误</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(row, i) in byKey" :key="i">
              <td>{{ row.key }}</td>
              <td>{{ formatNum(row.calls) }}</td>
              <td>{{ formatNum(row.inputTokens) }}</td>
              <td>{{ formatNum(row.outputTokens) }}</td>
              <td :class="{ 'ai-err': row.errors > 0 }">{{ formatNum(row.errors) }}</td>
            </tr>
          </tbody>
        </table>
      </section>

      <section class="pf-card mb-4">
        <h3 class="m-0 mb-3 text-[16px] font-semibold">按模型</h3>
        <pf-empty v-if="emptyModel">该时间范围内无模型用量。</pf-empty>
        <table v-else class="pf-table">
          <thead>
            <tr>
              <th>模型</th>
              <th>调用</th>
              <th>Input Tokens</th>
              <th>Output Tokens</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(row, i) in byModel" :key="i">
              <td class="ai-mono">{{ row.model }}</td>
              <td>{{ formatNum(row.calls) }}</td>
              <td>{{ formatNum(row.inputTokens) }}</td>
              <td>{{ formatNum(row.outputTokens) }}</td>
            </tr>
          </tbody>
        </table>
      </section>

      <section class="pf-card">
        <h3 class="m-0 mb-3 text-[16px] font-semibold">最近调用</h3>
        <pf-empty v-if="emptyRecent">暂无调用记录。</pf-empty>
        <table v-else class="pf-table">
          <thead>
            <tr>
              <th>时间</th>
              <th>Key</th>
              <th>模型</th>
              <th>Tokens</th>
              <th>状态</th>
              <th>延迟</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(row, i) in recent" :key="i">
              <td class="pf-muted whitespace-nowrap">{{ formatAbs(row.createdAt) }}</td>
              <td>{{ row.keyName || "—" }}</td>
              <td class="ai-mono">{{ row.model || row.alias || "—" }}</td>
              <td>{{ formatNum(row.inputTokens) }} / {{ formatNum(row.outputTokens) }}</td>
              <td>
                <pf-badge :tone="statusTone(row.status)">{{ row.status }}</pf-badge>
              </td>
              <td>{{ formatNum(row.latencyMs) }} ms</td>
            </tr>
          </tbody>
        </table>
      </section>
    </template>
  </div>
</template>

<style scoped>
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
