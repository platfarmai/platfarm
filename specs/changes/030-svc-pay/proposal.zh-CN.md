# 030: svc-pay — 微信/支付宝支付服务商系统

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## 动机

在 PlatFarm 上做一套支付服务商（ISV）系统：帮子商户向微信支付、支付宝进件，审核进件资料，代为收款、退款、分账，并完成对账。设计从第一天起同时支持两种资金模式：

- **直清模式**（普通服务商）：渠道把资金直接结算到子商户银行卡，平台赚渠道返佣。钱不过平台的手。
- **归集模式**（微信电商收付通 / 支付宝直付通）：资金进入平台可控账户，平台做结算确认、分账，子商户再提现。

设计参考（研究后借鉴，不引入其代码）：[jeepay](https://github.com/jeequan/jeepay) 的平台骨架——`if_code`×`way_code` 矩阵、`ChannelRetMsg` 归一、订单 CAS 状态流转、商户通知 outbox、`(商户, 商户订单号)` 幂等、金额用**分**；[scholar-ink/payment](https://github.com/scholar-ink/payment) 的进件字段全集。两者都不同时具备两种资金模式：jeepay 没有进件、没有结算台账、没有对账。

## 方案

### 一个业务服务，不是新底座

`svc-pay` 是平级业务服务（`/api/pay`，自有库 `pf_svc_pay`，Go/gin）。jeepay 自己造的基础设施这里直接复用平台：

| 需求 | 由谁提供 |
|---|---|
| 身份、粗角色、租户 | auth（specs/022 的 `tenantId`） |
| 异步投递 + 定时 | svc-jobs（specs/026） |
| 邮件/webhook 出口 | svc-notify（specs/017） |
| 证照图片 | svc-file（specs/016，预签名直传） |
| 运营后台 SSO 壳 | `admin_ui`（specs/006） |
| 合作方 API 身份 + 配额 | app token（specs/009），签名约定见下 |

商户归属于**租户**：一个租户是一个平台客户，其下可有多个商户。所有行带 `tenant_id`，管理端的每个读写都限定在 `claims.tenantId` 内。只有平台管理员（`role=admin`）跨租户；租户 0 是平台自己的租户（即 auth 的“未分配”），不是“看全部”。商户可以挂在本租户或平台（租户 0）的服务商下；服务商凭据与对账批次归服务商所在租户。

### 资金模式是渠道账户的属性，不是平台的属性

```
pay_isv.fund_mode = direct | controlled
```

`direct`（直清）——渠道结算给子商户；本系统里的"结算"= **返佣台账 + 对账**，仅此而已。
`controlled`（归集）——资金被托管；结算 = **结算确认 + 分账 + 提现**，这些记录是真实的资金运动。

两种模式共用商户、应用、订单、退款、通知、对账，差别只在绑定的渠道适配器以及支付后哪些流程合法：分账在 `direct` 下是可选的（微信 `settle_info.profit_sharing`，比例受子商户被授予的上限约束），在 `controlled` 下是必要环节；提现只存在于 `controlled`。下面的台账表两种模式都覆盖，`direct` 只是永远不写提现行。

### 模块（svc-pay 内部的包）

```
services/svc-pay/
├── service.yaml
├── migrations/NNN_*.sql        # specs/015 append-only，启动自应用
├── merchant.go                 # 商户 / 应用 / 服务商配置
├── applyment.go                # 进件单 + 审核流
├── order.go                    # 下单 / 查单 / 关单，CAS 状态机
├── refund.go  transfer.go  division.go  withdraw.go
├── notify_out.go               # 通知下游商户（outbox）
├── reconcile.go                # 账单下载 + 差异
├── ledger.go                   # 手续费快照、返佣台账、余额（归集模式）
├── channel/
│   ├── channel.go              # PayChannel + OnboardingChannel + 注册表
│   ├── retmsg.go               # 渠道返回归一
│   ├── wxpay/                  # 服务商 v3，两种模式
│   └── alipay/                 # 先做直付通（归集），代签约（直清）后续
└── web/                        # Vue3 + pf-ui 管理台（审核、订单、对账）
```

### 数据模型（金额一律 BIGINT 分，禁用浮点）

| 表 | 用途 | 关键约束 |
|---|---|---|
| `pay_isv` | 服务商主体：渠道凭据、`fund_mode` | `isv_no` 主键 |
| `pay_merchant` | 商户：`mch_no`、`tenant_id`、`mch_type`(1 普通/2 特约)、`isv_no`、`state` | `mch_no` 主键 |
| `pay_app` | 商户应用：`app_id`、`mch_no`、哈希后的 `app_secret`、IP 白名单 | `app_id` 主键 |
| `pay_applyment` | 进件单：规范化 `payload` jsonb、`if_code`、`business_code`、`channel_applyment_id`、`sub_mchid`、`state`、`reject_reason` | `(isv_no, if_code, business_code)` 唯一 |
| `pay_channel_config` | 渠道参数，`if_params` 用 AES-GCM 加密 | `(owner_type, owner_id, if_code)` 唯一，owner 为 isv 或 merchant |
| `pay_passage` | 某应用的 `way_code` 路由到哪个 `if_code`，带 `rate` | `(app_id, way_code)` 唯一——一种支付方式只有一条生效路由 |
| `pay_order` | 支付订单 | **`(mch_no, mch_order_no)` 唯一 = 幂等** |
| `pay_refund` | 退款 | `(mch_no, mch_refund_no)` 唯一 |
| `pay_transfer` | 代付到用户（商家转账 / 单笔转账） | `(mch_no, mch_order_no)` 唯一 |
| `pay_division_receiver` | 已在渠道侧绑定的分账接收方 | `(mch_no, if_code, account)` 唯一 |
| `pay_division` | 一次分账执行，接收方与比例**快照**入库 | `division_no` 主键 |
| `pay_withdrawal` | 子商户提现，**仅归集模式** | `(mch_no, mch_withdraw_no)` 唯一 |
| `pay_notify` | 下游通知 outbox | `(order_id, order_type)` 唯一 |
| `pay_ledger` | 只追加的资金流水：手续费、返佣、分账、提现 | `id` 主键，索引 `(mch_no, created_at)` |
| `pay_recon_batch` / `pay_recon_diff` | 每日对账：渠道账单 vs 本地订单 | `(if_code, bill_date, bill_type)` 唯一 |

密钥（APIv3 密钥、商户私钥、证书、支付宝应用私钥）落库前用 `PAY_SECRET_KEY` 做 AES-256-GCM 密封，模式与 svc-data 的 `secret.go` 相同。明文密钥不进日志、不进任何 API 响应。

### 进件资料（规范化，与渠道无关）

用 jsonb 存储，避免渠道的个别字段污染表结构。必填分组取自微信 `applyment4sub`、支付宝直付通 `zft.create`、以及 scholar-ink 各渠道 enter 结构的字段交集：

- `subject` — 主体类型 `individual|enterprise|institution`、名称、简称、执照号、有效期、mcc
- `legal_person` — 姓名、证件类型、证件号、有效期、手机（证件号密封）
- `contact` — 姓名、手机、邮箱
- `business` — 客服电话、地址（省/市/区/详细）、经营场景（线下/小程序/公众号/APP + appid）
- `settlement_account` — 账户类型 `corporate|personal`、银行、支行、户名、账号（账号密封）
- `rates` — 按产品：`{way_code, fee_rate, settle_cycle}`
- `images` — `{类型: file_id}`，指向 svc-file（执照、身份证正反面、门头、内景…）

每个渠道适配器负责把这份资料映射成渠道请求，并回报哪些字段它无法表达。

### 状态机（所有流转都是 CAS：`UPDATE … WHERE id=? AND state=?`，影响行数 0 = 并发冲突，直接放弃）

```
pay_order     created → paying → success | failed | closed
                                failed | closed → success（迟到支付：订单结束后渠道确认已付款，
                                                           err_code 记 LATE_PAYMENT，重新通知商户）
                                success 后：refund_state none → partial → full
pay_refund    created → processing → success | failed | closed
pay_applyment draft → reviewing → approved → submitted → channel_auditing
                      → need_sign | need_verify → finished
                      任何非终态 → rejected（可修改重提）| canceled
pay_division  pending → accepted → success | failed
pay_notify    notifying → success | failed（达到重试上限）
```

渠道特有状态在适配器内折叠：微信 `APPLYMENT_STATE_AUDITING/REJECTED/TO_BE_CONFIRMED/TO_BE_SIGNED/SIGNING/FINISHED/CANCELED` 与支付宝直付通 `passed/rejected` 异步通知，都映射到上面的进件状态。订单结果通过归一化的渠道返回折叠：

```
SUCCESS  明确成功          → 推进到 success
FAIL     明确失败          → 推进到 failed
WAITING  上游处理中        → 保持不动，等回调或补单查询
UNKNOWN  超时 / 验签失败   → 什么都不改，稍后查询
```

只有渠道明确的答复才是 `FAIL`。5xx、限频、`SYSTEM_ERROR` 类错误码、重提时的“已支付 / 单号已使用”，以及所有查询错误（包括“交易不存在”——支付宝要等付款人打开收银台才创建交易）都折叠成 `UNKNOWN`。渠道报的成功金额与订单不符时不推进。

### 渠道适配器

```go
type PayChannel interface {
    IfCode() string
    FundMode() string            // direct | controlled
    Ways() []string              // WX_JSAPI WX_NATIVE WX_LITE WX_H5 WX_APP WX_BAR / ALI_*
    Pay(ctx, PayIn) (PayOut, error)
    Query(ctx, OrderRef) (Result, error)
    Close(ctx, OrderRef) error
    Refund(ctx, RefundIn) (Result, error)
    ParseNotify(r) (Notify, error)   // 验签 + 解密
}

type OnboardingChannel interface {
    Submit(ctx, Applyment) (channelApplymentID string, err error)
    Query(ctx, businessCode string) (ApplymentView, error)
    UploadMedia(ctx, file) (mediaID string, error)
}
```

按 `if_code` 在启动时注册（`wxpay_partner`、`wxpay_ecommerce`、`alipay_zft`）。支付时的路由：`way_code` → `pay_passage` → 唯一的 `if_code` → 适配器，同时把 `pay_channel_config` 解析成服务商参数 + 子商户参数（`sub_mchid`/`sub_appid` 或 `app_auth_token`/`smid`）。

### 三条入口

1. **商户 API** — `open_api` 路由由 app token 的 scope 把关。资金类接口额外要求**请求签名**：用 `app_secret` 对 `app_id + 时间戳 + 随机串 + body` 做 HMAC-SHA256，±5 分钟窗口，随机串进 Redis 防重放。app token 回答"你是谁、配额多少"，签名回答"没被篡改、不是重放"。
2. **渠道回调** — `public_routes: POST /notify/{ifCode}/{orderId}`。验签在适配器内，失败返回 400。状态推进是 CAS，所以同一回调来 N 次只改变一次状态。到达终态后写 `pay_notify`，投递交给 svc-jobs。
3. **下游通知** — `pay_notify` 行就是 outbox（每单一行，因此至多一条通知流）。投递时把签名后的内容 POST 到商户的 `notify_url`，商户回复 `SUCCESS`。`notify_url` 是不可信输入：只接受公网 https，连接时按解析出的真实 IP 拒绝回环/私网/链路本地/保留地址，且不跟随重定向。重试间隔 0、30 秒、60 秒、150 秒、10 分、30 分、1 小时，之后记 `failed`。调度由 svc-jobs 回调 svc-pay 驱动，不再造第二套 worker 框架。

### 后台任务

- 订单超时：下单时把失效时间传给渠道（`time_expire`）。超过 `expired_at` 后先查渠道（可能刚付成功）；仍可支付 → 先关上游单，再置 `closed`；渠道查无此单或不可达 → 宽限 10 分钟后本地置 `closed`。
- 补单：没有收到终态回调的 `paying` 订单按退避节奏轮询 `Query`——回调可能丢，而查询不会重复扣款，因为有 CAS。
- 进件轮询：`submitted|channel_auditing|need_sign|need_verify` 轮询到终态（渠道不保证推送）。
- 对账：每日定时拉取渠道账单（微信 `/v3/bill/tradebill`、支付宝 `bill.downloadurl.query`），与本地终态订单比对，差异写入 `pay_recon_diff`（本地缺失、上游缺失、金额不符）。

### 管理台

`admin_ui` 的 Vue3 + pf-ui 页面：进件审核队列（通过/驳回并写理由）、商户 + 应用 + 通道路由配置、订单/退款检索、对账差异。审核动作要求 `role: reviewer`（清单词汇表，bootstrap `platform-admin=reviewer`）。

### 安全约定（支付特有）

- 幂等靠唯一键，不靠锁。锁可以做性能优化，但正确性来自约束 + CAS（架构附录 D）。
- 金额运算全是整数分；手续费 = `amount * rate_bps / 10000` 四舍五入，下单时快照进订单。
- `UNKNOWN` 永不推动状态。超时不是失败。
- 渠道凭据静态加密；非 dev 环境 `PAY_SECRET_KEY` 缺失是启动致命错误（dev 可自动生成并持久化，与 svc-data 相同）。
- 每次状态变更写一行结构化日志，带 `requestId`、`orderId`、`from`、`to`。

## 影响面

只新增 `services/svc-pay/`，外加每个服务都要的接线：`deploy/initdb.sql` 里 `CREATE DATABASE pf_svc_pay`，`.env.example` 里 `PAY_DATABASE_URL` / `PAY_SECRET_KEY`，以及 `pctl sync` 重新生成网关。不改平台契约——本提案只消费既有的 `open_api`、`admin_ui`、`roles`、`exposes`。svc-jobs 与 svc-notify 只在 svc-pay 的回调以 service token 到达时多一条 accept 白名单（确实会）。

## 验收标准

```bash
pctl check --e2e                                   # svc-pay 契约三件套绿
# 渠道沙箱（微信服务商 v3，直清模式）：
#   下单 → paying；沙箱回调 → success 恰好一次（重放无副作用）
#   重复的 mch_order_no → 返回原订单，不产生第二笔扣款
#   退款 → 支付单的 partial/full 正确反映
#   商户通知重试到收到 SUCCESS，pay_notify.state=success
# 进件：draft → reviewing → approved → submitted（沙箱）→ sub_mchid 落库
# 对账：构造一张少一笔的账单 → 产生一行 pay_recon_diff
# 非 reviewer 调用审核通过 → 403
```

## 分期

| 阶段 | 交付 | 覆盖模式 |
|---|---|---|
| M1 | 骨架、商户/应用/服务商配置、`wxpay_partner` 支付+退款+通知、管理台只读 | 直清 |
| M2 | 进件资料 + 审核流 + 微信进件提交/查询 + 经 svc-file 的图片上传 | 直清 |
| M3 | 分账 + 每日对账 + 返佣台账 | 直清 |
| M4 | `alipay_zft` + `wxpay_ecommerce`：结算确认、分账、提现、余额台账 | 归集 |

每个阶段在 `tasks.md` 里有自己的任务清单，前一阶段验收通过后才开始。M1 是第一个实现任务；本提案把模型冻结，后续阶段不再改动表结构。

## 不做

- 代理商/分销层级——出现第二层租户需求时再议。
- 通道路由限额之外的风控（jeepay 的 `risk_config`）——量上来再加。
- 上述两个渠道产品之外的资金托管。不做内部钱包，不做沉淀资金。
