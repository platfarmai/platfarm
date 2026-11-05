-- svc-pay M1（specs/030）：商户体系 + 支付/退款订单 + 下游通知 outbox。
-- 金额一律 BIGINT 分。幂等靠唯一键，状态流转靠 CAS。

-- 服务商主体。fund_mode: direct 直清（渠道结算给子商户）| controlled 归集（M4）。
CREATE TABLE pay_isv (
    isv_no      TEXT PRIMARY KEY,
    isv_name    TEXT NOT NULL,
    fund_mode   TEXT NOT NULL DEFAULT 'direct',
    state       TEXT NOT NULL DEFAULT 'active', -- active|disabled
    remark      TEXT NOT NULL DEFAULT '',
    tenant_id   INT  NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 商户。mch_type: 1 普通商户（自有渠道参数）| 2 特约商户（挂服务商名下）。
CREATE TABLE pay_merchant (
    mch_no        TEXT PRIMARY KEY,
    mch_name      TEXT NOT NULL,
    mch_short_name TEXT NOT NULL DEFAULT '',
    mch_type      INT  NOT NULL DEFAULT 2,
    isv_no        TEXT NOT NULL DEFAULT '',
    contact_name  TEXT NOT NULL DEFAULT '',
    contact_phone TEXT NOT NULL DEFAULT '',
    state         TEXT NOT NULL DEFAULT 'active', -- active|disabled
    remark        TEXT NOT NULL DEFAULT '',
    tenant_id     INT  NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 商户应用：下游系统调用开放 API 的身份。app_secret 只存哈希。
CREATE TABLE pay_app (
    app_id        TEXT PRIMARY KEY,
    app_name      TEXT NOT NULL DEFAULT '',
    mch_no        TEXT NOT NULL REFERENCES pay_merchant(mch_no),
    app_secret    TEXT NOT NULL,
    state         TEXT NOT NULL DEFAULT 'active', -- active|disabled
    remark        TEXT NOT NULL DEFAULT '',
    tenant_id     INT  NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX pay_app_mch_idx ON pay_app (mch_no);

-- 渠道参数。owner_type: isv|merchant。if_params 为 AES-GCM 密封后的密文。
CREATE TABLE pay_channel_config (
    id          BIGSERIAL PRIMARY KEY,
    owner_type  TEXT NOT NULL,
    owner_id    TEXT NOT NULL,
    if_code     TEXT NOT NULL,
    if_params   TEXT NOT NULL,
    state       TEXT NOT NULL DEFAULT 'active',
    tenant_id   INT  NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner_type, owner_id, if_code)
);

-- 支付方式路由：一个应用的一种 way_code 只有一条生效路由。
CREATE TABLE pay_passage (
    id         BIGSERIAL PRIMARY KEY,
    app_id     TEXT NOT NULL REFERENCES pay_app(app_id),
    mch_no     TEXT NOT NULL,
    if_code    TEXT NOT NULL,
    way_code   TEXT NOT NULL,
    rate_bps   INT  NOT NULL DEFAULT 0, -- 费率，万分位：60 = 0.60%
    state      TEXT NOT NULL DEFAULT 'active',
    tenant_id  INT  NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (app_id, way_code)
);

-- 支付订单。state: created|paying|success|failed|closed。
CREATE TABLE pay_order (
    pay_order_id      TEXT PRIMARY KEY,
    mch_no            TEXT NOT NULL,
    isv_no            TEXT NOT NULL DEFAULT '',
    app_id            TEXT NOT NULL,
    mch_type          INT  NOT NULL,
    mch_order_no      TEXT NOT NULL,
    if_code           TEXT NOT NULL,
    way_code          TEXT NOT NULL,
    amount            BIGINT NOT NULL,
    currency          TEXT NOT NULL DEFAULT 'CNY',
    fee_rate_bps      INT  NOT NULL DEFAULT 0,
    fee_amount        BIGINT NOT NULL DEFAULT 0,
    state             TEXT NOT NULL DEFAULT 'created',
    refund_state      TEXT NOT NULL DEFAULT 'none', -- none|partial|full
    refund_amount     BIGINT NOT NULL DEFAULT 0,
    subject           TEXT NOT NULL,
    body              TEXT NOT NULL DEFAULT '',
    client_ip         TEXT NOT NULL DEFAULT '',
    channel_extra     TEXT NOT NULL DEFAULT '',
    channel_user      TEXT NOT NULL DEFAULT '',
    channel_order_no  TEXT NOT NULL DEFAULT '',
    err_code          TEXT NOT NULL DEFAULT '',
    err_msg           TEXT NOT NULL DEFAULT '',
    notify_url        TEXT NOT NULL DEFAULT '',
    return_url        TEXT NOT NULL DEFAULT '',
    notify_state      TEXT NOT NULL DEFAULT 'none', -- none|pending|sent
    ext_param         TEXT NOT NULL DEFAULT '',
    expired_at        TIMESTAMPTZ,
    success_at        TIMESTAMPTZ,
    tenant_id         INT  NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (mch_no, mch_order_no)
);
CREATE INDEX pay_order_state_idx ON pay_order (state, created_at);

-- 退款订单。state: created|processing|success|failed|closed。
CREATE TABLE pay_refund (
    refund_order_id  TEXT PRIMARY KEY,
    pay_order_id     TEXT NOT NULL,
    mch_no           TEXT NOT NULL,
    isv_no           TEXT NOT NULL DEFAULT '',
    app_id           TEXT NOT NULL,
    mch_refund_no    TEXT NOT NULL,
    if_code          TEXT NOT NULL,
    way_code         TEXT NOT NULL,
    pay_amount       BIGINT NOT NULL,
    refund_amount    BIGINT NOT NULL,
    currency         TEXT NOT NULL DEFAULT 'CNY',
    state            TEXT NOT NULL DEFAULT 'created',
    reason           TEXT NOT NULL DEFAULT '',
    channel_order_no TEXT NOT NULL DEFAULT '',
    err_code         TEXT NOT NULL DEFAULT '',
    err_msg          TEXT NOT NULL DEFAULT '',
    notify_url       TEXT NOT NULL DEFAULT '',
    success_at       TIMESTAMPTZ,
    tenant_id        INT  NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (mch_no, mch_refund_no)
);

-- 下游商户通知 outbox。order_type: pay|refund。state: notifying|success|failed。
CREATE TABLE pay_notify (
    id            BIGSERIAL PRIMARY KEY,
    order_id      TEXT NOT NULL,
    order_type    TEXT NOT NULL,
    mch_no        TEXT NOT NULL,
    mch_order_no  TEXT NOT NULL,
    app_id        TEXT NOT NULL,
    notify_url    TEXT NOT NULL,
    payload       TEXT NOT NULL,
    res_result    TEXT NOT NULL DEFAULT '',
    notify_count  INT  NOT NULL DEFAULT 0,
    notify_limit  INT  NOT NULL DEFAULT 7,
    state         TEXT NOT NULL DEFAULT 'notifying',
    next_retry_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    tenant_id     INT  NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (order_id, order_type)
);
CREATE INDEX pay_notify_due_idx ON pay_notify (next_retry_at) WHERE state='notifying';
