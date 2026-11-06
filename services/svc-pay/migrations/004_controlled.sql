-- svc-pay M4（specs/030）：归集模式。资金先落平台控制账户，平台确认结算、分账，子商户提现。
-- 直清渠道没有这些行。金额 BIGINT 分；提现按 (mch_no, mch_withdraw_no) 幂等。

-- 结算确认：一笔支付单只有一条未失败的确认（失败可重试）。
-- amount = 订单金额 - 已分账金额（向下取整的分账之后剩下的，归子商户）。
CREATE TABLE pay_settle (
    settle_no        TEXT PRIMARY KEY,
    pay_order_id     TEXT NOT NULL,
    mch_no           TEXT NOT NULL,
    isv_no           TEXT NOT NULL DEFAULT '',
    if_code          TEXT NOT NULL,
    order_amount     BIGINT NOT NULL,
    shared_amount    BIGINT NOT NULL DEFAULT 0,
    amount           BIGINT NOT NULL,               -- 本次确认结算给子商户的金额
    state            TEXT NOT NULL DEFAULT 'pending', -- pending|success|failed
    channel_order_no TEXT NOT NULL DEFAULT '',
    err_code         TEXT NOT NULL DEFAULT '',
    err_msg          TEXT NOT NULL DEFAULT '',
    tenant_id        INT  NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX pay_settle_order_uq ON pay_settle (pay_order_id) WHERE state <> 'failed';

-- 子商户提现。只在归集模式渠道上存在。
CREATE TABLE pay_withdrawal (
    withdraw_no      TEXT PRIMARY KEY,
    mch_no           TEXT NOT NULL,
    isv_no           TEXT NOT NULL DEFAULT '',
    if_code          TEXT NOT NULL,
    mch_withdraw_no  TEXT NOT NULL,                 -- 商户侧单号，幂等键
    amount           BIGINT NOT NULL,
    remark           TEXT NOT NULL DEFAULT '',
    state            TEXT NOT NULL DEFAULT 'pending', -- pending|accepted|success|failed
    channel_order_no TEXT NOT NULL DEFAULT '',
    err_code         TEXT NOT NULL DEFAULT '',
    err_msg          TEXT NOT NULL DEFAULT '',
    created_by       TEXT NOT NULL DEFAULT '',
    tenant_id        INT  NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (mch_no, mch_withdraw_no)
);
CREATE INDEX pay_withdrawal_state_idx ON pay_withdrawal (state, updated_at);

-- 订单是否已确认结算（归集模式）。none|pending|done。
ALTER TABLE pay_order ADD COLUMN settle_state TEXT NOT NULL DEFAULT 'none';
