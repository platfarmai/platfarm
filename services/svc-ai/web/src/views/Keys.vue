<script setup>
import { ref, onMounted, computed } from "vue";
import { api, toastOk, toastErr, confirmDanger } from "../api.js";
import { formatAbs } from "../utils.js";

const loading = ref(true);
const keys = ref([]);
const showAdd = ref(false);
const saving = ref(false);
const form = ref({ name: "" });
const created = ref(null);

const empty = computed(() => !loading.value && !keys.value.length);

async function load() {
  loading.value = true;
  try {
    const data = await api("GET", "/admin/keys");
    keys.value = data.keys || [];
  } catch (e) {
    toastErr(e);
  } finally {
    loading.value = false;
  }
}

async function create() {
  try {
    if (!form.value.name.trim()) throw new Error("名称必填");
    saving.value = true;
    const res = await api("POST", "/admin/keys", { name: form.value.name.trim() });
    created.value = {
      id: res.id,
      key: res.key,
      notice: res.notice || "明文仅此一次返回",
      name: form.value.name.trim(),
    };
    form.value = { name: "" };
    showAdd.value = false;
    toastOk("虚拟 Key 已创建");
    await load();
  } catch (e) {
    toastErr(e);
  } finally {
    saving.value = false;
  }
}

async function copyKey() {
  if (!created.value?.key) return;
  try {
    await navigator.clipboard.writeText(created.value.key);
    toastOk("已复制到剪贴板");
  } catch (e) {
    toastErr(e instanceof Error ? e : new Error("复制失败"));
  }
}

async function toggleEnabled(k) {
  try {
    await api("PATCH", `/admin/keys/${k.id}`, { enabled: !k.enabled });
    toastOk(k.enabled ? "已停用" : "已启用");
    await load();
  } catch (e) {
    toastErr(e);
  }
}

async function remove(k) {
  if (!(await confirmDanger(`删除虚拟 Key「${k.name}」？调用方将立即失效。`))) return;
  try {
    await api("DELETE", `/admin/keys/${k.id}`);
    toastOk("已删除");
    if (created.value?.id === k.id) created.value = null;
    await load();
  } catch (e) {
    toastErr(e);
  }
}

onMounted(load);
</script>

<template>
  <div>
    <div class="pf-toolbar mb-4">
      <pf-button variant="default" size="sm" @click="load">刷新</pf-button>
      <pf-button variant="primary" size="sm" @click="showAdd = !showAdd">
        {{ showAdd ? "收起表单" : "创建虚拟 Key" }}
      </pf-button>
    </div>

    <div v-if="created" class="ai-key-box mb-4">
      <div class="flex items-start justify-between gap-3 flex-wrap">
        <div>
          <strong>明文仅此一次返回</strong>
          <p class="ai-hint m-0 mt-1">{{ created.notice }}（{{ created.name }}）</p>
        </div>
        <pf-button variant="ghost" size="sm" @click="created = null">关闭</pf-button>
      </div>
      <div class="ai-key-value">{{ created.key }}</div>
      <div class="pf-toolbar">
        <pf-button variant="primary" size="sm" @click="copyKey">复制</pf-button>
      </div>
    </div>

    <div v-if="showAdd" class="pf-card mb-4">
      <h3 class="m-0 mb-3 text-[16px] font-semibold">创建虚拟 Key</h3>
      <form class="pf-form" @submit.prevent="create">
        <div class="pf-field max-w-md">
          <label for="key-name">名称</label>
          <input id="key-name" v-model="form.name" placeholder="调用方服务名，如 svc-gitpulse" />
          <p class="ai-hint">建议使用调用方服务名，便于用量归属</p>
        </div>
        <div class="pf-toolbar mt-3">
          <pf-button variant="primary" :loading="saving || undefined" @click="create">创建</pf-button>
          <pf-button variant="ghost" @click="showAdd = false">取消</pf-button>
        </div>
      </form>
    </div>

    <div class="pf-card mb-4">
      <h3 class="m-0 mb-2 text-[14px] font-semibold">服务接入提示</h3>
      <p class="ai-hint mb-2">调用方配置 Anthropic 兼容客户端：</p>
      <pre class="pf-code m-0 text-[13px] leading-relaxed">ANTHROPIC_BASE_URL=http://svc-ai:8080/api/ai
ANTHROPIC_API_KEY=&lt;key&gt;</pre>
    </div>

    <div v-if="loading" class="pf-card flex items-center gap-3">
      <pf-spinner></pf-spinner>
      <span class="pf-muted">加载虚拟 Key…</span>
    </div>

    <div v-else-if="empty" class="pf-card">
      <pf-empty>暂无虚拟 Key。</pf-empty>
    </div>

    <div v-else class="pf-card">
      <table class="pf-table">
        <thead>
          <tr>
            <th>名称</th>
            <th>状态</th>
            <th>创建时间</th>
            <th>最近使用</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="k in keys" :key="k.id">
            <td>{{ k.name }}</td>
            <td>
              <span class="pf-dot" :data-tone="k.enabled ? 'ok' : 'danger'">
                {{ k.enabled ? "启用" : "停用" }}
              </span>
            </td>
            <td class="pf-muted">{{ formatAbs(k.createdAt) }}</td>
            <td class="pf-muted">{{ k.lastUsedAt ? formatAbs(k.lastUsedAt) : "从未使用" }}</td>
            <td class="whitespace-nowrap space-x-1.5">
              <pf-button variant="default" size="sm" @click="toggleEnabled(k)">
                {{ k.enabled ? "停用" : "启用" }}
              </pf-button>
              <pf-button variant="danger" size="sm" @click="remove(k)">删除</pf-button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
