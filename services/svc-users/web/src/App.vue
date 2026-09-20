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
  if (!confirm(`重置 ${u.username} 的密码？旧密码立即失效。`)) return;
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
  <div class="wrap">
    <header><b>PlatFarm 用户管理</b><button v-if="isAdmin" @click="load">刷新</button>
      <button v-if="isAdmin" class="primary" @click="creating = true">＋ 新建用户</button></header>
    <p v-if="err" class="err">{{ err }}</p>
    <p v-if="ok" class="ok">{{ ok }}</p>

    <div v-if="newCred" class="secret">
      <b>初始密码（只显示一次，请立即复制）</b>
      <div>用户 = <code>{{ newCred.username || ('#' + newCred.id) }}</code></div>
      <div>密码 = <code>{{ newCred.password }}</code></div>
      <button @click="newCred = null">我已保存</button>
    </div>

    <div v-if="creating" class="editor">
      <input v-model="form.username" placeholder="用户名" />
      <select v-model="form.role"><option value="user">user</option><option value="admin">admin</option></select>
      <div><button class="primary" @click="create">创建</button><button @click="creating = false">取消</button></div>
    </div>

    <div v-if="isAdmin" class="bar">
      <input v-model="q" @input="onSearch" placeholder="搜索用户名…" class="search" />
      <span class="muted">共 {{ total }} 个 · 第 {{ total ? offset + 1 : 0 }}–{{ offset + users.length }}</span>
      <button @click="prevPage" :disabled="offset === 0">上一页</button>
      <button @click="nextPage" :disabled="offset + limit >= total">下一页</button>
    </div>
    <table v-if="isAdmin">
      <thead><tr><th>ID</th><th>用户名</th><th>平台角色</th><th>状态</th><th>操作</th></tr></thead>
      <tbody>
        <tr v-for="u in users" :key="u.id">
          <td>{{ u.id }}</td><td>{{ u.username }}</td>
          <td>
            <select :value="u.role" @change="e => setRole(u, e.target.value)">
              <option value="user">user</option><option value="admin">admin</option>
            </select>
          </td>
          <td><span class="badge" :data-s="u.status">{{ u.status === 1 ? '启用' : '停用' }}</span></td>
          <td class="ops">
            <button v-if="u.status === 1" @click="setStatus(u, 0)">停用</button>
            <button v-else @click="setStatus(u, 1)">启用</button>
            <button @click="reset(u)">重置密码</button>
          </td>
        </tr>
        <tr v-if="!users.length"><td colspan="5" class="muted">（无匹配用户）</td></tr>
      </tbody>
    </table>
    <p v-else class="muted">你不是平台管理员，仅可修改自己的密码。</p>

    <section class="self">
      <h3>修改我的密码</h3>
      <input v-model="pw.old" type="password" placeholder="旧密码" />
      <input v-model="pw.neo" type="password" placeholder="新密码" />
      <button class="primary" @click="changeOwn">修改密码</button>
      <span class="muted">修改后当前登录失效，需重新登录。</span>
    </section>
  </div>
</template>

<style>
*{box-sizing:border-box}body{margin:0;font:14px/1.6 system-ui,sans-serif;background:#0d1117;color:#c9d1d9}
.wrap{max-width:900px;margin:0 auto;padding:16px 20px}
header{display:flex;align-items:center;gap:12px;border-bottom:1px solid #21262d;padding-bottom:12px}
header b{color:#58a6ff;flex:1}
.err{background:#3d1418;border:1px solid #f85149;color:#ffb4ab;padding:8px 12px;border-radius:6px}
.ok{background:#1c2b1c;border:1px solid #2ea043;color:#3fb950;padding:8px 12px;border-radius:6px}
.secret{background:#1c2b1c;border:1px solid #2ea043;border-radius:8px;padding:14px;margin:14px 0}
.secret code{color:#3fb950;user-select:all}
.editor{border:1px solid #21262d;border-radius:8px;padding:14px;margin:14px 0;background:#161b22}
input,select{background:#0d1117;border:1px solid #30363d;border-radius:6px;color:#c9d1d9;padding:7px 10px;font:inherit;margin-bottom:8px}
button{background:#21262d;color:#c9d1d9;border:1px solid #30363d;border-radius:6px;padding:5px 12px;cursor:pointer;font:inherit;margin-left:6px}
button:hover{border-color:#58a6ff}button.primary{background:#238636;border-color:#2ea043;color:#fff}
table{width:100%;border-collapse:collapse;margin-top:14px}
th,td{text-align:left;padding:8px 10px;border-bottom:1px solid #21262d}
th{color:#8b949e;font-weight:normal}
.badge{padding:1px 8px;border-radius:10px;font-size:12px;border:1px solid #30363d}
.badge[data-s="1"]{color:#3fb950;border-color:#238636}.badge[data-s="0"]{color:#8b949e}
.ops{white-space:nowrap}.muted{color:#8b949e}code{background:#161b22;padding:1px 5px;border-radius:4px}
.self{margin-top:28px;border-top:1px solid #21262d;padding-top:16px}
.self input{width:220px}
.bar{display:flex;align-items:center;gap:10px;margin-top:14px}
.search{width:240px;margin:0}
button:disabled{opacity:.4;cursor:not-allowed}
</style>
