-- svc-pay：代理商（分销）层级与级差分润。
-- 代理商挂在服务商（isv_no）名下，归属服务商所在租户，按 parent_agent_no 构成树（最多 5 级）。
-- rate_bps 是上级让给本代理商整条线（自己 + 所有下级）的分润费率（万分位）：
--   isv.rebate_rate_bps >= 一级.rate_bps >= 二级.rate_bps >= ... >= 0
-- 某一级实得 = 本级费率对应金额 − 紧邻下级费率对应金额（级差），各级之和恒等于一级的金额，不超过服务商返佣。
-- 分润是服务商对代理商的应付款，只记台账（线下结算后登记 agent_payout），不经渠道划拨。

CREATE TABLE pay_agent (
    agent_no        TEXT PRIMARY KEY,
    agent_name      TEXT NOT NULL,
    isv_no          TEXT NOT NULL REFERENCES pay_isv(isv_no),
    parent_agent_no TEXT NOT NULL DEFAULT '',       -- 空 = 一级代理商
    level           INT  NOT NULL DEFAULT 1,
    rate_bps        INT  NOT NULL DEFAULT 0,
    contact_name    TEXT NOT NULL DEFAULT '',
    contact_phone   TEXT NOT NULL DEFAULT '',
    state           TEXT NOT NULL DEFAULT 'active', -- active|disabled（停用后新订单不再给它计分润）
    remark          TEXT NOT NULL DEFAULT '',
    tenant_id       INT  NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX pay_agent_isv_idx ON pay_agent (isv_no);
CREATE INDEX pay_agent_parent_idx ON pay_agent (parent_agent_no) WHERE parent_agent_no <> '';

-- 商户归属的代理商（直属那一级）。
ALTER TABLE pay_merchant ADD COLUMN agent_no TEXT NOT NULL DEFAULT '';
CREATE INDEX pay_merchant_agent_idx ON pay_merchant (agent_no) WHERE agent_no <> '';

-- 下单时快照：直属代理商、各级分润合计、逐级明细 [{agentNo, level, rateBps, amount, tenantId}]。
-- 之后改费率、改归属都不影响已有订单。
ALTER TABLE pay_order ADD COLUMN agent_no     TEXT   NOT NULL DEFAULT '';
ALTER TABLE pay_order ADD COLUMN agent_amount BIGINT NOT NULL DEFAULT 0;
ALTER TABLE pay_order ADD COLUMN agent_shares JSONB  NOT NULL DEFAULT '[]'::jsonb;

-- 台账新增科目 agent_share / agent_share_refund / agent_payout，agent_no 标明归属的代理商。
ALTER TABLE pay_ledger ADD COLUMN agent_no TEXT NOT NULL DEFAULT '';
CREATE INDEX pay_ledger_agent_idx ON pay_ledger (agent_no, created_at) WHERE agent_no <> '';
