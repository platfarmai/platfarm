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

- [ ] migration `002_applyment.sql`: `pay_applyment`
- [ ] normalized payload validation (subject/legal_person/contact/business/settlement_account/rates/images) + sealed id_no/account_no
- [ ] image upload via svc-file presigned URLs, file ids referenced from payload
- [ ] review flow: draft → reviewing → approved/rejected, reviewer role gate, rejection reason
- [ ] `OnboardingChannel` for `wxpay_partner`: media upload, applyment4sub submit, query, state fold into applyment states, `sub_mchid` written back to `pay_channel_config`
- [ ] applyment polling job for non-terminal channel states
- [ ] console: review queue with approve/reject
- [ ] QA: full path draft → finished against sandbox; rejected application resubmittable; non-reviewer → 403

## M3 — profit sharing + reconciliation + rebate ledger (direct mode)

- [ ] migration `003_division_recon.sql`: `pay_division_receiver` `pay_division` `pay_ledger` `pay_recon_batch` `pay_recon_diff`
- [ ] receiver bind + sharing execution, ratios snapshotted, ratio cap enforced from sub-merchant grant
- [ ] fee snapshot on order creation (`amount * rate_bps / 10000`, half-up) + rebate ledger rows
- [ ] daily bill pull (WeChat trade bill) + diff; cron via svc-jobs
- [ ] console: reconciliation diff view
- [ ] QA: synthetic bill missing one order produces one diff; sharing of an already-shared order is a no-op

## M4 — controlled mode (支付宝直付通 + 微信收付通)

- [ ] migration `004_controlled.sql`: `pay_withdrawal`, balance columns/ledger kinds
- [ ] `alipay_zft` adapter: zft onboarding, trade, settle confirm, royalty, uni.transfer, bill
- [ ] `wxpay_ecommerce` adapter: ecommerce applyments, combine/regular pay, profitsharing, fund withdraw
- [ ] withdrawal flow + balance invariant (ledger sum == reported balance) checked in tests
- [ ] QA: sandbox settle-confirm → sharing → withdrawal balances to zero; direct-mode isv cannot call withdrawal (400)
