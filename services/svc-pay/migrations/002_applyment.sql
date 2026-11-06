-- svc-pay M2（specs/030）：子商户进件 + 审核流。
-- 状态机（全部 CAS）：draft → reviewing → approved → submitted → channel_auditing
--                     → need_sign | need_verify → finished；任何非终态 → rejected（可重提）| canceled。
-- payload 为渠道无关的归一结构（subject/legal_person/contact/business/settlement_account/rates/images），
-- 其中 legal_person.id_no 与 settlement_account.account_no 以 AES-GCM 密文存储。

CREATE TABLE pay_applyment (
    applyment_id         TEXT PRIMARY KEY,
    isv_no               TEXT NOT NULL,
    mch_no               TEXT NOT NULL,
    if_code              TEXT NOT NULL,
    business_code        TEXT NOT NULL,              -- 渠道侧业务申请编号，重提沿用
    payload              JSONB NOT NULL,
    state                TEXT NOT NULL DEFAULT 'draft',
    reject_reason        TEXT NOT NULL DEFAULT '',
    reviewer             TEXT NOT NULL DEFAULT '',
    reviewed_at          TIMESTAMPTZ,
    channel_applyment_id TEXT NOT NULL DEFAULT '',
    channel_state        TEXT NOT NULL DEFAULT '',   -- 渠道原始状态，排障用
    channel_msg          TEXT NOT NULL DEFAULT '',   -- 渠道驳回原因 / 最近一次提交错误
    sign_url             TEXT NOT NULL DEFAULT '',
    sub_mchid            TEXT NOT NULL DEFAULT '',
    unexpressed          TEXT NOT NULL DEFAULT '',   -- 渠道无法表达的归一字段（逗号分隔）
    poll_count           INT  NOT NULL DEFAULT 0,
    next_poll_at         TIMESTAMPTZ,
    created_by           TEXT NOT NULL DEFAULT '',
    tenant_id            INT  NOT NULL DEFAULT 0,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (isv_no, if_code, business_code)
);
CREATE INDEX pay_applyment_state_idx ON pay_applyment (state, next_poll_at);
CREATE INDEX pay_applyment_mch_idx ON pay_applyment (mch_no);

-- 进件图片：操作员经 svc-file 预签名上传后，由本服务在保存资料时拉取字节并留存，
-- 提交渠道时上传为渠道 media_id。审核员提交渠道无需再有文件读权限。
CREATE TABLE pay_applyment_media (
    id            BIGSERIAL PRIMARY KEY,
    applyment_id  TEXT NOT NULL REFERENCES pay_applyment(applyment_id) ON DELETE CASCADE,
    kind          TEXT NOT NULL,                     -- license|id_front|id_back|storefront|interior|...
    file_id       TEXT NOT NULL,                     -- svc-file 文件 id
    filename      TEXT NOT NULL DEFAULT '',
    mime          TEXT NOT NULL DEFAULT '',
    size          INT  NOT NULL DEFAULT 0,
    sha256        TEXT NOT NULL DEFAULT '',
    content       BYTEA NOT NULL,
    media_id      TEXT NOT NULL DEFAULT '',          -- 渠道 media_id（按 if_code 上传后回填）
    uploaded_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (applyment_id, kind)
);
