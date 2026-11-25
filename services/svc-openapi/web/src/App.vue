<script setup>
import { ref, computed, onMounted } from "vue";

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

// scope 目录来自各服务清单的 open_api（pctl sync 生成）：按服务分组展示。
const scopeGroups = computed(() => {
  const groups = [];
  for (const s of scopes.value) {
    let g = groups.find((x) => x.service === s.service);
    if (!g) groups.push((g = { service: s.service || "其他", scopes: [] }));
    g.scopes.push(s);
  }
  return groups;
});
// app 上已授权、但目录里已经没有的 scope（服务下线或路由删除）：仍列出来，方便收回。
function staleScopes(app) {
  const known = new Set(scopes.value.map((s) => s.name));
  return (app.scopes || []).filter((s) => !known.has(s));
}
const routeTip = (s) => (s.routes || []).join("\n");

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
      <pf-theme-toggle></pf-theme-toggle>
      <pf-button variant="default" size="sm" @click="load">刷新</pf-button>
      <pf-button variant="primary" size="sm" @click="creating = true">新建应用</pf-button>
    </pf-topbar>

    <div class="pf-container">
      <pf-alert v-if="err" tone="danger">{{ err }}</pf-alert>

      <div v-if="newSecret" class="my-4 flex flex-col gap-3">
        <pf-secret label="APP_KEY">{{ newSecret.appKey }}</pf-secret>
        <pf-secret label="APP_SECRET（只显示一次，请立即复制）">{{ newSecret.appSecret }}</pf-secret>
        <div class="pf-toolbar">
          <pf-button variant="default" size="sm" @click="newSecret = null">我已保存</pf-button>
        </div>
      </div>

      <div v-if="creating" class="pf-card my-4">
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
          <div v-for="g in scopeGroups" :key="g.service" class="my-2 flex flex-col gap-1.5">
            <span class="text-[12px] font-semibold">{{ g.service }}</span>
            <div class="flex flex-wrap gap-x-4 gap-y-1.5">
              <label v-for="s in g.scopes" :key="s.name" class="inline-flex items-center gap-1.5 text-[13px]" :title="routeTip(s)">
                <input type="checkbox" :value="s.name" v-model="form.scopes" />
                <span>{{ s.name }}</span>
                <span class="pf-muted">{{ s.desc }}</span>
              </label>
            </div>
          </div>
          <p v-if="!scopes.length" class="pf-muted my-2">
            当前没有可授权的 scope：没有启用的服务在清单里声明 open_api，或改清单后还没重新执行 pctl sync。
          </p>
          <div class="pf-toolbar">
            <pf-button variant="primary" @click="create">创建</pf-button>
            <pf-button variant="ghost" @click="creating = false">取消</pf-button>
          </div>
        </form>
      </div>

      <div class="pf-card flex flex-col gap-3">
        <div class="pf-toolbar">
          <span class="pf-muted">用量窗口</span>
          <div class="pf-field w-24">
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
              <td>
                <div class="flex flex-col gap-1.5">
                  <div v-for="g in scopeGroups" :key="g.service" class="flex flex-col gap-0.5">
                    <span class="pf-muted text-[11px]">{{ g.service }}</span>
                    <label
                      v-for="s in g.scopes"
                      :key="s.name"
                      class="chip text-[12px] text-muted"
                      :title="[s.desc, routeTip(s)].filter(Boolean).join('\n')"
                      :data-on="(a.scopes || []).includes(s.name) ? '1' : null"
                    >
                      <input
                        type="checkbox"
                        :checked="(a.scopes || []).includes(s.name)"
                        @change="toggleScope(a, s.name)"
                      />
                      {{ s.name }}
                    </label>
                  </div>
                  <div v-if="staleScopes(a).length" class="flex flex-col gap-0.5">
                    <span class="pf-muted text-[11px]">已失效（目录里没有，取消勾选即收回）</span>
                    <label v-for="s in staleScopes(a)" :key="s" class="chip text-[12px] text-muted" data-on="1">
                      <input type="checkbox" checked @change="toggleScope(a, s)" />
                      {{ s }}
                    </label>
                  </div>
                </div>
              </td>
              <td>
                <div class="pf-field w-20">
                  <input
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
              <td class="whitespace-nowrap space-x-1.5">
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
      </div>

      <p class="pf-muted mt-4">
        后台管理长期凭据与规则；合作方自行调
        <span class="pf-code">POST /oauth/token</span>
        换 access token。
      </p>
    </div>
  </div>
</template>

<style>
/* 仅保留无法用工具类表达的状态选择器；其余布局用 pf-ui 组件类 + tw 工具类 */
.chip[data-on="1"] { color: var(--pf-success); }
</style>
