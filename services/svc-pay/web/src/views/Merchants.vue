<script setup>
import { ref, computed, onMounted } from "vue";
import { api } from "../api.js";
import Field from "../components/Field.vue";

const err = ref("");
const msg = ref("");
const merchants = ref([]);
const isvs = ref([]);
const showISV = ref(false);
const showMch = ref(false);
const isvForm = ref({ isvNo: "", isvName: "", fundMode: "direct", rebateRateBps: 0 });
const mchForm = ref({ mchNo: "", mchName: "", mchShortName: "", mchType: 2, isvNo: "", contactName: "", contactPhone: "" });
const editing = ref(null); // 正在编辑的商户：{ mchNo, mchName, mchShortName, mchType, isvNo, contactName, contactPhone }

const isvName = (no) => isvs.value.find((i) => i.isvNo === no)?.isvName || "";
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

    <div class="flex gap-2">
      <pf-button size="sm" variant="primary" @click="showMch = !showMch; showISV = false; editing = null">新建商户</pf-button>
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
      <table class="pf-table">
        <thead>
          <tr><th>商户号</th><th>名称</th><th>类型</th><th>服务商</th><th>代理商</th><th>联系人</th><th>状态</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="m in merchants" :key="m.mchNo">
            <td><span class="pf-code">{{ m.mchNo }}</span></td>
            <td>{{ m.mchName }}<span v-if="m.mchShortName" class="pf-muted">　{{ m.mchShortName }}</span></td>
            <td>{{ m.mchType === 1 ? "普通" : "特约" }}</td>
            <td>{{ m.isvNo ? (isvName(m.isvNo) || m.isvNo) : "—" }}</td>
            <td>{{ m.agentNo || "—" }}</td>
            <td>{{ m.contactName || "—" }}<span v-if="m.contactPhone" class="pf-muted">　{{ m.contactPhone }}</span></td>
            <td><span class="pf-dot" :data-tone="m.state === 'active' ? 'ok' : 'danger'">{{ m.state === "active" ? "启用" : "停用" }}</span></td>
            <td>
              <div class="flex gap-2 flex-wrap">
                <pf-button size="sm" @click="startEdit(m)">编辑</pf-button>
                <pf-button v-if="m.state === 'active'" size="sm" variant="ghost" @click="setState(m, 'disabled')">停用</pf-button>
                <pf-button v-else size="sm" variant="ghost" @click="setState(m, 'active')">启用</pf-button>
              </div>
            </td>
          </tr>
          <tr v-if="!merchants.length">
            <td colspan="8"><span class="pf-muted">暂无商户</span></td>
          </tr>
        </tbody>
      </table>
      <p class="pf-muted m-0 mt-2">代理商归属在「代理商·分润」页设置；商户的微信特约商户号在「应用·通道 → 渠道参数」里查看。</p>
    </div>
  </div>
</template>
