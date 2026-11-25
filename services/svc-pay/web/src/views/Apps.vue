<script setup>
import { ref, computed, onMounted } from "vue";
import { api } from "../api.js";
import { when } from "../utils.js";
import Field from "../components/Field.vue";

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
const channelNames = {
  wxpay_partner: "微信支付 · 普通服务商",
  wxpay_ecommerce: "微信支付 · 收付通",
  alipay_zft: "支付宝 · 直付通",
};
const channelName = (code) => (channelNames[code] ? `${channelNames[code]}（${code}）` : code);
const channelLabel = (c) =>
  c.ifCode === SANDBOX ? "沙箱（sandbox，不收真钱）" : `${channelNames[c.ifCode] || c.ifCode}（${c.ifCode}，${c.fundMode === "controlled" ? "归集" : "直清"}）`;
// 每个渠道是什么、什么时候选它。
const channelNotes = {
  wxpay_partner: "微信「普通服务商」模式：顾客付的钱由微信直接结算到商户自己的银行卡（直清），不经过你的账户。做微信收款的绝大多数情况选它。",
  wxpay_ecommerce: "微信「收付通」电商平台模式：资金先冻结在二级商户账户，由平台分账、确认结算、代为提现（归集）。必须先向微信单独申请开通收付通，没开通的不要选。",
  alipay_zft: "支付宝「直付通」平台模式：平台确认结算后资金才到二级商户的结算账户（归集）。必须先在支付宝开放平台签约直付通产品。",
  sandbox: "沙箱：不连任何真实渠道、不收真钱，只给下游系统联调用，没有参数要填。",
};
const wayNotes = {
  WX_JSAPI: "微信内支付：在公众号网页或小程序里调起微信支付。下单时要传付款人的 openid。",
  WX_NATIVE: "微信扫码支付：系统返回一个二维码链接，顾客用微信「扫一扫」付款。适合 PC 网站、线下收银台。",
  ALI_APP: "支付宝 APP 支付：在你自己的手机 APP 里调起支付宝付款。",
  ALI_WAP: "支付宝手机网站支付：手机浏览器里的 H5 页面跳转到支付宝付款。",
  ALI_PAGE: "支付宝电脑网站支付：PC 浏览器跳转到支付宝收银台付款。",
  ALI_QR: "支付宝扫码支付：系统返回一个二维码链接，顾客用支付宝「扫一扫」付款。",
  ALI_JSAPI: "支付宝小程序 / 生活号内支付。下单时要传付款人的 buyer_id 或 buyer_open_id。",
};
const wayLabel = (w) => (wayNotes[w] ? `${w}（${wayNotes[w].split("：")[0]}）` : w);

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
      <p class="pf-muted m-0">
        把这两项交给商家系统的开发人员：App-Id 放在每个请求的 X-Pay-App-Id 头里，签名密钥用来给请求算签名、验证我们发给它的支付结果通知。
        密钥关掉这个框之后就再也查不到了（库里只存加密后的值）；没存下来只能点列表里的「换密钥」重新发一个，旧的立即作废。
      </p>
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
      <p class="pf-muted m-0">
        渠道参数 = 调用微信 / 支付宝接口要用的账号和密钥，分两层：<b>服务商</b>一层填你自己的商户号、证书、密钥（只填一次，名下所有商户共用）；
        <b>商户</b>一层只填这个商户在渠道那边的子商户号。先用下面三个下拉框选好“给谁、配哪个渠道”，再填下面的字段。
        带 <span class="text-danger">*</span> 的是必填，缺任何一项该渠道都不能进件和下单。
      </p>
      <div class="grid grid-cols-3 gap-3">
        <Field label="给谁配置">
          <select v-model="owner.type" class="pf-input" @change="onOwnerType">
            <option value="isv">服务商（你自己的商户号、证书、密钥）</option>
            <option value="merchant">商户（它的特约 / 二级商户号）</option>
          </select>
          <template #hint>
            第一次配置先选<b>服务商</b>把必填项填完。<b>商户</b>一层在进件通过后会自动写入特约商户号，通常不用手工配；只有商户早就在你名下开过户、想跳过进件时才来这里手填。
          </template>
        </Field>
        <Field :label="owner.type === 'isv' ? '哪个服务商' : '哪个商户'" :hint="owner.type === 'isv' ? '列表里是「商户」页登记过的服务商。为空就先去「商户」页点「新建服务商」。' : '列表里是「商户」页建过的商户。为空就先去「商户」页点「新建商户」。'">
          <select v-model="owner.id" class="pf-input" @change="loadConfigs">
            <option v-for="o in owners" :key="o.id" :value="o.id">{{ o.label }}</option>
          </select>
        </Field>
        <Field label="哪个渠道" :hint="channelNotes[owner.ifCode] || ''">
          <select v-model="owner.ifCode" class="pf-input" @change="fillParamForm">
            <option v-for="c in channels" :key="c.ifCode" :value="c.ifCode">{{ channelName(c.ifCode) }}</option>
          </select>
        </Field>
      </div>
      <div>
        <span class="pf-dot" :data-tone="current ? 'ok' : 'neutral'">{{ current ? "已配置，更新于 " + when(current.updatedAt) : "这个组合还没有配置过" }}</span>
      </div>
      <p v-if="!owners.length" class="pf-muted m-0">还没有{{ owner.type === "isv" ? "服务商" : "商户" }}，先到「商户」页创建。</p>
      <template v-else-if="fields.length">
        <pf-alert v-if="isWx && owner.type === 'isv'" tone="info">
          微信的验签方式二选一，看你的服务商平台「账户中心 → API 安全」页面：有「微信支付公钥」一栏的，填下面的「微信支付公钥 ID」和「微信支付公钥」两项；
          没有这一栏的老商户号，这两项留空，先保存五个必填项，再点最下面的「同步平台证书」，系统会自动填好「平台证书序列号」和「平台证书公钥」。两种都没配时，进件和支付回调都会失败。
        </pf-alert>
        <div class="grid grid-cols-2 gap-3">
          <Field v-for="f in fields" :key="f.key" :required="f.required" :hint="f.hint">
            <template #label>
              {{ f.label }}
              <span class="pf-code">{{ f.key }}</span>
              <span v-if="f.secret" class="pf-muted">{{ stored(f.key)?.set ? "已配置，留空保持不变" : "未配置" }}</span>
            </template>
            <textarea v-if="f.multiline" v-model="paramForm[f.key]" class="pf-input" rows="4" autocomplete="off" spellcheck="false"></textarea>
            <input v-else v-model="paramForm[f.key]" class="pf-input" :type="f.secret ? 'password' : 'text'" autocomplete="off">
          </Field>
        </div>
        <p v-if="extraKeys.length" class="pf-muted m-0">另有渠道未声明的参数已配置（不回显、保存时保留）：{{ extraKeys.join("、") }}</p>
        <div class="flex gap-2">
          <pf-button variant="primary" size="sm" @click="saveParams">保存参数</pf-button>
          <pf-button v-if="isWx && owner.type === 'isv' && current" size="sm" @click="syncCert">同步平台证书</pf-button>
        </div>
        <p class="pf-muted m-0">
          保存规则：只提交你改过的项。密钥类字段（私钥、APIv3 密钥等）保存后不回显，输入框留空 = 保持原值不变；普通字段清空后保存 = 删除该项。
          <template v-if="isWx && owner.type === 'isv'">「同步平台证书」要在必填项保存之后才会出现；它用你填的证书和 APIv3 密钥向微信拉取平台证书，报“解密失败”说明 APIv3 密钥填错了。</template>
        </p>
      </template>
      <p v-else class="pf-muted m-0">{{ owner.ifCode === SANDBOX ? "沙箱渠道不需要任何参数，直接到下面给应用配通道即可。" : "该渠道没有声明这一侧的参数。" }}</p>
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

      <p class="pf-muted m-0">
        应用 = 商家那边一个要接入收款的系统（商城、收银台、小程序后台……）。每个应用有自己的 App-Id 和签名密钥，互不影响；
        一个商户可以建多个应用。建好应用后还要点「配通道」，否则它不能下单。上面的下拉框只是筛选列表，不影响新建。
      </p>

      <div v-if="showNewApp" class="flex flex-col gap-3">
        <div class="grid grid-cols-2 gap-3">
          <Field label="所属商户" required hint="这个应用替哪个商户收款。用它下的每一笔订单，钱都进这个商户在渠道那边的账户。建好后不能改到别的商户名下，选错了就停用重建。">
            <select v-model="appForm.mchNo" class="pf-input">
              <option value="">选择商户</option>
              <option v-for="m in merchants" :key="m.mchNo" :value="m.mchNo">{{ m.mchName }}（{{ m.mchNo }}）</option>
            </select>
          </Field>
          <Field label="应用名称" hint="选填，只在本列表里显示，方便你分辨是哪个系统，例如「商城收银台」「门店小程序」。App-Id 由系统自动生成，不用填。">
            <input v-model="appForm.appName" class="pf-input" placeholder="例如 商城收银台">
          </Field>
        </div>
        <p class="pf-muted m-0">点「创建」后页面顶部会显示 App-Id 和签名密钥，密钥只显示这一次，请当场复制保存。</p>
        <div class="flex gap-2">
          <pf-button size="sm" variant="primary" @click="createApp">创建</pf-button>
          <pf-button size="sm" @click="showNewApp = false">取消</pf-button>
        </div>
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
                <div class="flex flex-col gap-3">
                  <p class="pf-muted m-0">
                    支付通道 = 告诉系统「这个应用用某种支付方式下单时，走哪个渠道、按多少费率记手续费」。商家要用几种支付方式就保存几条，例如微信扫码一条、微信内支付一条。
                    走真实渠道前，先确认上面「渠道参数」里服务商一层已配齐，并且这个商户已经有特约商户号（进件完成或手工填写）。
                  </p>
                  <div class="grid grid-cols-3 gap-3">
                    <Field label="渠道" required :hint="channelNotes[passForm.ifCode] || ''">
                      <select v-model="passForm.ifCode" class="pf-input" @change="onPassChannel">
                        <option v-for="c in channels" :key="c.ifCode" :value="c.ifCode">{{ channelLabel(c) }}</option>
                      </select>
                    </Field>
                    <Field label="支付方式" required :hint="(wayNotes[passForm.wayCode] || '') + ' 商家系统下单时传的 wayCode 要和这里一致。'">
                      <select v-model="passForm.wayCode" class="pf-input">
                        <option v-for="w in passWays" :key="w" :value="w">{{ wayLabel(w) }}</option>
                      </select>
                    </Field>
                    <Field label="手续费率（%）" required hint="填百分数：0.60 表示 0.60%，也就是每收 100 元记 0.60 元手续费。请填这个商户在渠道实际签约的费率（微信常见 0.60%，以进件时选的结算规则为准）。它不会改变渠道真实扣的钱，只决定本系统在订单和台账上记多少手续费；填得和实际不一致，对账与可提现余额会对不上。范围 0–100，最多两位小数。">
                      <input v-model="passForm.ratePct" class="pf-input" type="number" min="0" max="100" step="0.01">
                    </Field>
                  </div>
                  <div><pf-button size="sm" variant="primary" @click="savePassage(a)">保存通道</pf-button></div>
                  <span class="pf-muted">同一种支付方式只能有一条通道，再次保存即覆盖原来那条（已停用的会重新启用）。改费率只影响之后新建的订单，已有订单不变。</span>
                  <div v-for="p in a.passages.filter((x) => x.state === 'active')" :key="p.wayCode" class="flex gap-2 items-center">
                    <span>{{ wayLabel(p.wayCode) }} → {{ channelName(p.ifCode) }} · {{ pct(p.rateBps) }}</span>
                    <pf-button size="sm" variant="ghost" @click="disablePassage(a, p)">停用</pf-button>
                  </div>
                  <span v-if="a.passages.some((x) => x.state === 'active')" class="pf-muted">停用某条通道后，该应用再用这种支付方式下单会被拒绝；已支付订单的退款、回调不受影响。</span>
                </div>
              </td>
            </tr>
          </template>
          <tr v-if="!apps.length">
            <td colspan="7"><span class="pf-muted">还没有应用。点上面的「新建应用」给商户建一个。</span></td>
          </tr>
        </tbody>
      </table>
      <p class="pf-muted m-0">
        「配通道」设置支付方式走哪个渠道；「换密钥」重新生成签名密钥，旧密钥立即失效，商家系统要同步换上新的，否则请求全部验签失败；
        「停用」后这个应用的所有开放 API 请求立即被拒绝，随时可以再启用。
      </p>
    </div>
  </div>
</template>
