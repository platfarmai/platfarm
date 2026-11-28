<script setup>
import { ref, computed, watch, onMounted } from "vue";
import { portal, copyText } from "../api.js";
import Field from "../components/Field.vue";

const err = ref("");
const me = ref(null);
const apps = ref([]);
const appId = ref("");
const op = ref("order.create");
const busy = ref(""); // 正在执行的动作：send | sign | flow
const runs = ref([]); // 最近的调试记录，新的在前
let seq = 0;

const today = () => new Date(Date.now() + 8 * 3600e3).toISOString().slice(0, 10); // 账单按北京时间自然日
const form = ref({
  wayCode: "",
  amountYuan: "0.01",
  subject: "接口调试",
  notifyUrl: "",
  channelExtra: "",
  mchOrderNo: "",
  state: "success",
  refundYuan: "0.01",
  mchRefundNo: "",
  reason: "",
  startDate: today(),
  endDate: "",
  type: "",
  page: 1,
  pageSize: 20,
});

const ops = [
  { key: "order.create", label: "下单", api: "POST /api/pay/v1/orders", desc: "创建一笔调试单，返回拉起支付要用的凭据（扫码支付是二维码链接）。" },
  { key: "order.query", label: "查询订单", api: "GET /api/pay/v1/orders/{mchOrderNo}", desc: "按商户订单号查状态。可以查任何一笔自己的订单，包括正式订单。" },
  { key: "order.mock", label: "模拟支付结果", api: "POST /api/pay/v1/orders/{mchOrderNo}/mock", sandbox: true, desc: "只对沙箱订单有效：不用真付款，直接把订单推进到已支付 / 失败，并触发结果通知。" },
  { key: "order.close", label: "关闭订单", api: "POST /api/pay/v1/orders/{mchOrderNo}/close", desc: "关闭一笔还没支付的调试单。" },
  { key: "refund.create", label: "退款", api: "POST /api/pay/v1/refunds", desc: "对已支付的调试单发起退款，可以分多次部分退。" },
  { key: "refund.query", label: "查询退款", api: "GET /api/pay/v1/refunds/{mchRefundNo}", desc: "按商户退款单号查退款状态。" },
  { key: "bill.summary", label: "按日账单汇总", api: "GET /api/pay/v1/bills/summary", desc: "按北京时间自然日汇总收款、退款、手续费与入账净额。" },
  { key: "bill.transactions", label: "交易流水", api: "GET /api/pay/v1/bills/transactions", desc: "逐笔拉取支付与退款成功的流水，对账用。" },
];
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

const app = computed(() => apps.value.find((a) => a.appId === appId.value));
const ways = computed(() => (app.value?.passages || []).filter((p) => p.state === "active"));
const way = computed(() => ways.value.find((p) => p.wayCode === form.value.wayCode));
const isSandbox = computed(() => way.value?.ifCode === "sandbox");
const sandboxWay = computed(() => ways.value.find((p) => p.ifCode === "sandbox"));
const current = computed(() => ops.find((o) => o.key === op.value));
const visibleOps = computed(() => ops.filter((o) => !o.sandbox || sandboxWay.value));
const needsOrderNo = computed(() => ["order.query", "order.mock", "order.close", "refund.create"].includes(op.value));
const opLabel = (key) => ops.find((o) => o.key === key)?.label || key;
const fen = (y) => Math.round(Number(y || 0) * 100);

watch(ways, (list) => {
  // 换应用后默认挑沙箱通道（不动真钱），没有再退到扫码。
  if (list.some((p) => p.wayCode === form.value.wayCode)) return;
  const pick = list.find((p) => p.ifCode === "sandbox") || list.find((p) => p.wayCode === "WX_NATIVE") || list[0];
  form.value.wayCode = pick?.wayCode || "";
});
watch(isSandbox, (sb) => {
  form.value.amountYuan = sb ? "1.00" : "0.01";
  form.value.refundYuan = sb ? "0.50" : "0.01";
});

async function load() {
  err.value = "";
  try {
    const [m, a] = await Promise.all([portal("GET", "/me"), portal("GET", "/apps")]);
    me.value = m;
    apps.value = (a.items || []).filter((x) => x.state === "active");
    if (!app.value) appId.value = apps.value[0]?.appId || "";
  } catch (e) {
    err.value = e.message || String(e);
  }
}

function paramsFor(key) {
  const f = form.value;
  switch (key) {
    case "order.create": {
      const p = { wayCode: f.wayCode, amount: fen(f.amountYuan), subject: f.subject.trim(), notifyUrl: f.notifyUrl.trim() };
      if (f.channelExtra.trim()) {
        let extra;
        try {
          extra = JSON.parse(f.channelExtra);
        } catch {
          throw new Error("渠道附加参数不是合法的 JSON，例如 {\"sub_openid\":\"oABC...\"}");
        }
        if (!extra || typeof extra !== "object" || Array.isArray(extra)) throw new Error("渠道附加参数要写成 JSON 对象，例如 {\"sub_openid\":\"oABC...\"}");
        p.channelExtra = Object.fromEntries(Object.entries(extra).map(([k, v]) => [k, String(v)]));
      }
      return p;
    }
    case "order.query":
    case "order.close":
      return { mchOrderNo: f.mchOrderNo.trim() };
    case "order.mock":
      return { mchOrderNo: f.mchOrderNo.trim(), state: f.state };
    case "refund.create":
      return { mchOrderNo: f.mchOrderNo.trim(), refundAmount: fen(f.refundYuan), reason: f.reason.trim(), notifyUrl: f.notifyUrl.trim() };
    case "refund.query":
      return { mchRefundNo: f.mchRefundNo.trim() };
    case "bill.summary":
      return { startDate: f.startDate, endDate: f.endDate };
    case "bill.transactions":
      return { startDate: f.startDate, endDate: f.endDate, type: f.type, page: Number(f.page) || 0, pageSize: Number(f.pageSize) || 0 };
  }
  return {};
}

const pretty = (v) => (typeof v === "string" ? v : JSON.stringify(v, null, 2));

// call 调一次调试接口并把结果记到列表顶部；返回这次调用（开放 API 返回非 200 时 ok=false）。
async function call(key, params, dryRun = false) {
  const data = await portal("POST", "/playground", { appId: appId.value, op: key, dryRun, params });
  const body = data.response?.body;
  const run = {
    id: ++seq,
    at: new Date().toLocaleTimeString(),
    ...data,
    status: data.response?.status || 0,
    ok: dryRun || data.response?.status === 200,
    apiError: (body && typeof body === "object" && body.error) || "",
  };
  runs.value = [run, ...runs.value].slice(0, 12);
  // 把新单号带到表单里，接着查单 / 退款不用再抄一遍。
  if (body?.order?.mchOrderNo) form.value.mchOrderNo = body.order.mchOrderNo;
  if (body?.refund?.mchRefundNo) form.value.mchRefundNo = body.refund.mchRefundNo;
  return run;
}

async function send(dryRun) {
  if (busy.value) return;
  err.value = "";
  busy.value = dryRun ? "sign" : "send";
  try {
    await call(op.value, paramsFor(op.value), dryRun);
  } catch (e) {
    err.value = e.message || String(e);
  } finally {
    busy.value = "";
  }
}

// 沙箱一键走通：下单 → 模拟支付 → 查单 → 部分退款 → 查退款 → 当日账单。任何一步没成功就停下。
async function runFlow() {
  if (busy.value || !sandboxWay.value) return;
  err.value = "";
  busy.value = "flow";
  try {
    const created = await call("order.create", { wayCode: sandboxWay.value.wayCode, amount: 100, subject: "沙箱联调" });
    if (!created.ok) return;
    const mchOrderNo = created.response.body.order.mchOrderNo;
    if (!(await call("order.mock", { mchOrderNo, state: "success" })).ok) return;
    if (!(await call("order.query", { mchOrderNo })).ok) return;
    const refunded = await call("refund.create", { mchOrderNo, refundAmount: 50, reason: "沙箱联调" });
    if (!refunded.ok) return;
    if (!(await call("refund.query", { mchRefundNo: refunded.response.body.refund.mchRefundNo })).ok) return;
    await call("bill.summary", { startDate: today() });
    pfToast("沙箱全流程已走通，下面按时间倒序列出了 6 次调用", "ok");
  } catch (e) {
    err.value = e.message || String(e);
  } finally {
    busy.value = "";
  }
}

onMounted(load);
</script>

<template>
  <div class="flex flex-col gap-3">
    <pf-alert v-if="err" tone="danger">{{ err }}</pf-alert>

    <div class="pf-card flex flex-col gap-2">
      <h2 class="m-0 text-[16px] font-semibold">接口调试</h2>
      <p class="pf-muted m-0">
        选一个接口、填几个参数，平台替你把请求拼好、用应用的签名密钥签好名，并<b>真实调用一次</b>。签名串、签名、请求头、curl 和响应都会原样列出来，
        拿它和你自己代码算出来的逐项比对，哪一步不一样一眼就能看出来。签名在服务器上完成，签名密钥不会出现在这个页面里。
      </p>
      <p class="pf-muted m-0">
        下单、关单、退款只作用于调试单（商户订单号以 <span class="pf-code">DBG</span> 开头），碰不到你的正式订单。
        走真实渠道的调试单<b>会真实扣款</b>，单笔上限 1 元，付完可以在这里原路退回；走沙箱通道的不动真钱。
      </p>
    </div>

    <pf-alert v-if="me && !apps.length" tone="warn">还没有可用的应用，请联系服务商为你创建应用并配置支付通道。</pf-alert>

    <template v-if="apps.length">
      <div v-if="sandboxWay" class="pf-card flex flex-col gap-2">
        <h2 class="m-0 text-[16px] font-semibold">一键走通沙箱全流程</h2>
        <p class="pf-muted m-0">
          这个应用的 <span class="pf-code">{{ sandboxWay.wayCode }}</span> 走沙箱通道。点下面的按钮会依次执行：下单 1.00 元 → 模拟支付成功 → 查询订单 → 退款 0.50 元 → 查询退款 → 查当日账单，
          全程不动真钱，6 次调用的签名与响应都会列在页面下方。
        </p>
        <div><pf-button variant="primary" size="sm" :loading="busy === 'flow'" @click="runFlow">一键走通</pf-button></div>
      </div>
      <pf-alert v-else-if="me && !me.sandbox" tone="info">
        平台当前没有开启沙箱通道，这里的下单会走真实渠道：建议用「微信扫码」下一笔 0.01 元的调试单，用手机扫码付款后查询订单，再原路退款。
      </pf-alert>

      <div class="pf-card flex flex-col gap-3">
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <Field label="应用" hint="用哪个应用的 App-Id 和签名密钥来签名。">
            <select v-model="appId" class="pf-input">
              <option v-for="a in apps" :key="a.appId" :value="a.appId">{{ a.appName }}（{{ a.appId }}）</option>
            </select>
          </Field>
          <Field label="接口" :hint="current.desc">
            <select v-model="op" class="pf-input">
              <option v-for="o in visibleOps" :key="o.key" :value="o.key">{{ o.label }}　{{ o.api }}</option>
            </select>
          </Field>
        </div>

        <div v-if="op === 'order.create'" class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <Field label="支付方式 wayCode" required>
            <select v-model="form.wayCode" class="pf-input">
              <option v-for="p in ways" :key="p.wayCode" :value="p.wayCode">{{ p.wayCode }}　{{ wayNames[p.wayCode] || "" }}{{ p.ifCode === "sandbox" ? "（沙箱）" : "（真实渠道）" }}</option>
            </select>
            <template #hint>
              <span v-if="!ways.length">这个应用还没有开通任何支付方式，请联系服务商配置支付通道。</span>
              <span v-else-if="isSandbox">沙箱通道：不动真钱。下单后用「模拟支付结果」接口把订单推进到已支付。</span>
              <span v-else><b>真实渠道，会真实扣款。</b>调试建议选「微信扫码」，下单后页面会画出二维码，手机扫码付款即可。</span>
            </template>
          </Field>
          <Field label="金额（元）" required :hint="isSandbox ? '接口里的 amount 单位是分，这里按元填写，发送时自动换算。' : '真实渠道调试单单笔最多 1.00 元。接口里的 amount 单位是分，发送时自动换算。'">
            <input v-model="form.amountYuan" class="pf-input" type="number" min="0.01" step="0.01">
          </Field>
          <Field label="商品标题 subject" required hint="顾客在付款页和账单里看到的名称。">
            <input v-model="form.subject" class="pf-input">
          </Field>
          <Field label="结果通知地址 notifyUrl" hint="选填。填了的话支付成功后平台会把结果 POST 到这个地址（必须是公网 https），可以顺便验证你的通知验签代码。">
            <input v-model="form.notifyUrl" class="pf-input" placeholder="https://你的域名/pay/notify">
          </Field>
          <Field label="渠道附加参数 channelExtra" hint="选填，JSON 对象。扫码支付不需要；微信 JSAPI / 小程序要带付款人的 openid，例如 {&quot;sub_openid&quot;:&quot;oABC...&quot;}；支付宝生活号要带 {&quot;buyer_id&quot;:&quot;2088...&quot;}。">
            <input v-model="form.channelExtra" class="pf-input" placeholder='{"sub_openid":"..."}' autocomplete="off" spellcheck="false">
          </Field>
        </div>

        <div v-if="needsOrderNo || op === 'refund.query'" class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <Field v-if="needsOrderNo" label="商户订单号 mchOrderNo" required :hint="op === 'order.query' ? '在这里下过单会自动带出。也可以填任何一笔你自己的订单号来查询。' : '只能是调试单（DBG 开头）。在这里下过单会自动带出。'">
            <input v-model="form.mchOrderNo" class="pf-input" placeholder="DBG..." autocomplete="off" spellcheck="false">
          </Field>
          <Field v-if="op === 'order.mock'" label="模拟成什么结果" required>
            <select v-model="form.state" class="pf-input">
              <option value="success">支付成功（success）</option>
              <option value="failed">支付失败（failed）</option>
            </select>
          </Field>
          <template v-if="op === 'refund.create'">
            <Field label="退款金额（元）" required hint="不能超过订单还没退的金额。接口里的 refundAmount 单位是分。">
              <input v-model="form.refundYuan" class="pf-input" type="number" min="0.01" step="0.01">
            </Field>
            <Field label="退款原因 reason" hint="选填。">
              <input v-model="form.reason" class="pf-input">
            </Field>
          </template>
          <Field v-if="op === 'refund.query'" label="商户退款单号 mchRefundNo" required hint="在这里发起过退款会自动带出。">
            <input v-model="form.mchRefundNo" class="pf-input" placeholder="DBG..." autocomplete="off" spellcheck="false">
          </Field>
        </div>

        <div v-if="op.startsWith('bill.')" class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <Field label="开始日期 startDate" required hint="北京时间自然日。">
            <input v-model="form.startDate" class="pf-input" type="date">
          </Field>
          <Field label="结束日期 endDate" hint="选填，留空只查开始日期这一天；一次最多查 31 天。">
            <input v-model="form.endDate" class="pf-input" type="date">
          </Field>
          <template v-if="op === 'bill.transactions'">
            <Field label="流水类型 type" hint="选填。">
              <select v-model="form.type" class="pf-input">
                <option value="">全部</option>
                <option value="pay">只看收款（pay）</option>
                <option value="refund">只看退款（refund）</option>
              </select>
            </Field>
            <Field label="页码 / 每页条数" hint="每页最多 200 条。">
              <div class="flex gap-2">
                <input v-model="form.page" class="pf-input" type="number" min="1">
                <input v-model="form.pageSize" class="pf-input" type="number" min="1" max="200">
              </div>
            </Field>
          </template>
        </div>

        <div class="flex gap-2 flex-wrap items-center">
          <pf-button variant="primary" size="sm" :loading="busy === 'send'" @click="send(false)">发送请求</pf-button>
          <pf-button size="sm" :loading="busy === 'sign'" @click="send(true)">只生成签名，不发送</pf-button>
          <span class="pf-muted">「只生成签名」得到的 curl 没被用过：把 &lt;accessToken&gt; 换成你自己的，5 分钟内可以原样发出一次。</span>
        </div>
      </div>

      <details v-for="(r, i) in runs" :key="r.id" class="pf-card" :open="i === 0">
        <summary>
          <span class="font-semibold">{{ opLabel(r.op) }}</span>
          <span class="pf-muted">　{{ r.request.method }} {{ r.request.uri }}　{{ r.at }}</span>
          <span v-if="r.dryRun" class="pf-dot" data-tone="neutral">只签名，未发送</span>
          <span v-else class="pf-dot" :data-tone="r.ok ? 'ok' : 'danger'">HTTP {{ r.status }}{{ r.apiError ? "　" + r.apiError : "" }}　{{ r.elapsedMs }} ms</span>
        </summary>
        <div class="flex flex-col gap-3 mt-2">
          <div v-if="r.response?.qr" class="flex gap-3 items-start flex-wrap">
            <img :src="r.response.qr" width="200" height="200" alt="支付二维码">
            <p class="pf-muted m-0 flex-1 min-w-0">
              用手机{{ r.request.body.includes("ALI_") ? "支付宝" : "微信" }}扫这个二维码付款（真实扣款）。付完把上面的接口切到「查询订单」点发送，状态变成 success 就说明整条链路通了；
              之后可以用「退款」把钱原路退回。二维码就是响应里 credential 的链接，你的业务系统拿到后同样画成二维码给顾客扫。
            </p>
          </div>

          <div class="flex flex-col gap-1">
            <span class="text-[13px] font-semibold">① 签名串</span>
            <pre class="pf-log">{{ r.signing.stringToSign }}</pre>
            <span class="pf-muted">
              六段用换行符 \n 连接：App-Id、时间戳（秒）、随机串、HTTP 方法、请求 URI（含查询串）、请求体原文。末尾没有换行{{ r.request.body ? "" : "；这个请求没有请求体，所以最后一段是空串（签名串以 \\n 结尾）" }}。
            </span>
          </div>

          <div class="flex flex-col gap-1">
            <span class="text-[13px] font-semibold">② 签名 = hex( HMAC-SHA256( 签名密钥, 签名串 ) )</span>
            <span class="pf-code break-all">{{ r.signing.signature }}</span>
            <span class="pf-muted">用你保存的签名密钥对上面的签名串算一遍，结果应该和这里一字不差。不一样的话，多半是请求体被重新序列化了，或者 URI 的查询串顺序 / 编码和实际发出的不一致。</span>
          </div>

          <div class="flex flex-col gap-1">
            <span class="text-[13px] font-semibold">③ 请求</span>
            <span><span class="pf-code break-all">{{ r.request.method }} {{ r.request.url }}</span></span>
            <table class="pf-table">
              <tbody>
                <tr v-for="h in r.request.headers" :key="h.name">
                  <td><span class="pf-code">{{ h.name }}</span></td>
                  <td><span class="font-mono break-all text-[13px]">{{ h.value }}</span></td>
                </tr>
              </tbody>
            </table>
            <pre v-if="r.request.body" class="pf-log">{{ r.request.body }}</pre>
            <span class="pf-muted">
              对外调用需要网关权限 <span class="pf-code">{{ r.scope }}</span>：Authorization 里的 accessToken 用你的 appKey + appSecret 向 /oauth/token 换取。
              这次调试是平台在内部直接调用的，所以不需要它。
            </span>
          </div>

          <div class="flex flex-col gap-1">
            <div class="flex items-center gap-2">
              <span class="text-[13px] font-semibold">④ curl</span>
              <pf-button size="sm" variant="ghost" @click="copyText(r.curl, 'curl 命令')">复制</pf-button>
            </div>
            <pre class="pf-log">{{ r.curl }}</pre>
            <span v-if="!r.dryRun" class="pf-muted">这条请求已经发送过，随机串已被用掉：原样再发会返回「重复请求」。要拿一条能自己发出去的 curl，用「只生成签名，不发送」。</span>
          </div>

          <div v-if="r.response" class="flex flex-col gap-1">
            <span class="text-[13px] font-semibold">⑤ 响应　HTTP {{ r.response.status }}</span>
            <pre class="pf-log">{{ pretty(r.response.body) }}</pre>
          </div>
        </div>
      </details>
    </template>
  </div>
</template>
