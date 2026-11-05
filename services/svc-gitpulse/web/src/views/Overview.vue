<script setup>
import { ref, onMounted, computed } from "vue";
import { api, toastOk, toastErr, confirmDanger } from "../api.js";
import {
  formatRate,
  rateTone,
  humanizeHours,
  excerpt,
  formatAbs,
} from "../utils.js";

const props = defineProps({
  isAdmin: { type: Boolean, default: false },
});
const emit = defineEmits(["changed"]);

const loading = ref(true);
const repos = ref([]);
const expanded = ref({});
const polling = ref({});
const analyzing = ref({});
const showAdd = ref(false);
const editing = ref(null);
const form = ref({ owner: "", name: "", token: "", branch: "", pollEnabled: true });
const editForm = ref({ token: "", branch: "", pollEnabled: true });

async function load() {
  loading.value = true;
  try {
    const data = await api("GET", "/overview");
    repos.value = data.repos || [];
  } catch (e) {
    toastErr(e);
  } finally {
    loading.value = false;
  }
}

function rateText(r) {
  const pct = formatRate(r);
  return pct == null ? "—" : pct + "%";
}

function toggleDigest(id) {
  expanded.value = { ...expanded.value, [id]: !expanded.value[id] };
}

async function pollRepo(repo) {
  polling.value = { ...polling.value, [repo.id]: true };
  try {
    const res = await api("POST", `/repos/${repo.id}/poll`);
    toastOk(`轮询完成，新增 ${res.newCommits ?? 0} 条提交`);
    await load();
    emit("changed");
  } catch (e) {
    toastErr(e);
  } finally {
    polling.value = { ...polling.value, [repo.id]: false };
  }
}

async function analyzeRepo(repo) {
  analyzing.value = { ...analyzing.value, [repo.id]: true };
  try {
    await api("POST", `/repos/${repo.id}/analyze`);
    toastOk("日报已加入队列");
  } catch (e) {
    toastErr(e);
  } finally {
    analyzing.value = { ...analyzing.value, [repo.id]: false };
  }
}

async function addRepo() {
  try {
    if (!form.value.owner || !form.value.name) throw new Error("owner 与 name 必填");
    const body = {
      owner: form.value.owner.trim(),
      name: form.value.name.trim(),
      pollEnabled: !!form.value.pollEnabled,
    };
    if (form.value.token) body.token = form.value.token;
    if (form.value.branch.trim()) body.branch = form.value.branch.trim();
    await api("POST", "/repos", body);
    toastOk("仓库已添加");
    showAdd.value = false;
    form.value = { owner: "", name: "", token: "", branch: "", pollEnabled: true };
    await load();
    emit("changed");
  } catch (e) {
    toastErr(e);
  }
}

function openEdit(repo) {
  editing.value = repo;
  editForm.value = { token: "", branch: repo.branch || "", pollEnabled: !!repo.pollEnabled };
}

async function saveEdit() {
  try {
    const body = {
      pollEnabled: !!editForm.value.pollEnabled,
      branch: editForm.value.branch.trim(), // 空串 = 回退默认分支
    };
    if (editForm.value.token) body.token = editForm.value.token;
    await api("PATCH", `/repos/${editing.value.id}`, body);
    toastOk("已更新");
    editing.value = null;
    await load();
    emit("changed");
  } catch (e) {
    toastErr(e);
  }
}

async function removeRepo(repo) {
  if (!(await confirmDanger(`删除仓库 ${repo.owner}/${repo.name}？历史数据将一并清除。`))) return;
  try {
    await api("DELETE", `/repos/${repo.id}`);
    toastOk("已删除");
    await load();
    emit("changed");
  } catch (e) {
    toastErr(e);
  }
}

const empty = computed(() => !loading.value && !repos.value.length);

onMounted(load);

defineExpose({ load });
</script>

<template>
  <div>
    <div class="pf-toolbar mb-4">
      <pf-button variant="default" size="sm" @click="load">刷新</pf-button>
      <pf-button v-if="isAdmin" variant="primary" size="sm" @click="showAdd = !showAdd">
        {{ showAdd ? "收起表单" : "添加仓库" }}
      </pf-button>
    </div>

    <div v-if="showAdd && isAdmin" class="pf-card mb-4">
      <h3 class="m-0 mb-3 text-[16px] font-semibold">添加仓库</h3>
      <form class="pf-form" @submit.prevent="addRepo">
        <div class="grid gap-3" style="grid-template-columns: repeat(auto-fit, minmax(180px, 1fr))">
          <div class="pf-field">
            <label>Owner</label>
            <input v-model="form.owner" placeholder="org 或用户名" />
          </div>
          <div class="pf-field">
            <label>Name</label>
            <input v-model="form.name" placeholder="仓库名" />
          </div>
          <div class="pf-field">
            <label>Token（可选）</label>
            <input v-model="form.token" type="password" placeholder="留空使用全局 Token" autocomplete="off" />
          </div>
          <div class="pf-field">
            <label>分支（可选）</label>
            <input v-model="form.branch" placeholder="留空使用默认分支" />
          </div>
          <div class="pf-field">
            <label>轮询</label>
            <label class="inline-flex items-center gap-2 text-[13px]">
              <input v-model="form.pollEnabled" type="checkbox" />
              启用自动轮询
            </label>
          </div>
        </div>
        <div class="pf-toolbar mt-3">
          <pf-button variant="primary" @click="addRepo">创建</pf-button>
          <pf-button variant="ghost" @click="showAdd = false">取消</pf-button>
        </div>
      </form>
    </div>

    <div v-if="loading" class="pf-card flex items-center gap-3">
      <pf-spinner></pf-spinner>
      <span class="pf-muted">加载总览…</span>
    </div>

    <pf-empty v-else-if="empty">
      暂无仓库。{{ isAdmin ? "请点击「添加仓库」接入第一个 GitHub 仓库。" : "请联系管理员添加仓库。" }}
    </pf-empty>

    <div v-else class="gp-grid">
      <article v-for="r in repos" :key="r.id" class="pf-card gp-repo-card">
        <header class="flex items-start justify-between gap-2 mb-3">
          <div>
            <div class="text-[16px] font-semibold leading-tight">
              <span class="pf-muted">{{ r.owner }}/</span>{{ r.name }}
            </div>
            <div class="pf-muted text-[12px] mt-1">
              {{ r.branch || r.defaultBranch || "—" }}{{ r.branch ? "（指定）" : "" }}
              · 轮询 {{ r.pollEnabled ? "开" : "关" }}
              · Token {{ r.hasToken ? "已配置" : "全局" }}
            </div>
          </div>
          <pf-badge v-if="r.openIssues > 0" tone="warn">{{ r.openIssues }} 问题</pf-badge>
          <pf-badge v-else tone="neutral">无未关问题</pf-badge>
        </header>

        <div class="gp-metrics">
          <div>
            <div class="gp-metric-label">7 日提交</div>
            <div class="gp-metric-val">{{ r.commits7d ?? 0 }}</div>
          </div>
          <div>
            <div class="gp-metric-label">30 日提交</div>
            <div class="gp-metric-val">{{ r.commits30d ?? 0 }}</div>
          </div>
          <div>
            <div class="gp-metric-label">规范率</div>
            <div class="gp-metric-val">
              <pf-badge :tone="rateTone(r.conventionRate30d)">{{ rateText(r.conventionRate30d) }}</pf-badge>
            </div>
          </div>
          <div>
            <div class="gp-metric-label">平均修复时长</div>
            <div class="gp-metric-val text-[14px]">{{ humanizeHours(r.avgLeadTimeHours) }}</div>
          </div>
        </div>

        <div class="mt-3 pt-3" style="border-top: 1px solid var(--pf-border)">
          <div class="pf-muted text-[12px] mb-1">
            最近摘要
            <span v-if="r.lastDigest?.createdAt"> · {{ formatAbs(r.lastDigest.createdAt) }}</span>
          </div>
          <template v-if="r.lastDigest?.summary">
            <p class="m-0 text-[13px] leading-relaxed">
              {{ expanded[r.id] ? r.lastDigest.summary : excerpt(r.lastDigest.summary, 120) }}
            </p>
            <button
              v-if="(r.lastDigest.summary || '').length > 120"
              type="button"
              class="pf-link text-[12px] mt-1 bg-transparent border-0 p-0 cursor-pointer"
              @click="toggleDigest(r.id)"
            >
              {{ expanded[r.id] ? "收起" : "展开" }}
            </button>
          </template>
          <p v-else class="m-0 pf-muted text-[13px]">暂无日报摘要</p>
        </div>

        <div class="pf-toolbar mt-3 flex-wrap gap-1.5">
          <pf-button
            variant="default"
            size="sm"
            :loading="!!polling[r.id]"
            @click="pollRepo(r)"
          >立即轮询</pf-button>
          <pf-button
            variant="default"
            size="sm"
            :loading="!!analyzing[r.id]"
            @click="analyzeRepo(r)"
          >生成日报</pf-button>
          <template v-if="isAdmin">
            <pf-button variant="ghost" size="sm" @click="openEdit(r)">编辑</pf-button>
            <pf-button variant="danger" size="sm" @click="removeRepo(r)">删除</pf-button>
          </template>
        </div>
        <div class="pf-muted text-[12px] mt-2">
          上次轮询 {{ formatAbs(r.lastPolledAt) }} · 最近提交 {{ formatAbs(r.lastCommitAt) }}
        </div>
      </article>
    </div>

    <div v-if="editing" class="gp-modal-backdrop" @click.self="editing = null">
      <div class="pf-card gp-modal">
        <h3 class="m-0 mb-3 text-[16px] font-semibold">编辑仓库</h3>
        <p class="m-0 mb-3 text-[13px]">
          <span class="pf-code">{{ editing.owner }}/{{ editing.name }}</span>
        </p>
        <form class="pf-form" @submit.prevent="saveEdit">
          <div class="pf-field">
            <label>Token（留空不改）</label>
            <input v-model="editForm.token" type="password" placeholder="新 Token" autocomplete="off" />
          </div>
          <div class="pf-field">
            <label>分支</label>
            <input v-model="editForm.branch" :placeholder="'留空使用默认分支（' + (editing.defaultBranch || '自动检测') + '）'" />
            <p class="pf-muted text-[12px] m-0 mt-1">切换分支后下次轮询自动回填该分支历史</p>
          </div>
          <div class="pf-field">
            <label class="inline-flex items-center gap-2">
              <input v-model="editForm.pollEnabled" type="checkbox" />
              启用自动轮询
            </label>
          </div>
          <div class="pf-toolbar">
            <pf-button variant="ghost" @click="editing = null">取消</pf-button>
            <pf-button variant="primary" @click="saveEdit">保存</pf-button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>