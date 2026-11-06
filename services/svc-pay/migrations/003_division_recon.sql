-- svc-pay M3（specs/030）：分账 + 台账 + 对账（直清模式）。
-- 金额 BIGINT 分；台账追加写、唯一键防重放；对账按 (渠道, 服务商, 账单日, 账单类型) 幂等。

-- 分账接收方：先在渠道绑定（微信 profitsharing/receivers/add），再参与分账。
-- max_ratio_bps 是子商户授权给该接收方的分账比例上限（万分位），分账时逐个校验。
CREATE TABLE pay_division_receiver (
    id                BIGSERIAL PRIMARY KEY,
    mch_no            TEXT NOT NULL,
    if_code           TEXT NOT NULL,
    receiver_type     TEXT NOT NULL,                -- MERCHANT_ID | PERSONAL_OPENID（微信）
    account           TEXT NOT NULL,
    name              TEXT NOT NULL DEFAULT '',     -- 接收方名称（AES-GCM 密文；微信 MERCHANT_ID 类型必填）
    relation_type     TEXT NOT NULL DEFAULT 'PARTNER',
    max_ratio_bps     INT  NOT NULL DEFAULT 3000,
    default_ratio_bps INT  NOT NULL DEFAULT 0,      -- 未显式指定接收方时使用的默认比例
    bind_state        TEXT NOT NULL DEFAULT 'unbound', -- unbound|bound|failed
    bind_msg          TEXT NOT NULL DEFAULT '',
    state             TEXT NOT NULL DEFAULT 'active',
    tenant_id         INT  NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (mch_no, if_code, account)
);

-- 分账单：接收方与比例快照。一笔支付单只有一条未失败的分账单（已分账再分 = no-op）。
CREATE TABLE pay_division (
    division_no      TEXT PRIMARY KEY,
    pay_order_id     TEXT NOT NULL,
    mch_no           TEXT NOT NULL,
    isv_no           TEXT NOT NULL DEFAULT '',
    app_id           TEXT NOT NULL,
    if_code          TEXT NOT NULL,
    way_code         TEXT NOT NULL,
    order_amount     BIGINT NOT NULL,
    division_amount  BIGINT NOT NULL,
    receivers        JSONB NOT NULL,               -- [{type, account, ratio_bps, amount, description, result, detail}]
    unfreeze_unsplit BOOL NOT NULL DEFAULT true,
    state            TEXT NOT NULL DEFAULT 'pending', -- pending|accepted|success|failed
    channel_order_no TEXT NOT NULL DEFAULT '',
    err_code         TEXT NOT NULL DEFAULT '',
    err_msg          TEXT NOT NULL DEFAULT '',
    created_by       TEXT NOT NULL DEFAULT '',
    tenant_id        INT  NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX pay_division_order_uq ON pay_division (pay_order_id) WHERE state <> 'failed';
CREATE INDEX pay_division_state_idx ON pay_division (state, updated_at);

-- 台账：追加写。amount 以 mch_no 视角有符号：贷方为正（settle / fee_refund），借方为负（fee / sharing / withdrawal）。
-- rebate / rebate_refund 归属服务商（isv_no），记在触发它的商户行上，按 isv_no 汇总。
-- kind: fee|fee_refund|rebate|rebate_refund|sharing|settle|withdrawal|withdrawal_reversal|refund
CREATE TABLE pay_ledger (
    id         BIGSERIAL PRIMARY KEY,
    mch_no     TEXT NOT NULL,
    isv_no     TEXT NOT NULL DEFAULT '',
    if_code    TEXT NOT NULL DEFAULT '',
    kind       TEXT NOT NULL,
    ref_type   TEXT NOT NULL,                       -- order|refund|division|withdrawal
    ref_id     TEXT NOT NULL,
    ref_sub    TEXT NOT NULL DEFAULT '',            -- 同一 ref 下的子项（如分账接收方 account）
    amount     BIGINT NOT NULL,
    currency   TEXT NOT NULL DEFAULT 'CNY',
    memo       TEXT NOT NULL DEFAULT '',
    tenant_id  INT  NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (kind, ref_type, ref_id, ref_sub)
);
CREATE INDEX pay_ledger_mch_idx ON pay_ledger (mch_no, created_at);
CREATE INDEX pay_ledger_isv_idx ON pay_ledger (isv_no, created_at);

-- 对账批次：渠道账单 vs 本地终态订单。
CREATE TABLE pay_recon_batch (
    id             BIGSERIAL PRIMARY KEY,
    if_code        TEXT NOT NULL,
    isv_no         TEXT NOT NULL DEFAULT '',
    bill_date      DATE NOT NULL,
    bill_type      TEXT NOT NULL DEFAULT 'ALL',
    state          TEXT NOT NULL DEFAULT 'pending', -- pending|done|no_bill|failed
    channel_count  INT  NOT NULL DEFAULT 0,
    channel_amount BIGINT NOT NULL DEFAULT 0,
    local_count    INT  NOT NULL DEFAULT 0,
    local_amount   BIGINT NOT NULL DEFAULT 0,
    diff_count     INT  NOT NULL DEFAULT 0,
    err_msg        TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (if_code, isv_no, bill_date, bill_type)
);

CREATE TABLE pay_recon_diff (
    id               BIGSERIAL PRIMARY KEY,
    batch_id         BIGINT NOT NULL REFERENCES pay_recon_batch(id) ON DELETE CASCADE,
    diff_type        TEXT NOT NULL,                 -- missing_local|missing_channel|amount_mismatch|state_mismatch
    kind             TEXT NOT NULL DEFAULT 'pay',   -- pay|refund
    local_id         TEXT NOT NULL DEFAULT '',      -- pay_order_id / refund_order_id
    channel_order_no TEXT NOT NULL DEFAULT '',
    sub_mchid        TEXT NOT NULL DEFAULT '',
    local_amount     BIGINT NOT NULL DEFAULT 0,
    channel_amount   BIGINT NOT NULL DEFAULT 0,
    local_state      TEXT NOT NULL DEFAULT '',
    channel_state    TEXT NOT NULL DEFAULT '',
    resolved         BOOL NOT NULL DEFAULT false,
    note             TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX pay_recon_diff_batch_idx ON pay_recon_diff (batch_id);

-- 向前加列：支付单的分账状态与返佣快照；服务商的渠道返佣费率。
ALTER TABLE pay_order ADD COLUMN division_state  TEXT   NOT NULL DEFAULT 'none'; -- none|pending|done
ALTER TABLE pay_order ADD COLUMN rebate_rate_bps INT    NOT NULL DEFAULT 0;
ALTER TABLE pay_order ADD COLUMN rebate_amount   BIGINT NOT NULL DEFAULT 0;
ALTER TABLE pay_isv   ADD COLUMN rebate_rate_bps INT    NOT NULL DEFAULT 0;
