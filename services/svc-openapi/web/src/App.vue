<script setup>
import { ref, onMounted } from "vue";

const base = "/api/openapi";
const err = ref("");
const apps = ref([]);
const scopes = ref([]);
const newSecret = ref(null); // {appKey, appSecret} shown once
const creating = ref(false);
const form = ref({ appKey: "", name: "", scopes: [], ratePerMin: 60 });
// 用量视图（specs/013）
const usage = ref({ enabled: false, usage: [] });
const usageWindow = ref("24h");

function fail(e) { err.value = e.message || String(e); setTimeout(() => (err.value = ""), 5000); }

async function api(method, path, body) {
  const opts = { method, credentials: "same-origin", headers: { "Content-Type": "application/json" } };
  if (body !== undefined) opts.body = JSON.stringify(body);
  const r = await fetch(base + path, opts);
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(data.error || `HTTP ${r.status}`);
  return data;
}

async function load() {
  try {
    apps.value = await api("GET", "/apps");
    scopes.value = await api("GET", "/scopes");
    await loadUsage();
  } catch (e) { fail(e); }
}

async function loadUsage() {
  try { usage.value = await api("GET", `/usage?window=${usageWindow.value}`); }
  catch (e) { usage.value = { enabled: true, error: e.message, usage: [] }; }
}
function usageFor(appKey) {
  const u = (usage.value.usage || []).find(x => x.appKey === appKey);
  return u ? Math.round(u.count) : 0;
}

async function create() {
  try {
    if (!form.value.appKey) throw new Error("appKey 必填");
    const res = await api("POST", "/apps", form.value);
    newSecret.value = res;           // secret shown once
    creating.value = false;
    form.value = { appKey: "", name: "", scopes: [], ratePerMin: 60 };
    await load();
  } catch (e) { fail(e); }
}

async function toggleScope(app, scope) {
  const set = new Set(app.scopes || []);
  set.has(scope) ? set.delete(scope) : set.add(scope);
  try { await api("PATCH", `/apps/${app.appKey}`, { scopes: [...set] }); await load(); } catch (e) { fail(e); }
}

async function setStatus(app, status) {
  try { await api("PATCH", `/apps/${app.appKey}`, { status }); await load(); } catch (e) { fail(e); }
}

async function setRate(app, val) {
  const n = Number(val);
  if (!n) return;
  try { await api("PATCH", `/apps/${app.appKey}`, { ratePerMin: n }); await load(); } catch (e) { fail(e); }
}

async function rotate(app) {
  if (!(await pfConfirm(`换发 ${app.appKey} 的 secret？旧 secret 立即失效。`, { danger: true }))) return;
  try { newSecret.value = await api("POST", `/apps/${app.appKey}/rotate`); } catch (e) { fail(e); }
}

onMounted(load);
</script>

<template>
  <div>
    <pf-topbar title="PlatFarm 开放平台">
      <pf-button variant="default" size="sm" @click="load">刷新</pf-button>
      <pf-button variant="primary" size="sm" @click="creating = true">新建应用</pf-button>
    </pf-topbar>

    <div class="pf-container page">
      <pf-alert v-if="err" tone="danger">{{ err }}</pf-alert>

      <div v-if="newSecret" class="cred-once">
        <pf-secret label="APP_KEY">{{ newSecret.appKey }}</pf-secret>
        <pf-secret label="APP_SECRET（只显示一次，请立即复制）">{{ newSecret.appSecret }}</pf-secret>
        <div class="pf-toolbar">
          <pf-button variant="default" size="sm" @click="newSecret = null">我已保存</pf-button>
        </div>
      </div>

      <div v-if="creating" class="pf-card editor">
        <form class="pf-form" @submit.prevent="create">
          <div class="pf-field">
            <label for="o-appkey">appKey（唯一）</label>
            <input id="o-appkey" v-model="form.appKey" placeholder="appKey（唯一）" />
          </div>
          <div class="pf-field">
            <label for="o-name">名称</label>
            <input id="o-name" v-model="form.name" placeholder="名称" />
          </div>
          <div class="pf-field">
            <label for="o-rate">每分钟限流</label>
            <input id="o-rate" v-model.number="form.ratePerMin" type="number" placeholder="每分钟限流" />
          </div>
          <div class="scopes">
            <label v-for="s in scopes" :key="s.name" class="scope-option">
              <input type="checkbox" :value="s.name" v-model="form.scopes" />
              <span>{{ s.name }}</span>
              <span class="pf-muted">{{ s.desc }}</span>
            </label>
          </div>
          <div class="pf-toolbar">
            <pf-button variant="primary" @click="create">创建</pf-button>
            <pf-button variant="ghost" @click="creating = false">取消</pf-button>
          </div>
        </form>
      </div>

      <div class="pf-toolbar">
        <span class="pf-muted">用量窗口</span>
        <div class="pf-field window-field">
          <select v-model="usageWindow" @change="loadUsage">
            <option>1h</option>
            <option>24h</option>
            <option>168h</option>
          </select>
        </div>
        <span v-if="!usage.enabled" class="pf-muted">（用量未启用：需 observability profile + LOKI_URL）</span>
        <span v-else-if="usage.error" class="pf-muted">Loki: {{ usage.error }}</span>
      </div>

      <table class="pf-table">
        <thead>
          <tr>
            <th>appKey</th>
            <th>名称</th>
            <th>scopes</th>
            <th>限流/min</th>
            <th>用量</th>
            <th>状态</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="a in apps" :key="a.appKey">
            <td><span class="pf-code">{{ a.appKey }}</span></td>
            <td>{{ a.name }}</td>
            <td class="scopes-cell">
              <label
                v-for="s in scopes"
                :key="s.name"
                class="chip"
                :data-on="(a.scopes || []).includes(s.name) ? '1' : null"
              >
                <input
                  type="checkbox"
                  :checked="(a.scopes || []).includes(s.name)"
                  @change="toggleScope(a, s.name)"
                />
                {{ s.name }}
              </label>
            </td>
            <td>
              <div class="pf-field rate-field">
                <input
                  class="rate"
                  :value="a.ratePerMin"
                  @change="e => setRate(a, e.target.value)"
                  type="number"
                />
              </div>
            </td>
            <td>{{ usage.enabled ? usageFor(a.appKey) : '—' }}</td>
            <td>
              <span class="pf-dot" :data-tone="a.status === 1 ? 'ok' : 'danger'">
                {{ a.status === 1 ? '启用' : '停用' }}
              </span>
            </td>
            <td class="ops">
              <pf-button v-if="a.status === 1" variant="default" size="sm" @click="setStatus(a, 0)">停用</pf-button>
              <pf-button v-else variant="default" size="sm" @click="setStatus(a, 1)">启用</pf-button>
              <pf-button variant="danger" size="sm" @click="rotate(a)">换 secret</pf-button>
            </td>
          </tr>
          <tr v-if="!apps.length">
            <td colspan="7"><span class="pf-muted">（暂无应用）</span></td>
          </tr>
        </tbody>
      </table>

      <p class="pf-muted foot">
        后台管理长期凭据与规则；合作方自行调
        <span class="pf-code">POST /oauth/token</span>
        换 access token。
      </p>
    </div>
  </div>
</template>

<style>
.page { width: min(1000px, calc(100% - 32px)); }
.cred-once { display: flex; flex-direction: column; gap: 12px; margin: 16px 0; }
.editor { margin: 16px 0; padding: 16px; }
.scopes { display: flex; flex-wrap: wrap; gap: 12px; margin: 8px 0; }
.scope-option {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--pf-fs-13);
  color: var(--pf-text);
}
.window-field { margin: 0; }
.window-field select { margin: 0; }
.scopes-cell { display: flex; flex-direction: column; gap: 2px; }
.chip {
  font-size: var(--pf-fs-12);
  color: var(--pf-text-muted);
}
.chip[data-on="1"] { color: var(--pf-success); }
.rate-field { margin: 0; }
.rate-field .rate { width: 70px; margin: 0; }
.ops { white-space: nowrap; }
.ops pf-button { margin-right: 6px; }
.foot { margin-top: 16px; }
</style>
