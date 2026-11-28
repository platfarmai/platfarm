<script setup>
import { ref, onMounted } from "vue";
import { portal, viewAs, copyText } from "../api.js";
import { when } from "../utils.js";
import Field from "../components/Field.vue";

const err = ref("");
const me = ref(null);
const apps = ref([]);
const loading = ref(true);
const newSecret = ref(null); // { appId, appSecret }：明文只在换发的这一次响应里
const pwd = ref({ old: "", next: "", again: "" });
const pwdErr = ref("");
const saving = ref(false);

const channelShort = { wxpay_partner: "微信特约商户号", wxpay_ecommerce: "微信收付通二级商户号", alipay_zft: "支付宝直付通 smid" };
const wayNames = {
  WX_JSAPI: "微信 JSAPI",
  WX_LITE: "微信小程序",
  WX_NATIVE: "微信扫码",
  WX_H5: "微信 H5",
  ALI_JSAPI: "支付宝生活号",
  ALI_QR: "支付宝扫码",
  ALI_WAP: "支付宝手机网页",
  ALI_PC: "支付宝电脑网页",
};
const scopeNames = {
  "pay.order.create": "下单、关单",
  "pay.order.query": "查询订单",
  "pay.refund.create": "发起退款",
  "pay.refund.query": "查询退款",
  "pay.bill.query": "查交易流水与日账单",
};

async function load() {
  err.value = "";
  loading.value = true;
  try {
    const [m, a] = await Promise.all([portal("GET", "/me"), portal("GET", "/apps")]);
    me.value = m;
    apps.value = a.items || [];
  } catch (e) {
    err.value = e.message || String(e);
  } finally {
    loading.value = false;
  }
}

async function rotate(a) {
  const sure = await pfConfirm(
    `给应用「${a.appName}」换一把新的签名密钥？旧密钥会立即失效：业务系统在换上新密钥之前，下单、退款请求都会被拒绝（签名不匹配），收到的支付结果通知也验不过。`,
    { danger: true },
  );
  if (!sure) return;
  err.value = "";
  try {
    newSecret.value = await portal("POST", "/apps/" + encodeURIComponent(a.appId) + "/secret");
  } catch (e) {
    err.value = e.message || String(e);
  }
}

async function changePassword() {
  pwdErr.value = "";
  const p = pwd.value;
  if (!p.old || !p.next) return (pwdErr.value = "请填写当前密码和新密码");
  if (p.next.length < 8) return (pwdErr.value = "新密码至少 8 位");
  if (p.next !== p.again) return (pwdErr.value = "两次输入的新密码不一致");
  saving.value = true;
  try {
    const r = await fetch("/auth/change-password", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ oldPassword: p.old, newPassword: p.next }),
    });
    if (!r.ok) {
      const data = await r.json().catch(() => ({}));
      throw new Error(data.error === "old password incorrect" ? "当前密码不正确" : data.error || `HTTP ${r.status}`);
    }
    pfToast("密码已修改，请用新密码重新登录", "ok");
    setTimeout(() => (window.location.href = "/"), 1200);
  } catch (e) {
    pwdErr.value = e.message || String(e);
  } finally {
    saving.value = false;
  }
}

onMounted(load);
</script>

<template>
  <div class="flex flex-col gap-3">
    <pf-alert v-if="err" tone="danger">{{ err }}</pf-alert>
    <pf-spinner v-if="loading"></pf-spinner>

    <template v-if="me">
      <div v-if="newSecret" :key="newSecret.appSecret" class="pf-card flex flex-col gap-2">
        <h2 class="m-0 text-[16px] font-semibold">新的签名密钥</h2>
        <pf-secret label="X-Pay-App-Id">{{ newSecret.appId }}</pf-secret>
        <pf-secret label="签名密钥（只显示这一次，请立即复制）">{{ newSecret.appSecret }}</pf-secret>
        <p class="pf-muted m-0">
          旧密钥已经失效。把新密钥配置到业务系统里：它既用来给请求算签名，也用来验证平台发给你的支付 / 退款结果通知。
          关掉这个框之后就再也查不到了（平台只保存加密后的值）；没存下来只能再换发一次。
        </p>
        <div><pf-button size="sm" @click="newSecret = null">我已保存</pf-button></div>
      </div>

      <div class="pf-card flex flex-col gap-3">
        <h2 class="m-0 text-[16px] font-semibold">{{ me.merchant.mchName }}</h2>
        <dl class="pf-kv">
          <dt>商户号</dt>
          <dd><span class="pf-code">{{ me.merchant.mchNo }}</span></dd>
          <dt>状态</dt>
          <dd><span class="pf-dot" :data-tone="me.merchant.state === 'active' ? 'ok' : 'danger'">{{ me.merchant.state === "active" ? "正常" : "已停用（下单、退款会被拒绝，请联系服务商）" }}</span></dd>
          <dt>渠道子商户号</dt>
          <dd>
            <div class="flex flex-col gap-1">
              <span v-for="s in me.subAccounts" :key="s.ifCode"><span class="pf-code">{{ s.subMchid }}</span><span class="pf-muted">　{{ channelShort[s.ifCode] || s.ifCode }}</span></span>
              <span v-if="!me.subAccounts.length" class="pf-muted">还没有开通。服务商为你完成进件、渠道签约后这里会出现微信 / 支付宝分配的商户号，在那之前不能用真实渠道收款。</span>
            </div>
          </dd>
          <dt>联系人</dt>
          <dd>{{ me.merchant.contactName || "—" }}<span v-if="me.merchant.contactPhone" class="pf-muted">　{{ me.merchant.contactPhone }}</span></dd>
          <dt>接口地址</dt>
          <dd><span class="pf-code">{{ me.apiBase }}</span></dd>
        </dl>
      </div>

      <div class="pf-card flex flex-col gap-3">
        <h2 class="m-0 text-[16px] font-semibold">接入凭据</h2>
        <p class="pf-muted m-0">
          业务系统调用支付接口要用两套凭据：<b>① 开放平台凭据</b>换访问令牌，网关据此判断你能调哪些接口；<b>② 应用签名密钥</b>给每个请求签名，并验证平台发给你的结果通知。
          两个密钥平台都不保存明文，所以这里只能看到编号，看不到密钥本身。写代码之前可以先到「接口调试」页把请求跑通。
        </p>

        <h3 class="m-0 text-[14px] font-semibold">① 开放平台凭据</h3>
        <dl class="pf-kv">
          <dt>appKey</dt>
          <dd>
            <div v-if="me.openAppKey" class="flex items-center gap-2 flex-wrap">
              <span class="pf-code">{{ me.openAppKey }}</span>
              <pf-button size="sm" variant="ghost" @click="copyText(me.openAppKey, 'appKey')">复制</pf-button>
            </div>
            <span v-else class="pf-muted">还没有签发，请联系服务商在「商户」页补发。</span>
          </dd>
          <dt>appSecret</dt>
          <dd><span class="pf-muted">开通商户登录时由服务商一次性交给你，之后任何人都查不到。遗失或泄露请联系服务商重置，旧的立即失效。</span></dd>
          <dt>可调用的接口</dt>
          <dd>
            <span v-if="!me.openAppKey" class="pf-muted">签发 appKey 后生效。</span>
            <div v-else class="flex gap-2 flex-wrap">
              <span v-for="s in me.scopes" :key="s" class="pf-code" :title="s">{{ scopeNames[s] || s }}</span>
            </div>
          </dd>
          <dt>怎么用</dt>
          <dd>
            <span class="pf-muted">
              用 appKey + appSecret 请求 <span class="pf-code">POST {{ me.apiBase }}/oauth/token</span> 换取 accessToken（1 小时有效，缓存复用），之后每个请求带
              <span class="pf-code">Authorization: Bearer &lt;accessToken&gt;</span>。
            </span>
          </dd>
        </dl>

        <h3 class="m-0 text-[14px] font-semibold">② 应用与签名密钥</h3>
        <div class="overflow-x-auto">
        <table class="pf-table whitespace-nowrap">
          <thead>
            <tr><th>应用</th><th>X-Pay-App-Id</th><th>已开通的支付方式</th><th>状态</th><th>创建时间</th><th></th></tr>
          </thead>
          <tbody>
            <tr v-for="a in apps" :key="a.appId">
              <td>{{ a.appName }}</td>
              <td>
                <div class="flex items-center gap-2 flex-wrap">
                  <span class="pf-code">{{ a.appId }}</span>
                  <pf-button size="sm" variant="ghost" @click="copyText(a.appId, 'App-Id')">复制</pf-button>
                </div>
              </td>
              <td>
                <div class="flex flex-col gap-1">
                  <span v-for="p in a.passages.filter((x) => x.state === 'active')" :key="p.wayCode">
                    <span class="pf-code">{{ p.wayCode }}</span>
                    <span class="pf-muted">　{{ wayNames[p.wayCode] || "" }} · 手续费 {{ (p.rateBps / 100).toFixed(2) }}%</span>
                    <pf-badge v-if="p.ifCode === 'sandbox'" tone="warn">沙箱</pf-badge>
                  </span>
                  <span v-if="!a.passages.some((x) => x.state === 'active')" class="pf-muted">还没有开通，请联系服务商配置支付通道</span>
                </div>
              </td>
              <td><span class="pf-dot" :data-tone="a.state === 'active' ? 'ok' : 'danger'">{{ a.state === "active" ? "启用" : "停用" }}</span></td>
              <td>{{ when(a.createdAt) }}</td>
              <td><pf-button size="sm" @click="rotate(a)">换发签名密钥</pf-button></td>
            </tr>
            <tr v-if="!apps.length">
              <td colspan="6"><span class="pf-muted">还没有应用，请联系服务商为你创建。</span></td>
            </tr>
          </tbody>
        </table>
        </div>
        <p class="pf-muted m-0">
          签名密钥只在创建或换发时显示一次。不记得了就点「换发签名密钥」拿一把新的——注意旧密钥会立即失效，请安排在业务低峰、并且能马上更新业务系统配置的时候做。
          「沙箱」通道不动真钱，用于联调；正式收款请确认支付方式走的是真实渠道。
        </p>
      </div>

      <div class="pf-card flex flex-col gap-2">
        <h2 class="m-0 text-[16px] font-semibold">接下来做什么</h2>
        <p class="pf-muted m-0">
          ① 到上方「接口调试」页选一个接口点「发送请求」：平台替你签好名并真实调用一次，签名串、请求头、curl 和响应都摆出来，对照着就能写出自己的代码。
          ② 按 <a class="pf-link" href="/docs" target="_blank" rel="noopener">《商户支付开放 API 接入指南》</a> 在业务系统里实现换 token、签名、下单与结果通知验签。
          ③ 上线后在「订单」页核对交易。
        </p>
      </div>

      <div v-if="!viewAs" class="pf-card flex flex-col gap-3">
        <h2 class="m-0 text-[16px] font-semibold">修改登录密码</h2>
        <p class="pf-muted m-0">
          登录账号 <span class="pf-code">{{ me.username }}</span>。第一次登录后请把服务商给的初始密码改掉；修改成功后需要用新密码重新登录。忘记密码请联系服务商重置。
        </p>
        <pf-alert v-if="pwdErr" tone="danger">{{ pwdErr }}</pf-alert>
        <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <Field label="当前密码" required>
            <input v-model="pwd.old" class="pf-input" type="password" autocomplete="current-password">
          </Field>
          <Field label="新密码" required hint="至少 8 位，不要和其它系统共用。">
            <input v-model="pwd.next" class="pf-input" type="password" autocomplete="new-password">
          </Field>
          <Field label="再输一次新密码" required>
            <input v-model="pwd.again" class="pf-input" type="password" autocomplete="new-password">
          </Field>
        </div>
        <div><pf-button size="sm" variant="primary" :loading="saving" @click="changePassword">修改密码</pf-button></div>
      </div>
    </template>
  </div>
</template>
