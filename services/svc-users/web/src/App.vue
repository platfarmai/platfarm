<script setup>
import { ref, onMounted } from "vue";

const base = "/api/users";
const err = ref("");
const ok = ref("");
const users = ref([]);
const isAdmin = ref(false);
const creating = ref(false);
const form = ref({ username: "", role: "user" });
const newCred = ref(null); // {username, password} shown once
const pw = ref({ old: "", neo: "" });
// 分页 + 搜索（specs/014）
const total = ref(0);
const limit = ref(20);
const offset = ref(0);
const q = ref("");
let searchTimer = null;

function fail(e) { err.value = e.message || String(e); setTimeout(() => (err.value = ""), 5000); }
function done(m) { ok.value = m; setTimeout(() => (ok.value = ""), 4000); }

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
    const qs = `?limit=${limit.value}&offset=${offset.value}&q=${encodeURIComponent(q.value)}`;
    const data = await api("GET", qs);
    users.value = data.items || [];
    total.value = data.total || 0;
    isAdmin.value = true;
  } catch (e) { if (String(e.message).includes("admin")) isAdmin.value = false; else fail(e); }
}

function onSearch() {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => { offset.value = 0; load(); }, 300);
}
function nextPage() { if (offset.value + limit.value < total.value) { offset.value += limit.value; load(); } }
function prevPage() { if (offset.value > 0) { offset.value = Math.max(0, offset.value - limit.value); load(); } }

async function create() {
  try {
    if (!form.value.username) throw new Error("用户名必填");
    newCred.value = await api("POST", "", form.value);
    creating.value = false; form.value = { username: "", role: "user" };
    await load();
  } catch (e) { fail(e); }
}
async function setRole(u, role) { try { await api("PATCH", `/${u.id}`, { role }); await load(); } catch (e) { fail(e); } }
async function setStatus(u, status) { try { await api("PATCH", `/${u.id}`, { status }); await load(); } catch (e) { fail(e); } }
async function reset(u) {
  if (!(await pfConfirm(`重置 ${u.username} 的密码？旧密码立即失效。`, { danger: true }))) return;
  try { newCred.value = await api("POST", `/${u.id}/reset-password`); } catch (e) { fail(e); }
}
async function changeOwn() {
  try {
    if (!pw.value.neo) throw new Error("新密码必填");
    await api("POST", "/change-password", { oldPassword: pw.value.old, newPassword: pw.value.neo });
    pw.value = { old: "", neo: "" };
    done("密码已修改，请用新密码重新登录");
    setTimeout(() => (location.href = "/platform/console/"), 1500);
  } catch (e) { fail(e); }
}

onMounted(load);
</script>

<template>
  <div>
    <pf-topbar title="PlatFarm 用户管理">
      <pf-button v-if="isAdmin" variant="default" size="sm" @click="load">刷新</pf-button>
      <pf-button v-if="isAdmin" variant="primary" size="sm" @click="creating = true">新建用户</pf-button>
    </pf-topbar>

    <div class="pf-container">
      <pf-alert v-if="err" tone="danger">{{ err }}</pf-alert>
      <pf-alert v-if="ok" tone="ok">{{ ok }}</pf-alert>

      <div v-if="newCred" class="cred-once">
        <pf-secret label="用户名">{{ newCred.username || ('#' + newCred.id) }}</pf-secret>
        <pf-secret label="初始密码（只显示一次，请立即复制）">{{ newCred.password }}</pf-secret>
        <div class="pf-toolbar">
          <pf-button variant="default" size="sm" @click="newCred = null">我已保存</pf-button>
        </div>
      </div>

      <div v-if="creating" class="pf-card editor">
        <form class="pf-form" @submit.prevent="create">
          <div class="pf-field">
            <label for="u-username">用户名</label>
            <input id="u-username" v-model="form.username" placeholder="用户名" />
          </div>
          <div class="pf-field">
            <label for="u-role">平台角色</label>
            <select id="u-role" v-model="form.role">
              <option value="user">user</option>
              <option value="admin">admin</option>
            </select>
          </div>
          <div class="pf-toolbar">
            <pf-button variant="primary" @click="create">创建</pf-button>
            <pf-button variant="ghost" @click="creating = false">取消</pf-button>
          </div>
        </form>
      </div>

      <div v-if="isAdmin" class="pf-toolbar">
        <div class="pf-field search-field">
          <input v-model="q" @input="onSearch" placeholder="搜索用户名…" />
        </div>
        <pf-pager
          :total="total"
          :limit="limit"
          :offset="offset"
          @pf-change="e => { offset = e.detail.offset; load(); }"
        ></pf-pager>
      </div>

      <table v-if="isAdmin" class="pf-table">
        <thead>
          <tr>
            <th>ID</th>
            <th>用户名</th>
            <th>平台角色</th>
            <th>状态</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="u in users" :key="u.id">
            <td>{{ u.id }}</td>
            <td>{{ u.username }}</td>
            <td>
              <div class="pf-field cell-field">
                <select :value="u.role" @change="e => setRole(u, e.target.value)">
                  <option value="user">user</option>
                  <option value="admin">admin</option>
                </select>
              </div>
            </td>
            <td>
              <span class="pf-dot" :data-tone="u.status === 1 ? 'ok' : 'danger'">
                {{ u.status === 1 ? '启用' : '停用' }}
              </span>
            </td>
            <td class="ops">
              <pf-button v-if="u.status === 1" variant="default" size="sm" @click="setStatus(u, 0)">停用</pf-button>
              <pf-button v-else variant="default" size="sm" @click="setStatus(u, 1)">启用</pf-button>
              <pf-button variant="danger" size="sm" @click="reset(u)">重置密码</pf-button>
            </td>
          </tr>
          <tr v-if="!users.length">
            <td colspan="5"><span class="pf-muted">（无匹配用户）</span></td>
          </tr>
        </tbody>
      </table>
      <p v-else class="pf-muted">你不是平台管理员，仅可修改自己的密码。</p>

      <section class="self">
        <h3>修改我的密码</h3>
        <form class="pf-form self-form" @submit.prevent="changeOwn">
          <div class="pf-field">
            <label for="pw-old">旧密码</label>
            <input id="pw-old" v-model="pw.old" type="password" placeholder="旧密码" />
          </div>
          <div class="pf-field">
            <label for="pw-neo">新密码</label>
            <input id="pw-neo" v-model="pw.neo" type="password" placeholder="新密码" />
          </div>
          <div class="pf-toolbar">
            <pf-button variant="primary" @click="changeOwn">修改密码</pf-button>
            <span class="pf-muted">修改后当前登录失效，需重新登录。</span>
          </div>
        </form>
      </section>
    </div>
  </div>
</template>

<style>
.cred-once { display: flex; flex-direction: column; gap: 12px; margin: 16px 0; }
.editor { margin: 16px 0; padding: 16px; }
.search-field { width: 240px; margin: 0; }
.search-field input { margin: 0; }
.cell-field { margin: 0; }
.ops { white-space: nowrap; }
.ops pf-button { margin-right: 6px; }
.self {
  margin-top: 28px;
  border-top: 1px solid var(--pf-border);
  padding-top: 16px;
}
.self h3 {
  margin: 0 0 12px;
  font-size: var(--pf-fs-16);
  font-weight: 600;
  color: var(--pf-text);
}
.self-form { max-width: 320px; }
</style>
