-- svc-ai 初始结构：上游 provider / 模型别名路由 / 虚拟 key / 用量计量
CREATE TABLE providers (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    kind       TEXT NOT NULL DEFAULT 'anthropic',  -- 协议类型，v1 仅 anthropic
    base_url   TEXT NOT NULL DEFAULT 'https://api.anthropic.com',
    api_key    TEXT NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE model_aliases (
    id          BIGSERIAL PRIMARY KEY,
    alias       TEXT NOT NULL UNIQUE,              -- 服务侧写的 "model" 值
    provider_id BIGINT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    model       TEXT NOT NULL,                     -- 转发给上游的真实模型名
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ai_keys (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,             -- 调用方标识（如 svc-gitpulse）
    key_hash     TEXT NOT NULL UNIQUE,             -- sha256(明文)，明文仅创建时返回一次
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ
);

CREATE TABLE usage (
    id            BIGSERIAL PRIMARY KEY,
    key_name      TEXT NOT NULL,
    alias         TEXT NOT NULL DEFAULT '',        -- 命中的别名（透传时为空）
    provider      TEXT NOT NULL,
    model         TEXT NOT NULL,
    input_tokens  INT NOT NULL DEFAULT 0,
    output_tokens INT NOT NULL DEFAULT 0,
    status        INT NOT NULL,                    -- 上游 HTTP 状态码
    latency_ms    INT NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_usage_time ON usage (created_at DESC);
CREATE INDEX idx_usage_key ON usage (key_name, created_at DESC);
