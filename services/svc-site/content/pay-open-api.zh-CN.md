# 支付开放 API 接入指南（svc-pay）

面向接入方（渠道商 / 下游业务系统）的对接文档：怎么拿凭据、怎么签名、有哪些接口、回调长什么样、怎么在沙箱里联调。

<!-- 维护者：实现见 services/svc-pay（api.go / params.go / notify.go），路由与 scope 的权威来源是 services/svc-pay/service.yaml。本文件由 svc-site 发布到 /docs，改完后同步 services/svc-site/content/ 下的副本（svc-site 的测试会校验两份一致）。 -->

---

## 1. 接入前要拿到什么

一次请求过两道闸，所以有两套凭据，都由平台运营方发放：

| 凭据 | 用途 | 在哪里发 |
|---|---|---|
| `appKey` / `appSecret` | 换平台 app token，网关据此校验 scope、按 app 限流 | 平台控制台「开放平台」，创建 app 并勾选 `pay.*` scope |
| `App-Id` / 签名密钥 | 对每个请求做 HMAC 签名，同时用来验我们发给你的回调 | 支付中心控制台「应用与通道」，新建应用（密钥只显示一次） |

应用还要配好**支付通道**（哪种 `wayCode` 走哪个渠道、费率多少），否则下单返回 `该应用未配置此支付方式`。

下文 `{base}` 指平台网关地址，例如 `https://apipay.example.com`。

## 2. 两层鉴权

### 2.1 平台 app token

```
POST {base}/oauth/token
Content-Type: application/json

{"appKey": "...", "appSecret": "..."}
```

```json
{"accessToken": "eyJ...", "tokenType": "app", "scopes": ["pay.order.create", "pay.order.query"], "expiresIn": 3600}
```

之后每个请求带 `Authorization: Bearer <accessToken>`。token 有效期 1 小时，过期前重新换取即可，不要每个请求换一次。

网关侧的拒绝：没有 token 或 token 无效 `401`；token 不含该接口要求的 scope `403 {"error":"missing scope pay.xxx"}`；超过限流 `429`。

### 2.2 请求签名

每个请求（包括 GET）带四个头：

| 头 | 值 |
|---|---|
| `X-Pay-App-Id` | 应用的 App-Id |
| `X-Pay-Timestamp` | Unix 时间戳，**秒** |
| `X-Pay-Nonce` | 一次性随机串，每个请求都换 |
| `X-Pay-Signature` | 见下，十六进制小写 |

签名串是六段用 `\n` 连接（末尾没有换行）：

```
App-Id
时间戳
随机串
HTTP 方法（大写）
请求 URI（路径 + 查询串，例如 /api/pay/v1/balance?ifCode=wxpay_ecommerce）
请求体原文（没有请求体就是空串）
```

`signature = hex( HMAC-SHA256( 签名密钥, 签名串 ) )`

要点：

- 请求体按**发出去的原始字节**参与签名。先序列化成字符串，对这个字符串签名，再把同一个字符串发出去；不要签完再让 HTTP 库重新序列化一遍。
- 方法和 URI 参与签名，所以一个签名不能挪到别的接口或别的单号上用。
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
      "Content-Type": "application/json",
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

签名相关的拒绝都是 `401`：`缺少签名头` / `时间戳非法` / `时间戳超出窗口` / `应用不存在或已停用` / `签名不匹配` / `重复请求`。

## 3. 通用约定

- 金额一律是整数，单位**分**。币种默认 `CNY`。
- 失败响应统一是 `{"error": "原因"}`，部分接口会附带当前单据（例如关单时发现已支付）。
- **幂等**：下单按 `mchOrderNo`、退款按 `mchRefundNo`、提现按 `mchWithdrawNo`、分账与确认结算按支付订单。同一个单号重复提交返回已有单据并带 `"idempotent": true`，不会重复扣款。请求超时或返回 5xx 时，用**同一个单号**重试。
- **`"pending": true`**：渠道超时或结果不明，单据状态没有推进。不要当失败处理，也不要换单号重下；等回调或轮询查询接口，平台后台会自动向渠道补查。
- 时间字段是 RFC 3339。

HTTP 状态码：

| 状态码 | 含义 |
|---|---|
| 200 | 请求已受理。业务结果看单据的 `state`，不是看状态码 |
| 400 | 参数不合法，或该渠道不支持这个操作 |
| 401 | token 或签名问题 |
| 403 | 缺 scope；商户已停用；单据不属于该应用 |
| 404 | 单据不存在（别的商户的单据同样是 404） |
| 409 | 当前状态不允许（已支付不能关单、超额退款、额度不足、渠道参数未配置等） |
| 429 | 限流或超出日配额 |
| 502 | 渠道拒绝或渠道不可达 |
| 503 | 防重放存储暂不可用，稍后重试 |

## 4. 接口

| 接口 | scope |
|---|---|
| `POST /api/pay/v1/orders` 下单 | `pay.order.create` |
| `GET /api/pay/v1/orders/{mchOrderNo}` 查单 | `pay.order.query` |
| `POST /api/pay/v1/orders/{mchOrderNo}/close` 关单 | `pay.order.create` |
| `POST /api/pay/v1/refunds` 退款 | `pay.refund.create` |
| `GET /api/pay/v1/refunds/{mchRefundNo}` 查退款 | `pay.refund.query` |
| `POST /api/pay/v1/divisions` 分账 | `pay.division.create` |
| `GET /api/pay/v1/divisions/{mchOrderNo}` 查分账 | `pay.division.query` |
| `POST /api/pay/v1/orders/{mchOrderNo}/settle` 确认结算（归集模式） | `pay.settle.confirm` |
| `POST /api/pay/v1/withdrawals` 提现（归集模式） | `pay.withdrawal.create` |
| `GET /api/pay/v1/withdrawals/{mchWithdrawNo}` 查提现 | `pay.withdrawal.query` |
| `GET /api/pay/v1/balance?ifCode=` 可提现余额 | `pay.withdrawal.query` |
| `POST /api/pay/v1/orders/{mchOrderNo}/mock` 模拟支付结果（仅沙箱） | `pay.order.create` |

### 4.1 下单

```json
{
  "mchOrderNo": "20261125-0001",
  "wayCode": "WX_NATIVE",
  "amount": 1000,
  "subject": "测试商品",
  "body": "",
  "clientIp": "203.0.113.9",
  "notifyUrl": "https://partner.example.com/pay/notify",
  "returnUrl": "",
  "extParam": "",
  "channelExtra": {}
}
```

`mchOrderNo`、`wayCode`、`subject`、`amount`（> 0）必填。`notifyUrl` 必须是公网 `https` 地址。`extParam` 原样保存在订单上。

`wayCode` 与 `channelExtra`：

| wayCode | 场景 | channelExtra | 返回的 credential |
|---|---|---|---|
| `WX_NATIVE` | 微信扫码 | — | `codeUrl` |
| `WX_JSAPI` | 公众号 / 小程序 | `sp_openid` 或 `sub_openid`（二选一，必填） | `appId` `timeStamp` `nonceStr` `package` `signType` `paySign` `prepayId`，直接交给前端调起支付 |
| `ALI_QR` | 支付宝扫码 | — | `qrCode` |
| `ALI_JSAPI` | 支付宝小程序 / 生活号 | `buyer_id` 或 `buyer_open_id`（二选一，必填） | `tradeNo` |
| `ALI_WAP` / `ALI_PAGE` | 手机网站 / 电脑网站 | `return_url`（可选） | `payUrl`（跳转地址） |
| `ALI_APP` | App 支付 | — | `payUrl`（交给支付宝 SDK 的订单串） |

微信订单要分账的，下单时传 `channelExtra.profitSharing = "true"`，微信才会冻结待分资金。

响应：

```json
{"order": { "payOrderId": "P...", "mchOrderNo": "20261125-0001", "state": "paying", "amount": 1000, "...": "..." },
 "credential": { "codeUrl": "weixin://wxpay/bizpayurl?pr=..." }}
```

订单 2 小时未支付自动关闭。一个 `mchOrderNo` 只对应一笔订单：对已关闭 / 已失败的单号再下单，拿到的是原订单（`"idempotent": true`，或已过期时的 `409`），不会重新开单——要重新收款请换单号。

订单状态：

| state | 含义 |
|---|---|
| `created` | 已建单，渠道下单结果尚未确定 |
| `paying` | 已在渠道下单，等用户支付 |
| `success` | 支付成功（终态） |
| `failed` | 支付失败（终态） |
| `closed` | 已关闭（商户关单或超时） |

`closed` / `failed` 之后如果渠道确认钱其实已经收到，订单会被推进到 `success`，`errCode` 为 `LATE_PAYMENT`，并补发一次成功通知——按需发货或发起退款。

### 4.2 查单 / 关单

- `GET /api/pay/v1/orders/{mchOrderNo}` → `{"order": {...}}`
- `POST /api/pay/v1/orders/{mchOrderNo}/close` → `{"order": {...}}`。已关闭 / 已失败的单重复关单返回 `"idempotent": true`；关单时发现已支付返回 `409` 并附带已推进到 `success` 的订单。

### 4.3 退款

```json
{"mchOrderNo": "20261125-0001", "mchRefundNo": "RF-0001", "refundAmount": 400, "reason": "", "notifyUrl": "https://partner.example.com/refund/notify"}
```

只有 `success` 的订单可退，可多次部分退款，累计不能超过订单金额（处理中的退款也占额度，失败后退回）。

退款状态：`created`（已建单、渠道结果未定）→ `processing`（渠道处理中）→ `success` / `failed`。

`GET /api/pay/v1/refunds/{mchRefundNo}` 返回 `{"refund": {...}}`。它读的是平台侧状态；处理中的退款由平台每分钟向渠道查询推进，所以轮询间隔不必小于一分钟，优先用回调。

### 4.4 分账

```json
{"mchOrderNo": "20261125-0001", "receivers": [{"account": "1900000109", "ratioBps": 1000}], "unfreezeUnsplit": true}
```

`ratioBps` 是万分位。接收方要先由运营方在支付中心绑定；`receivers` 不传则按已绑定接收方的默认比例分。一笔订单只有一张有效分账单；已退款或退款处理中的订单不能分账。

### 4.5 归集模式：确认结算、提现、余额

只对归集渠道（微信收付通 `wxpay_ecommerce`、支付宝直付通 `alipay_zft`）有意义，直清渠道调用返回 `400`。

- `POST /api/pay/v1/orders/{mchOrderNo}/settle`，body `{"holdForDivision": false}`。直付通要在结算后分账的，传 `true`。
- `POST /api/pay/v1/withdrawals`，body `{"ifCode": "wxpay_ecommerce", "mchWithdrawNo": "W-0001", "amount": 5000, "remark": ""}`。超出可提现额度返回 `409`。
- `GET /api/pay/v1/balance?ifCode=wxpay_ecommerce` 返回 `ledger`（平台台账余额）、`available` / `pending`（渠道余额）、`withdrawable`（本次最多可提）。

## 5. 结果通知（回调）

订单进入 `success` / `failed`、退款进入终态时，平台向下单时给的 `notifyUrl` 发 `POST`，`Content-Type: application/json`，所有值都是**字符串**：

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
  "notifyUrl": "https://partner.example.com/pay/notify",
  "sign": "9f2c..."
}
```

退款通知的 `orderType` 是 `refund`，`orderId` 是平台退款单号，`mchOrderNo` 是你的 `mchRefundNo`，另带 `payOrderId`。

**验签**（必须做，`notifyUrl` 是公网地址，谁都能往上发请求）：

1. 去掉 `sign`，去掉值为空串的字段；
2. 剩下的按 key 字典序排序，拼成 `k1=v1&k2=v2&...`（值不做 URL 编码）；
3. `hex( HMAC-SHA256( 签名密钥, 拼接串 ) )` 与 `sign` 比较。

验签通过后还要核对 `amount` 与自己订单的金额一致。

**应答**：10 秒内返回 `2xx`，正文为 `SUCCESS`。其他任何响应（包括 3xx 重定向，平台不跟随）都算失败并重试：立即、30 秒、60 秒、150 秒、10 分钟、30 分钟、1 小时，共 7 次。

**必须幂等**：同一笔单可能收到多次通知；状态变化时（如关单后确认已支付）还会收到内容不同的新通知。以通知里的 `state` 为准，重复通知直接回 `SUCCESS`。

没收到通知时用查单接口兜底，不要只依赖回调。

## 6. 沙箱联调

沙箱是一个不连任何上游、不动真钱的渠道（`ifCode = sandbox`），下单、查单、关单、退款、回调走的是和真实渠道完全相同的代码路径。签名、幂等、回调验签在沙箱里调通，上线只是换通道。

**开启**（运营方）：服务端设 `PAY_SANDBOX=on` 并重启 svc-pay；在支付中心「应用与通道」给联调应用的 `wayCode` 配一条走 `sandbox` 的通道。沙箱不需要任何渠道参数。上线时把通道改到真实渠道即可，接入方代码不用动。

建议给联调单独建应用，不要把正式应用的通道指向沙箱：走沙箱的订单会显示支付成功，但没有任何资金入账。沙箱不支持分账、确认结算和提现。

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

沙箱返回的 `credential` 键名与真实渠道同一 `wayCode` 的一致（`WX_JSAPI` 只有 `prepayId`，没有调起支付用的签名字段），另带 `"sandbox": "true"`；其中的地址是 `sandbox://` 占位，不能拿去调起微信 / 支付宝。

**模拟支付结果**：

```
POST /api/pay/v1/orders/{mchOrderNo}/mock
{"state": "success"}      // 或 "failed"；不传 body 等同 success
```

订单立即进入终态并触发回调，和真实支付成功走同一条路径。这个接口只对沙箱渠道的订单生效：对真实渠道的订单返回 `400`，沙箱未开启时返回 `403`，已关闭的订单返回 `409`。

联调清单：

1. 换 token、签名下单，拿到 `credential`；
2. `mock` 成功，收到回调并验签通过，回 `SUCCESS`；
3. 故意不回 `SUCCESS`，确认能收到重试且处理幂等；
4. 用同一个 `mchOrderNo` 重复下单，确认拿到 `"idempotent": true`；
5. 尾数 `02` 下单，确认 `pending` 时没有当失败处理；
6. 尾数 `03` 下单后关单；
7. 全额、部分、尾数 `01` / `02` 的退款，核对查退款接口与退款回调。
