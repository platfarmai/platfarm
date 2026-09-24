-- 业务角色分配表（ADR #18）：某服务下某角色授予某用户；分配集中，语义留在各服务。
CREATE TABLE grants (
    id          BIGSERIAL PRIMARY KEY,
    service_id  TEXT NOT NULL,                        -- 目标服务 id（如 svc-file）
    role        TEXT NOT NULL,                        -- 服务内业务角色名（语义由该服务定义）
    user_id     INT  NOT NULL,                        -- 被授予的用户 id
    granted_by  TEXT NOT NULL DEFAULT '',             -- 授予人（admin username）
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (service_id, role, user_id)
);
CREATE INDEX grants_user_idx ON grants (user_id);
CREATE INDEX grants_service_role_idx ON grants (service_id, role);
