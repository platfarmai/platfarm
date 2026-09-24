-- 找回密码验证码（specs/018）：hash 存储，15 分钟有效，attempts 防爆破。
CREATE TABLE reset_codes (
    id         BIGSERIAL PRIMARY KEY,
    user_id    INT  NOT NULL,
    email      TEXT NOT NULL,
    code_hash  TEXT NOT NULL,
    attempts   INT  NOT NULL DEFAULT 0,
    used       BOOL NOT NULL DEFAULT false,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX reset_codes_email_idx ON reset_codes (email, created_at);
