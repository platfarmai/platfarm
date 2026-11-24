<script setup>
import { ref, onMounted } from "vue";
import { api } from "../api.js";

const err = ref("");
const msg = ref("");
const merchants = ref([]);
const isvs = ref([]);
const showISV = ref(false);
const showMch = ref(false);
const isvForm = ref({ isvNo: "", isvName: "", fundMode: "direct", rebateRateBps: 0 });
const mchForm = ref({ mchNo: "", mchName: "", mchShortName: "", mchType: 2, isvNo: "", contactName: "", contactPhone: "" });

async function load() {
  err.value = "";
  try {
    const [m, i] = await Promise.all([api("GET", "/admin/merchants"), api("GET", "/admin/isv")]);
    merchants.value = m.items || [];
    isvs.value = i.items || [];
    if (!mchForm.value.isvNo && isvs.value.length) mchForm.value.isvNo = isvs.value[0].isvNo;
  } catch (e) {
    err.value = e.message || String(e);
  }
}

async function createISV() {
  err.value = "";
  msg.value = "";
  try {
    const r = await api("POST", "/admin/isv", {
      ...isvForm.value,
      rebateRateBps: Number(isvForm.value.rebateRateBps) || 0,
    });
    msg.value = "服务商已保存：" + r.isvNo;
    isvForm.value = { isvNo: "", isvName: "", fundMode: "direct", rebateRateBps: 0 };
    showISV.value = false;
    await load();
  } catch (e) {
    err.value = e.message || String(e);
  }
}

async function createMerchant() {
  err.value = "";
  msg.value = "";
  if (!mchForm.value.isvNo) {
    err.value = "进件前必须先选择服务商";
    return;
  }
  try {
    const r = await api("POST", "/admin/merchants", { ...mchForm.value, mchType: Number(mchForm.value.mchType) });
    msg.value = "商户已创建：" + r.mchNo + "。到「进件审核」新建进件时可以选择它。";
    mchForm.value = { mchNo: "", mchName: "", mchShortName: "", mchType: 2, isvNo: mchForm.value.isvNo, contactName: "", contactPhone: "" };
    showMch.value = false;
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
    <pf-alert v-if="msg" tone="success">{{ msg }}</pf-alert>

    <div class="flex gap-2">
      <pf-button size="sm" variant="primary" @click="showMch = !showMch; showISV = false">新建商户</pf-button>
      <pf-button size="sm" @click="showISV = !showISV; showMch = false">新建服务商</pf-button>
    </div>

    <div v-if="showISV" class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">登记你的微信支付服务商</h2>
      <p class="pf-muted m-0">这里不是再建一个微信服务商。你自己就是服务商，填你在微信服务商平台的商户号和名称，后面的特约商户都挂在它下面。资金模式选直清；返佣没有就填 0。服务商号已存在时会更新。</p>
      <div class="grid grid-cols-2 gap-3">
        <label class="flex flex-col gap-1 text-[13px]">服务商号
          <input v-model="isvForm.isvNo" class="pf-input" placeholder="例如 ISV001">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">名称
          <input v-model="isvForm.isvName" class="pf-input">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">资金模式
          <select v-model="isvForm.fundMode" class="pf-input">
            <option value="direct">直清（渠道结算给子商户）</option>
            <option value="controlled">归集</option>
          </select>
        </label>
        <label class="flex flex-col gap-1 text-[13px]">返佣费率（万分位，0–1000）
          <input v-model="isvForm.rebateRateBps" class="pf-input" type="number" min="0" max="1000">
        </label>
      </div>
      <div class="flex gap-2">
        <pf-button variant="primary" size="sm" @click="createISV">保存服务商</pf-button>
        <pf-button size="sm" @click="showISV = false">取消</pf-button>
      </div>
    </div>

    <div v-if="showMch" class="pf-card flex flex-col gap-3">
      <h2 class="m-0 text-[16px] font-semibold">新建商户</h2>
      <p class="pf-muted m-0">商户号留空会自动生成。微信进件请选「特约」，并挂到已有服务商。</p>
      <div class="grid grid-cols-2 gap-3">
        <label class="flex flex-col gap-1 text-[13px]">商户名称
          <input v-model="mchForm.mchName" class="pf-input">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">简称
          <input v-model="mchForm.mchShortName" class="pf-input">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">商户号（可空）
          <input v-model="mchForm.mchNo" class="pf-input" placeholder="留空自动生成">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">类型
          <select v-model="mchForm.mchType" class="pf-input">
            <option :value="2">特约（挂服务商，用于进件）</option>
            <option :value="1">普通</option>
          </select>
        </label>
        <label class="flex flex-col gap-1 text-[13px]">服务商
          <select v-model="mchForm.isvNo" class="pf-input">
            <option value="">请选择</option>
            <option v-for="i in isvs" :key="i.isvNo" :value="i.isvNo">{{ i.isvName }}（{{ i.isvNo }}）</option>
          </select>
        </label>
        <label class="flex flex-col gap-1 text-[13px]">联系人
          <input v-model="mchForm.contactName" class="pf-input">
        </label>
        <label class="flex flex-col gap-1 text-[13px]">联系电话
          <input v-model="mchForm.contactPhone" class="pf-input">
        </label>
      </div>
      <div class="flex gap-2">
        <pf-button variant="primary" size="sm" @click="createMerchant">保存商户</pf-button>
        <pf-button size="sm" @click="showMch = false">取消</pf-button>
      </div>
    </div>

    <div class="pf-card">
      <h2 class="m-0 mb-2 text-[14px] font-semibold">服务商</h2>
      <table class="pf-table">
        <thead>
          <tr><th>服务商号</th><th>名称</th><th>资金模式</th><th>返佣</th><th>状态</th></tr>
        </thead>
        <tbody>
          <tr v-for="i in isvs" :key="i.isvNo">
            <td><span class="pf-code">{{ i.isvNo }}</span></td>
            <td>{{ i.isvName }}</td>
            <td>{{ i.fundMode === "controlled" ? "归集" : "直清" }}</td>
            <td>{{ i.rebateRateBps }}</td>
            <td>{{ i.state }}</td>
          </tr>
          <tr v-if="!isvs.length">
            <td colspan="5"><span class="pf-muted">还没有服务商，进件前需要先建一个</span></td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="pf-card">
      <h2 class="m-0 mb-2 text-[14px] font-semibold">商户</h2>
      <table class="pf-table">
        <thead>
          <tr><th>商户号</th><th>名称</th><th>类型</th><th>服务商</th><th>状态</th></tr>
        </thead>
        <tbody>
          <tr v-for="m in merchants" :key="m.mchNo">
            <td><span class="pf-code">{{ m.mchNo }}</span></td>
            <td>{{ m.mchName }}</td>
            <td>{{ m.mchType === 1 ? "普通" : "特约" }}</td>
            <td>{{ m.isvNo || "—" }}</td>
            <td>{{ m.state }}</td>
          </tr>
          <tr v-if="!merchants.length">
            <td colspan="5"><span class="pf-muted">暂无商户</span></td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
