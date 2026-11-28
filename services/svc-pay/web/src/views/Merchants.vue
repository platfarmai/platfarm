<script setup>
import { ref, computed, onMounted } from "vue";
import { api, viewAs, copyText } from "../api.js";
import Field from "../components/Field.vue";

const err = ref("");
const msg = ref("");
const portalOf = ref(null); // 正在管理商户登录的商户：{ mchNo, mchName, username }
const issued = ref(null); // 刚签发的一次性凭据：{ seq, title, mchNo, mchName, username, password?, appKey?, appSecret?, payApp?, partial?, error?, warning? }
const portalBusy = ref("");
let issuedSeq = 0;
const merchants = ref([]);
const isvs = ref([]);
const showISV = ref(false);
const showMch = ref(false);
const isvForm = ref({ isvNo: "", isvName: "", fundMode: "direct", rebateRateBps: 0 });
const mchForm = ref({ mchNo: "", mchName: "", mchShortName: "", mchType: 2, isvNo: "", contactName: "", contactPhone: "" });
const editing = ref(null); // 正在编辑的商户：{ mchNo, mchName, mchShortName, mchType, isvNo, contactName, contactPhone }

const isvName = (no) => isvs.value.find((i) => i.isvNo === no)?.isvName || "";
const channelShort = { wxpay_partner: "微信特约", wxpay_ecommerce: "微信收付通", alipay_zft: "支付宝直付通" };
const rebatePct = computed(() => (Number(isvForm.value.rebateRateBps || 0) / 100).toFixed(2));

function reset() {
  err.value = "";
  msg.value = "";
}
function fail(e) {
  err.value = e.message || String(e);
}

async function load() {
  err.value = "";
  try {
    const [m, i] = await Promise.all([api("GET", "/admin/merchants"), api("GET", "/admin/isv")]);
    merchants.value = m.items || [];
    isvs.value = i.items || [];
    if (!mchForm.value.isvNo && isvs.value.length) mchForm.value.isvNo = isvs.value[0].isvNo;
  } catch (e) {
    fail(e);
  }
}

function openISV(i) {
  isvForm.value = i
    ? { isvNo: i.isvNo, isvName: i.isvName, fundMode: i.fundMode, rebateRateBps: i.rebateRateBps }
    : { isvNo: "", isvName: "", fundMode: "direct", rebateRateBps: 0 };
  showISV.value = true;
  showMch.value = false;
  editing.value = null;
  portalOf.value = null;
}

async function createISV() {
  reset();
  try {
    const r = await api("POST", "/admin/isv", {
      ...isvForm.value,
      rebateRateBps: Number(isvForm.value.rebateRateBps) || 0,
    });
    msg.value = "服务商已保存：" + r.isvNo + "。下一步到「应用·通道」页的渠道参数里填它的微信商户号、证书和密钥。";
    isvForm.value = { isvNo: "", isvName: "", fundMode: "direct", rebateRateBps: 0 };
    showISV.value = false;
    await load();
  } catch (e) {
    fail(e);
  }
}

async function createMerchant() {
  reset();
  if (!mchForm.value.mchName.trim()) return fail(new Error("商户名称必填"));
  if (Number(mchForm.value.mchType) === 2 && !mchForm.value.isvNo) return fail(new Error("特约商户必须选择服务商（没有的话先点「新建服务商」登记你自己）"));
  try {
    const r = await api("POST", "/admin/merchants", { ...mchForm.value, mchType: Number(mchForm.value.mchType) });
    msg.value = "商户已创建：" + r.mchNo + "。下一步：到「进件审核」新建进件；已经有微信特约商户号的，直接到「应用·通道」填它的渠道参数。";
    mchForm.value = { mchNo: "", mchName: "", mchShortName: "", mchType: 2, isvNo: mchForm.value.isvNo, contactName: "", contactPhone: "" };
    showMch.value = false;
    await load();
  } catch (e) {
    fail(e);
  }
}

function startEdit(m) {
  reset();
  editing.value = {
    mchNo: m.mchNo,
    mchName: m.mchName,
    mchShortName: m.mchShortName || "",
    mchType: m.mchType,
    isvNo: m.isvNo || "",
    contactName: m.contactName || "",
    contactPhone: m.contactPhone || "",
  };
  showMch.value = false;
  showISV.value = false;
  portalOf.value = null;
}

async function saveEdit() {
  reset();
  const e = editing.value;
  if (!e.mchName.trim()) return fail(new Error("商户名称必填"));
  try {
    await api("PUT", "/admin/merchants/" + encodeURIComponent(e.mchNo), {
      mchName: e.mchName,
      mchShortName: e.mchShortName,
      mchType: Number(e.mchType),
      isvNo: e.isvNo,
      contactName: e.contactName,
      contactPhone: e.contactPhone,
    });
    msg.value = "商户 " + e.mchNo + " 已更新";
    editing.value = null;
    await load();
  } catch (ex) {
    fail(ex);
  }
}

async function setState(m, state) {
  if (state === "disabled" && !(await pfConfirm(`停用商户 ${m.mchName}（${m.mchNo}）？它名下所有应用的下单、退款请求会立即被拒绝，已支付订单的回调和对账不受影响。`, { danger: true }))) return;
  reset();
  try {
    await api("PUT", "/admin/merchants/" + encodeURIComponent(m.mchNo), { state });
    msg.value = `商户 ${m.mchNo} 已${state === "active" ? "启用" : "停用"}`;
    await load();
  } catch (e) {
    fail(e);
  }
}

// ── 商户登录（商户中心）──────────────────────────────────────
const portalMch = computed(() => merchants.value.find((m) => m.mchNo === portalOf.value?.mchNo));
const loginUrl = window.location.origin + "/";

function openPortal(m) {
  reset();
  // 登录名默认用商户号小写：全平台唯一、不会撞名；运营可以改成商家好记的。
  portalOf.value = { mchNo: m.mchNo, mchName: m.mchName, username: m.portal?.username || m.mchNo.toLowerCase() };
  showMch.value = false;
  showISV.value = false;
  editing.value = null;
}

function seeAs(mchNo) {
  viewAs.value = mchNo; // App.vue 监听它，切到商户中心页签
}

function showIssued(title, r) {
  issued.value = { seq: ++issuedSeq, title, ...r };
  window.scrollTo({ top: 0, behavior: "smooth" });
}

async function portalCall(action, fn) {
  if (portalBusy.value) return;
  reset();
  portalBusy.value = action;
  try {
    await fn();
    await load();
  } catch (e) {
    fail(e);
  } finally {
    portalBusy.value = "";
  }
}

const provision = () =>
  portalCall("provision", async () => {
    const p = portalOf.value;
    const username = p.username.trim();
    if (!portalMch.value?.portal && !/^[A-Za-z0-9][A-Za-z0-9._-]{2,31}$/.test(username)) {
      throw new Error("登录名需为 3–32 位字母、数字或 . _ -，且以字母或数字开头");
    }
    const r = await api("POST", "/admin/merchants/" + encodeURIComponent(p.mchNo) + "/portal", { username });
    showIssued(r.partial ? "商户登录只开通了一半" : "商户登录已开通", r);
  });

const resetPassword = () =>
  portalCall("password", async () => {
    const p = portalOf.value;
    if (!(await pfConfirm(`重置 ${p.mchName} 的商户登录密码？原密码立即失效，已登录的会话会被踢下线。`, { danger: true }))) return;
    const r = await api("POST", "/admin/merchants/" + encodeURIComponent(p.mchNo) + "/portal/password");
    showIssued("登录密码已重置", r);
  });

const resetAppSecret = () =>
  portalCall("appSecret", async () => {
    const p = portalOf.value;
    const sure = await pfConfirm(
      `给 ${p.mchName} 换发开放平台 appSecret？旧 appSecret 立即失效：商家的业务系统在换上新的之前换不到新 accessToken（已经换到手的最长还能用 1 小时），之后下单、退款都会被网关拒绝。`,
      { danger: true },
    );
    if (!sure) return;
    const r = await api("POST", "/admin/merchants/" + encodeURIComponent(p.mchNo) + "/portal/app-secret");
    showIssued("开放平台 appSecret 已换发", r);
  });

const setPortalState = (state) =>
  portalCall("state", async () => {
    const p = portalOf.value;
    if (
      state === "disabled" &&
      !(await pfConfirm(
        `停用 ${p.mchName} 的商户登录与接口？这个账号将进不了商户中心，它的 appKey 同时停用：商家业务系统立刻不能再下单、退款、查单（已经换到手的 accessToken 也会被拒绝）。已付款订单的入账和回调不受影响。`,
        { danger: true },
      ))
    )
      return;
    await api("PUT", "/admin/merchants/" + encodeURIComponent(p.mchNo) + "/portal/state", { state });
    msg.value = `商户 ${p.mchNo} 的商户登录与 appKey 已${state === "active" ? "启用" : "停用"}`;
  });

// 把这次签发的凭据拼成一段可以直接发给商家的文字。
function issuedText(i) {
  const lines = [`【${i.mchName}】支付接入凭据（商户号 ${i.mchNo}）`, "", `商户中心登录地址：${loginUrl}`, `登录账号：${i.username}`];
  if (i.password) lines.push(`初始密码：${i.password}（首次登录后请在「概览·接入凭据」页修改）`);
  if (i.appKey) lines.push("", "开放平台凭据（换取 accessToken）", `appKey：${i.appKey}`);
  if (i.appSecret) lines.push(`appSecret：${i.appSecret}`);
  if (i.payApp) lines.push("", "应用签名密钥（给请求签名、验证结果通知）", `X-Pay-App-Id：${i.payApp.appId}`, `签名密钥：${i.payApp.appSecret}`);
  lines.push("", `接入文档：${loginUrl}docs`, "以上密钥只发放这一次，请妥善保存，不要通过群聊等公开渠道转发。");
  return lines.join("\n");
}

onMounted(load);
</script>

<template>
  <div class="flex flex-col gap-3">
    <pf-alert v-if="err" tone="danger">{{ err }}</pf-alert>
    <pf-alert v-if="msg" tone="ok">{{ msg }}</pf-alert>

    <div class="pf-card flex flex-col gap-2">
      <h2 class="m-0 text-[16px] font-semibold">从零到能收款，按这个顺序做</h2>
      <p class="pf-muted m-0">
        ① 本页「新建服务商」登记你自己的微信支付服务商（只做一次）→ ② 「应用·通道」页给服务商填渠道参数（商户号、证书、密钥）→
        ③ 本页「新建商户」登记要帮它收款的商家 → ④ 「进件审核」新建进件，把商家资料报给微信，审核通过并签约后自动拿到特约商户号 →
        ⑤ 「应用·通道」页给商户建应用、配支付通道 → ⑥ 把 App-Id 和签名密钥交给商家的系统，开始下单。
      </p>
      <p class="pf-muted m-0">本页登记的名称、联系人只是平台内部台账，不会发给微信；发给微信审核的资料在「进件审核」里单独填写。</p>
    </div>

    <div v-if="issued" :key="issued.seq" class="pf-card flex flex-col gap-2">
      <h2 class="m-0 text-[16px] font-semibold">{{ issued.title }}：{{ issued.mchName }}</h2>
      <pf-alert v-if="issued.partial" tone="warn">{{ issued.error }}</pf-alert>
      <pf-alert v-if="issued.warning" tone="warn">{{ issued.warning }}</pf-alert>
      <pf-secret label="商户中心登录地址">{{ loginUrl }}</pf-secret>
      <pf-secret label="登录账号">{{ issued.username }}</pf-secret>
      <pf-secret v-if="issued.password" label="初始密码（只显示这一次）">{{ issued.password }}</pf-secret>
      <pf-secret v-if="issued.appKey" label="开放平台 appKey">{{ issued.appKey }}</pf-secret>
      <pf-secret v-if="issued.appSecret" label="开放平台 appSecret（只显示这一次）">{{ issued.appSecret }}</pf-secret>
      <template v-if="issued.payApp">
        <pf-secret label="默认应用 X-Pay-App-Id">{{ issued.payApp.appId }}</pf-secret>
        <pf-secret label="默认应用签名密钥（只显示这一次）">{{ issued.payApp.appSecret }}</pf-secret>
      </template>
      <p class="pf-muted m-0">
        平台只保存这些密码 / 密钥加密或哈希后的值，关掉这个框之后任何人都查不到明文，没存下来只能重置。请现在就交给商家：
        登录账号和密码给商家负责人登录商户中心；appKey / appSecret 和签名密钥给商家的开发人员配置到业务系统里。
        <span v-if="issued.payApp">这个商户原来没有应用，已顺手建了一个「默认应用」，还需要到「应用·通道」页给它配支付通道才能下单。</span>
        <span v-else-if="issued.appSecret && issued.password">这个商户已有的应用没有动，它们的签名密钥保持不变（商家登录后可以自己换发）。</span>
      </p>
      <div class="flex gap-2">
        <pf-button size="sm" variant="primary" @click="copyText(issuedText(issued), '全部凭据')">复制全部，发给商家</pf-button>
        <pf-button size="sm" @click="issued = null">我已保存，关闭</pf-button>
      </div>
    </div>

    <div class="flex gap-2">
      <pf-button size="sm" variant="primary" @click="showMch = !showMch; showISV = false; editing = null; portalOf = null">新建商户</pf-button>
      <pf-button size="sm" @click="showISV ? (showISV = false) : openISV(null)">新建服务商</pf-button>
    </div>

    <div v-if="showISV" class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">登记你的微信支付服务商</h2>
      <p class="pf-muted m-0">
        这里不是去微信再申请一个服务商，而是把你已有的服务商身份登记到本系统：后面所有特约商户都挂在它下面，用它的证书和密钥调微信。
        一般只需要登记一个。本页只起名字和编号，真正的微信商户号、证书、密钥保存后到「应用·通道 → 渠道参数」里填。
      </p>
      <div class="grid grid-cols-2 gap-3">
        <Field label="服务商号" required hint="本系统内部的编号，自己起，例如 ISV001。只能填一次，之后作为唯一标识；填一个已存在的编号再保存 = 修改那个服务商的名称、资金模式和返佣。它不是微信的商户号，微信商户号在渠道参数里填。">
          <input v-model="isvForm.isvNo" class="pf-input" placeholder="例如 ISV001">
        </Field>
        <Field label="名称" required hint="你的服务商公司名称，只在本系统的下拉框和列表里显示，方便辨认，例如「三润科技（微信服务商）」。">
          <input v-model="isvForm.isvName" class="pf-input" placeholder="例如 三润科技（微信服务商）">
        </Field>
        <Field label="资金模式" required>
          <select v-model="isvForm.fundMode" class="pf-input">
            <option value="direct">直清（渠道直接结算给商户）</option>
            <option value="controlled">归集（先进平台账户再分账）</option>
          </select>
          <template #hint>
            微信「普通服务商」选<b>直清</b>：顾客付的钱由微信直接结算到商户自己的银行卡，不经过你的账户，你只赚返佣。
            只有开通了微信「收付通（电商平台）」或支付宝「直付通」、需要平台先收款再分给各商家的，才选归集。拿不准就选直清。
          </template>
        </Field>
        <Field label="返佣费率（万分位，0–1000）" required>
          <input v-model="isvForm.rebateRateBps" class="pf-input" type="number" min="0" max="1000">
          <template #hint>
            微信按交易额返给你（服务商）的技术服务费比例，单位是万分之一：填 20 = 0.20%，填 100 = 1%。当前填的是 {{ rebatePct }}%。
            系统用它在每笔订单上估算你的返佣收入，代理商分润也从这里面分；没有返佣或不清楚就填 0，以后可以改，只影响之后的新订单。
          </template>
        </Field>
      </div>
      <div class="flex gap-2">
        <pf-button variant="primary" size="sm" @click="createISV">保存服务商</pf-button>
        <pf-button size="sm" @click="showISV = false">取消</pf-button>
      </div>
    </div>

    <div v-if="showMch" class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">新建商户</h2>
      <p class="pf-muted m-0">
        商户 = 你要帮它收款的商家。这里先在本系统建一个档案（内部台账），建好后才能给它做进件、建应用、配通道。填错了没关系，建好后列表里可以随时「编辑」。
      </p>
      <div class="grid grid-cols-2 gap-3">
        <Field label="商户名称" required hint="本系统内部显示用，不会发给微信，所以不强制，但建议直接填营业执照上的公司全称（个体户填执照上的字号），以后对账、查订单不容易混。真正报给微信的名称在「进件审核」里单独填，那里必须和执照一字不差。">
          <input v-model="mchForm.mchName" class="pf-input" placeholder="例如 成都某某商贸有限公司">
        </Field>
        <Field label="简称" hint="选填。商家的常用叫法或门店名，例如「某某咖啡春熙路店」，只用于本系统列表展示。">
          <input v-model="mchForm.mchShortName" class="pf-input" placeholder="例如 某某咖啡">
        </Field>
        <Field label="商户号" hint="选填。本系统内部的商户编号，留空自动生成 M 开头的 21 位编号（推荐）；也可以自己起，例如 M0001，建好后不能改。它不是微信的特约商户号——那个要等进件通过后微信才会分配。">
          <input v-model="mchForm.mchNo" class="pf-input" placeholder="留空自动生成">
        </Field>
        <Field label="类型" required>
          <select v-model="mchForm.mchType" class="pf-input">
            <option :value="2">特约（挂在服务商名下，用于进件）</option>
            <option :value="1">普通（商家自己有微信商户号）</option>
          </select>
          <template #hint>
            绝大多数情况选<b>特约</b>：商家没有自己的微信支付商户号，由你作为服务商替它进件、开户、收款。
            「普通」只用于商家自己已经直接和微信签约、有自己的商户号和证书的情况，这种商户不走进件。
          </template>
        </Field>
        <Field label="服务商" :required="Number(mchForm.mchType) === 2" hint="这个商户挂在哪个服务商名下，进件和收款都用该服务商的微信凭据。特约商户必选；列表为空说明还没登记服务商，先点上面的「新建服务商」。一旦商户有了订单或进件，就不能再换服务商。">
          <select v-model="mchForm.isvNo" class="pf-input">
            <option value="">{{ Number(mchForm.mchType) === 2 ? "请选择" : "不挂服务商" }}</option>
            <option v-for="i in isvs" :key="i.isvNo" :value="i.isvNo">{{ i.isvName }}（{{ i.isvNo }}）</option>
          </select>
        </Field>
        <Field label="联系人" hint="选填。商家那边平时和你对接的人的姓名，内部备忘用，不发给微信。">
          <input v-model="mchForm.contactName" class="pf-input" placeholder="例如 张三">
        </Field>
        <Field label="联系电话" hint="选填。上面这位联系人的手机号或座机，内部备忘用。">
          <input v-model="mchForm.contactPhone" class="pf-input" placeholder="例如 13800000000">
        </Field>
      </div>
      <div class="flex gap-2">
        <pf-button variant="primary" size="sm" @click="createMerchant">保存商户</pf-button>
        <pf-button size="sm" @click="showMch = false">取消</pf-button>
      </div>
    </div>

    <div v-if="editing" class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">编辑商户 <span class="pf-code">{{ editing.mchNo }}</span></h2>
      <p class="pf-muted m-0">
        名称、简称、联系人是内部台账，随时可改，不影响已提交给微信的进件资料。商户号建好后不能改。
        服务商和类型决定钱走谁的通道：商户一旦有了订单、未取消的进件或已归属代理商，这两项就改不了（保存时会提示原因）。
      </p>
      <div class="grid grid-cols-2 gap-3">
        <Field label="商户名称" required hint="内部显示用，建议填营业执照上的公司全称。改这里不会改动已提交给微信的进件名称。">
          <input v-model="editing.mchName" class="pf-input">
        </Field>
        <Field label="简称" hint="选填。商家的常用叫法或门店名，只用于本系统展示。">
          <input v-model="editing.mchShortName" class="pf-input">
        </Field>
        <Field label="类型" required hint="特约 = 由你替它进件收款；普通 = 商家自己有微信商户号。投入使用后不能改。">
          <select v-model="editing.mchType" class="pf-input">
            <option :value="2">特约（挂在服务商名下，用于进件）</option>
            <option :value="1">普通（商家自己有微信商户号）</option>
          </select>
        </Field>
        <Field label="服务商" hint="建商户时选错了服务商，可以趁还没进件、没订单时在这里改过来。">
          <select v-model="editing.isvNo" class="pf-input">
            <option value="">不挂服务商</option>
            <option v-for="i in isvs" :key="i.isvNo" :value="i.isvNo">{{ i.isvName }}（{{ i.isvNo }}）</option>
          </select>
        </Field>
        <Field label="联系人" hint="选填。商家那边的日常对接人。">
          <input v-model="editing.contactName" class="pf-input">
        </Field>
        <Field label="联系电话" hint="选填。对接人的手机号或座机。">
          <input v-model="editing.contactPhone" class="pf-input">
        </Field>
      </div>
      <div class="flex gap-2">
        <pf-button variant="primary" size="sm" @click="saveEdit">保存修改</pf-button>
        <pf-button size="sm" @click="editing = null">取消</pf-button>
      </div>
    </div>

    <div v-if="portalOf && portalMch" class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">商户登录：{{ portalMch.mchName }} <span class="pf-code">{{ portalMch.mchNo }}</span></h2>

      <template v-if="!portalMch.portal">
        <p class="pf-muted m-0">
          开通后商家可以用自己的账号登录「商户中心」：查看商户号、appKey、App-Id 等接入凭据，换发签名密钥，查订单，并用「接口调试」页一键把下单 / 退款 / 查账接口跑通。
          商家只看得到自己这一个商户的数据，看不到服务商、其它商户和运营功能。
        </p>
        <p class="pf-muted m-0">
          点「一键开通」会依次完成：① 创建登录账号（生成初始密码）→ ② 签发开放平台 appKey / appSecret，并勾好下单、退款、查账权限 →
          ③ 商户还没有应用时创建一个「默认应用」。密码和密钥只在开通成功的那一刻显示一次。
        </p>
        <div class="grid grid-cols-2 gap-3">
          <Field label="登录名" required hint="商家登录商户中心用的账号，3–32 位字母、数字或 . _ -，全平台不能重复，开通后不能改。默认是商户号的小写，可以改成商家好记的，例如公司拼音缩写。">
            <input v-model="portalOf.username" class="pf-input" autocomplete="off" spellcheck="false">
          </Field>
        </div>
        <div class="flex gap-2">
          <pf-button variant="primary" size="sm" :loading="portalBusy === 'provision'" @click="provision">一键开通</pf-button>
          <pf-button size="sm" @click="portalOf = null">取消</pf-button>
        </div>
      </template>

      <template v-else>
        <dl class="pf-kv">
          <dt>登录账号</dt>
          <dd>
            <span class="pf-code">{{ portalMch.portal.username }}</span>
            <span class="pf-dot" :data-tone="portalMch.portal.state === 'active' ? 'ok' : 'danger'">{{ portalMch.portal.state === "active" ? "可登录" : "已停用" }}</span>
          </dd>
          <dt>开放平台 appKey</dt>
          <dd>
            <span v-if="portalMch.portal.appKey" class="pf-code">{{ portalMch.portal.appKey }}</span>
            <span v-else class="pf-muted">还没有签发（上次开通中途失败）</span>
          </dd>
          <dt>登录地址</dt>
          <dd><span class="pf-code">{{ loginUrl }}</span></dd>
        </dl>
        <pf-alert v-if="!portalMch.portal.appKey" tone="warn">
          登录账号已经建好，但开放平台 appKey 没签发成功，商家拿不到 accessToken、调不了接口。点「补发 appKey」把剩下的步骤做完（不会重建账号、不会改密码）。
        </pf-alert>
        <div class="flex gap-2 flex-wrap">
          <pf-button v-if="!portalMch.portal.appKey" variant="primary" size="sm" :loading="portalBusy === 'provision'" @click="provision">补发 appKey</pf-button>
          <pf-button size="sm" @click="seeAs(portalMch.mchNo)">以商户视角查看</pf-button>
          <pf-button size="sm" :loading="portalBusy === 'password'" @click="resetPassword">重置登录密码</pf-button>
          <pf-button v-if="portalMch.portal.appKey" size="sm" :loading="portalBusy === 'appSecret'" @click="resetAppSecret">换发 appSecret</pf-button>
          <pf-button v-if="portalMch.portal.state === 'active'" size="sm" variant="ghost" :loading="portalBusy === 'state'" @click="setPortalState('disabled')">停用登录与接口</pf-button>
          <pf-button v-else size="sm" variant="primary" :loading="portalBusy === 'state'" @click="setPortalState('active')">启用登录与接口</pf-button>
          <pf-button size="sm" variant="ghost" @click="portalOf = null">收起</pf-button>
        </div>
        <p class="pf-muted m-0">
          商家忘了密码点「重置登录密码」；appSecret 遗失或泄露点「换发 appSecret」，新值都只显示一次。应用的签名密钥由商家登录后自己换发，也可以在「应用·通道」页替它换。
          「停用登录与接口」会同时关掉商户中心登录和这个商户的 appKey：商家进不了后台，它的业务系统也立刻不能下单、退款、查单；已经付款的订单照常入账、照常回调。开通、重置、换发、启停都需要平台管理员身份。
        </p>
      </template>
    </div>

    <div class="pf-card">
      <h2 class="m-0 mb-2 text-[14px] font-semibold">服务商</h2>
      <table class="pf-table">
        <thead>
          <tr><th>服务商号</th><th>名称</th><th>资金模式</th><th>返佣</th><th>状态</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="i in isvs" :key="i.isvNo">
            <td><span class="pf-code">{{ i.isvNo }}</span></td>
            <td>{{ i.isvName }}</td>
            <td>{{ i.fundMode === "controlled" ? "归集" : "直清" }}</td>
            <td>{{ (i.rebateRateBps / 100).toFixed(2) }}%</td>
            <td><span class="pf-dot" :data-tone="i.state === 'active' ? 'ok' : 'danger'">{{ i.state === "active" ? "启用" : "停用" }}</span></td>
            <td><pf-button size="sm" @click="openISV(i)">编辑</pf-button></td>
          </tr>
          <tr v-if="!isvs.length">
            <td colspan="6"><span class="pf-muted">还没有服务商。先点上面的「新建服务商」登记你自己，之后才能建特约商户和进件。</span></td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="pf-card">
      <h2 class="m-0 mb-2 text-[14px] font-semibold">商户</h2>
      <div class="overflow-x-auto">
        <table class="pf-table">
          <thead>
            <tr class="whitespace-nowrap"><th>商户</th><th>渠道子商户号</th><th>归属</th><th>联系人</th><th>商户登录</th><th>状态</th><th></th></tr>
          </thead>
          <tbody>
            <tr v-for="m in merchants" :key="m.mchNo">
              <td>
                <div class="flex flex-col gap-1">
                  <span>{{ m.mchName }}</span>
                  <span class="whitespace-nowrap"><span class="pf-code">{{ m.mchNo }}</span><span class="pf-muted">　{{ m.mchType === 1 ? "普通" : "特约" }}<template v-if="m.mchShortName"> · {{ m.mchShortName }}</template></span></span>
                </div>
              </td>
              <td>
                <div class="flex flex-col gap-1">
                  <span v-for="s in m.subAccounts || []" :key="s.ifCode" class="whitespace-nowrap"><span class="pf-code">{{ s.subMchid }}</span><span class="pf-muted">　{{ channelShort[s.ifCode] || s.ifCode }}</span></span>
                  <span v-if="!(m.subAccounts || []).length" class="pf-muted whitespace-nowrap">未开通</span>
                </div>
              </td>
              <td>
                <div class="flex flex-col gap-1">
                  <span>{{ m.isvNo ? (isvName(m.isvNo) || m.isvNo) : "—" }}</span>
                  <span v-if="m.agentNo" class="pf-muted whitespace-nowrap">代理 {{ m.agentNo }}</span>
                </div>
              </td>
              <td>
                <div class="flex flex-col gap-1 whitespace-nowrap">
                  <span>{{ m.contactName || "—" }}</span>
                  <span v-if="m.contactPhone" class="pf-muted">{{ m.contactPhone }}</span>
                </div>
              </td>
              <td>
                <div v-if="m.portal" class="flex flex-col gap-1 whitespace-nowrap">
                  <span class="pf-code">{{ m.portal.username }}</span>
                  <span v-if="m.portal.state !== 'active'" class="pf-dot" data-tone="danger">已停用</span>
                  <span v-else-if="!m.portal.appKey" class="pf-dot" data-tone="danger">appKey 未签发</span>
                </div>
                <span v-else class="pf-muted whitespace-nowrap">未开通</span>
              </td>
              <td class="whitespace-nowrap"><span class="pf-dot" :data-tone="m.state === 'active' ? 'ok' : 'danger'">{{ m.state === "active" ? "启用" : "停用" }}</span></td>
              <td>
                <div class="flex gap-2 flex-wrap justify-end">
                  <pf-button size="sm" @click="startEdit(m)">编辑</pf-button>
                  <pf-button size="sm" :variant="m.portal ? 'default' : 'primary'" @click="openPortal(m)">{{ m.portal ? "商户登录" : "开通商户登录" }}</pf-button>
                  <pf-button size="sm" variant="ghost" @click="seeAs(m.mchNo)">商户视角</pf-button>
                  <pf-button v-if="m.state === 'active'" size="sm" variant="ghost" @click="setState(m, 'disabled')">停用</pf-button>
                  <pf-button v-else size="sm" variant="ghost" @click="setState(m, 'active')">启用</pf-button>
                </div>
              </td>
            </tr>
            <tr v-if="!merchants.length">
              <td colspan="7"><span class="pf-muted">暂无商户</span></td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="pf-muted m-0 mt-2">
        名称下面的「商户号」是本系统内部的编号；「渠道子商户号」是微信 / 支付宝给这个商户开的号（微信叫特约商户号），进件完成后自动写入，
        显示「未开通」说明还没进件完成，这时不能用真实渠道下单。要手工填写或修改它，到「应用·通道 → 渠道参数」把「给谁配置」切到商户。代理商归属在「代理商·分润」页设置。
        「开通商户登录」给商家发一个能自己登录的账号和一套接口凭据；「商户视角」是你以运营身份看一眼商家登录后会看到的页面，方便替它排查接入问题。
      </p>
    </div>
  </div>
</template>
