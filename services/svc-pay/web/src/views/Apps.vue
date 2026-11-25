<script setup>
import { ref, computed, onMounted } from "vue";
import { api } from "../api.js";
import { when } from "../utils.js";

const err = ref("");
const msg = ref("");
const merchants = ref([]);
const isvs = ref([]);
const channels = ref([]);
const apps = ref([]);

// ── 应用 ──
const appFilter = ref("");
const showNewApp = ref(false);
const appForm = ref({ mchNo: "", appName: "" });
const newSecret = ref(null); // { appId, appSecret }：明文只在这一次响应里
const editing = ref(""); // 正在配通道的 appId
const passForm = ref({ ifCode: "", wayCode: "", ratePct: "0.60" });

// ── 渠道参数 ──
const owner = ref({ type: "isv", id: "", ifCode: "" });
const configs = ref([]); // 该归属已配置的渠道
const paramForm = ref({}); // key → 输入值

const qs = (o) => new URLSearchParams(o).toString();
const pct = (bps) => (Number(bps || 0) / 100).toFixed(2) + "%";
const mchName = (no) => merchants.value.find((m) => m.mchNo === no)?.mchName || no;

function fail(e) {
  err.value = e.message || String(e);
}
function reset() {
  err.value = "";
  msg.value = "";
}

const owners = computed(() =>
  owner.value.type === "isv"
    ? isvs.value.map((i) => ({ id: i.isvNo, label: `${i.isvName}（${i.isvNo}）` }))
    : merchants.value.map((m) => ({ id: m.mchNo, label: `${m.mchName}（${m.mchNo}）` })),
);
const channel = computed(() => channels.value.find((c) => c.ifCode === owner.value.ifCode));
const fields = computed(() => (owner.value.type === "isv" ? channel.value?.providerFields : channel.value?.subFields) || []);
const current = computed(() => configs.value.find((c) => c.ifCode === owner.value.ifCode));
const stored = (key) => current.value?.fields.find((f) => f.key === key);
// 渠道没有声明、但库里有的键（历史遗留）：只提示存在，不回显。
const extraKeys = computed(() => {
  const known = new Set(fields.value.map((f) => f.key));
  return (current.value?.fields || []).filter((f) => f.set && !known.has(f.key)).map((f) => f.key);
});
const isWx = computed(() => owner.value.ifCode.startsWith("wxpay_"));
const passWays = computed(() => channels.value.find((c) => c.ifCode === passForm.value.ifCode)?.ways || []);
// 沙箱渠道只在服务端 PAY_SANDBOX=on 时才会出现在渠道列表里。
const SANDBOX = "sandbox";
const sandboxOn = computed(() => channels.value.some((c) => c.ifCode === SANDBOX));
const channelLabel = (c) => (c.ifCode === SANDBOX ? "sandbox（沙箱，不收真钱）" : `${c.ifCode}（${c.fundMode === "controlled" ? "归集" : "直清"}）`);

function fillParamForm() {
  const next = {};
  for (const f of fields.value) next[f.key] = f.secret ? "" : stored(f.key)?.value || "";
  paramForm.value = next;
}

async function loadConfigs() {
  configs.value = [];
  if (owner.value.id) {
    try {
      const r = await api("GET", "/admin/channel-config?" + qs({ ownerType: owner.value.type, ownerId: owner.value.id }));
      configs.value = r.items || [];
    } catch (e) {
      fail(e);
    }
  }
  fillParamForm();
}

function onOwnerType() {
  owner.value.id = owners.value[0]?.id || "";
  loadConfigs();
}

async function saveParams() {
  reset();
  // 只提交改动：密钥字段留空 = 保持不变；非密钥字段清空 = 删除该键。
  const patch = {};
  for (const f of fields.value) {
    const v = (paramForm.value[f.key] || "").trim();
    if (f.secret) {
      if (v) patch[f.key] = v;
    } else if (v !== (stored(f.key)?.value || "")) {
      patch[f.key] = v;
    }
  }
  if (!Object.keys(patch).length) {
    msg.value = "没有改动";
    return;
  }
  const missing = fields.value.filter((f) => f.required && !(f.key in patch ? patch[f.key] : stored(f.key)?.set));
  try {
    await api("PUT", "/admin/channel-config", {
      ownerType: owner.value.type,
      ownerId: owner.value.id,
      ifCode: owner.value.ifCode,
      params: patch,
      merge: true,
    });
    msg.value = missing.length
      ? "已保存。还缺必填项：" + missing.map((f) => f.label).join("、") + "，补齐前该渠道不能下单。"
      : "渠道参数已保存";
    await loadConfigs();
  } catch (e) {
    fail(e);
  }
}

async function syncCert() {
  reset();
  try {
    const r = await api("POST", "/admin/channel-config/wxpay/platform-cert", {
      ownerType: owner.value.type,
      ownerId: owner.value.id,
      ifCode: owner.value.ifCode,
    });
    msg.value = `平台证书已同步：序列号 ${r.serial}，有效期至 ${when(r.notAfter)}`;
    await loadConfigs();
  } catch (e) {
    fail(e);
  }
}

async function loadApps() {
  const q = appFilter.value ? "?" + qs({ mchNo: appFilter.value }) : "";
  apps.value = (await api("GET", "/admin/apps" + q)).items || [];
}

async function load() {
  reset();
  try {
    const [m, i, c] = await Promise.all([api("GET", "/admin/merchants"), api("GET", "/admin/isv"), api("GET", "/admin/channels")]);
    merchants.value = m.items || [];
    isvs.value = i.items || [];
    channels.value = c.items || [];
    if (!owner.value.ifCode) owner.value.ifCode = channels.value[0]?.ifCode || "";
    if (!owner.value.id) owner.value.id = owners.value[0]?.id || "";
    if (!appForm.value.mchNo) appForm.value.mchNo = merchants.value[0]?.mchNo || "";
    await Promise.all([loadApps(), loadConfigs()]);
  } catch (e) {
    fail(e);
  }
}

async function createApp() {
  reset();
  if (!appForm.value.mchNo) return fail(new Error("先选择商户"));
  try {
    newSecret.value = await api("POST", "/admin/apps", appForm.value);
    appForm.value.appName = "";
    showNewApp.value = false;
    await loadApps();
  } catch (e) {
    fail(e);
  }
}

async function rotate(a) {
  if (!(await pfConfirm(`换发 ${a.appId} 的签名密钥？旧密钥立即失效，下游系统须同步更新。`, { danger: true }))) return;
  reset();
  try {
    newSecret.value = await api("POST", `/admin/apps/${encodeURIComponent(a.appId)}/secret`);
  } catch (e) {
    fail(e);
  }
}

async function setState(a, state) {
  if (state === "disabled" && !(await pfConfirm(`停用 ${a.appId}？该应用的开放 API 请求会立即被拒绝。`, { danger: true }))) return;
  reset();
  try {
    await api("PUT", `/admin/apps/${encodeURIComponent(a.appId)}/state`, { state });
    await loadApps();
  } catch (e) {
    fail(e);
  }
}

function editPassages(a) {
  editing.value = editing.value === a.appId ? "" : a.appId;
  const ifCode = passForm.value.ifCode || channels.value[0]?.ifCode || "";
  passForm.value = { ifCode, wayCode: channels.value.find((c) => c.ifCode === ifCode)?.ways[0] || "", ratePct: "0.60" };
}

function onPassChannel() {
  passForm.value.wayCode = passWays.value[0] || "";
}

async function savePassage(a) {
  reset();
  const rateBps = Math.round(Number(passForm.value.ratePct) * 100);
  if (!Number.isFinite(rateBps) || rateBps < 0 || rateBps > 10000) return fail(new Error("费率须在 0%–100% 之间"));
  try {
    await api("PUT", "/admin/passages", { appId: a.appId, ifCode: passForm.value.ifCode, wayCode: passForm.value.wayCode, rateBps });
    msg.value = `${a.appId} 的 ${passForm.value.wayCode} 已走 ${passForm.value.ifCode}，费率 ${pct(rateBps)}`;
    await loadApps();
  } catch (e) {
    fail(e);
  }
}

async function disablePassage(a, p) {
  reset();
  try {
    await api("DELETE", "/admin/passages?" + qs({ appId: a.appId, wayCode: p.wayCode }));
    await loadApps();
  } catch (e) {
    fail(e);
  }
}

onMounted(load);
</script>

<template>
  <div class="flex flex-col gap-3">
    <pf-alert v-if="err" tone="danger">{{ err }}</pf-alert>
    <pf-alert v-if="msg" tone="ok">{{ msg }}</pf-alert>

    <div v-if="newSecret" class="pf-card flex flex-col gap-2">
      <h2 class="m-0 text-[16px] font-semibold">应用签名密钥</h2>
      <pf-secret label="X-Pay-App-Id">{{ newSecret.appId }}</pf-secret>
      <pf-secret label="签名密钥（只显示一次，请立即复制）">{{ newSecret.appSecret }}</pf-secret>
      <div><pf-button size="sm" @click="newSecret = null">我已保存</pf-button></div>
    </div>

    <div class="pf-card flex flex-col gap-2">
      <h2 class="m-0 text-[16px] font-semibold">接入一个下游系统要配什么</h2>
      <p class="pf-muted m-0">
        ① 服务商和商户各自的渠道参数（下面第一块）；② 给商户建一个应用，拿到 App-Id 和签名密钥；③ 给应用配支付通道：哪种支付方式走哪个渠道、收多少手续费。
        另外下游调用开放 API 还需要平台的 app token，在「开放平台」控制台创建 app 并勾选 pay.* scope 获得。
      </p>
      <pf-alert v-if="sandboxOn" tone="warn">
        沙箱渠道已启用（PAY_SANDBOX=on）。把联调应用的支付通道指向 sandbox 即可不动真钱地走通下单、退款与回调，无需配置渠道参数。
        走 sandbox 的订单会显示支付成功但没有任何资金入账，不要把正式应用的通道指向它。
      </pf-alert>
    </div>

    <div class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">渠道参数</h2>
      <div class="flex gap-2 items-center flex-wrap">
        <select v-model="owner.type" class="pf-input" @change="onOwnerType">
          <option value="isv">服务商</option>
          <option value="merchant">商户</option>
        </select>
        <select v-model="owner.id" class="pf-input" @change="loadConfigs">
          <option v-for="o in owners" :key="o.id" :value="o.id">{{ o.label }}</option>
        </select>
        <select v-model="owner.ifCode" class="pf-input" @change="fillParamForm">
          <option v-for="c in channels" :key="c.ifCode" :value="c.ifCode">{{ c.ifCode }}</option>
        </select>
        <span class="pf-dot" :data-tone="current ? 'ok' : 'neutral'">{{ current ? "已配置，更新于 " + when(current.updatedAt) : "未配置" }}</span>
      </div>
      <p v-if="!owners.length" class="pf-muted m-0">还没有{{ owner.type === "isv" ? "服务商" : "商户" }}，先到「商户」页创建。</p>
      <template v-else-if="fields.length">
        <div class="grid grid-cols-2 gap-3">
          <label v-for="f in fields" :key="f.key" class="flex flex-col gap-1 text-[13px]">
            <span>
              {{ f.label }}<span v-if="f.required"> *</span>
              <span class="pf-code">{{ f.key }}</span>
              <span v-if="f.secret" class="pf-muted">{{ stored(f.key)?.set ? "已配置，留空保持不变" : "未配置" }}</span>
            </span>
            <textarea v-if="f.multiline" v-model="paramForm[f.key]" class="pf-input" rows="4" autocomplete="off" spellcheck="false"></textarea>
            <input v-else v-model="paramForm[f.key]" class="pf-input" :type="f.secret ? 'password' : 'text'" autocomplete="off">
            <span v-if="f.hint" class="pf-muted text-[12px]">{{ f.hint }}</span>
          </label>
        </div>
        <p v-if="extraKeys.length" class="pf-muted m-0">另有渠道未声明的参数已配置（不回显、保存时保留）：{{ extraKeys.join("、") }}</p>
        <div class="flex gap-2">
          <pf-button variant="primary" size="sm" @click="saveParams">保存参数</pf-button>
          <pf-button v-if="isWx && owner.type === 'isv' && current" size="sm" @click="syncCert">同步平台证书</pf-button>
        </div>
      </template>
      <p v-else class="pf-muted m-0">该渠道没有声明这一侧的参数。</p>
    </div>

    <div class="pf-card flex flex-col gap-3">
      <div class="flex gap-2 items-center flex-wrap">
        <h2 class="m-0 text-[16px] font-semibold">应用与支付通道</h2>
        <select v-model="appFilter" class="pf-input" @change="loadApps().catch(fail)">
          <option value="">全部商户</option>
          <option v-for="m in merchants" :key="m.mchNo" :value="m.mchNo">{{ m.mchName }}（{{ m.mchNo }}）</option>
        </select>
        <pf-button size="sm" variant="primary" @click="showNewApp = !showNewApp">新建应用</pf-button>
      </div>

      <div v-if="showNewApp" class="flex gap-2 items-center flex-wrap">
        <select v-model="appForm.mchNo" class="pf-input">
          <option value="">选择商户</option>
          <option v-for="m in merchants" :key="m.mchNo" :value="m.mchNo">{{ m.mchName }}（{{ m.mchNo }}）</option>
        </select>
        <input v-model="appForm.appName" class="pf-input" placeholder="应用名称，例如 商城收银台">
        <pf-button size="sm" variant="primary" @click="createApp">创建</pf-button>
        <pf-button size="sm" @click="showNewApp = false">取消</pf-button>
      </div>

      <table class="pf-table">
        <thead>
          <tr><th>App-Id</th><th>名称</th><th>商户</th><th>支付通道</th><th>状态</th><th>创建时间</th><th></th></tr>
        </thead>
        <tbody>
          <template v-for="a in apps" :key="a.appId">
            <tr>
              <td><span class="pf-code">{{ a.appId }}</span></td>
              <td>{{ a.appName || "—" }}</td>
              <td>{{ mchName(a.mchNo) }}</td>
              <td>
                <div class="flex flex-col gap-1">
                  <span v-for="p in a.passages" :key="p.wayCode" class="pf-dot" :data-tone="p.state !== 'active' ? 'neutral' : p.ifCode === SANDBOX ? 'warn' : 'ok'">
                    {{ p.wayCode }} → {{ p.ifCode }} · {{ pct(p.rateBps) }}{{ p.state === "active" ? "" : "（已停用）" }}
                  </span>
                  <span v-if="!a.passages.length" class="pf-muted">未配置，不能下单</span>
                </div>
              </td>
              <td><span class="pf-dot" :data-tone="a.state === 'active' ? 'ok' : 'danger'">{{ a.state === "active" ? "启用" : "停用" }}</span></td>
              <td>{{ when(a.createdAt) }}</td>
              <td>
                <div class="flex gap-2 flex-wrap">
                  <pf-button size="sm" @click="editPassages(a)">{{ editing === a.appId ? "收起" : "配通道" }}</pf-button>
                  <pf-button size="sm" variant="danger" @click="rotate(a)">换密钥</pf-button>
                  <pf-button v-if="a.state === 'active'" size="sm" variant="ghost" @click="setState(a, 'disabled')">停用</pf-button>
                  <pf-button v-else size="sm" variant="ghost" @click="setState(a, 'active')">启用</pf-button>
                </div>
              </td>
            </tr>
            <tr v-if="editing === a.appId">
              <td colspan="7">
                <div class="flex flex-col gap-2">
                  <div class="flex gap-2 items-center flex-wrap">
                    <select v-model="passForm.ifCode" class="pf-input" @change="onPassChannel">
                      <option v-for="c in channels" :key="c.ifCode" :value="c.ifCode">{{ channelLabel(c) }}</option>
                    </select>
                    <select v-model="passForm.wayCode" class="pf-input">
                      <option v-for="w in passWays" :key="w" :value="w">{{ w }}</option>
                    </select>
                    <label class="inline-flex items-center gap-1.5 text-[13px]">费率 %
                      <input v-model="passForm.ratePct" class="pf-input" type="number" min="0" max="100" step="0.01">
                    </label>
                    <pf-button size="sm" variant="primary" @click="savePassage(a)">保存通道</pf-button>
                  </div>
                  <span class="pf-muted text-[12px]">同一种支付方式只有一条通道，再次保存即覆盖（并重新启用）。费率只影响之后创建的订单。</span>
                  <div v-for="p in a.passages.filter((x) => x.state === 'active')" :key="p.wayCode" class="flex gap-2 items-center">
                    <span>{{ p.wayCode }} → {{ p.ifCode }} · {{ pct(p.rateBps) }}</span>
                    <pf-button size="sm" variant="ghost" @click="disablePassage(a, p)">停用</pf-button>
                  </div>
                </div>
              </td>
            </tr>
          </template>
          <tr v-if="!apps.length">
            <td colspan="7"><span class="pf-muted">还没有应用</span></td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
