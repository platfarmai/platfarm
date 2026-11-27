<script setup>
import { ref, computed, onMounted, onUnmounted } from "vue";
import { api } from "../api.js";
import { when, applymentStates, stateTone } from "../utils.js";
import Field from "../components/Field.vue";

const DRAFT_KEY = "svc_pay_applyment_draft_v1";
const MAX_IMAGE_BYTES = 2 * 1024 * 1024;

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
const uploadProgressName = ref("");
const uploads = ref({}); // kind -> { previewUrl, blob, filename, size, fileId, url, error, broken }
const fileInputs = {};
let writing = false;

const imageNames = {
  license: "营业执照 / 法人证书",
  id_front: "法人证件人像面",
  id_back: "法人证件国徽面 / 反面",
  contact_id_front: "经办人身份证人像面",
  contact_id_back: "经办人身份证国徽面",
  storefront: "门头照",
  interior: "店内环境照",
  mp_pic: "公众号页面截图",
  mini_program_pic: "小程序截图",
  app_pic: "APP 首页截图",
  app_pic_2: "APP 尾页截图",
  app_pic_3: "APP 应用内截图",
  app_pic_4: "APP 支付页截图",
};

// 每种证照拍什么、有什么要求。
const imageHints = {
  license: "营业执照正本或副本的彩色照片 / 扫描件（事业单位传事业单位法人证书）。四角完整、文字清晰、不能有水印遮挡，且在有效期内。",
  id_front: "法人（个体户为经营者）身份证有照片的那一面，彩色原件拍照。用护照等其他证件的，传证件资料页。",
  id_back: "身份证有国徽和有效期的那一面。护照没有反面：把资料页再传一次即可（系统要求这一项不为空，护照的反面不会发给微信）。",
  contact_id_front: "经办人本人身份证有照片的那一面（不是法人的）。",
  contact_id_back: "经办人本人身份证有国徽和有效期的那一面。",
  storefront: "门店正门的外观照片，必须能看清招牌上的店名，店名要和上面填的「简称」对得上。没有招牌的，拍经营场所的入口。",
  interior: "店内的经营环境：能看到货架、商品、餐桌或收银台等，证明确实在这里营业。",
  mp_pic: "商户公众号里展示商品 / 服务的页面截图，能看出卖的是什么。",
  mini_program_pic: "小程序已经上线的可以不传；还没上线的传设计稿或体验版的页面截图。",
  app_pic: "APP 打开后的首页截图。",
  app_pic_2: "选填。APP 最后一屏 / 底部的截图。",
  app_pic_3: "选填。APP 里展示商品 / 服务的页面截图。",
  app_pic_4: "选填。APP 里的支付（收银台）页面截图。",
};

// 证照槽位随主体 / 联系人身份 / 经营场景变化，只列微信要求的。
const imageKinds = computed(() => {
  const f = fields.value;
  const types = f.business.scene.types;
  const kinds = [
    ["license", f.subject.type === "institution" ? "事业单位法人证书" : "营业执照", true],
    ["id_front", "法人证件人像面", true],
    ["id_back", "法人证件国徽面 / 反面", true],
  ];
  if (f.extra.wx_contact_type === "SUPER") {
    kinds.push(["contact_id_front", "经办人身份证人像面", true], ["contact_id_back", "经办人身份证国徽面", true]);
  }
  if (types.includes("offline")) kinds.push(["storefront", "门头照", true], ["interior", "店内环境照", true]);
  if (types.includes("mp")) kinds.push(["mp_pic", "公众号页面截图", true]);
  if (types.includes("mini_program")) kinds.push(["mini_program_pic", "小程序截图（未上线时提供）", false]);
  if (types.includes("app")) {
    kinds.push(["app_pic", "APP 首页截图", true], ["app_pic_2", "APP 尾页截图", false], ["app_pic_3", "APP 应用内截图", false], ["app_pic_4", "APP 支付页截图", false]);
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

// 每个状态现在该谁做什么。
const stateGuide = {
  draft: "草稿：资料已保存，还没有人审核。检查无误后点「提交审核」；要改资料点「修改资料」。",
  reviewing: "等待内部审核：需要一位有「审核员」角色的同事打开这张申请单，点「通过并提交渠道」或「驳回」。录入或修改过这份资料的人不能自己审核自己（平台管理员除外）。",
  approved: "内部审核已通过，但提交给微信时失败了（原因见上面的黄色提示）。常见原因是服务商渠道参数没配齐或证书不对；处理好后点「重新提交渠道」。",
  submitted: "已提交给微信，等待微信开始审核。系统每分钟自动查询一次结果，也可以点「同步渠道状态」立即查。",
  channel_auditing: "微信审核中，通常 1–2 个工作日。系统每分钟自动查询一次，不用一直守着；也可以点「同步渠道状态」立即查。",
  need_verify: "微信要求账户验证：把下面的「签约链接」发给联系人（超级管理员），由他用自己的微信打开，按页面指引完成验证（通常是往指定账户汇一笔小额款，或法人扫码确认）。完成后状态会自动往下走。",
  need_sign: "微信审核通过，等待签约：把下面的「签约链接」发给联系人（超级管理员），由他用自己的微信打开并确认签约。签完后状态自动变为「完成」。",
  finished: "进件完成。微信分配的特约商户号已经自动写入这个商户的渠道参数。下一步：到「应用·通道」给商户建应用、配支付通道，就可以收款了。",
  rejected: "被驳回（内部审核员或微信驳回，原因见上面）。点「修改资料重提」按原因改好后保存，再重新「提交审核」。",
  canceled: "已取消，不能再操作。需要的话为这个商户重新「新建进件」。",
};

// 后端校验信息里是 JSON 字段路径，换成表单上的名称。长路径排前面，避免被短路径抢先替换。
const pathLabels = [
  ["settlement_account.account_name", "结算账户·户名"],
  ["settlement_account.account_no", "结算账户·账号"],
  ["settlement_account.branch", "结算账户·开户行全称"],
  ["settlement_account.bank", "结算账户·开户银行"],
  ["settlement_account.type", "结算账户·账户类型"],
  ["business.address.code", "经营·微信地区编码"],
  ["business.address", "经营·地址"],
  ["business.service_phone", "经营·客服电话"],
  ["business.scene.types", "经营场景"],
  ["business.scene.appid", "经营·AppID"],
  ["legal_person.id_valid", "法人·证件有效期"],
  ["legal_person.id_type", "法人·证件类型"],
  ["legal_person.id_no", "法人·证件号"],
  ["legal_person.phone", "法人·手机"],
  ["legal_person.name", "法人·姓名"],
  ["subject.license_valid", "主体·执照有效期"],
  ["subject.license_no", "主体·执照号"],
  ["subject.short_name", "主体·简称"],
  ["subject.name", "主体·商户名称"],
  ["subject.type", "主体类型"],
  ["subject.mcc", "主体·结算规则"],
  ["contact.email", "联系人·邮箱"],
  ["contact.phone", "联系人·手机"],
  ["contact.name", "联系人·姓名"],
  ["extra.wx_qualification_type", "主体·所属行业"],
  ["extra.wx_id_card_address", "法人·证件居住地址"],
  ["extra.wx_contact_id_valid", "联系人·经办人证件有效期"],
  ["extra.wx_contact_id_no", "联系人·经办人身份证号"],
  ["extra.web_domain", "经营·网站域名"],
  ["account_name", "户名"],
];
function humanize(text) {
  let out = String(text);
  out = out.replace(/images\.([a-z0-9_]+)/g, (_, kind) => "证照「" + (imageKinds.value.find(([k]) => k === kind)?.[1] || imageNames[kind] || kind) + "」");
  out = out.replace(/rates\[\d+\]\.fee_rate_bps/g, "「费率」").replace(/rates\[\d+\]\.way_code/g, "「费率·支付方式」");
  for (const [path, label] of pathLabels) out = out.replaceAll(path, "「" + label + "」");
  return out;
}
function fail(e) {
  err.value = humanize(e.message || String(e));
}

function isNumericId(v) {
  return typeof v === "string" && /^[1-9]\d*$/.test(v.trim());
}

function formatBytes(n) {
  const b = Number(n || 0);
  if (!b) return "—";
  if (b < 1024) return b + " B";
  if (b < 1024 * 1024) return (b / 1024).toFixed(1) + " KB";
  return (b / (1024 * 1024)).toFixed(2) + " MB";
}

function cleanStorageUrl(rawUrl) {
  if (!rawUrl) return "";
  try {
    const u = new URL(rawUrl, window.location.origin);
    return u.origin + u.pathname;
  } catch {
    return String(rawUrl).split("?")[0];
  }
}

function mediaUrl(applymentId, kind) {
  return `/api/pay/admin/applyments/${encodeURIComponent(applymentId)}/media/${encodeURIComponent(kind)}`;
}

function setFileInput(kind, el) {
  if (el) fileInputs[kind] = el;
  else delete fileInputs[kind];
}

function pickFile(kind) {
  fileInputs[kind]?.click();
}

function revokeUpload(kind) {
  const prev = uploads.value[kind];
  if (prev?.blob && prev.previewUrl) URL.revokeObjectURL(prev.previewUrl);
}

function revokeAllUploads() {
  for (const k of Object.keys(uploads.value)) revokeUpload(k);
  uploads.value = {};
}

function previewSrc(kind) {
  const u = uploads.value[kind];
  if (u?.broken) return "";
  const id = String(fields.value.images[kind] || "");
  if (u?.previewUrl && (!u.fileId || u.fileId === id)) return u.previewUrl;
  if (!isNumericId(id)) return "";
  if (current.value?.applyment?.applymentId) {
    const hit = (current.value.media || []).find((m) => m.kind === kind && String(m.fileId) === id);
    if (hit) return mediaUrl(current.value.applyment.applymentId, kind);
  }
  return `/api/pay/admin/applyment-files/${encodeURIComponent(id)}`;
}

function onThumbError(kind) {
  uploads.value = {
    ...uploads.value,
    [kind]: { ...(uploads.value[kind] || {}), broken: true },
  };
}

function saveLocalDraft() {
  if (!editing.value || current.value) return;
  try {
    const meta = {};
    for (const [k, u] of Object.entries(uploads.value)) {
      if (u && (u.fileId || u.filename || u.url)) {
        meta[k] = { filename: u.filename || "", size: u.size || 0, fileId: u.fileId || "", url: u.url || "" };
      }
    }
    sessionStorage.setItem(DRAFT_KEY, JSON.stringify({
      mchNo: form.value.mchNo,
      ifCode: form.value.ifCode,
      fields: fields.value,
      uploads: meta,
    }));
  } catch {
    // 隐私模式或配额满时忽略本地缓存
  }
}

function clearLocalDraft() {
  try {
    sessionStorage.removeItem(DRAFT_KEY);
  } catch {
    // ignore
  }
}

function restoreLocalDraft() {
  try {
    const raw = sessionStorage.getItem(DRAFT_KEY);
    if (!raw) return false;
    const saved = JSON.parse(raw);
    if (!saved || typeof saved !== "object" || !saved.fields) return false;
    revokeAllUploads();
    const first = merchants.value.find((m) => m.isvNo) || merchants.value[0];
    form.value = {
      mchNo: saved.mchNo || first?.mchNo || "",
      ifCode: saved.ifCode || "wxpay_partner",
      payloadText: "",
    };
    current.value = null;
    fields.value = fill(saved.fields);
    const restored = {};
    for (const [k, u] of Object.entries(saved.uploads || {})) {
      restored[k] = { previewUrl: "", blob: false, filename: u.filename || "", size: u.size || 0, fileId: u.fileId || "", url: u.url || "", error: "", broken: false };
    }
    uploads.value = restored;
    editing.value = true;
    writeJson();
    return true;
  } catch {
    return false;
  }
}

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
const isOffline = computed(() => fields.value.business.scene.types.includes("offline"));
const ruleOptions = computed(() => settlementRules[fields.value.subject.type] || settlementRules.enterprise);
const industryOptions = computed(() => industries[fields.value.subject.mcc] || []);
const regionValue = computed(() => {
  const code = fields.value.business.address.code || "";
  const hit = regions.find(([c]) => c === code || c.slice(0, 2) === code.slice(0, 2));
  return hit ? hit[0] : "";
});
const personWord = computed(() => (fields.value.subject.type === "individual" ? "经营者" : "法定代表人"));
const feePct = computed(() => (Number(fields.value.rates[0].fee_rate_bps || 0) / 100).toFixed(2));

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
  saveLocalDraft();
  queueMicrotask(() => { writing = false; });
}

function readJson() {
  if (writing) return;
  try {
    fields.value = fill(JSON.parse(form.value.payloadText));
    jsonError.value = "";
    saveLocalDraft();
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
    fail(e);
  }
}

async function open(id) {
  err.value = "";
  try {
    current.value = await api("GET", "/admin/applyments/" + id);
    revokeAllUploads();
    editing.value = false;
  } catch (e) {
    fail(e);
  }
}

async function act(action, body) {
  if (!current.value) return;
  err.value = "";
  msg.value = "";
  try {
    const r = await api("POST", `/admin/applyments/${current.value.applyment.applymentId}/${action}`, body);
    if (r.channelError) msg.value = "审核已通过，但提交渠道失败：" + humanize(r.channelError);
    await open(r.applyment.applymentId);
    await load();
  } catch (e) {
    fail(e);
  }
}

function reject() {
  const reason = window.prompt("驳回原因（会展示给录入资料的操作员，请写清楚哪一项要怎么改）");
  if (reason && reason.trim()) act("reject", { reason: reason.trim() });
}

function begin(payload) {
  fields.value = fill(payload);
  err.value = "";
  jsonError.value = "";
  showJson.value = false;
  editing.value = true;
  writeJson();
}

function startNew() {
  if (restoreLocalDraft()) return;
  resetNewDraft();
}

function resetNewDraft() {
  clearLocalDraft();
  revokeAllUploads();
  // 默认选第一个能进件的商户（已挂服务商）。
  const first = merchants.value.find((m) => m.isvNo) || merchants.value[0];
  form.value = { mchNo: first?.mchNo || "", ifCode: "wxpay_partner", payloadText: "" };
  current.value = null;
  begin(blank());
}

function cancelEdit() {
  if (!current.value) clearLocalDraft();
  revokeAllUploads();
  err.value = "";
  editing.value = false;
}

function startEdit() {
  const a = current.value.applyment;
  revokeAllUploads();
  // 脱敏字段需重新填写：后端只回传掩码。
  const p = JSON.parse(JSON.stringify(a.payload));
  p.legal_person.id_no = "";
  p.settlement_account.account_no = "";
  if (p.extra?.wx_contact_id_no) p.extra.wx_contact_id_no = "";
  const existingMedia = {};
  for (const m of current.value.media || []) {
    existingMedia[m.kind] = {
      previewUrl: mediaUrl(a.applymentId, m.kind),
      blob: false,
      filename: m.filename || "",
      size: m.size || 0,
      fileId: String(m.fileId || p.images?.[m.kind] || ""),
      url: "",
      error: "",
      broken: false,
    };
  }
  uploads.value = existingMedia;
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

function clearImage(kind) {
  revokeUpload(kind);
  const nextUploads = { ...uploads.value };
  delete nextUploads[kind];
  uploads.value = nextUploads;
  const nextImages = { ...fields.value.images };
  delete nextImages[kind];
  fields.value.images = nextImages;
  if (!jsonError.value) writeJson();
  else saveLocalDraft();
}

async function upload(kind, ev) {
  const file = ev.target.files?.[0];
  ev.target.value = "";
  if (!file) return;
  const okType = file.type === "image/jpeg" || file.type === "image/png" || /\.(jpe?g|png)$/i.test(file.name || "");
  if (!okType) {
    const m = "只支持 JPG 或 PNG 格式图片";
    uploads.value = { ...uploads.value, [kind]: { ...(uploads.value[kind] || {}), error: m } };
    err.value = humanize(`images.${kind}：${m}`);
    return;
  }
  if (file.size > MAX_IMAGE_BYTES) {
    const m = `图片大小 ${formatBytes(file.size)} 超过微信限制的 2MB，请先压缩后再上传`;
    uploads.value = { ...uploads.value, [kind]: { ...(uploads.value[kind] || {}), error: m } };
    err.value = humanize(`images.${kind}：${m}`);
    return;
  }
  uploading.value = kind;
  uploadProgressName.value = file.name;
  err.value = "";
  uploads.value = { ...uploads.value, [kind]: { ...(uploads.value[kind] || {}), error: "" } };
  try {
    const r = await api("POST", "/admin/applyments/upload-url", { filename: file.name, mime: file.type || "image/jpeg", size: file.size });
    const put = await fetch(r.uploadUrl, { method: "PUT", body: file, headers: { "Content-Type": file.type || "application/octet-stream" } });
    if (!put.ok) throw new Error("上传到对象存储失败 HTTP " + put.status);
    const fileId = String(r.fileId);
    const storageUrl = cleanStorageUrl(r.uploadUrl);
    revokeUpload(kind);
    uploads.value = {
      ...uploads.value,
      [kind]: {
        previewUrl: URL.createObjectURL(file),
        blob: true,
        filename: file.name,
        size: file.size,
        fileId,
        url: storageUrl,
        error: "",
        broken: false,
      },
    };
    fields.value.images[kind] = fileId;
    if (!jsonError.value) writeJson();
    else saveLocalDraft();
  } catch (e) {
    const m = humanize(e.message || String(e));
    uploads.value = { ...uploads.value, [kind]: { ...(uploads.value[kind] || {}), error: m } };
    err.value = m;
  } finally {
    uploading.value = "";
    uploadProgressName.value = "";
  }
}

async function save() {
  const payload = parsePayload();
  if (!payload) return;
  for (const [kind, val] of Object.entries(payload.images || {})) {
    const s = String(val ?? "").trim();
    if (s && !isNumericId(s)) {
      err.value = humanize(`images.${kind} 必须是上传返回的文件编号（不能直接填写图片 URL，请点击页面上的「选择图片 / 重新上传」按钮上传）`);
      return;
    }
  }
  err.value = "";
  msg.value = "";
  try {
    let r;
    if (current.value) {
      r = await api("PUT", "/admin/applyments/" + current.value.applyment.applymentId, { payload });
    } else {
      r = await api("POST", "/admin/applyments", { mchNo: form.value.mchNo, ifCode: form.value.ifCode, payload });
    }
    clearLocalDraft();
    revokeAllUploads();
    editing.value = false;
    await open(r.applyment.applymentId);
    await load();
  } catch (e) {
    fail(e);
  }
}

onMounted(async () => {
  await load();
  restoreLocalDraft();
});
onUnmounted(revokeAllUploads);
</script>

<template>
  <div class="flex flex-col gap-3">
    <pf-alert v-if="err" :key="'top-' + err" tone="danger">{{ err }}</pf-alert>
    <pf-alert v-if="msg" :key="'msg-' + msg" tone="warn">{{ msg }}</pf-alert>

    <div class="pf-card flex flex-col gap-2">
      <h2 class="m-0 text-[16px] font-semibold">进件是什么、怎么走</h2>
      <p class="pf-muted m-0">
        进件 = 把商家的营业执照、法人、银行卡等资料报给微信，由微信审核后给它开一个「特约商户号」，之后才能替它收款。
        流程：① 操作员「新建进件」填资料并保存草稿 → ② 「提交审核」→ ③ 另一位审核员「通过并提交渠道」（系统这时才把资料发给微信）→
        ④ 微信审核（通常 1–2 个工作日）→ ⑤ 联系人用微信完成账户验证和签约 → ⑥ 完成，特约商户号自动写入该商户。
      </p>
      <p class="pf-muted m-0">
        开始前请确认：商户已在「商户」页建好并挂了服务商；服务商的微信渠道参数已在「应用·通道」页配齐（否则第 ③ 步提交微信会失败）。
        手边准备好：营业执照、法人身份证正反面、结算银行卡信息，线下门店还要门头照和店内照。
      </p>
    </div>

    <div class="flex gap-2 items-center">
      <select v-model="filter" class="pf-input" @change="load">
        <option value="">全部状态</option>
        <option v-for="(label, key) in applymentStates" :key="key" :value="key">{{ label }}</option>
      </select>
      <pf-button size="sm" variant="primary" @click="startNew">新建进件</pf-button>
      <span class="pf-muted">左边的下拉框按状态筛选下面的列表；点某一行的「查看」看详情和下一步该做什么。</span>
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
            <td colspan="8"><span class="pf-muted">暂无进件申请。点上面的「新建进件」开始。</span></td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 新建 / 修改资料 -->
    <div v-if="editing" class="pf-card flex flex-col gap-3">
      <div class="flex items-center gap-2 flex-wrap">
        <h2 class="m-0 text-[16px] font-semibold">{{ current ? "修改资料 " + current.applyment.applymentId : "新建进件" }}</h2>
        <pf-button v-if="!current" size="sm" variant="ghost" @click="resetNewDraft">清空重填</pf-button>
      </div>
      <p class="pf-muted m-0">
        带 <span class="text-danger">*</span> 的必填。这里填的内容会原样报给微信审核，请照着证件原件一字一字填，不要凭印象写。
        点最下面的「保存草稿」时系统会先检查一遍，缺什么、哪里格式不对会用红色提示逐条列出来。
      </p>
      <div v-if="!current" class="grid grid-cols-2 gap-3">
        <Field label="商户" required hint="给哪个商户进件。列表来自「商户」页；只有挂了服务商的商户才能进件，没挂的保存时会提示「商户未挂服务商」，先去「商户」页编辑它。保存后不能换商户。">
          <select :value="form.mchNo" class="pf-input" @change="form.mchNo = $event.target.value; saveLocalDraft()">
            <option v-for="m in merchants" :key="m.mchNo" :value="m.mchNo">{{ m.mchName }}（{{ m.mchNo }}）{{ m.isvNo ? "" : " · 未挂服务商" }}</option>
          </select>
        </Field>
        <Field label="渠道" required hint="把资料报给哪个支付渠道。目前本页表单只支持「微信普通服务商」（钱由微信直接结算给商户）。">
          <select :value="form.ifCode" class="pf-input" @change="form.ifCode = $event.target.value; saveLocalDraft()">
            <option value="wxpay_partner">微信普通服务商</option>
          </select>
        </Field>
      </div>

      <h3 class="m-0 text-[14px] font-semibold">一、主体（营业执照上的信息）</h3>
      <div class="grid grid-cols-2 gap-3">
        <Field label="主体类型" required>
          <select class="pf-input" :value="fields.subject.type" @change="onSubjectType">
            <option value="enterprise">企业</option>
            <option value="individual">个体户</option>
            <option value="institution">事业单位</option>
          </select>
          <template #hint>
            看营业执照上的「类型」一栏：写着有限责任公司、股份有限公司、合伙企业等的选<b>企业</b>；写着「个体工商户」的选<b>个体户</b>；
            持有《事业单位法人证书》的选<b>事业单位</b>。没有营业执照的个人、政府机关、社会组织，本系统暂不支持进件。
          </template>
        </Field>
        <Field label="结算规则" required hint="决定微信向商户收多少手续费、几天到账，按商户实际卖什么来选：开店卖东西、餐饮、生活服务选「餐饮/零售/生活服务」（0.6%，次日到账）；做软件、广告、网络服务的选「互联网服务」（1%）。括号里的数字是微信的结算规则编号。教育、医疗、金融等需要特殊资质的行业不在列表里，不能在这里进件。">
          <select class="pf-input" :value="fields.subject.mcc" @change="onInput('subject.mcc', $event); onRule()">
            <option v-for="[id, label] in ruleOptions" :key="id" :value="id">{{ label }}（{{ id }}）</option>
          </select>
        </Field>
        <Field label="所属行业" required hint="上面结算规则下面的细分行业，选和营业执照「经营范围」最接近的一个。选得和实际经营不符（比如卖服装却选餐饮）微信会驳回。">
          <select class="pf-input" :value="fields.extra.wx_qualification_type" @change="onInput('extra.wx_qualification_type', $event)">
            <option v-for="name in industryOptions" :key="name" :value="name">{{ name }}</option>
          </select>
        </Field>
        <Field label="商户名称" required>
          <input class="pf-input" :value="fields.subject.name" placeholder="例如 成都某某商贸有限公司" @input="onInput('subject.name', $event)">
          <template #hint>
            必须和营业执照上的「名称」<b>一字不差</b>，包括括号是中文还是英文。个体户执照上名称是空的或写着「无」「无字号」的，填「个体户」加经营者姓名，例如「个体户张三」。
            用对公账户结算时，下面的「户名」也必须和这里完全相同。
          </template>
        </Field>
        <Field label="简称" required hint="顾客付款成功后在微信支付凭证上看到的商家名字，例如「某某咖啡」。要和实际经营的店名 / 品牌相符，不要填公司全称，也不要带「有限公司」。线下门店场景下它同时作为门店名称，要和门头照上的招牌对得上。">
          <input class="pf-input" :value="fields.subject.short_name" placeholder="例如 某某咖啡" @input="onInput('subject.short_name', $event)">
        </Field>
        <Field :label="fields.subject.type === 'institution' ? '事业单位法人证书编号' : '执照号'" required :hint="fields.subject.type === 'institution' ? '事业单位法人证书上的统一社会信用代码，18 位，照着证书填。' : '营业执照上的「统一社会信用代码」，18 位数字和大写字母，照着执照填；很老的执照没有统一代码的，填 15 位注册号。'">
          <input class="pf-input" :value="fields.subject.license_no" placeholder="例如 91510100MA6XXXXX0X" @input="onInput('subject.license_no', $event)">
        </Field>
        <Field :label="fields.subject.type === 'institution' ? '证书有效期' : '执照有效期'" required>
          <input class="pf-input" :value="fields.subject.license_valid" placeholder="2020-01-01,长期" @input="onInput('subject.license_valid', $event)">
          <template #hint>
            格式固定为「开始日期,结束日期」，中间用<b>英文逗号</b>，日期写成 2020-01-01 这种 10 位形式。
            开始日期填执照上的成立日期（或营业期限的起始日）；结束日期照执照的营业期限填，写着「长期」或没写截止日的填「长期」两个字。
            例如：2018-05-20,2038-05-19 或 2018-05-20,长期。
          </template>
        </Field>
      </div>

      <h3 class="m-0 text-[14px] font-semibold">二、法人 / 经营者（{{ personWord }}的证件信息）</h3>
      <div class="grid grid-cols-2 gap-3">
        <Field label="姓名" required :hint="`营业执照上「${personWord}」一栏的姓名，必须和他本人证件上的姓名完全一致。${fields.subject.type === 'individual' ? '个体户用对私银行卡结算时，下面的「户名」也必须是这个名字。' : ''}`">
          <input class="pf-input" :value="fields.legal_person.name" placeholder="例如 张三" @input="onInput('legal_person.name', $event)">
        </Field>
        <Field label="证件类型" required :hint="`${personWord}用的是哪种证件。中国大陆居民选身份证；港澳台居民、外籍人士按实际持有的证件选。下面的证件号、有效期和证件照片都要用同一个证件。`">
          <select class="pf-input" :value="fields.legal_person.id_type" @change="onInput('legal_person.id_type', $event)">
            <option value="id_card">身份证</option>
            <option value="passport">护照</option>
            <option value="hk_macao">港澳通行证</option>
            <option value="taiwan">台胞证</option>
            <option value="foreign_residence">外国人居留证</option>
          </select>
        </Field>
        <Field label="证件号" required hint="证件上的号码，照着原件填；身份证是 18 位，末位是 X 的写大写 X。保存后只显示前 3 后 4 位，中间打码；所以每次点「修改资料」都要把完整号码重新填一遍。发给微信时是加密传输的。">
          <input class="pf-input" :value="fields.legal_person.id_no" placeholder="例如 51010019900101123X" autocomplete="off" @input="onInput('legal_person.id_no', $event)">
        </Field>
        <Field label="证件有效期" required>
          <input class="pf-input" :value="fields.legal_person.id_valid" placeholder="2020-01-01,长期" @input="onInput('legal_person.id_valid', $event)">
          <template #hint>
            身份证看<b>国徽那一面</b>下方的「有效期限」，格式和执照有效期一样：证件上写 2015.06.01-2035.06.01 的填 2015-06-01,2035-06-01；写「长期」的填 2015-06-01,长期。
            证件快到期（剩余不足两个月）的微信一般不予通过，请先换证。
          </template>
        </Field>
        <Field label="手机" :hint="`选填。${personWord}的手机号，只在本系统留存、方便联系，不会报给微信。填的话要写 11 位手机号，或带区号的座机如 028-12345678。`">
          <input class="pf-input" :value="fields.legal_person.phone" placeholder="例如 13800000000" @input="onInput('legal_person.phone', $event)">
        </Field>
        <Field v-if="fields.subject.type === 'enterprise'" label="证件居住地址" required hint="企业主体必填。照着法人身份证人像面的「住址」一栏原样填写，例如「四川省成都市武侯区某某路 1 号 2 栋 3 单元 401 号」。用护照等其他证件的，填证件上登记的居住地址。">
          <input class="pf-input" :value="fields.extra.wx_id_card_address" placeholder="按证件上的住址填写" @input="onInput('extra.wx_id_card_address', $event)">
        </Field>
      </div>

      <h3 class="m-0 text-[14px] font-semibold">三、联系人（微信称为「超级管理员」）</h3>
      <p class="pf-muted m-0">
        联系人是商家那边负责这个微信支付账户的人：微信会把审核结果、签约通知发给他，并要求他用自己的微信完成账户验证和签约；开户后他也是商户平台的管理员。
        请填商家的人，不要填你们自己的员工。
      </p>
      <div class="grid grid-cols-3 gap-3">
        <Field label="身份" required>
          <select class="pf-input" :value="fields.extra.wx_contact_type" @change="onInput('extra.wx_contact_type', $event)">
            <option value="LEGAL">法人 / 经营者本人</option>
            <option value="SUPER">经办人</option>
          </select>
          <template #hint>
            <b>推荐选「法人 / 经营者本人」</b>：不用额外材料，此时下面的姓名要和上面法人姓名一致。
            选「经办人」（法人以外的员工）要另外提供他本人的身份证号、有效期和正反面照片；微信通常还要求一份加盖公章的《业务办理授权函》，本系统目前还不能上传这份文件，选经办人有可能被微信驳回。
          </template>
        </Field>
        <Field label="姓名" required :hint="fields.extra.wx_contact_type === 'LEGAL' ? '联系人的真实姓名。身份选的是法人本人，所以这里要和上面「法人 / 经营者」的姓名一模一样。' : '经办人的真实姓名，要和他身份证上的一致。'">
          <input class="pf-input" :value="fields.contact.name" placeholder="例如 张三" @input="onInput('contact.name', $event)">
        </Field>
        <Field label="手机" required hint="联系人本人正在使用的 11 位手机号，要能收短信：微信的审核结果、签约提醒都发到这个号码，后续登录商户平台也用它验证。">
          <input class="pf-input" :value="fields.contact.phone" placeholder="例如 13800000000" @input="onInput('contact.phone', $event)">
        </Field>
        <Field label="邮箱" required hint="必填，填联系人常用且能正常收信的邮箱。开户成功后，微信会把商户平台的登录账号和初始密码发到这个邮箱，填错就收不到。">
          <input class="pf-input" :value="fields.contact.email" placeholder="例如 zhangsan@example.com" @input="onInput('contact.email', $event)">
        </Field>
        <template v-if="fields.extra.wx_contact_type === 'SUPER'">
          <Field label="经办人身份证号" required hint="经办人本人的 18 位身份证号（不是法人的）。保存后同样只显示掩码，修改资料时要重新填。">
            <input class="pf-input" :value="fields.extra.wx_contact_id_no || ''" autocomplete="off" @input="onInput('extra.wx_contact_id_no', $event)">
          </Field>
          <Field label="经办人证件有效期" required hint="经办人身份证国徽面的有效期限，格式同上：2015-06-01,2035-06-01 或 2015-06-01,长期（英文逗号）。">
            <input class="pf-input" :value="fields.extra.wx_contact_id_valid || ''" placeholder="2020-01-01,长期" @input="onInput('extra.wx_contact_id_valid', $event)">
          </Field>
        </template>
      </div>
      <p v-if="fields.extra.wx_contact_type === 'SUPER'" class="pf-muted m-0">经办人的身份证正反面照片在最下面「证照」处上传。</p>

      <h3 class="m-0 text-[14px] font-semibold">四、经营信息（在哪里、怎么收款）</h3>
      <div class="grid grid-cols-3 gap-3">
        <Field label="客服电话" required hint="顾客在微信账单里能看到并拨打的客服电话，必须真实、能打通。格式：11 位手机号；或带区号的座机 028-12345678；400 电话要加横线写成 400-1234567。">
          <input class="pf-input" :value="fields.business.service_phone" placeholder="例如 028-12345678" @input="onInput('business.service_phone', $event)">
        </Field>
        <Field label="省 / 直辖市" required hint="实际经营地址（门店或办公地）所在的省份。选了以后会自动带出右边的「微信地区编码」（省级）。">
          <select class="pf-input" :value="regionValue" @change="onRegion">
            <option value="">请选择</option>
            <option v-for="[code, name] in regions" :key="code" :value="code">{{ name }}</option>
          </select>
        </Field>
        <Field label="市" required hint="经营地址所在的城市，带上「市」字，例如「成都市」。北京、上海、天津、重庆四个直辖市，这里直接填区名（例如「浦东新区」），下面的「区」留空。">
          <input class="pf-input" :value="fields.business.address.city" placeholder="例如 成都市" @input="onInput('business.address.city', $event)">
        </Field>
        <Field label="区" hint="选填。区或县的名称，例如「武侯区」。">
          <input class="pf-input" :value="fields.business.address.district" placeholder="例如 武侯区" @input="onInput('business.address.district', $event)">
        </Field>
        <Field label="详细地址" required hint="街道、门牌号、楼层房号，不要再重复写省市区，例如「天府大道北段 1700 号 1 栋 101 号」。系统会把省、市、区和这里拼成完整地址报给微信；线下门店要和门头照拍的实际位置一致。">
          <input class="pf-input" :value="fields.business.address.detail" placeholder="例如 天府大道北段 1700 号 1 栋 101 号" @input="onInput('business.address.detail', $event)">
        </Field>
        <Field label="微信地区编码" :required="isOffline">
          <input class="pf-input" :value="fields.business.address.code" placeholder="例如 510107" @input="onInput('business.address.code', $event)">
          <template #hint>
            勾了「线下门店」时必填。6 位数字的行政区划代码（和身份证号前 6 位是同一套编码）。选省后自动填省级编码（如四川 510000），
            <b>建议改成门店所在市或区县的编码</b>（如成都市 510100、成都市武侯区 510107），只填到省可能被微信退回。查不到可以搜索「行政区划代码 + 区县名」。
          </template>
        </Field>
      </div>
      <div class="flex flex-col gap-1 text-[13px]">
        <span>经营场景<span class="text-danger" title="必填"> *</span></span>
        <div class="flex flex-wrap gap-3">
          <label v-for="[key, label] in scenes" :key="key" class="flex gap-1 items-center">
            <input type="checkbox" :checked="fields.business.scene.types.includes(key)" @change="toggleScene(key, $event.target.checked)">
            {{ label }}
          </label>
        </div>
        <span class="pf-muted">
          商户打算在哪里收款，至少勾一个，可以多选；勾了哪个就要补哪个的材料，材料不够的不要勾。
          线下门店：要门头照、店内照和地区编码（实体店最常见，一般只勾这个就够）；小程序 / 公众号 / APP：要填对应的 AppID，公众号和 APP 还要截图；网站：要填已备案的域名。
        </span>
      </div>
      <Field v-if="needsAppID" label="小程序 / 公众号 / APP 的 AppID" required hint="商户用来收款的那个小程序、公众号或 APP 的 AppID，wx 开头共 18 位，在微信公众平台 / 开放平台的「开发设置」里能看到。这个 AppID 的认证主体要和进件主体是同一家。同时勾了多个线上场景时，它们共用这一个 AppID。">
        <input class="pf-input" :value="fields.business.scene.appid" placeholder="例如 wx1234567890abcdef" @input="onInput('business.scene.appid', $event)">
      </Field>
      <Field v-if="needsWeb" label="网站域名" required hint="勾了「网站」时必填。商户收款网站的域名，不带 http:// 和路径，例如 shop.example.com。网站要能正常打开，并且已经做了 ICP 备案，备案主体应是这家商户。">
        <input class="pf-input" :value="fields.extra.web_domain" placeholder="例如 shop.example.com" @input="onInput('extra.web_domain', $event)">
      </Field>

      <h3 class="m-0 text-[14px] font-semibold">五、结算账户（微信把钱打到哪张卡）</h3>
      <div class="grid grid-cols-2 gap-3">
        <Field label="账户类型" required>
          <select class="pf-input" :value="fields.settlement_account.type" @change="onInput('settlement_account.type', $event)">
            <option value="corporate">对公</option>
            <option value="personal" :disabled="fields.subject.type !== 'individual'">对私（仅个体户）</option>
          </select>
          <template #hint>
            <b>对公</b> = 以公司 / 店铺名义开的银行账户（基本户、一般户）；<b>对私</b> = 经营者本人的个人银行卡（储蓄卡，不能是信用卡）。
            企业和事业单位只能选对公；个体户两种都可以，没有对公户的选对私。
          </template>
        </Field>
        <Field label="开户银行" required hint="这张卡是哪家银行的。列表里只有 17 家全国性银行；城市商业银行、农商行、农村信用社等不在列表里的，选最后的「其他银行」，并在右边填开户行全称。">
          <select class="pf-input" :value="fields.settlement_account.bank" @change="onInput('settlement_account.bank', $event)">
            <option value="">请选择</option>
            <option v-for="bank in banks" :key="bank" :value="bank">{{ bank }}</option>
          </select>
        </Field>
        <Field label="开户行全称（含支行）" :required="fields.settlement_account.bank === '其他银行'" hint="开户银行选了「其他银行」时必填，其余情况可以留空。填银行给的完整支行名称，例如「成都农村商业银行股份有限公司高新支行」，可以在开户许可证、网银或银行 APP 的账户详情里查到，不要自己简写。">
          <input class="pf-input" :value="fields.settlement_account.branch" placeholder="例如 成都农村商业银行股份有限公司高新支行" @input="onInput('settlement_account.branch', $event)">
        </Field>
        <Field label="户名" required :hint="fields.settlement_account.type === 'corporate' ? '对公账户的户名。必须和上面「商户名称」一字不差（系统保存时会核对，不一致会直接报错）。' : '对私银行卡的持卡人姓名。必须和上面「法人 / 经营者」的姓名一字不差（系统保存时会核对，不一致会直接报错）。'">
          <input class="pf-input" :value="fields.settlement_account.account_name" @input="onInput('settlement_account.account_name', $event)">
        </Field>
        <Field label="账号" required hint="对公账号或银行卡号，只填数字，不要带空格和横线。保存后只显示前 3 后 4 位，所以每次「修改资料」都要重新填完整账号。填错会导致微信打款验证失败或结算不到账，请对着开户许可证 / 银行卡核对。">
          <input class="pf-input" :value="fields.settlement_account.account_no" autocomplete="off" @input="onInput('settlement_account.account_no', $event)">
        </Field>
        <Field label="开户行省市编码" hint="开户支行所在城市的 6 位行政区划代码（如成都市 510100）。选省时会自动带出省级编码；留空则用上面经营地址的地区编码。开户行和门店不在同一个城市时，请改成开户行所在城市的编码。">
          <input class="pf-input" :value="fields.settlement_account.address_code" placeholder="例如 510100" @input="onInput('settlement_account.address_code', $event)">
        </Field>
      </div>

      <h3 class="m-0 text-[14px] font-semibold">六、费率（仅本系统留档）</h3>
      <p class="pf-muted m-0">
        微信进件不接受自定义费率：商户实际的手续费和到账周期由上面选的「结算规则」决定。这一组只是在申请单上留个记录，不会发给微信，保持默认即可。
        真正用于订单记账的费率，在进件完成后到「应用·通道 → 配通道」里填。
      </p>
      <div class="grid grid-cols-3 gap-3">
        <Field label="支付方式" required hint="留档用。JSAPI / 小程序 = 在微信里付款；Native = 顾客扫二维码付款。">
          <select class="pf-input" :value="fields.rates[0].way_code" @change="fields.rates[0].way_code = $event.target.value; writeJson()">
            <option v-for="[code, label] in ways" :key="code" :value="code">{{ label }}</option>
          </select>
        </Field>
        <Field label="费率（万分位）" required :hint="`单位是万分之一：填 60 = 0.60%，填 100 = 1%。当前填的是 ${feePct}%。范围 0–1000，建议和上面结算规则里的费率写成一样。`">
          <input class="pf-input" type="number" min="0" max="1000" :value="fields.rates[0].fee_rate_bps" @input="fields.rates[0].fee_rate_bps = Number($event.target.value); writeJson()">
        </Field>
        <Field label="结算周期" required hint="留档用。T1 = 下一个工作日到账（微信默认）；D1 = 下一个自然日到账；D0 = 当天到账。">
          <select class="pf-input" :value="fields.rates[0].settle_cycle" @change="fields.rates[0].settle_cycle = $event.target.value; writeJson()">
            <option value="T1">T1</option>
            <option value="D1">D1</option>
            <option value="D0">D0</option>
          </select>
        </Field>
      </div>

      <h3 class="m-0 text-[14px] font-semibold">七、证照（照片）</h3>
      <p class="pf-muted m-0">
        只接受 JPG / PNG，每张不超过 2MB（超过的先压缩再传）。要用彩色原件拍照或扫描，四角完整、文字清晰、没有反光和遮挡，不要用复印件和截图（截图类材料除外）。
        点「选择图片」后立即上传，上传成功后卡片会保留缩略图、文件名、文件编号和存储地址；传错了点「重新上传」即可覆盖。需要哪些照片会随上面的主体类型、联系人身份、经营场景自动增减。
      </p>
      <div class="grid grid-cols-2 gap-3">
        <Field v-for="[kind, label, must] in imageKinds" :key="kind" tag="div" :required="must" :hint="imageHints[kind] || ''">
          <template #label>
            {{ label }}
            <span v-if="uploading === kind" class="pf-dot" data-tone="info">上传中…</span>
            <span v-else-if="fields.images[kind] && isNumericId(fields.images[kind])" class="pf-dot" data-tone="ok">已上传 #{{ fields.images[kind] }}</span>
            <span v-else-if="fields.images[kind]" class="pf-dot" data-tone="danger">格式错误</span>
            <span v-else class="pf-muted">未上传</span>
          </template>
          <input :ref="(el) => setFileInput(kind, el)" type="file" accept="image/jpeg,image/png" hidden @change="upload(kind, $event)" />
          <div class="flex flex-col gap-2 p-2 rounded border border-border bg-surface-2">
            <div v-if="uploading === kind" class="pf-muted">正在上传 {{ uploadProgressName }}…</div>
            <div v-if="fields.images[kind] && isNumericId(fields.images[kind])" class="flex items-start gap-3">
              <a v-if="previewSrc(kind)" :href="previewSrc(kind)" target="_blank" rel="noopener" class="shrink-0" title="点击在新标签页查看原图">
                <img :src="previewSrc(kind)" :alt="label" class="w-20 h-20 object-cover rounded border border-border bg-surface" @error="onThumbError(kind)" />
              </a>
              <div class="flex flex-col gap-1 min-w-0 flex-1">
                <div class="font-medium break-all">{{ uploads[kind]?.filename || ("文件 #" + fields.images[kind]) }}</div>
                <div class="pf-muted">
                  文件编号：<span class="pf-code">#{{ fields.images[kind] }}</span>
                  <template v-if="uploads[kind]?.size"> · {{ formatBytes(uploads[kind].size) }}</template>
                </div>
                <div v-if="uploads[kind]?.url" class="pf-muted break-all">
                  存储地址：<span class="pf-code break-all">{{ uploads[kind].url }}</span>
                </div>
                <div v-else-if="previewSrc(kind)" class="pf-muted break-all">
                  预览地址：<a class="pf-link break-all" :href="previewSrc(kind)" target="_blank" rel="noopener">{{ previewSrc(kind) }}</a>
                </div>
              </div>
            </div>
            <div v-else-if="fields.images[kind]" class="text-danger break-all">
              当前填写的「{{ fields.images[kind] }}」不是上传返回的数字文件编号。请不要在 JSON 中直接粘贴图片链接，点击下方「重新上传」即可自动记录。
            </div>
            <div v-else-if="uploading !== kind" class="pf-muted">尚未上传图片</div>
            <div v-if="uploads[kind]?.error" class="text-danger">{{ uploads[kind].error }}</div>
            <div class="flex flex-wrap items-center gap-2">
              <pf-button v-if="fields.images[kind]" key="reupload" size="sm" :loading="uploading === kind" :disabled="!!uploading" @click="pickFile(kind)">重新上传</pf-button>
              <pf-button v-else key="upload" size="sm" :loading="uploading === kind" :disabled="!!uploading" @click="pickFile(kind)">选择图片</pf-button>
              <pf-button v-if="fields.images[kind] && !uploading" key="clear" size="sm" variant="ghost" @click="clearImage(kind)">移除</pf-button>
              <a v-if="fields.images[kind] && isNumericId(fields.images[kind]) && previewSrc(kind)" class="pf-link pf-muted" :href="previewSrc(kind)" target="_blank" rel="noopener">查看原图</a>
            </div>
          </div>
        </Field>
      </div>

      <div class="flex gap-2 items-center">
        <pf-button :key="String(showJson)" size="sm" variant="ghost" @click="showJson = !showJson">{{ showJson ? "收起 JSON" : "查看 / 编辑 JSON" }}</pf-button>
        <span v-if="jsonError" class="text-danger">{{ jsonError }}</span>
      </div>
      <textarea v-if="showJson" v-model="form.payloadText" class="pf-input font-mono text-[12px]" rows="18" spellcheck="false" @input="readJson"></textarea>
      <p class="pf-muted m-0">
        「查看 / 编辑 JSON」是给技术人员批量粘贴资料用的，和上面的表单双向同步，普通录入不用管它（JSON 里的 images 各字段填的是上传生成的数字编号，不要粘贴图片 URL）。
        「保存草稿」只是把资料存到本系统，<b>不会</b>发给微信；保存成功后在详情里点「提交审核」，审核员通过后才会提交给微信。
      </p>
      <pf-alert v-if="err" :key="'btm-' + err" tone="danger">{{ err }}</pf-alert>
      <div class="flex gap-2">
        <pf-button variant="primary" size="sm" :disabled="!!uploading" @click="save">保存草稿</pf-button>
        <pf-button size="sm" @click="cancelEdit">取消</pf-button>
      </div>
    </div>

    <!-- 详情 -->
    <div v-else-if="current" class="pf-card flex flex-col gap-3">
      <div class="flex items-center gap-2">
        <h2 class="m-0 text-[16px] font-semibold">{{ current.applyment.applymentId }}</h2>
        <span class="pf-dot" :data-tone="stateTone(current.applyment.state)">{{ applymentStates[current.applyment.state] }}</span>
        <span class="pf-muted">{{ current.applyment.ifCode }} · {{ current.applyment.mchNo }}</span>
      </div>
      <pf-alert v-if="stateGuide[current.applyment.state]" tone="info">下一步：{{ stateGuide[current.applyment.state] }}</pf-alert>
      <pf-alert v-if="current.applyment.rejectReason" tone="danger">驳回原因：{{ current.applyment.rejectReason }}</pf-alert>
      <pf-alert v-if="current.applyment.channelMsg" tone="warn">渠道：{{ current.applyment.channelState }} {{ current.applyment.channelMsg }}</pf-alert>
      <p v-if="current.applyment.signUrl" class="m-0">签约链接：<a :href="current.applyment.signUrl" target="_blank" rel="noopener">{{ current.applyment.signUrl }}</a>
        <span class="pf-muted">　把它发给联系人（超级管理员），用他本人的微信打开。</span></p>
      <p v-if="current.applyment.subMchid" class="m-0">子商户号：<span class="pf-code">{{ current.applyment.subMchid }}</span>
        <span class="pf-muted">　微信分配的特约商户号，已自动写入该商户的渠道参数。</span></p>
      <p v-if="current.applyment.unexpressed" class="m-0 pf-muted">渠道未表达字段：{{ current.applyment.unexpressed }}（这些内容渠道不接收，只在本系统留档；rates 出现在这里是正常的）</p>

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
        <thead><tr><th>预览</th><th>证照项</th><th>文件</th><th>大小</th><th>渠道 media_id</th></tr></thead>
        <tbody>
          <tr v-for="m in current.media" :key="m.kind">
            <td>
              <a :href="mediaUrl(current.applyment.applymentId, m.kind)" target="_blank" rel="noopener" title="查看原图">
                <img :src="mediaUrl(current.applyment.applymentId, m.kind)" :alt="m.kind" class="w-12 h-12 object-cover rounded border border-border bg-surface" />
              </a>
            </td>
            <td>{{ imageNames[m.kind] || m.kind }} <span class="pf-muted">({{ m.kind }})</span></td>
            <td>
              <a class="pf-link" :href="mediaUrl(current.applyment.applymentId, m.kind)" target="_blank" rel="noopener">{{ m.filename || ("#" + m.fileId) }}</a>
              <span class="pf-muted"> · #{{ m.fileId }}</span>
            </td>
            <td>{{ formatBytes(m.size) }}</td>
            <td><span class="pf-code">{{ m.mediaId || "—" }}</span></td>
          </tr>
          <tr v-if="!current.media?.length">
            <td colspan="5"><span class="pf-muted">暂无已保存的证照图片。</span></td>
          </tr>
        </tbody>
      </table>
      <p class="pf-muted m-0">「渠道 media_id」在提交微信时才会生成，草稿和审核阶段显示为 — 是正常的。下面是这张申请单的完整资料（证件号、银行账号已打码）。</p>
      <pre class="pf-log">{{ JSON.stringify(current.applyment.payload, null, 2) }}</pre>
    </div>
  </div>
</template>
