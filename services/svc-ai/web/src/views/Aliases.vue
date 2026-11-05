<script setup>
import { ref, onMounted, computed } from "vue";
import { api, toastOk, toastErr, confirmDanger } from "../api.js";

const loading = ref(true);
const aliases = ref([]);
const providers = ref([]);
const showAdd = ref(false);
const saving = ref(false);
const form = ref({ alias: "", providerId: "", model: "" });

const empty = computed(() => !loading.value && !aliases.value.length);
const enabledProviders = computed(() => (providers.value || []).filter((p) => p.enabled));

async function loadProviders() {
  const data = await api("GET", "/admin/providers");
  providers.value = data.providers || [];
  if (!form.value.providerId && enabledProviders.value.length) {
    form.value.providerId = String(enabledProviders.value[0].id);
  }
}

async function load() {
  loading.value = true;
  try {
    const [aliasData] = await Promise.all([
      api("GET", "/admin/aliases"),
      loadProviders(),
    ]);
    aliases.value = aliasData.aliases || [];
  } catch (e) {
    toastErr(e);
  } finally {
    loading.value = false;
  }
}

async function create() {
  try {
    if (!form.value.alias.trim()) throw new Error("别名必填");
    if (!form.value.providerId) throw new Error("请选择 Provider");
    if (!form.value.model.trim()) throw new Error("真实模型必填");
    saving.value = true;
    await api("POST", "/admin/aliases", {
      alias: form.value.alias.trim(),
      providerId: Number(form.value.providerId),
      model: form.value.model.trim(),
    });
    toastOk("别名已添加");
    showAdd.value = false;
    form.value = {
      alias: "",
      providerId: enabledProviders.value[0] ? String(enabledProviders.value[0].id) : "",
      model: "",
    };
    await load();
  } catch (e) {
    toastErr(e);
  } finally {
    saving.value = false;
  }
}

async function remove(a) {
  if (!(await confirmDanger(`删除别名「${a.alias}」？`))) return;
  try {
    await api("DELETE", `/admin/aliases/${a.id}`);
    toastOk("已删除");
    await load();
  } catch (e) {
    toastErr(e);
  }
}

onMounted(load);
</script>

<template>
  <div>
    <p class="pf-muted mb-4 text-[13px] leading-relaxed">
      服务侧 model 命中别名时路由到对应 provider+真实模型；未命中则透传至默认 provider。
    </p>

    <div class="pf-toolbar mb-4">
      <pf-button variant="default" size="sm" @click="load">刷新</pf-button>
      <pf-button variant="primary" size="sm" @click="showAdd = !showAdd">
        {{ showAdd ? "收起表单" : "添加别名" }}
      </pf-button>
    </div>

    <div v-if="showAdd" class="pf-card mb-4">
      <h3 class="m-0 mb-3 text-[16px] font-semibold">添加模型别名</h3>
      <form class="pf-form" @submit.prevent="create">
        <div class="ai-form-grid">
          <div class="pf-field">
            <label for="alias-name">别名</label>
            <input id="alias-name" v-model="form.alias" class="ai-mono" placeholder="如 claude-sonnet" />
          </div>
          <div class="pf-field">
            <label for="alias-provider">Provider</label>
            <select id="alias-provider" v-model="form.providerId">
              <option value="" disabled>选择 Provider</option>
              <option v-for="p in providers" :key="p.id" :value="String(p.id)">
                {{ p.name }}{{ p.enabled ? "" : "（已停用）" }}
              </option>
            </select>
          </div>
          <div class="pf-field">
            <label for="alias-model">真实模型</label>
            <input id="alias-model" v-model="form.model" class="ai-mono" placeholder="如 claude-sonnet-4-20250514" />
          </div>
        </div>
        <div class="pf-toolbar mt-3">
          <pf-button variant="primary" :loading="saving || undefined" @click="create">创建</pf-button>
          <pf-button variant="ghost" @click="showAdd = false">取消</pf-button>
        </div>
      </form>
    </div>

    <div v-if="loading" class="pf-card flex items-center gap-3">
      <pf-spinner></pf-spinner>
      <span class="pf-muted">加载别名…</span>
    </div>

    <div v-else-if="empty" class="pf-card">
      <pf-empty>暂无模型别名。</pf-empty>
    </div>

    <div v-else class="pf-card">
      <table class="pf-table">
        <thead>
          <tr>
            <th>别名</th>
            <th>Provider</th>
            <th>真实模型</th>
            <th>状态</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="a in aliases" :key="a.id">
            <td class="ai-mono">{{ a.alias }}</td>
            <td>{{ a.providerName }}</td>
            <td class="ai-mono">{{ a.model }}</td>
            <td>
              <span class="pf-dot" :data-tone="a.enabled ? 'ok' : 'danger'">
                {{ a.enabled ? "启用" : "停用" }}
              </span>
            </td>
            <td>
              <pf-button variant="danger" size="sm" @click="remove(a)">删除</pf-button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
