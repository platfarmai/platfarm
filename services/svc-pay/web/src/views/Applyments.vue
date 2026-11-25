<script setup>
import { ref, computed, onMounted } from "vue";
import { api } from "../api.js";
import { when, applymentStates, stateTone } from "../utils.js";

const err = ref("");
const msg = ref("");
const items = ref([]);
const merchants = ref([]);
const filter = ref("");
const current = ref(null); // { applyment, media }
const editing = ref(false);
const form = ref({ mchNo: "", ifCode: "wxpay_partner", payloadText: "" });
const fields = ref(blank());
const jsonError = ref("");
const showJson = ref(false);
const uploading = ref("");
let writing = false;

// 证照槽位随主体 / 联系人身份 / 经营场景变化，只列微信要求的。
const imageKinds = computed(() => {
  const f = fields.value;
  const types = f.business.scene.types;
  const kinds = [
    ["license", f.subject.type === "institution" ? "事业单位法人证书" : "营业执照"],
    ["id_front", "法人证件人像面"],
    ["id_back", "法人证件国徽面 / 反面"],
  ];
  if (f.extra.wx_contact_type === "SUPER") {
    kinds.push(["contact_id_front", "经办人身份证人像面"], ["contact_id_back", "经办人身份证国徽面"]);
  }
  if (types.includes("offline")) kinds.push(["storefront", "门头照"], ["interior", "店内环境照"]);
  if (types.includes("mp")) kinds.push(["mp_pic", "公众号页面截图"]);
  if (types.includes("mini_program")) kinds.push(["mini_program_pic", "小程序截图（未上线时提供）"]);
  if (types.includes("app")) {
    kinds.push(["app_pic", "APP 首页截图"], ["app_pic_2", "APP 尾页截图"], ["app_pic_3", "APP 应用内截图"], ["app_pic_4", "APP 支付页截图"]);
  }
  return kinds;
});
const scenes = [
  ["offline", "线下门店"],
  ["mini_program", "小程序"],
  ["mp", "公众号"],
  ["app", "APP"],
  ["web", "网站"],
];
// 微信特约进件：subject.mcc 存结算规则 ID，extra.wx_qualification_type 存所属行业。
// 只收录无需特殊资质、费率 0.6% 的常用项。来源：微信支付入驻结算规则对照表。
const settlementRules = {
  enterprise: [
    ["716", "企业 · 餐饮/零售/生活服务（0.6%，T+1）"],
    ["711", "企业 · 互联网服务（1%，T+1）"],
  ],
  individual: [
    ["719", "个体户 · 餐饮/零售/生活服务（0.6%，T+1）"],
    ["746", "个体户 · 互联网服务（1%，T+1）"],
  ],
  // 725 是事业单位的规则；727 属于社会组织（SUBJECT_TYPE_OTHERS），本系统未开放该主体。
  institution: [
    ["725", "事业单位 · 餐饮/零售/生活服务（0.6%，T+1）"],
  ],
};
const industries = {
  716: ["餐饮", "电商平台", "零售", "食品生鲜", "咨询/娱乐票务", "房产中介", "休闲娱乐/旅游服务", "居民生活服务", "景区/酒店", "停车缴费"],
  719: ["餐饮", "电商平台", "零售", "食品生鲜", "咨询/娱乐票务", "房产中介", "休闲娱乐/旅游服务", "居民生活服务", "景区/酒店", "停车缴费"],
  711: ["门户论坛/网络广告及推广/软件开发/其他互联网服务"],
  746: ["门户论坛/网络广告及推广/软件开发/其他互联网服务"],
  725: ["餐饮", "电商平台", "零售", "居民生活服务", "停车缴费"],
};
const banks = ["工商银行", "交通银行", "招商银行", "民生银行", "中信银行", "浦发银行", "兴业银行", "光大银行", "广发银行", "平安银行", "北京银行", "华夏银行", "农业银行", "建设银行", "邮政储蓄银行", "中国银行", "宁波银行", "其他银行"];
const ways = [
  ["WX_JSAPI", "微信 JSAPI / 小程序"],
  ["WX_NATIVE", "微信 Native 扫码"],
];
const regions = [
  ["110000", "北京市"], ["120000", "天津市"], ["130000", "河北省"], ["140000", "山西省"],
  ["150000", "内蒙古自治区"], ["210000", "辽宁省"], ["220000", "吉林省"], ["230000", "黑龙江省"],
  ["310000", "上海市"], ["320000", "江苏省"], ["330000", "浙江省"], ["340000", "安徽省"],
  ["350000", "福建省"], ["360000", "江西省"], ["370000", "山东省"], ["410000", "河南省"],
  ["420000", "湖北省"], ["430000", "湖南省"], ["440000", "广东省"], ["450000", "广西壮族自治区"],
  ["460000", "海南省"], ["500000", "重庆市"], ["510000", "四川省"], ["520000", "贵州省"],
  ["530000", "云南省"], ["540000", "西藏自治区"], ["610000", "陕西省"], ["620000", "甘肃省"],
  ["630000", "青海省"], ["640000", "宁夏回族自治区"], ["650000", "新疆维吾尔自治区"],
];

function blank() {
  return {
    subject: { type: "enterprise", name: "", short_name: "", license_no: "", license_valid: "2020-01-01,长期", mcc: "716" },
    legal_person: { name: "", id_type: "id_card", id_no: "", id_valid: "2020-01-01,长期", phone: "" },
    contact: { name: "", phone: "", email: "" },
    business: {
      service_phone: "",
      address: { province: "", city: "", district: "", detail: "", code: "" },
      scene: { types: ["offline"], appid: "" },
    },
    settlement_account: { type: "corporate", bank: "", branch: "", account_name: "", account_no: "", address_code: "" },
    rates: [{ way_code: "WX_JSAPI", fee_rate_bps: 60, settle_cycle: "T1" }],
    images: {},
    extra: { wx_qualification_type: "零售", wx_contact_type: "LEGAL", wx_id_card_address: "", web_domain: "" },
  };
}

const reviewable = computed(() => current.value?.applyment?.state === "reviewing");
const pollable = computed(() => ["submitted", "channel_auditing", "need_sign", "need_verify"].includes(current.value?.applyment?.state));
const needsAppID = computed(() => fields.value.business.scene.types.some((t) => t === "mini_program" || t === "mp" || t === "app"));
const needsWeb = computed(() => fields.value.business.scene.types.includes("web"));
const ruleOptions = computed(() => settlementRules[fields.value.subject.type] || settlementRules.enterprise);
const industryOptions = computed(() => industries[fields.value.subject.mcc] || []);
const regionValue = computed(() => {
  const code = fields.value.business.address.code || "";
  const hit = regions.find(([c]) => c === code || c.slice(0, 2) === code.slice(0, 2));
  return hit ? hit[0] : "";
});

function fill(payload) {
  const base = blank();
  const src = payload && typeof payload === "object" ? payload : {};
  for (const key of ["subject", "legal_person", "contact", "settlement_account", "extra"]) {
    base[key] = { ...base[key], ...(src[key] || {}) };
  }
  base.business = {
    service_phone: src.business?.service_phone || "",
    address: { ...base.business.address, ...(src.business?.address || {}) },
    scene: {
      types: Array.isArray(src.business?.scene?.types) ? [...src.business.scene.types] : ["offline"],
      appid: src.business?.scene?.appid || "",
    },
  };
  base.rates = Array.isArray(src.rates) && src.rates.length
    ? src.rates.map((r) => ({ way_code: r.way_code || "", fee_rate_bps: Number(r.fee_rate_bps) || 0, settle_cycle: r.settle_cycle || "T1" }))
    : base.rates;
  base.images = { ...(src.images || {}) };
  return base;
}

function writeJson() {
  writing = true;
  form.value.payloadText = JSON.stringify(fields.value, null, 2);
  jsonError.value = "";
  queueMicrotask(() => { writing = false; });
}

function readJson() {
  if (writing) return;
  try {
    fields.value = fill(JSON.parse(form.value.payloadText));
    jsonError.value = "";
  } catch (e) {
    jsonError.value = "JSON 还不能解析：" + e.message;
  }
}

function set(path, value) {
  const parts = path.split(".");
  let cur = fields.value;
  for (let i = 0; i < parts.length - 1; i++) cur = cur[parts[i]];
  cur[parts.at(-1)] = value;
  writeJson();
}

function onInput(path, ev) {
  set(path, ev.target.value);
}

function onSubjectType(ev) {
  const type = ev.target.value;
  fields.value.subject.type = type;
  const rules = settlementRules[type] || [];
  if (!rules.some(([id]) => id === fields.value.subject.mcc)) fields.value.subject.mcc = rules[0]?.[0] || "";
  // 微信只允许个体户用对私卡结算。
  if (type !== "individual") fields.value.settlement_account.type = "corporate";
  onRule();
}

function onRule() {
  const list = industries[fields.value.subject.mcc] || [];
  if (!list.includes(fields.value.extra.wx_qualification_type)) fields.value.extra.wx_qualification_type = list[0] || "";
  writeJson();
}

function onRegion(ev) {
  const code = ev.target.value;
  const name = regions.find(([c]) => c === code)?.[1] || "";
  fields.value.business.address.province = name;
  fields.value.business.address.code = code;
  if (!fields.value.settlement_account.address_code) fields.value.settlement_account.address_code = code;
  writeJson();
}

function toggleScene(key, on) {
  const types = new Set(fields.value.business.scene.types);
  if (on) types.add(key);
  else types.delete(key);
  set("business.scene.types", [...types]);
}

async function load() {
  err.value = "";
  try {
    const q = filter.value ? "?state=" + filter.value : "";
    const [a, m] = await Promise.all([api("GET", "/admin/applyments" + q), api("GET", "/admin/merchants")]);
    items.value = a.items || [];
    merchants.value = m.items || [];
  } catch (e) {
    err.value = e.message || String(e);
  }
}

async function open(id) {
  err.value = "";
  try {
    current.value = await api("GET", "/admin/applyments/" + id);
    editing.value = false;
  } catch (e) {
    err.value = e.message || String(e);
  }
}

async function act(action, body) {
  if (!current.value) return;
  err.value = "";
  msg.value = "";
  try {
    const r = await api("POST", `/admin/applyments/${current.value.applyment.applymentId}/${action}`, body);
    if (r.channelError) msg.value = "审核已通过，但提交渠道失败：" + r.channelError;
    await open(r.applyment.applymentId);
    await load();
  } catch (e) {
    err.value = e.message || String(e);
  }
}

function reject() {
  const reason = window.prompt("驳回原因（会展示给操作员）");
  if (reason && reason.trim()) act("reject", { reason: reason.trim() });
}

function begin(payload) {
  fields.value = fill(payload);
  writeJson();
  jsonError.value = "";
  showJson.value = false;
  editing.value = true;
}

function startNew() {
  form.value = { mchNo: merchants.value[0]?.mchNo || "", ifCode: "wxpay_partner", payloadText: "" };
  current.value = null;
  begin(blank());
}

function startEdit() {
  const a = current.value.applyment;
  // 脱敏字段需重新填写：后端只回传掩码。
  const p = JSON.parse(JSON.stringify(a.payload));
  p.legal_person.id_no = "";
  p.settlement_account.account_no = "";
  if (p.extra?.wx_contact_id_no) p.extra.wx_contact_id_no = "";
  form.value = { mchNo: a.mchNo, ifCode: a.ifCode, payloadText: "" };
  begin(p);
}

function parsePayload() {
  if (jsonError.value) {
    err.value = jsonError.value;
    return null;
  }
  try {
    return JSON.parse(form.value.payloadText);
  } catch (e) {
    err.value = "payload 不是合法 JSON：" + e.message;
    return null;
  }
}

async function upload(kind, ev) {
  const file = ev.target.files?.[0];
  if (!file) return;
  const payload = parsePayload();
  if (!payload) return;
  uploading.value = kind;
  err.value = "";
  try {
    const r = await api("POST", "/admin/applyments/upload-url", { filename: file.name, mime: file.type, size: file.size });
    const put = await fetch(r.uploadUrl, { method: "PUT", body: file, headers: { "Content-Type": file.type || "application/octet-stream" } });
    if (!put.ok) throw new Error("上传到对象存储失败 HTTP " + put.status);
    set("images." + kind, String(r.fileId));
  } catch (e) {
    err.value = e.message || String(e);
  } finally {
    uploading.value = "";
    ev.target.value = "";
  }
}

async function save() {
  const payload = parsePayload();
  if (!payload) return;
  err.value = "";
  msg.value = "";
  try {
    let r;
    if (current.value) {
      r = await api("PUT", "/admin/applyments/" + current.value.applyment.applymentId, { payload });
    } else {
      r = await api("POST", "/admin/applyments", { mchNo: form.value.mchNo, ifCode: form.value.ifCode, payload });
    }
    editing.value = false;
    await open(r.applyment.applymentId);
    await load();
  } catch (e) {
    err.value = e.message || String(e);
  }
}

onMounted(load);
</script>

<template>
  <div class="flex flex-col gap-3">
    <pf-alert v-if="err" tone="danger">{{ err }}</pf-alert>
    <pf-alert v-if="msg" tone="warning">{{ msg }}</pf-alert>

    <div class="flex gap-2 items-center">
      <select v-model="filter" class="pf-input" @change="load">
        <option value="">全部状态</option>
        <option v-for="(label, key) in applymentStates" :key="key" :value="key">{{ label }}</option>
      </select>
      <pf-button size="sm" variant="primary" @click="startNew">新建进件</pf-button>
    </div>

    <div class="pf-card">
      <table class="pf-table">
        <thead>
          <tr><th>申请单</th><th>商户</th><th>渠道</th><th>状态</th><th>渠道状态</th><th>审核员</th><th>更新时间</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="a in items" :key="a.applymentId">
            <td><span class="pf-code">{{ a.applymentId }}</span></td>
            <td><span class="pf-code">{{ a.mchNo }}</span></td>
            <td>{{ a.ifCode }}</td>
            <td><span class="pf-dot" :data-tone="stateTone(a.state)">{{ applymentStates[a.state] || a.state }}</span></td>
            <td><span class="pf-muted">{{ a.channelState || "—" }}</span></td>
            <td>{{ a.reviewer || "—" }}</td>
            <td>{{ when(a.updatedAt) }}</td>
            <td><pf-button size="sm" variant="ghost" @click="open(a.applymentId)">查看</pf-button></td>
          </tr>
          <tr v-if="!items.length">
            <td colspan="8"><span class="pf-muted">暂无进件申请</span></td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 新建 / 修改资料 -->
    <div v-if="editing" class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">{{ current ? "修改资料 " + current.applyment.applymentId : "新建进件" }}</h2>
      <div v-if="!current" class="grid grid-cols-2 gap-3">
        <label class="flex flex-col gap-1 text-[13px]">商户
          <select :value="form.mchNo" class="pf-input" @change="form.mchNo = $event.target.value">
            <option v-for="m in merchants" :key="m.mchNo" :value="m.mchNo">{{ m.mchName }}（{{ m.mchNo }}）</option>
          </select>
        </label>
        <label class="flex flex-col gap-1 text-[13px]">渠道
          <select :value="form.ifCode" class="pf-input" @change="form.ifCode = $event.target.value">
            <option value="wxpay_partner">微信普通服务商</option>
          </select>
        </label>
      </div>

      <h3 class="m-0 text-[14px] font-semibold">主体</h3>
      <div class="grid grid-cols-2 gap-3">
        <label class="flex flex-col gap-1 text-[13px]">主体类型
          <select class="pf-input" :value="fields.subject.type" @change="onSubjectType">
            <option value="enterprise">企业</option>
            <option value="individual">个体户</option>
            <option value="institution">事业单位</option>
          </select>
        </label>
        <label class="flex flex-col gap-1 text-[13px]">结算规则
          <select class="pf-input" :value="fields.subject.mcc" @change="onInput('subject.mcc', $event); onRule()">
            <option v-for="[id, label] in ruleOptions" :key="id" :value="id">{{ label }}（{{ id }}）</option>
          </select>
        </label>
        <label class="flex flex-col gap-1 text-[13px]">所属行业
          <select class="pf-input" :value="fields.extra.wx_qualification_type" @change="onInput('extra.wx_qualification_type', $event)">
            <option v-for="name in industryOptions" :key="name" :value="name">{{ name }}</option>
          </select>
        </label>
        <label class="flex flex-col gap-1 text-[13px]">商户名称
          <input class="pf-input" :value="fields.subject.name" @input="onInput('subject.name', $event)">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">简称
          <input class="pf-input" :value="fields.subject.short_name" @input="onInput('subject.short_name', $event)">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">{{ fields.subject.type === "institution" ? "事业单位法人证书编号" : "执照号" }}
          <input class="pf-input" :value="fields.subject.license_no" @input="onInput('subject.license_no', $event)">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">{{ fields.subject.type === "institution" ? "证书有效期" : "执照有效期" }}
          <input class="pf-input" :value="fields.subject.license_valid" placeholder="2020-01-01,长期" @input="onInput('subject.license_valid', $event)">
        </label>
      </div>

      <h3 class="m-0 text-[14px] font-semibold">法人 / 经营者</h3>
      <div class="grid grid-cols-2 gap-3">
        <label class="flex flex-col gap-1 text-[13px]">姓名
          <input class="pf-input" :value="fields.legal_person.name" @input="onInput('legal_person.name', $event)">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">证件类型
          <select class="pf-input" :value="fields.legal_person.id_type" @change="onInput('legal_person.id_type', $event)">
            <option value="id_card">身份证</option>
            <option value="passport">护照</option>
            <option value="hk_macao">港澳通行证</option>
            <option value="taiwan">台胞证</option>
            <option value="foreign_residence">外国人居留证</option>
          </select>
        </label>
        <label class="flex flex-col gap-1 text-[13px]">证件号
          <input class="pf-input" :value="fields.legal_person.id_no" @input="onInput('legal_person.id_no', $event)">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">证件有效期
          <input class="pf-input" :value="fields.legal_person.id_valid" placeholder="2020-01-01,长期" @input="onInput('legal_person.id_valid', $event)">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">手机
          <input class="pf-input" :value="fields.legal_person.phone" @input="onInput('legal_person.phone', $event)">
        </label>
        <label v-if="fields.subject.type === 'enterprise'" class="flex flex-col gap-1 text-[13px]">证件居住地址（企业必填）
          <input class="pf-input" :value="fields.extra.wx_id_card_address" placeholder="按证件上的住址填写" @input="onInput('extra.wx_id_card_address', $event)">
        </label>
      </div>

      <h3 class="m-0 text-[14px] font-semibold">联系人</h3>
      <div class="grid grid-cols-3 gap-3">
        <label class="flex flex-col gap-1 text-[13px]">身份
          <select class="pf-input" :value="fields.extra.wx_contact_type" @change="onInput('extra.wx_contact_type', $event)">
            <option value="LEGAL">法人 / 经营者本人</option>
            <option value="SUPER">经办人</option>
          </select>
        </label>
        <label class="flex flex-col gap-1 text-[13px]">姓名
          <input class="pf-input" :value="fields.contact.name" @input="onInput('contact.name', $event)">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">手机
          <input class="pf-input" :value="fields.contact.phone" @input="onInput('contact.phone', $event)">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">邮箱（必填，接收开户邮件）
          <input class="pf-input" :value="fields.contact.email" @input="onInput('contact.email', $event)">
        </label>
        <template v-if="fields.extra.wx_contact_type === 'SUPER'">
          <label class="flex flex-col gap-1 text-[13px]">经办人身份证号
            <input class="pf-input" :value="fields.extra.wx_contact_id_no || ''" @input="onInput('extra.wx_contact_id_no', $event)">
          </label>
          <label class="flex flex-col gap-1 text-[13px]">经办人证件有效期
            <input class="pf-input" :value="fields.extra.wx_contact_id_valid || ''" placeholder="2020-01-01,长期" @input="onInput('extra.wx_contact_id_valid', $event)">
          </label>
        </template>
      </div>
      <p v-if="fields.extra.wx_contact_type === 'SUPER'" class="pf-muted m-0">经办人需提供本人身份证号、有效期和正反面照片（在下方证照处上传）。</p>

      <h3 class="m-0 text-[14px] font-semibold">经营</h3>
      <div class="grid grid-cols-3 gap-3">
        <label class="flex flex-col gap-1 text-[13px]">客服电话
          <input class="pf-input" :value="fields.business.service_phone" @input="onInput('business.service_phone', $event)">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">省 / 直辖市
          <select class="pf-input" :value="regionValue" @change="onRegion">
            <option value="">请选择</option>
            <option v-for="[code, name] in regions" :key="code" :value="code">{{ name }}</option>
          </select>
        </label>
        <label class="flex flex-col gap-1 text-[13px]">市
          <input class="pf-input" :value="fields.business.address.city" @input="onInput('business.address.city', $event)">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">区
          <input class="pf-input" :value="fields.business.address.district" @input="onInput('business.address.district', $event)">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">详细地址
          <input class="pf-input" :value="fields.business.address.detail" @input="onInput('business.address.detail', $event)">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">微信地区编码
          <input class="pf-input" :value="fields.business.address.code" placeholder="选省后自动填省级编码，可改成区县编码" @input="onInput('business.address.code', $event)">
        </label>
      </div>
      <div class="flex flex-wrap gap-3 text-[13px]">
        <label v-for="[key, label] in scenes" :key="key" class="flex gap-1 items-center">
          <input type="checkbox" :checked="fields.business.scene.types.includes(key)" @change="toggleScene(key, $event.target.checked)">
          {{ label }}
        </label>
      </div>
      <label v-if="needsAppID" class="flex flex-col gap-1 text-[13px]">小程序 / 公众号 / APP 的 AppID
        <input class="pf-input" :value="fields.business.scene.appid" @input="onInput('business.scene.appid', $event)">
      </label>
      <label v-if="needsWeb" class="flex flex-col gap-1 text-[13px]">网站域名
        <input class="pf-input" :value="fields.extra.web_domain" placeholder="example.com" @input="onInput('extra.web_domain', $event)">
      </label>

      <h3 class="m-0 text-[14px] font-semibold">结算账户</h3>
      <div class="grid grid-cols-2 gap-3">
        <label class="flex flex-col gap-1 text-[13px]">账户类型
          <select class="pf-input" :value="fields.settlement_account.type" @change="onInput('settlement_account.type', $event)">
            <option value="corporate">对公</option>
            <option value="personal" :disabled="fields.subject.type !== 'individual'">对私（仅个体户）</option>
          </select>
        </label>
        <label class="flex flex-col gap-1 text-[13px]">开户银行
          <select class="pf-input" :value="fields.settlement_account.bank" @change="onInput('settlement_account.bank', $event)">
            <option value="">请选择</option>
            <option v-for="bank in banks" :key="bank" :value="bank">{{ bank }}</option>
          </select>
        </label>
        <label class="flex flex-col gap-1 text-[13px]">开户行全称（含支行，选“其他银行”时必填）
          <input class="pf-input" :value="fields.settlement_account.branch" @input="onInput('settlement_account.branch', $event)">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">户名
          <input class="pf-input" :value="fields.settlement_account.account_name" @input="onInput('settlement_account.account_name', $event)">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">账号
          <input class="pf-input" :value="fields.settlement_account.account_no" @input="onInput('settlement_account.account_no', $event)">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">开户行省市编码
          <input class="pf-input" :value="fields.settlement_account.address_code" @input="onInput('settlement_account.address_code', $event)">
        </label>
      </div>
      <p class="pf-muted m-0">对公户名必须与商户名称一致，对私户名必须与法人姓名一致。</p>

      <h3 class="m-0 text-[14px] font-semibold">费率</h3>
      <div class="grid grid-cols-3 gap-3">
        <label class="flex flex-col gap-1 text-[13px]">支付方式
          <select class="pf-input" :value="fields.rates[0].way_code" @change="fields.rates[0].way_code = $event.target.value; writeJson()">
            <option v-for="[code, label] in ways" :key="code" :value="code">{{ label }}</option>
          </select>
        </label>
        <label class="flex flex-col gap-1 text-[13px]">费率（万分位）
          <input class="pf-input" type="number" min="0" max="1000" :value="fields.rates[0].fee_rate_bps" @input="fields.rates[0].fee_rate_bps = Number($event.target.value); writeJson()">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">结算周期
          <select class="pf-input" :value="fields.rates[0].settle_cycle" @change="fields.rates[0].settle_cycle = $event.target.value; writeJson()">
            <option value="T1">T1</option>
            <option value="D1">D1</option>
            <option value="D0">D0</option>
          </select>
        </label>
      </div>
      <p class="pf-muted m-0">微信不接受自定义费率，这里仅作平台记录。</p>

      <h3 class="m-0 text-[14px] font-semibold">证照</h3>
      <div class="flex flex-wrap gap-3">
        <label v-for="[kind, label] in imageKinds" :key="kind" class="flex flex-col gap-1 text-[13px]">
          <span>{{ label }} <span class="pf-muted">{{ fields.images[kind] ? "已选 " + fields.images[kind] : (uploading === kind ? "上传中…" : "未上传") }}</span></span>
          <input type="file" accept="image/jpeg,image/png" :disabled="!!uploading" @change="upload(kind, $event)" />
        </label>
      </div>

      <div class="flex gap-2 items-center">
        <pf-button size="sm" variant="ghost" @click="showJson = !showJson">{{ showJson ? "收起 JSON" : "查看 / 编辑 JSON" }}</pf-button>
        <span v-if="jsonError" class="pf-muted">{{ jsonError }}</span>
      </div>
      <textarea v-if="showJson" v-model="form.payloadText" class="pf-input font-mono text-[12px]" rows="18" spellcheck="false" @input="readJson"></textarea>
      <p class="pf-muted m-0">表单和 JSON 双向同步。证件号与银行账号保存后只以掩码回显，修改时需重新填写。</p>
      <div class="flex gap-2">
        <pf-button variant="primary" size="sm" @click="save">保存草稿</pf-button>
        <pf-button size="sm" @click="editing = false">取消</pf-button>
      </div>
    </div>

    <!-- 详情 -->
    <div v-else-if="current" class="pf-card flex flex-col gap-3">
      <div class="flex items-center gap-2">
        <h2 class="m-0 text-[16px] font-semibold">{{ current.applyment.applymentId }}</h2>
        <span class="pf-dot" :data-tone="stateTone(current.applyment.state)">{{ applymentStates[current.applyment.state] }}</span>
        <span class="pf-muted">{{ current.applyment.ifCode }} · {{ current.applyment.mchNo }}</span>
      </div>
      <pf-alert v-if="current.applyment.rejectReason" tone="danger">驳回原因：{{ current.applyment.rejectReason }}</pf-alert>
      <pf-alert v-if="current.applyment.channelMsg" tone="warning">渠道：{{ current.applyment.channelState }} {{ current.applyment.channelMsg }}</pf-alert>
      <p v-if="current.applyment.signUrl" class="m-0">签约链接：<a :href="current.applyment.signUrl" target="_blank" rel="noopener">{{ current.applyment.signUrl }}</a></p>
      <p v-if="current.applyment.subMchid" class="m-0">子商户号：<span class="pf-code">{{ current.applyment.subMchid }}</span></p>
      <p v-if="current.applyment.unexpressed" class="m-0 pf-muted">渠道未表达字段：{{ current.applyment.unexpressed }}</p>

      <div class="flex flex-wrap gap-2">
        <template v-if="current.applyment.state === 'draft'">
          <pf-button size="sm" variant="primary" @click="act('submit-review')">提交审核</pf-button>
          <pf-button size="sm" @click="startEdit">修改资料</pf-button>
          <pf-button size="sm" variant="ghost" @click="act('cancel')">取消申请</pf-button>
        </template>
        <template v-if="reviewable">
          <pf-button size="sm" variant="primary" @click="act('approve')">通过并提交渠道</pf-button>
          <pf-button size="sm" variant="danger" @click="reject">驳回</pf-button>
        </template>
        <template v-if="current.applyment.state === 'approved'">
          <pf-button size="sm" variant="primary" @click="act('submit')">重新提交渠道</pf-button>
          <pf-button size="sm" variant="ghost" @click="act('cancel')">取消申请</pf-button>
        </template>
        <template v-if="pollable">
          <pf-button size="sm" @click="act('sync')">同步渠道状态</pf-button>
        </template>
        <template v-if="current.applyment.state === 'rejected'">
          <pf-button size="sm" variant="primary" @click="startEdit">修改资料重提</pf-button>
          <pf-button size="sm" variant="ghost" @click="act('cancel')">取消申请</pf-button>
        </template>
      </div>

      <table class="pf-table">
        <thead><tr><th>图片</th><th>文件</th><th>大小</th><th>渠道 media_id</th></tr></thead>
        <tbody>
          <tr v-for="m in current.media" :key="m.kind">
            <td>{{ m.kind }}</td><td>{{ m.filename }}</td><td>{{ m.size }}</td>
            <td><span class="pf-code">{{ m.mediaId || "—" }}</span></td>
          </tr>
        </tbody>
      </table>
      <pre class="pf-log">{{ JSON.stringify(current.applyment.payload, null, 2) }}</pre>
    </div>
  </div>
</template>
