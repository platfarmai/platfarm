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
  if (!confirm(`换发 ${app.appKey} 的 secret？旧 secret 立即失效。`)) return;
  try { newSecret.value = await api("POST", `/apps/${app.appKey}/rotate`); } catch (e) { fail(e); }
}

onMounted(load);
</script>

<template>
  <div class="wrap">
    <header><b>PlatFarm 开放平台</b><button @click="load">刷新</button>
      <button class="primary" @click="creating = true">＋ 新建应用</button></header>
    <p v-if="err" class="err">{{ err }}</p>

    <div v-if="newSecret" class="secret">
      <b>应用密钥（只显示一次，请立即复制）</b>
      <div>APP_KEY = <code>{{ newSecret.appKey }}</code></div>
      <div>APP_SECRET = <code>{{ newSecret.appSecret }}</code></div>
      <button @click="newSecret = null">我已保存</button>
    </div>

    <div v-if="creating" class="editor">
      <input v-model="form.appKey" placeholder="appKey（唯一）" />
      <input v-model="form.name" placeholder="名称" />
      <input v-model.number="form.ratePerMin" type="number" placeholder="每分钟限流" />
      <div class="scopes">
        <label v-for="s in scopes" :key="s.name">
          <input type="checkbox" :value="s.name" v-model="form.scopes" /> {{ s.name }} <span class="muted">{{ s.desc }}</span>
        </label>
      </div>
      <div><button class="primary" @click="create">创建</button><button @click="creating = false">取消</button></div>
    </div>

    <div class="bar">
      <span class="muted">用量窗口</span>
      <select v-model="usageWindow" @change="loadUsage"><option>1h</option><option>24h</option><option>168h</option></select>
      <span v-if="!usage.enabled" class="muted">（用量未启用：需 observability profile + LOKI_URL）</span>
      <span v-else-if="usage.error" class="muted">Loki: {{ usage.error }}</span>
    </div>
    <table>
      <thead><tr><th>appKey</th><th>名称</th><th>scopes</th><th>限流/min</th><th>用量</th><th>状态</th><th>操作</th></tr></thead>
      <tbody>
        <tr v-for="a in apps" :key="a.appKey">
          <td><code>{{ a.appKey }}</code></td>
          <td>{{ a.name }}</td>
          <td class="scopes-cell">
            <label v-for="s in scopes" :key="s.name" class="chip" :class="{ on: (a.scopes||[]).includes(s.name) }">
              <input type="checkbox" :checked="(a.scopes||[]).includes(s.name)" @change="toggleScope(a, s.name)" /> {{ s.name }}
            </label>
          </td>
          <td><input class="rate" :value="a.ratePerMin" @change="e => setRate(a, e.target.value)" type="number" /></td>
          <td>{{ usage.enabled ? usageFor(a.appKey) : '—' }}</td>
          <td><span class="badge" :data-s="a.status">{{ a.status === 1 ? '启用' : '停用' }}</span></td>
          <td class="ops">
            <button v-if="a.status === 1" @click="setStatus(a, 0)">停用</button>
            <button v-else @click="setStatus(a, 1)">启用</button>
            <button @click="rotate(a)">换 secret</button>
          </td>
        </tr>
        <tr v-if="!apps.length"><td colspan="7" class="muted">（暂无应用）</td></tr>
      </tbody>
    </table>
    <p class="muted">后台管理长期凭据与规则；合作方自行调 <code>POST /oauth/token</code> 换 access token。</p>
  </div>
</template>

<style>
*{box-sizing:border-box}body{margin:0;font:14px/1.6 system-ui,sans-serif;background:#0d1117;color:#c9d1d9}
.wrap{max-width:1000px;margin:0 auto;padding:16px 20px}
header{display:flex;align-items:center;gap:12px;border-bottom:1px solid #21262d;padding-bottom:12px}
header b{color:#58a6ff;flex:1}
.err{background:#3d1418;border:1px solid #f85149;color:#ffb4ab;padding:8px 12px;border-radius:6px}
.secret{background:#1c2b1c;border:1px solid #2ea043;border-radius:8px;padding:14px;margin:14px 0}
.secret code{color:#3fb950;user-select:all}
.editor{border:1px solid #21262d;border-radius:8px;padding:14px;margin:14px 0;background:#161b22}
.editor .scopes{display:flex;flex-wrap:wrap;gap:12px;margin:8px 0}
input{background:#0d1117;border:1px solid #30363d;border-radius:6px;color:#c9d1d9;padding:7px 10px;font:inherit;margin-bottom:8px}
input.rate{width:70px;margin:0}
button{background:#21262d;color:#c9d1d9;border:1px solid #30363d;border-radius:6px;padding:5px 12px;cursor:pointer;font:inherit;margin-left:6px}
button:hover{border-color:#58a6ff}button.primary{background:#238636;border-color:#2ea043;color:#fff}
table{width:100%;border-collapse:collapse;margin-top:14px}
th,td{text-align:left;padding:8px 10px;border-bottom:1px solid #21262d;vertical-align:top}
th{color:#8b949e;font-weight:normal}
.scopes-cell{display:flex;flex-direction:column;gap:2px}
.chip{font-size:12px;color:#8b949e}.chip.on{color:#3fb950}
.badge{padding:1px 8px;border-radius:10px;font-size:12px;border:1px solid #30363d}
.badge[data-s="1"]{color:#3fb950;border-color:#238636}.badge[data-s="0"]{color:#8b949e}
.ops{white-space:nowrap}.muted{color:#8b949e}code{background:#161b22;padding:1px 5px;border-radius:4px}
</style>
