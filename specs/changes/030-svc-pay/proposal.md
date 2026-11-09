# 030: svc-pay — WeChat/Alipay payment service-provider system

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## Motivation

Build a payment service-provider (ISV) system on PlatFarm: onboard sub-merchants to WeChat Pay and Alipay (进件), review their applications, take payments / refunds / profit-sharing on their behalf, and reconcile. It must support **both** fund models from day one of the design:

- **Direct settlement** (普通服务商): the channel settles funds straight to the sub-merchant's bank account; the platform earns channel rebate (返佣). The platform never touches the money.
- **Platform-controlled** (微信电商收付通 / 支付宝直付通): funds land in a platform-controlled account; the platform confirms settlement, splits (分账), and the sub-merchant withdraws.

Design references (studied, not vendored): [jeepay](https://github.com/jeequan/jeepay) for the platform skeleton — `if_code`×`way_code` matrix, `ChannelRetMsg` normalization, CAS order state transitions, merchant-notify outbox, `(mch, merchant_order_no)` idempotency, amounts in 分 — and [scholar-ink/payment](https://github.com/scholar-ink/payment) for the onboarding field inventory. Neither has both fund models; jeepay has no onboarding, no settlement ledger, no reconciliation.

## Design

### One service, not a platform

`svc-pay` is a peer business service (`/api/pay`, own DB `pf_svc_pay`, Go/gin). It reuses the platform instead of re-inventing jeepay's infrastructure:

| Need | Provided by |
|---|---|
| Identity, coarse role, tenant | auth (specs/022 `tenantId`) |
| Async delivery + cron | svc-jobs (specs/026) |
| Email/webhook egress | svc-notify (specs/017) |
| License/id images | svc-file (specs/016, presigned upload) |
| Operator console SSO shell | `admin_ui` (specs/006) |
| Partner API identity + quota | app tokens (specs/009) — see signing note below |

A merchant belongs to a **tenant**: one tenant is one platform customer and may own many merchants. All rows carry `tenant_id`; every admin read and write is scoped to `claims.tenantId`. Only the platform admin (`role=admin`) crosses tenants; tenant 0 is the platform's own tenant (auth's "unassigned"), not a see-all scope. A merchant may hang under an ISV of its own tenant or a platform ISV (tenant 0); ISV credentials and reconciliation batches belong to the ISV's tenant.

### Fund model is a property of the channel account, not of the platform

```
pay_isv.fund_mode = direct | controlled
```

`direct` — channel settles to the sub-merchant; "settlement" in this system means **rebate ledger + reconciliation** only.
`controlled` — funds are held; settlement means **settle-confirm + profit-sharing + withdrawal**, and those rows are real money movements.

Both modes share merchants, apps, orders, refunds, notify and reconciliation. They differ only in which channel adapter is bound and which post-payment flows are legal: profit-sharing is opt-in under `direct` (WeChat `settle_info.profit_sharing`, ratio capped by the sub-merchant's granted maximum) and mandatory under `controlled`; withdrawal exists only under `controlled`. The ledger tables below cover both; `direct` simply never writes withdrawal rows.

### Modules (packages inside svc-pay)

```
services/svc-pay/
├── service.yaml
├── migrations/NNN_*.sql        # specs/015 append-only, self-applied at boot
├── merchant.go                 # merchant / app / isv config
├── applyment.go                # onboarding application + review flow
├── order.go                    # pay / query / close, CAS state machine
├── refund.go  transfer.go  division.go  withdraw.go
├── notify_out.go               # downstream merchant notify (outbox)
├── reconcile.go                # bill download + diff
├── ledger.go                   # fee snapshot, rebate ledger, balance (controlled mode)
├── channel/
│   ├── channel.go              # PayChannel + OnboardingChannel + registry
│   ├── retmsg.go               # normalized channel result
│   ├── wxpay/                  # partner v3, both modes
│   └── alipay/                 # 直付通 (controlled) first; 代签约 (direct) later
└── web/                        # Vue3 + pf-ui console (review, orders, reconciliation)
```

### Data model (amounts are BIGINT 分, never float)

| Table | Purpose | Key constraints |
|---|---|---|
| `pay_isv` | service-provider entity: channel credentials, `fund_mode` | `isv_no` pk |
| `pay_merchant` | merchant: `mch_no`, `tenant_id`, `mch_type` (1 direct-mch / 2 sub-mch), `isv_no`, `state` | `mch_no` pk |
| `pay_app` | merchant app: `app_id`, `mch_no`, hashed `app_secret`, ip whitelist | `app_id` pk |
| `pay_applyment` | onboarding application: normalized `payload` jsonb, `if_code`, `business_code`, `channel_applyment_id`, `sub_mchid`, `state`, `reject_reason` | `business_code` unique per `(isv_no, if_code)` |
| `pay_channel_config` | channel params, AES-GCM encrypted `if_params` | unique `(owner_type, owner_id, if_code)` — owner is isv or merchant |
| `pay_passage` | which `way_code` routes to which `if_code` for an app, with `rate` | unique `(app_id, way_code)` — one active route per way |
| `pay_order` | payment order | **unique `(mch_no, mch_order_no)`** = idempotency |
| `pay_refund` | refund | unique `(mch_no, mch_refund_no)` |
| `pay_transfer` | payout to user (商家转账 / 单笔转账) | unique `(mch_no, mch_order_no)` |
| `pay_division_receiver` | profit-sharing receiver bound upstream | unique `(mch_no, if_code, account)` |
| `pay_division` | one sharing execution, receivers + ratios **snapshotted** | `division_no` pk |
| `pay_withdrawal` | sub-merchant withdrawal, **controlled mode only** | unique `(mch_no, mch_withdraw_no)` |
| `pay_notify` | downstream notify outbox | unique `(order_id, order_type)` |
| `pay_ledger` | append-only money log: fee, rebate, sharing, withdrawal | `id` pk, indexed `(mch_no, created_at)` |
| `pay_recon_batch` / `pay_recon_diff` | daily reconciliation: channel bill vs local orders | unique `(if_code, bill_date, bill_type)` |

Secrets (API v3 key, merchant private key, certs, alipay app private key) are sealed with AES-256-GCM under `PAY_SECRET_KEY` before storage, same pattern as svc-data `secret.go`. Plaintext secrets never appear in logs or API responses.

### Onboarding payload (normalized, channel-agnostic)

Stored as jsonb so channel quirks don't leak into columns. Required groups, derived from the field intersection of WeChat `applyment4sub`, Alipay 直付通 `zft.create`, and the scholar-ink per-channel enter structs:

- `subject` — type `individual|enterprise|institution`, name, short_name, license_no, license_valid, mcc
- `legal_person` — name, id_type, id_no, id_valid, phone (id_no sealed)
- `contact` — name, phone, email
- `business` — service_phone, address (province/city/district/detail), scene (offline/mini_program/mp/app + appid)
- `settlement_account` — type `corporate|personal`, bank, branch, account_name, account_no (sealed)
- `rates` — per product: `{way_code, fee_rate, settle_cycle}`
- `images` — `{kind: file_id}` referencing svc-file (license, id_front, id_back, storefront, interior, …)

Each channel adapter maps this payload onto the channel's request and reports back what it could not express.

### State machines (all transitions are CAS: `UPDATE … WHERE id=? AND state=?`, zero rows = lost race, do nothing)

```
pay_order     created → paying → success | failed | closed
                                failed | closed → success  (late payment: channel confirms a payment
                                                            after the order ended; err_code LATE_PAYMENT,
                                                            merchant is re-notified)
                                success: refund_state none → partial → full
pay_refund    created → processing → success | failed | closed
pay_applyment draft → reviewing → approved → submitted → channel_auditing
                      → need_sign | need_verify → finished
                      any non-terminal → rejected (resubmittable) | canceled
pay_division  pending → accepted → success | failed
pay_notify    notifying → success | failed (attempt cap)
```

Channel-specific states fold in the adapter: WeChat `APPLYMENT_STATE_AUDITING/REJECTED/TO_BE_CONFIRMED/TO_BE_SIGNED/SIGNING/FINISHED/CANCELED` and Alipay 直付通 `passed/rejected` async notices map onto the applyment states above. Order outcomes fold through the normalized channel result:

```
SUCCESS  explicit success        → advance to success
FAIL     explicit failure        → advance to failed
WAITING  in progress upstream    → stay, wait for notify or reissue query
UNKNOWN  timeout / bad signature → touch nothing, query later
```

Only a definitive channel answer is `FAIL`. 5xx, rate limiting, `SYSTEM_ERROR`-class codes, "already paid / number already used" on resubmission, and every query error (including "trade does not exist" — Alipay creates the trade only when the payer opens the cashier) fold to `UNKNOWN`. A success whose channel-reported amount differs from the order is not applied.

### Channel adapters

```go
type PayChannel interface {
    IfCode() string
    FundMode() string            // direct | controlled
    Ways() []string              // WX_JSAPI WX_NATIVE WX_LITE WX_H5 WX_APP WX_BAR / ALI_*
    Pay(ctx, PayIn) (PayOut, error)
    Query(ctx, OrderRef) (Result, error)
    Close(ctx, OrderRef) error
    Refund(ctx, RefundIn) (Result, error)
    ParseNotify(r) (Notify, error)   // verify + decrypt
}

type OnboardingChannel interface {
    Submit(ctx, Applyment) (channelApplymentID string, err error)
    Query(ctx, businessCode string) (ApplymentView, error)
    UploadMedia(ctx, file) (mediaID string, error)
}
```

Registered by `if_code` at startup (`wxpay_partner`, `wxpay_ecommerce`, `alipay_zft`). Routing at pay time: `way_code` → `pay_passage` → exactly one `if_code` → adapter, with `pay_channel_config` resolved into provider params + sub-merchant params (`sub_mchid`/`sub_appid` or `app_auth_token`/`smid`).

### Three inbound paths

1. **Merchant API** — `open_api` routes gated by app-token scope. Money endpoints additionally require a **request signature**: HMAC-SHA256 over `app_id + timestamp + nonce + body` with `app_secret`, ±5 min window, nonce cached in Redis for replay rejection. The app token answers "who / quota"; the signature answers "untampered, not replayed".
2. **Channel notify** — `public_routes: POST /notify/{ifCode}/{orderId}`. Signature verification inside the adapter; failure returns 400. State advance is CAS, so N deliveries change state once. On reaching a terminal state, write `pay_notify` and hand delivery to svc-jobs.
3. **Downstream notify** — `pay_notify` row is the outbox (unique per order, so at most one stream). Delivery POSTs signed form/json to the merchant's `notify_url`; merchant replies `SUCCESS`. `notify_url` is untrusted input: public https only, the resolved IP is checked at connect time (loopback/private/link-local/reserved refused) and redirects are not followed. Retry delays 0, 30s, 60s, 150s, 10m, 30m, 1h, then `failed`. Schedule is driven by svc-jobs webhooks back into svc-pay, so no second worker framework exists.

### Background work

- Order expiry: the expiry is passed to the channel at pay time (`time_expire`). Past `expired_at`: query first (it may have just been paid); still payable → close upstream, then `closed`; channel has no such trade or is unreachable → `closed` locally after a 10-minute grace.
- Reissue: `paying` orders with no terminal notify get polled (`Query`) on a backoff — the callback can be lost, the query cannot double-charge because of CAS.
- Applyment polling: states `submitted|channel_auditing|need_sign|need_verify` polled until terminal (channels don't always push).
- Reconciliation: daily cron pulls the channel bill (WeChat `/v3/bill/tradebill`, Alipay `bill.downloadurl.query`), diffs against local terminal orders, writes `pay_recon_diff` (missing locally, missing upstream, amount mismatch).

### Console

`admin_ui` Vue3 + pf-ui page: applyment review queue (approve/reject with reason), merchant + app + passage config, order/refund search, reconciliation diffs. Review actions require `role: reviewer` (manifest vocabulary, bootstrap `platform-admin=reviewer`).

### Security notes (payment-specific)

- Idempotency is the unique key, not a lock. Locks may optimize but correctness comes from the constraint plus CAS (architecture appendix D).
- Amount math is integer 分; fee = `amount * rate_bps / 10000` rounded half-up, snapshotted onto the order at creation.
- `UNKNOWN` never moves state. A timeout is not a failure.
- Channel credentials are sealed at rest; `PAY_SECRET_KEY` absent in non-dev boot is a fatal startup error (dev may auto-generate and persist, as svc-data does).
- Every state change writes one structured log line with `requestId`, `orderId`, `from`, `to`.

## Blast radius

New `services/svc-pay/` only, plus wiring that every service needs: `CREATE DATABASE pf_svc_pay` in `deploy/initdb.sql`, `PAY_DATABASE_URL` / `PAY_SECRET_KEY` in `.env.example`, and `pctl sync` regenerating the gateway. No platform contract changes — this consumes `open_api`, `admin_ui`, `roles`, `exposes` as specified. svc-jobs and svc-notify gain nothing but an accept-list entry if svc-pay's callbacks arrive as service tokens (they will).

## Acceptance

```bash
pctl check --e2e                                   # contract trio green for svc-pay
# channel sandbox (WeChat partner v3, direct mode):
#   create order → paying; sandbox notify → success exactly once (replay is a no-op)
#   duplicate mch_order_no → original order returned, not a second charge
#   refund → partial/full reflected on the pay order
#   merchant notify retried until SUCCESS, then pay_notify.state=success
# applyment: draft → reviewing → approved → submitted (sandbox) → sub_mchid stored
# reconciliation: synthetic bill with one missing order → one pay_recon_diff row
# non-reviewer calling approve → 403
```

## Phasing

| Phase | Delivers | Modes exercised |
|---|---|---|
| M1 | skeleton, merchant/app/isv config, `wxpay_partner` pay+refund+notify, console read views | direct |
| M2 | applyment payload + review flow + WeChat applyment submit/query + image upload via svc-file | direct |
| M3 | profit-sharing + daily reconciliation + rebate ledger | direct |
| M4 | `alipay_zft` + `wxpay_ecommerce`: settle-confirm, sharing, withdrawal, balance ledger | controlled |

Each phase gets its own tasks checklist in `tasks.md` and lands behind the previous phase being green. M1 is the first implementation task; this proposal freezes the model so later phases don't reshape tables.

## Out of scope

- Agent/分销 hierarchy (代理商) — revisit when a second tenant tier is needed.
- Risk control beyond per-passage limits (jeepay's `risk_config`) — add when volume warrants.
- Holding custody of funds outside the two channel products above. No internal wallet, no float.
