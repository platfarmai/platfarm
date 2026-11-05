<script setup>
import { ref, watch, onMounted, computed } from "vue";
import { api, toastOk, toastErr } from "../api.js";
import {
  statusLabel,
  statusTone,
  shortSha,
  formatAbs,
  humanizeSeconds,
} from "../utils.js";

const props = defineProps({
  repos: { type: Array, default: () => [] },
});

const filterRepo = ref("");
const filterStatus = ref("");
const issues = ref([]);
const loading = ref(false);
const selectedId = ref(null);
const detail = ref(null);
const detailLoading = ref(false);
const showForm = ref(false);
const form = ref({ repoId: "", title: "", description: "" });
const acting = ref({});

async function loadList() {
  loading.value = true;
  try {
    const qs = new URLSearchParams();
    if (filterRepo.value) qs.set("repoId", filterRepo.value);
    if (filterStatus.value) qs.set("status", filterStatus.value);
    const q = qs.toString() ? `?${qs}` : "";
    const data = await api("GET", `/issues${q}`);
    issues.value = data.issues || [];
  } catch (e) {
    toastErr(e);
  } finally {
    loading.value = false;
  }
}

async function openDetail(id) {
  selectedId.value = id;
  detailLoading.value = true;
  try {
    detail.value = await api("GET", `/issues/${id}`);
  } catch (e) {
    toastErr(e);
    detail.value = null;
  } finally {
    detailLoading.value = false;
  }
}

async function submitIssue() {
  try {
    if (!form.value.repoId) throw new Error("请选择仓库");
    if (!form.value.title.trim()) throw new Error("标题必填");
    await api("POST", "/issues", {
      repoId: Number(form.value.repoId) || form.value.repoId,
      title: form.value.title.trim(),
      description: form.value.description,
    });
    toastOk("问题已提交");
    showForm.value = false;
    form.value = { repoId: filterRepo.value || "", title: "", description: "" };
    await loadList();
  } catch (e) {
    toastErr(e);
  }
}

async function setStatus(status) {
  if (!selectedId.value) return;
  try {
    await api("PATCH", `/issues/${selectedId.value}`, { status });
    toastOk(status === "closed" ? "已关闭" : "已重新打开");
    await openDetail(selectedId.value);
    await loadList();
  } catch (e) {
    toastErr(e);
  }
}

async function confirmMatch(matchId) {
  acting.value = { ...acting.value, [matchId]: true };
  try {
    await api("POST", `/issues/${selectedId.value}/matches/${matchId}/confirm`);
    toastOk("已确认修复");
    await openDetail(selectedId.value);
    await loadList();
  } catch (e) {
    toastErr(e);
  } finally {
    acting.value = { ...acting.value, [matchId]: false };
  }
}

async function rejectMatch(matchId) {
  acting.value = { ...acting.value, [matchId]: true };
  try {
    await api("POST", `/issues/${selectedId.value}/matches/${matchId}/reject`);
    toastOk("已驳回该匹配");
    await openDetail(selectedId.value);
  } catch (e) {
    toastErr(e);
  } finally {
    acting.value = { ...acting.value, [matchId]: false };
  }
}

function sourceTag(src) {
  if (src === "message_ref") return "显式引用";
  if (src === "ai_match") return "AI匹配";
  return src || "—";
}

const issue = computed(() => detail.value?.issue || null);
const matches = computed(() => detail.value?.matches || []);

watch([filterRepo, filterStatus], loadList);
onMounted(loadList);
</script>

<template>
  <div>
    <div class="pf-toolbar mb-4 flex-wrap gap-2">
      <div class="pf-field w-52 m-0">
        <select v-model="filterRepo">
          <option value="">全部仓库</option>
          <option v-for="r in repos" :key="r.id" :value="String(r.id)">
            {{ r.owner }}/{{ r.name }}
          </option>
        </select>
      </div>
      <div class="pf-field w-40 m-0">
        <select v-model="filterStatus">
          <option value="">全部状态</option>
          <option value="open">待处理</option>
          <option value="ai_matched">AI已匹配</option>
          <option value="verified">已核验</option>
          <option value="closed">已关闭</option>
        </select>
      </div>
      <pf-button variant="default" size="sm" @click="loadList">刷新</pf-button>
      <pf-button variant="primary" size="sm" @click="showForm = !showForm">提交问题</pf-button>
    </div>

    <div v-if="showForm" class="pf-card mb-4">
      <h3 class="m-0 mb-3 text-[16px] font-semibold">提交问题</h3>
      <form class="pf-form" @submit.prevent="submitIssue">
        <div class="pf-field">
          <label>仓库</label>
          <select v-model="form.repoId">
            <option disabled value="">选择仓库</option>
            <option v-for="r in repos" :key="r.id" :value="String(r.id)">
              {{ r.owner }}/{{ r.name }}
            </option>
          </select>
        </div>
        <div class="pf-field">
          <label>标题</label>
          <input v-model="form.title" placeholder="简要描述问题" />
        </div>
        <div class="pf-field">
          <label>描述</label>
          <textarea v-model="form.description" rows="4" placeholder="复现步骤、期望结果等"></textarea>
        </div>
        <div class="pf-toolbar">
          <pf-button variant="primary" @click="submitIssue">提交</pf-button>
          <pf-button variant="ghost" @click="showForm = false">取消</pf-button>
        </div>
      </form>
    </div>

    <div class="gp-issues-layout">
      <section class="pf-card">
        <div v-if="loading" class="flex items-center gap-3 py-4">
          <pf-spinner></pf-spinner>
          <span class="pf-muted">加载问题…</span>
        </div>
        <pf-empty v-else-if="!issues.length">暂无问题</pf-empty>
        <ul v-else class="m-0 p-0 list-none divide-y" style="border-color: var(--pf-border)">
          <li
            v-for="it in issues"
            :key="it.id"
            class="gp-issue-row"
            :class="{ active: selectedId === it.id }"
            @click="openDetail(it.id)"
          >
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0">
                <div class="text-[14px] font-medium truncate">{{ it.title }}</div>
                <div class="pf-muted text-[12px] mt-0.5">
                  {{ it.repoName || it.repoId }} · {{ it.createdByName || it.createdBy }} · {{ formatAbs(it.createdAt) }}
                </div>
              </div>
              <pf-badge :tone="statusTone(it.status)">{{ statusLabel(it.status) }}</pf-badge>
            </div>
            <div v-if="it.matchCount" class="pf-muted text-[12px] mt-1">{{ it.matchCount }} 条匹配</div>
          </li>
        </ul>
      </section>

      <section class="pf-card">
        <pf-empty v-if="!selectedId">点击左侧问题查看详情</pf-empty>
        <div v-else-if="detailLoading" class="flex items-center gap-3">
          <pf-spinner></pf-spinner>
          <span class="pf-muted">加载详情…</span>
        </div>
        <template v-else-if="issue">
          <header class="mb-3">
            <div class="flex flex-wrap items-center gap-2 mb-1">
              <h3 class="m-0 text-[16px] font-semibold">{{ issue.title }}</h3>
              <pf-badge :tone="statusTone(issue.status)">{{ statusLabel(issue.status) }}</pf-badge>
            </div>
            <div class="pf-muted text-[12px]">
              {{ issue.repoName || issue.repoId }} · 创建于 {{ formatAbs(issue.createdAt) }}
              <span v-if="issue.verifiedAt"> · 核验 {{ formatAbs(issue.verifiedAt) }}</span>
            </div>
          </header>
          <p class="text-[13px] leading-relaxed whitespace-pre-wrap">{{ issue.description || "（无描述）" }}</p>

          <div v-if="issue.leadTimeSeconds != null" class="my-3">
            <span class="pf-muted text-[12px]">修复耗时</span>
            <div class="text-[18px] font-semibold">{{ humanizeSeconds(issue.leadTimeSeconds) }}</div>
          </div>

          <div class="pf-toolbar mb-4">
            <pf-button
              v-if="issue.status !== 'closed'"
              variant="danger"
              size="sm"
              @click="setStatus('closed')"
            >关闭问题</pf-button>
            <pf-button
              v-else
              variant="default"
              size="sm"
              @click="setStatus('open')"
            >重新打开</pf-button>
          </div>

          <h4 class="m-0 mb-2 text-[14px] font-semibold">匹配提交</h4>
          <pf-empty v-if="!matches.length">尚无匹配提交</pf-empty>
          <ul v-else class="m-0 p-0 list-none flex flex-col gap-2">
            <li v-for="m in matches" :key="m.id" class="gp-match-card">
              <div class="flex flex-wrap items-center gap-2 mb-1">
                <span class="pf-code">{{ shortSha(m.sha) }}</span>
                <pf-badge tone="info">{{ sourceTag(m.source) }}</pf-badge>
                <span class="pf-muted text-[12px]">
                  置信度 {{ m.confidence != null ? Math.round(m.confidence * 100) + "%" : "—" }}
                </span>
                <pf-badge
                  v-if="m.status === 'confirmed'"
                  tone="ok"
                >已确认</pf-badge>
                <pf-badge
                  v-else-if="m.status === 'rejected'"
                  tone="neutral"
                >已驳回</pf-badge>
              </div>
              <div class="text-[13px]">{{ m.message }}</div>
              <div class="pf-muted text-[12px] mt-1">
                {{ m.authorName }} · {{ formatAbs(m.committedAt) }}
              </div>
              <p v-if="m.reason" class="m-0 mt-2 text-[12px] pf-muted">{{ m.reason }}</p>
              <div v-if="m.status === 'pending'" class="pf-toolbar mt-2">
                <pf-button
                  variant="primary"
                  size="sm"
                  :loading="!!acting[m.id]"
                  @click="confirmMatch(m.id)"
                >确认修复</pf-button>
                <pf-button
                  variant="ghost"
                  size="sm"
                  :loading="!!acting[m.id]"
                  @click="rejectMatch(m.id)"
                >不是这个</pf-button>
              </div>
            </li>
          </ul>
        </template>
      </section>
    </div>
  </div>
</template>