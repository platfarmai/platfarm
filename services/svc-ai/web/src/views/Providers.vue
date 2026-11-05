<script setup>
import { ref, onMounted, computed } from "vue";
import { api, toastOk, toastErr, confirmDanger } from "../api.js";
import { formatAbs } from "../utils.js";

const loading = ref(true);
const providers = ref([]);
const showAdd = ref(false);
const editing = ref(null);
const form = ref({ name: "", kind: "anthropic", baseUrl: "", apiKey: "" });
const editForm = ref({ baseUrl: "", apiKey: "", enabled: true });
const saving = ref(false);

const empty = computed(() => !loading.value && !providers.value.length);

async function load() {
  loading.value = true;
  try {
    const data = await api("GET", "/admin/providers");
    providers.value = data.providers || [];
  } catch (e) {
    toastErr(e);
  } finally {
    loading.value = false;
  }
}

async function create() {
  try {
    if (!form.value.name.trim()) throw new Error("名称必填");
    if (!form.value.apiKey.trim()) throw new Error("API Key 必填");
    saving.value = true;
    const body = {
      name: form.value.name.trim(),
      kind: form.value.kind,
      apiKey: form.value.apiKey,
    };
    if (form.value.baseUrl.trim()) body.baseUrl = form.value.baseUrl.trim();
    await api("POST", "/admin/providers", body);
    toastOk("Provider 已添加");
    showAdd.value = false;
    form.value = { name: "", kind: "anthropic", baseUrl: "", apiKey: "" };
    await load();
  } catch (e) {
    toastErr(e);
  } finally {
    saving.value = false;
  }
}

function openEdit(p) {
  editing.value = p;
  editForm.value = {
    baseUrl: p.baseUrl || "",
    apiKey: "",
    enabled: !!p.enabled,
  };
}

async function saveEdit() {
  try {
    saving.value = true;
    const body = {
      baseUrl: editForm.value.baseUrl.trim(),
      enabled: !!editForm.value.enabled,
    };
    if (editForm.value.apiKey.trim()) body.apiKey = editForm.value.apiKey;
    await api("PATCH", `/admin/providers/${editing.value.id}`, body);
    toastOk("已更新");
    editing.value = null;
    await load();
  } catch (e) {
    toastErr(e);
  } finally {
    saving.value = false;
  }
}

async function toggleEnabled(p) {
  try {
    await api("PATCH", `/admin/providers/${p.id}`, { enabled: !p.enabled });
    toastOk(p.enabled ? "已停用" : "已启用");
    await load();
  } catch (e) {
    toastErr(e);
  }
}

async function remove(p) {
  if (!(await confirmDanger(`删除 Provider「${p.name}」？关联别名可能失效。`))) return;
  try {
    await api("DELETE", `/admin/providers/${p.id}`);
    toastOk("已删除");
    if (editing.value?.id === p.id) editing.value = null;
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
        {{ showAdd ? "收起表单" : "添加 Provider" }}
      </pf-button>
    </div>

    <div v-if="showAdd" class="pf-card mb-4">
      <h3 class="m-0 mb-3 text-[16px] font-semibold">添加上游 Provider</h3>
      <form class="pf-form" @submit.prevent="create">
        <div class="ai-form-grid">
          <div class="pf-field">
            <label for="prov-name">名称</label>
            <input id="prov-name" v-model="form.name" placeholder="如 anthropic-default" />
          </div>
          <div class="pf-field">
            <label for="prov-kind">协议</label>
            <select id="prov-kind" v-model="form.kind">
              <option value="anthropic">Anthropic Messages</option>
              <option value="openai">OpenAI Chat Completions</option>
            </select>
          </div>
          <div class="pf-field">
            <label for="prov-base">Base URL</label>
            <input
              id="prov-base"
              v-model="form.baseUrl"
              :placeholder="form.kind === 'openai' ? 'https://api.openai.com/v1' : 'https://api.anthropic.com'"
            />
            <p class="ai-hint">
              {{ form.kind === "openai"
                ? "OpenAI 兼容网关（one-api / litellm / apihub 等），URL 需含 /v1"
                : "Anthropic Messages 协议网关，URL 不含 /v1" }}
            </p>
          </div>
          <div class="pf-field">
            <label for="prov-key">API Key</label>
            <input id="prov-key" v-model="form.apiKey" type="password" placeholder="上游密钥" autocomplete="off" />
          </div>
        </div>
        <div class="pf-toolbar mt-3">
          <pf-button variant="primary" :loading="saving || undefined" @click="create">创建</pf-button>
          <pf-button variant="ghost" @click="showAdd = false">取消</pf-button>
        </div>
      </form>
    </div>

    <div v-if="editing" class="pf-card mb-4">
      <h3 class="m-0 mb-3 text-[16px] font-semibold">编辑 {{ editing.name }}</h3>
      <form class="pf-form" @submit.prevent="saveEdit">
        <div class="ai-form-grid">
          <div class="pf-field">
            <label for="edit-base">Base URL</label>
            <input id="edit-base" v-model="editForm.baseUrl" placeholder="https://api.anthropic.com" />
          </div>
          <div class="pf-field">
            <label for="edit-key">API Key</label>
            <input
              id="edit-key"
              v-model="editForm.apiKey"
              type="password"
              placeholder="留空不改"
              autocomplete="off"
            />
            <p class="ai-hint">留空保留原密钥</p>
          </div>
          <div class="pf-field">
            <label for="edit-enabled">启用</label>
            <select id="edit-enabled" v-model="editForm.enabled">
              <option :value="true">启用</option>
              <option :value="false">停用</option>
            </select>
          </div>
        </div>
        <div class="pf-toolbar mt-3">
          <pf-button variant="primary" :loading="saving || undefined" @click="saveEdit">保存</pf-button>
          <pf-button variant="ghost" @click="editing = null">取消</pf-button>
        </div>
      </form>
    </div>

    <div v-if="loading" class="pf-card flex items-center gap-3">
      <pf-spinner></pf-spinner>
      <span class="pf-muted">加载 Provider…</span>
    </div>

    <div v-else-if="empty" class="pf-card">
      <pf-empty>暂无上游 Provider，请先添加。</pf-empty>
    </div>

    <div v-else class="pf-card">
      <table class="pf-table">
        <thead>
          <tr>
            <th>名称</th>
            <th>协议</th>
            <th>Base URL</th>
            <th>Key</th>
            <th>状态</th>
            <th>创建时间</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in providers" :key="p.id">
            <td>{{ p.name }}</td>
            <td><span class="pf-badge">{{ p.kind || "anthropic" }}</span></td>
            <td class="ai-mono">{{ p.baseUrl || "—" }}</td>
            <td class="ai-mono">{{ p.keyMasked || "****" }}</td>
            <td>
              <span class="pf-dot" :data-tone="p.enabled ? 'ok' : 'danger'">
                {{ p.enabled ? "启用" : "停用" }}
              </span>
            </td>
            <td class="pf-muted">{{ formatAbs(p.createdAt) }}</td>
            <td class="whitespace-nowrap space-x-1.5">
              <pf-button variant="default" size="sm" @click="toggleEnabled(p)">
                {{ p.enabled ? "停用" : "启用" }}
              </pf-button>
              <pf-button variant="default" size="sm" @click="openEdit(p)">编辑</pf-button>
              <pf-button variant="danger" size="sm" @click="remove(p)">删除</pf-button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
