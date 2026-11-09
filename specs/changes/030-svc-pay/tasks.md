# 030 tasks

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

Phasing per proposal. A phase starts only when the previous one is green. Do not reshape tables in later phases — add columns/tables forward via new migration files.

## M1 — skeleton + WeChat partner pay/refund/notify (direct mode)

- [x] `services/svc-pay/` from go template conventions: `auth.go` (pfauth), `migrate.go`, `/healthz` `/readyz` drain, `Dockerfile`, contract trio
- [x] `service.yaml`: mount `/api/pay`, `public_routes` for `POST /notify/*`, `open_api` scopes `pay.order.create|pay.order.query|pay.refund.create`, `roles.vocabulary` reviewer/operator, `admin_ui`, `data.database: pf_svc_pay`, env `PAY_DATABASE_URL` `PAY_SECRET_KEY`
- [x] migrations `001_init.sql`: `pay_isv` `pay_merchant` `pay_app` `pay_channel_config` `pay_passage` `pay_order` `pay_refund` `pay_notify` with the unique keys from the proposal
- [x] secret sealing (AES-256-GCM, `PAY_SECRET_KEY`, dev auto-persist) before any credential hits a table
- [x] `channel/`: `PayChannel` interface, result states SUCCESS/FAIL/WAITING/UNKNOWN, registry; `wxpay` adapter for partner v3 JSAPI + Native (sandbox endpoint switch)
- [x] order flow: create (idempotent on `(mch_no, mch_order_no)`), CAS transitions, query, close; request HMAC + nonce replay window on money endpoints
- [x] notify: verify in adapter, CAS advance, `pay_notify` outbox, in-process worker on the retry ladder (0/30s/60s/150s/10m/30m/1h); svc-jobs is on the accept list, webhook scheduling waits for the recon cron
- [x] refund flow with `refund_state` roll-up onto the pay order
- [x] console: read-only order + merchant list (Vue3 + pf-ui, hash router)
- [x] wiring: `deploy/initdb.sql`, `.env.example`, svc-jobs accept list, `pctl sync`
- [ ] QA: sandbox pay → one success on replayed notify; duplicate mch order no returns original; refund roll-up; notify reaches SUCCESS; contract trio; non-scope app token → 403

## M2 — onboarding + review

- [x] migration `002_applyment.sql`: `pay_applyment` + `pay_applyment_media` (image bytes retained so the reviewer's channel submit needs no file read permission)
- [x] normalized payload validation (`channel.Applyment.Problems()`) + sealed id_no/account_no, masked in every response
- [x] image upload via svc-file presigned URLs (console → `/admin/applyments/upload-url` proxy → PUT), file ids referenced from payload, bytes fetched with the operator's own token
- [x] review flow: draft → reviewing → approved/rejected, reviewer role gate via svc-grants `/mine` (admin bypass), rejection reason, cancel before channel submit
- [x] `OnboardingChannel` for `wxpay_partner`: media upload (multipart, meta-signed), applyment4sub submit (RSA-OAEP sensitive fields + `Wechatpay-Serial`), query, state fold, `sub_mchid` merged into merchant `pay_channel_config`; `/admin/channel-config/wxpay/platform-cert` pulls + decrypts the platform certificate
- [x] applyment polling worker (1m → 2m → … → 1h backoff) for submitted/channel_auditing/need_sign/need_verify
- [x] console: review queue with filter, detail, approve/reject/sync/cancel, new/edit form with image upload
- [x] QA (stub channel + real Postgres): draft → finished writes sub_mchid; rejected → edit → resubmit; non-reviewer approve → 403; poll respects `next_poll_at`; finished is terminal; channel submit error keeps `approved` and is retryable
- [ ] QA against real WeChat: applyment4sub submit needs a genuine sub-merchant's documents — not exercised (request signing, APIv3 decryption and response verification are proven live by `wxpay/live_test.go`)

## M3 — profit sharing + reconciliation + rebate ledger (direct mode)

- [x] migration `003_division_recon.sql`: `pay_division_receiver` `pay_division` (partial unique on `pay_order_id WHERE state<>'failed'`) `pay_ledger` (unique `(kind, ref_type, ref_id, ref_sub)` = replay-safe) `pay_recon_batch` `pay_recon_diff`; `pay_order.division_state/rebate_*`, `pay_isv.rebate_rate_bps` added forward
- [x] receiver bind (`/admin/division/receivers` → wxpay `profitsharing/receivers/add`, name RSA-OAEP) + sharing execution (`POST /v1/divisions`, scope `pay.division.create`), ratios snapshotted, per-receiver cap from grant + total cap from merchant param `max_division_ratio_bps` (default 30%), amounts floored; pay-time `settle_info.profit_sharing` via `channelExtra.profitSharing`
- [x] fee + rebate snapshot on order creation (half-up) → ledger `fee`/`rebate` rows on success, proportional `fee_refund`/`rebate_refund` on refund success, `sharing` rows per succeeded receiver; `onOrderTerminal`/`onRefundTerminal` unify the three terminal paths (also fixes M1 gap: synchronous SUCCESS from `Pay` never enqueued notify)
- [x] daily bill pull (`GET /v3/bill/tradebill` → signed download → CSV parse by header names, backtick fields, summary excluded) + `diffBill` (missing_local / missing_channel / amount_mismatch / state_mismatch, pay + refund); `/internal/recon/daily` for the svc-jobs cron (service token), `/admin/recon/schedule` registers the schedule with the admin's own token (svc-jobs schedules are admin-only)
- [x] console: 分账·台账 (receivers, divisions, ledger) + 对账 (run, batches, diff view, resolve, schedule)
- [x] QA (stub channel + real Postgres): synthetic bill missing one order → exactly one `missing_channel` diff; already-shared order → same division returned, channel `Share` called once; failed division releases the order for retry; ledger replay adds no rows; recon batch idempotent unless `force`; `no_bill` / `failed` states

## M4 — controlled mode (支付宝直付通 + 微信收付通)

- [x] migration `004_controlled.sql`: `pay_settle`, `pay_withdrawal`, `pay_order.settle_state`; ledger kinds `settle` / `withdrawal` / `withdrawal_reversal` (added in M3's ledger, posted here)
- [x] `alipay_zft` adapter: zft onboarding, trade (royalty freeze), settle confirm, royalty, uni.transfer, bill
- [x] `wxpay_ecommerce` adapter: ecommerce applyments, combine/regular pay (profit_sharing frozen), profitsharing + finish-order, fund withdraw, balance
- [x] withdrawal flow + balance invariant (ledger sum == reported balance) checked before any withdrawal; mismatch refuses without calling the channel
- [x] QA (stub channel + real Postgres): settle-confirm → sharing → withdrawal balances to zero; direct-mode isv calling withdrawal/settle/balance → 400; idempotent withdrawal; over-balance and mismatch rejected; channel FAIL on a succeeded withdrawal posts `withdrawal_reversal`; adapter fake-server tests for both channels

### M4 audit corrections (supersede the lines above where they differ)

- [x] `alipay_zft` has no withdrawal and no platform balance: settle confirm pays the 二级商户's default settlement account. `uni.transfer` / `bill.balance.query` removed (they moved the platform's own money). Settle amount = trade amount − refunded (Alipay rule, not minus sharing); settle before royalty, `royalty_freeze` at settle time only when `holdForDivision`, last royalty carries `royalty_finish`; pay no longer freezes every order
- [x] `wxpay_ecommerce`: sharing happens before settle (division never unfreezes; settle = finish-order releases the remainder, `NOT_ENOUGH` = already released); `WX_COMBINE` withdrawn until sub-orders are modelled (amount and receiving sub_mchid came from caller input)
- [x] withdrawal rule replaces the exact-equality invariant: per merchant+channel advisory lock, debit posted at creation (hold), reversal on failure or bank return; allowed amount = min(ledger, channel available − unconfirmed submissions); ledger/channel difference shown as drift, not blocking. Migration `007_controlled_money.sql`
- [x] settle and withdrawal results applied atomically with their ledger rows; unknown settle/withdrawal resubmitted with the same number; succeeded withdrawals re-queried for 10 days (bank return); settle refuses while a refund is in flight, refund refuses while a settle is pending; refunds after settle debit the withdrawable ledger
- [ ] sandbox / real-channel verification of the corrected ZFT settle → royalty flow and the ecommerce finish-order / withdrawal flow
