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
const uploading = ref("");

const imageKinds = [
  ["license", "营业执照"],
  ["id_front", "身份证人像面"],
  ["id_back", "身份证国徽面"],
  ["storefront", "门头照（线下场景）"],
  ["interior", "店内环境照"],
];

const payloadTemplate = {
  subject: { type: "enterprise", name: "", short_name: "", license_no: "", license_valid: "2020-01-01,长期", mcc: "" },
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
  extra: { wx_qualification_type: "" },
};

const reviewable = computed(() => current.value?.applyment?.state === "reviewing");
const pollable = computed(() => ["submitted", "channel_auditing", "need_sign", "need_verify"].includes(current.value?.applyment?.state));

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

function startNew() {
  form.value = { mchNo: merchants.value[0]?.mchNo || "", ifCode: "wxpay_partner", payloadText: JSON.stringify(payloadTemplate, null, 2) };
  current.value = null;
  editing.value = true;
}

function startEdit() {
  const a = current.value.applyment;
  // 脱敏字段需重新填写：后端只回传掩码。
  const p = JSON.parse(JSON.stringify(a.payload));
  p.legal_person.id_no = "";
  p.settlement_account.account_no = "";
  form.value = { mchNo: a.mchNo, ifCode: a.ifCode, payloadText: JSON.stringify(p, null, 2) };
  editing.value = true;
}

function parsePayload() {
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
    payload.images = payload.images || {};
    payload.images[kind] = String(r.fileId);
    form.value.payloadText = JSON.stringify(payload, null, 2);
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
      <div v-if="!current" class="flex gap-2">
        <select v-model="form.mchNo" class="pf-input">
          <option v-for="m in merchants" :key="m.mchNo" :value="m.mchNo">{{ m.mchName }}（{{ m.mchNo }}）</option>
        </select>
        <select v-model="form.ifCode" class="pf-input">
          <option value="wxpay_partner">微信普通服务商</option>
        </select>
      </div>
      <div class="flex flex-wrap gap-3">
        <label v-for="[kind, label] in imageKinds" :key="kind" class="flex flex-col gap-1 text-[13px]">
          <span>{{ label }} <span class="pf-muted">{{ uploading === kind ? "上传中…" : "" }}</span></span>
          <input type="file" accept="image/jpeg,image/png" :disabled="!!uploading" @change="upload(kind, $event)" />
        </label>
      </div>
      <textarea v-model="form.payloadText" class="pf-input font-mono text-[12px]" rows="24" spellcheck="false"></textarea>
      <p class="pf-muted m-0">证件号与银行账号保存后只以掩码回显；修改资料时需重新填写。微信不接受自定义费率，rates 仅作平台记录。</p>
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
