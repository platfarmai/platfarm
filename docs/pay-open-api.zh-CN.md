# 商户支付开放 API 接入指南

面向**商户技术团队与下游业务系统**的对接文档：怎么拿凭据、怎么签名、怎么下单收款与退款、怎么查询交易流水和每日账单汇总、回调怎么验签、怎么在沙箱里联调。

如果你是支付服务商或平台运营人员（需要配置服务商证书、提交商户进件、配置应用通道、管理代理分润或每日渠道对账），请看另一份文档：[服务商入驻与运营指南](/guide/isv)。

<!-- 维护者：实现见 services/svc-pay（api.go / bill.go / params.go / notify.go），路由与 scope 的权威来源是 services/svc-pay/service.yaml。本文件由 svc-site 发布到 /docs，改完后同步 services/svc-site/content/pay-open-api.zh-CN.md（svc-site 的测试会校验两份一致）。 -->

---

## 1. 接入前要拿到什么

服务商为你完成商户开户、应用创建与支付通道配置后，会向你发放两套凭据（一次请求过网关与支付服务两道闸）：

| 凭据 | 用途 | 由谁在哪里发放 |
|---|---|---|
| `appKey` / `appSecret` | 换取平台 `accessToken`，网关据此校验接口权限（scope）并按应用限流 | 平台管理员在「开放平台控制台」创建并勾选 `pay.*` 权限 |
| `App-Id` / 签名密钥 | 对每个请求做 HMAC-SHA256 签名，同时用来验证平台发给你的异步回调 | 服务商在支付中心「应用·通道」为你创建应用时生成（签名密钥只显示一次） |

接入前请和服务商确认两件事：

1. 应用已配好你要用的**支付方式通道**（例如 `WX_JSAPI`、`WX_NATIVE`、`ALI_QR`），否则下单会返回 `该应用未配置此支付方式`。
2. 开放平台凭据已勾选你需要的权限范围（例如收款勾选 `pay.order.create` / `pay.order.query`，退款勾选 `pay.refund.*`，对账查流水勾选 `pay.bill.query`）。

下文 `{base}` 指平台网关地址，例如 `https://apipay.sanrun.fun`。

## 2. 两层鉴权

### 2.1 平台 app token

```
POST {base}/oauth/token
Content-Type: application/json

{"appKey": "...", "appSecret": "..."}
```

```json
{"accessToken": "eyJ...", "tokenType": "app", "scopes": ["pay.order.create", "pay.order.query", "pay.bill.query"], "expiresIn": 3600}
```

之后每个请求带 `Authorization: Bearer <accessToken>`。token 有效期 1 小时，在过期前缓存复用并定时刷新即可，不要每个请求都换一次。

网关侧的拒绝：没有 token 或 token 无效返回 `401`；token 不含该接口要求的 scope 返回 `403 {"error":"missing scope pay.xxx"}`；超过限流返回 `429`。

### 2.2 请求签名

每个请求（包括 `GET`）带四个请求头：

| 头 | 值 |
|---|---|
| `X-Pay-App-Id` | 应用的 `App-Id` |
| `X-Pay-Timestamp` | Unix 时间戳，**秒** |
| `X-Pay-Nonce` | 一次性随机串，每个请求都换 |
| `X-Pay-Signature` | 见下，十六进制小写 |

签名串是六段用 `\n` 连接（末尾没有换行）：

```
App-Id
时间戳
随机串
HTTP 方法（大写）
请求 URI（路径 + 完整查询串，例如 /api/pay/v1/bills/transactions?startDate=2026-03-01&page=1）
请求体原文（没有请求体就是空串）
```

`signature = hex( HMAC-SHA256( 签名密钥, 签名串 ) )`

要点：

- 请求体按**发出去的原始字节**参与签名。先序列化成字符串，对这个字符串签名，再把同一个字符串发出去；不要签完再让 HTTP 库重新序列化一遍。
- 方法和完整 URI（含 `?` 后的查询参数，按实际发出的顺序与编码）都参与签名，所以签名不能挪到别的接口、别的单号或别的查询日期上复用。
- 时间戳与服务器时间相差超过 5 分钟（或超前超过 1 分钟）拒绝；同一个随机串 10 分钟内只能用一次。**重试时必须换随机串并重新签名**，业务幂等靠商户单号保证（见第 3 节）。

Node.js 示例：

```js
import { createHmac, randomUUID } from "node:crypto";

async function payCall(method, uri, payload) {
  const body = payload === undefined ? "" : JSON.stringify(payload);
  const ts = String(Math.floor(Date.now() / 1000));
  const nonce = randomUUID();
  const signature = createHmac("sha256", SIGN_SECRET)
    .update([APP_ID, ts, nonce, method, uri, body].join("\n"))
    .digest("hex");
  const res = await fetch(BASE + uri, {
    method,
    headers: {
      Authorization: "Bearer " + accessToken,
      ...(body ? { "Content-Type": "application/json" } : {}),
      "X-Pay-App-Id": APP_ID,
      "X-Pay-Timestamp": ts,
      "X-Pay-Nonce": nonce,
      "X-Pay-Signature": signature,
    },
    body: body || undefined,
  });
  return { status: res.status, data: await res.json() };
}
```

签名相关的拒绝统一返回 `401`：`缺少签名头` / `时间戳非法` / `时间戳超出窗口` / `应用不存在或已停用` / `签名不匹配` / `重复请求`。

## 3. 通用约定

- 金额一律是整数，单位**分**。币种默认 `CNY`。
- 失败响应统一是 `{"error": "原因"}`，部分接口会附带当前单据（例如关单时发现已支付）。
- **幂等**：下单按 `mchOrderNo`、退款按 `mchRefundNo`、提现按 `mchWithdrawNo`、分账与确认结算按支付订单。同一个单号重复提交返回已有单据并带 `"idempotent": true`，不会重复扣款。请求超时或返回 5xx 时，用**同一个单号**重试。
- **`"pending": true`**：渠道超时或结果不明，单据状态没有推进。不要当失败处理，也不要换单号重下；等回调或轮询查询接口，平台后台会自动向渠道补查。
- 时间字段是 RFC 3339（查账接口的 `tradeTime` 固定为北京时间 `+08:00`）。

HTTP 状态码：

| 状态码 | 含义 |
|---|---|
| 200 | 请求已受理。下单/退款等业务终态请看返回单据里的 `state`，不要只看状态码 |
| 400 | 参数不合法，或该渠道不支持这个操作 |
| 401 | token 或签名问题 |
| 403 | 缺 scope；商户已停用；单据不属于该应用 |
| 404 | 单据不存在（别的商户的单据同样返回 404） |
| 409 | 当前状态不允许（已支付不能关单、超额退款、额度不足、渠道参数未就绪等） |
| 429 | 限流或超出日配额 |
| 502 | 渠道拒绝或渠道不可达 |
| 503 | 防重放存储暂不可用，稍后重试 |

## 4. 接口一览

| 接口 | 用途 | 所需 scope |
|---|---|---|
| `POST /api/pay/v1/orders` | 统一下单 | `pay.order.create` |
| `GET /api/pay/v1/orders/{mchOrderNo}` | 查询支付单 | `pay.order.query` |
| `POST /api/pay/v1/orders/{mchOrderNo}/close` | 关闭未支付订单 | `pay.order.create` |
| `POST /api/pay/v1/refunds` | 申请退款 | `pay.refund.create` |
| `GET /api/pay/v1/refunds/{mchRefundNo}` | 查询退款单 | `pay.refund.query` |
| `POST /api/pay/v1/divisions` | 发起订单分账 | `pay.division.create` |
| `GET /api/pay/v1/divisions/{mchOrderNo}` | 查询分账结果 | `pay.division.query` |
| `POST /api/pay/v1/orders/{mchOrderNo}/settle` | 确认结算（归集模式） | `pay.settle.confirm` |
| `POST /api/pay/v1/withdrawals` | 申请余额提现（归集模式） | `pay.withdrawal.create` |
| `GET /api/pay/v1/withdrawals/{mchWithdrawNo}` | 查询提现单 | `pay.withdrawal.query` |
| `GET /api/pay/v1/balance?ifCode=` | 查询账户与可提现余额 | `pay.withdrawal.query` |
| `GET /api/pay/v1/bills/transactions` | 查询交易流水明细（支付 + 退款） | `pay.bill.query` |
| `GET /api/pay/v1/bills/summary` | 查询按日账单汇总 | `pay.bill.query` |
| `POST /api/pay/v1/orders/{mchOrderNo}/mock` | 模拟支付结果（仅沙箱通道） | `pay.order.create` |

### 4.1 下单

```json
{
  "mchOrderNo": "20261125-0001",
  "wayCode": "WX_NATIVE",
  "amount": 1000,
  "subject": "测试商品",
  "body": "",
  "clientIp": "203.0.113.9",
  "notifyUrl": "https://merchant.example.com/pay/notify",
  "returnUrl": "",
  "extParam": "",
  "channelExtra": {}
}
```

`mchOrderNo`、`wayCode`、`subject`、`amount`（> 0）必填。`notifyUrl` 必须是公网 `https` 地址。`extParam` 原样保存在订单上并在查询时带回。

`wayCode` 与 `channelExtra`：

| wayCode | 场景 | channelExtra | 返回的 credential |
|---|---|---|---|
| `WX_NATIVE` | 微信扫码 | — | `codeUrl`（转成二维码给用户扫） |
| `WX_JSAPI` | 微信公众号 / 小程序 | `sp_openid` 或 `sub_openid`（二选一，必填） | `appId` `timeStamp` `nonceStr` `package` `signType` `paySign` `prepayId`，直接交给前端调起支付 |
| `ALI_QR` | 支付宝扫码 | — | `qrCode` |
| `ALI_JSAPI` | 支付宝小程序 / 生活号 | `buyer_id` 或 `buyer_open_id`（二选一，必填） | `tradeNo` |
| `ALI_WAP` / `ALI_PAGE` | 手机网站 / 电脑网站 | `return_url`（可选） | `payUrl`（跳转地址） |
| `ALI_APP` | App 支付 | — | `payUrl`（交给支付宝 SDK 的订单串） |

说明：用服务商公众号/小程序拿到的用户 openid 传 `sp_openid`；用商户自己已绑定的公众号/小程序拿到的 openid 传 `sub_openid`（需先让服务商在商户渠道参数里填好你的子商户 `sub_appid`）。

微信订单后续要分账的，下单时传 `channelExtra.profitSharing = "true"`（或让服务商为你开启默认分账冻结），微信才会冻结待分资金。

响应：

```json
{
  "order": { "payOrderId": "P...", "mchOrderNo": "20261125-0001", "state": "paying", "amount": 1000, "...": "..." },
  "credential": { "codeUrl": "weixin://wxpay/bizpayurl?pr=..." }
}
```

订单 2 小时未支付自动关闭。一个 `mchOrderNo` 只对应一笔订单：对已关闭 / 已失败的单号再下单，拿到的是原订单（`"idempotent": true`，或已过期时的 `409`），不会重新开单——要重新收款请换新单号。

订单状态：

| state | 含义 |
|---|---|
| `created` | 已建单，渠道下单结果尚未确定 |
| `paying` | 已在渠道下单，等用户支付 |
| `success` | 支付成功（终态） |
| `failed` | 支付失败（终态） |
| `closed` | 已关闭（商户主动关单或超时关闭） |

`closed` / `failed` 之后如果渠道确认钱其实已经收到，订单会被推进到 `success`，`errCode` 记为 `LATE_PAYMENT`，并补发一次成功通知——业务侧可按需履约发货或直接发起退款。

### 4.2 查单 / 关单

- `GET /api/pay/v1/orders/{mchOrderNo}` → `{"order": {...}}`
- `POST /api/pay/v1/orders/{mchOrderNo}/close` → `{"order": {...}}`。已关闭 / 已失败的单重复关单返回 `"idempotent": true`；关单时若渠道确认已支付，返回 `409` 并附带已推进到 `success` 的订单。

### 4.3 退款

```json
{
  "mchOrderNo": "20261125-0001",
  "mchRefundNo": "RF-0001",
  "refundAmount": 400,
  "reason": "用户协商部分退款",
  "notifyUrl": "https://merchant.example.com/refund/notify"
}
```

只有 `success` 的订单可退，支持多次部分退款，累计退款不能超过订单金额（处理中的退款也暂占额度，失败后退回）。

退款状态：`created`（已建单、渠道结果未定）→ `processing`（渠道处理中）→ `success` / `failed`。

`GET /api/pay/v1/refunds/{mchRefundNo}` 返回 `{"refund": {...}}`。处理中的退款由平台每分钟向渠道同步推进，轮询间隔不必小于一分钟，建议优先使用异步通知。

### 4.4 分账

```json
{
  "mchOrderNo": "20261125-0001",
  "receivers": [{"account": "1900000109", "ratioBps": 1000}],
  "unfreezeUnsplit": true
}
```

`ratioBps` 为万分位（`1000` = 10%）。分账接收方需先由服务商在支付中心为你绑定；`receivers` 不传则按已绑定接收方的默认比例分账。一笔订单只允许一张有效分账单；已发生退款或有退款处理中的订单不能分账。

### 4.5 归集模式：确认结算、提现、余额

以下三个接口仅适用于归集渠道（微信收付通 `wxpay_ecommerce`、支付宝直付通 `alipay_zft`）。普通微信服务商直清模式（`wxpay_partner`）的资金由微信按结算周期自动打到你的结算银行卡，不需要确认结算或提现，调用这些接口会返回 `400`。

- `POST /api/pay/v1/orders/{mchOrderNo}/settle`，body `{"holdForDivision": false}`。支付宝直付通若要在结算后再分账，传 `true`。
- `POST /api/pay/v1/withdrawals`，body `{"ifCode": "wxpay_ecommerce", "mchWithdrawNo": "W-0001", "amount": 5000, "remark": ""}`。超出可提现额度返回 `409`。
- `GET /api/pay/v1/balance?ifCode=wxpay_ecommerce` 返回 `ledger`（平台台账余额）、`available` / `pending`（渠道侧可用与待结算余额）、`withdrawable`（本次最多可提现金额）。

### 4.6 查账：交易流水与按日汇总

用于商户财务对账、ERP 入账与门店营收统计。需要开放平台凭据包含 `pay.bill.query` 权限。

口径说明：

- **数据隔离**：自动按签名应用所属的商户号（`mchNo`）过滤，只能查到自己商户的数据；同一商户下有多个应用时，明细里的 `appId` 标明该笔流水来自哪个应用。
- **计入范围**：仅包含**支付成功**的支付单（`type = "pay"`）和**退款成功**的退款单（`type = "refund"`），分别按支付成功时间、退款成功时间归入**北京时间（UTC+8）自然日**。未支付、处理中、已关闭、失败的单据不进入账单流水。
- **日期跨度**：`startDate` 必填，`endDate` 选填（不传则等同 `startDate`，即只查当天），格式均为 `YYYY-MM-DD`（闭区间），单次最多查询 **31 天**。
- **手续费口径**：`feeAmount` / `feeRefundAmount` 是平台按通道费率计算的记账手续费（与平台台账完全一致；部分退款按 `round(订单手续费 × 退款金额 / 订单金额)` 冲回），**并非微信/支付宝的最终资金结算单**，银行卡实际到账金额以渠道结算为准。

#### 4.6.1 交易流水明细

```
GET /api/pay/v1/bills/transactions?startDate=2026-03-01&endDate=2026-03-02&type=&page=1&pageSize=50
```

查询参数（均放在 URL 查询串中，记得连同 `?` 查询串一起参与请求签名）：

| 参数 | 必填 | 说明 |
|---|---|---|
| `startDate` | 是 | 起始账单日，`YYYY-MM-DD`（北京时间） |
| `endDate` | 否 | 结束账单日，`YYYY-MM-DD`，默认等于 `startDate`，与 `startDate` 最多相隔 31 天 |
| `type` | 否 | `pay` 只看收款、`refund` 只看退款，不传或空串返回全部 |
| `page` | 否 | 页码，从 `1` 开始，默认 `1` |
| `pageSize` | 否 | 每页条数，默认 `50`，最大 `200`（超出自动按 `200` 处理） |

结果按成交时间（`tradeTime`）升序排列：

```json
{
  "mchNo": "M...",
  "startDate": "2026-03-01",
  "endDate": "2026-03-02",
  "page": 1,
  "pageSize": 50,
  "total": 2,
  "hasMore": false,
  "items": [
    {
      "type": "pay",
      "tradeTime": "2026-03-01T10:15:30+08:00",
      "billDate": "2026-03-01",
      "appId": "A...",
      "mchOrderNo": "20260301-0001",
      "payOrderId": "P...",
      "mchRefundNo": "",
      "refundOrderId": "",
      "channelOrderNo": "42000021...",
      "ifCode": "wxpay_partner",
      "wayCode": "WX_JSAPI",
      "subject": "门店收银-美式咖啡",
      "amount": 10000,
      "feeAmount": 60,
      "netAmount": 9940
    },
    {
      "type": "refund",
      "tradeTime": "2026-03-02T12:00:00+08:00",
      "billDate": "2026-03-02",
      "appId": "A...",
      "mchOrderNo": "20260301-0001",
      "payOrderId": "P...",
      "mchRefundNo": "RF-0001",
      "refundOrderId": "R...",
      "channelOrderNo": "50300408...",
      "ifCode": "wxpay_partner",
      "wayCode": "WX_JSAPI",
      "subject": "门店收银-美式咖啡",
      "amount": 2500,
      "feeAmount": 15,
      "netAmount": -2485
    }
  ]
}
```

字段提示：`amount`（交易本金）与 `feeAmount`（支付记的手续费 / 退款冲回的手续费）恒为非负整数（分）；`netAmount` 为带符号入账净额——收款时为 `amount - feeAmount`，退款时为 `-(amount - feeAmount)`，直接把区间内所有 `netAmount` 相加即得净入账额。没有流水时 `items` 返回空数组 `[]`。

#### 4.6.2 按日账单汇总

```
GET /api/pay/v1/bills/summary?startDate=2026-03-01&endDate=2026-03-03
```

返回区间总计 `total` 以及逐日数组 `days`（当天没有任何成交也会返回一行全 `0` 记录，方便财务报表按自然日连续落库）：

```json
{
  "mchNo": "M...",
  "startDate": "2026-03-01",
  "endDate": "2026-03-03",
  "total": {
    "payCount": 1,
    "payAmount": 10000,
    "refundCount": 1,
    "refundAmount": 2500,
    "feeAmount": 60,
    "feeRefundAmount": 15,
    "netAmount": 7455
  },
  "days": [
    {
      "date": "2026-03-01",
      "payCount": 1,
      "payAmount": 10000,
      "refundCount": 0,
      "refundAmount": 0,
      "feeAmount": 60,
      "feeRefundAmount": 0,
      "netAmount": 9940
    },
    {
      "date": "2026-03-02",
      "payCount": 0,
      "payAmount": 0,
      "refundCount": 1,
      "refundAmount": 2500,
      "feeAmount": 0,
      "feeRefundAmount": 15,
      "netAmount": -2485
    },
    {
      "date": "2026-03-03",
      "payCount": 0,
      "payAmount": 0,
      "refundCount": 0,
      "refundAmount": 0,
      "feeAmount": 0,
      "feeRefundAmount": 0,
      "netAmount": 0
    }
  ]
}
```

其中 `netAmount = payAmount - refundAmount - feeAmount + feeRefundAmount`。复用第 2.2 节的 `payCall` 即可直接调用：

```js
// 拉取昨日汇总与第一页流水明细
const sum = await payCall("GET", "/api/pay/v1/bills/summary?startDate=2026-03-01&endDate=2026-03-01");
const list = await payCall("GET", "/api/pay/v1/bills/transactions?startDate=2026-03-01&page=1&pageSize=100");
```

## 5. 结果通知（回调）

订单进入 `success` / `failed`、退款进入终态时，平台向下单或退款时传的 `notifyUrl` 发 `POST`，`Content-Type: application/json`，所有字段值均为**字符串**：

```json
{
  "orderType": "pay",
  "orderId": "P1a2b3c...",
  "mchNo": "M...",
  "mchOrderNo": "20261125-0001",
  "appId": "A...",
  "state": "success",
  "amount": "1000",
  "channelOrderNo": "4200001234...",
  "errCode": "",
  "errMsg": "",
  "notifyUrl": "https://merchant.example.com/pay/notify",
  "sign": "9f2c..."
}
```

退款通知的 `orderType` 是 `refund`，`orderId` 是平台退款单号，`mchOrderNo` 是你的 `mchRefundNo`，另带 `payOrderId`。

**验签**（必须做，`notifyUrl` 是公网地址，任何人都能往上发请求）：

1. 去掉 `sign`，去掉值为空串的字段；
2. 剩下的按 key 字典序排序，拼成 `k1=v1&k2=v2&...`（值不做 URL 编码）；
3. `hex( HMAC-SHA256( 签名密钥, 拼接串 ) )` 与 `sign` 比较。

验签通过后还要核对 `amount` 与自己业务单据的金额一致。

**应答**：10 秒内返回 `2xx`，响应正文为纯文本 `SUCCESS`。其他任何响应（包括 3xx 重定向，平台不跟随）都算通知失败并按退避策略重试：立即、30 秒、60 秒、150 秒、10 分钟、30 分钟、1 小时，共 7 次。

**必须幂等**：同一笔单可能收到多次通知；状态变化时（如超时关单后渠道确认已支付）还会收到内容不同的新通知。以通知里的 `state` 为准，重复通知直接回 `SUCCESS`。

没收到通知时用查单接口兜底，不要只依赖回调。

## 6. 沙箱联调

联调期间可让服务商为你单独创建一个走沙箱通道（`ifCode = sandbox`）的测试应用：不连真实上游、不动真钱，但下单、查单、关单、退款、查账、回调验签走的是和真实渠道完全相同的代码路径。在沙箱里把流程调通，正式上线只需切换到正式应用或让服务商把通道切回真实渠道，你的业务代码不用改。

注意：走沙箱通道的订单会显示支付成功并进入查账流水，但没有任何真实资金入账，请勿把生产环境正式收款应用指向沙箱。沙箱不支持分账、确认结算和提现。

**结果由金额的最后两位决定**（单位分）：

| 场景 | 金额尾数 | 行为 |
|---|---|---|
| 下单 | `01`（如 1001） | 立即失败，`errCode = SANDBOX_PAY_FAIL` |
| 下单 | `02` | 模拟渠道超时：返回 `"pending": true`，订单停在 `created`，约 30–60 秒后被补单推进到 `success` |
| 下单 | `03` | 一直待支付，用来测关单和超时关闭 |
| 下单 | 其他 | `paying`，约 30–60 秒后自动成功；或调用模拟接口立即出结果 |
| 退款 | `01` | 失败，`errCode = SANDBOX_REFUND_FAIL`，额度退回 |
| 退款 | `02` | `processing`，约一分钟后成功 |
| 退款 | 其他 | 立即成功 |

沙箱返回的 `credential` 键名与真实渠道同一 `wayCode` 的一致（`WX_JSAPI` 只有 `prepayId`，没有调起支付用的签名字段），另带 `"sandbox": "true"`；其中的地址是 `sandbox://` 占位，不能拿去调起微信 / 支付宝客户端。

**模拟支付结果**：

```
POST /api/pay/v1/orders/{mchOrderNo}/mock
{"state": "success"}      // 或 "failed"；不传 body 等同 success
```

订单立即进入终态并触发回调，和真实支付成功走同一条路径。这个接口只对沙箱通道的订单生效：对真实渠道订单返回 `400`，沙箱未启用时返回 `403`，已关闭订单返回 `409`。

联调自检清单：

1. 换取 `accessToken`，签名调用下单接口，拿到 `credential`；
2. 调用 `mock` 推进成功，收到异步通知并验签通过，返回 `SUCCESS`；
3. 故意不返回 `SUCCESS`，确认能收到重试且业务侧幂等处理；
4. 用同一个 `mchOrderNo` 重复下单，确认拿到 `"idempotent": true`；
5. 金额尾数 `02` 下单，确认 `"pending": true` 时没有误判为失败；
6. 金额尾数 `03` 下单后调用关单；
7. 测试全额、部分及尾数 `01` / `02` 的退款，核对查退款接口与退款回调；
8. 调用 `/api/pay/v1/bills/transactions` 与 `/api/pay/v1/bills/summary`，核对成功支付与成功退款笔数、手续费冲回及净额。
