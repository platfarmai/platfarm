-- svc-pay：商户自助门户（商户中心）。
-- 一个商户绑定一个平台登录账号（auth 的 users.id）和一个开放平台 appKey。
-- 这张表本身就是授权：有 active 行的用户才能访问 /api/pay/portal/*，且只能看到 mch_no 这一个商户的数据。
-- 登录密码与 appSecret 都不落在这里（前者在 auth，后者 auth 只存哈希）。
CREATE TABLE pay_merchant_portal (
    mch_no       TEXT PRIMARY KEY REFERENCES pay_merchant(mch_no),
    user_id      INT  NOT NULL UNIQUE,            -- 一个登录账号只绑一个商户
    username     TEXT NOT NULL DEFAULT '',
    open_app_key TEXT NOT NULL DEFAULT '',        -- 开放平台 appKey（换 access token 用）；空 = 还没签发
    state        TEXT NOT NULL DEFAULT 'active',  -- active|disabled（停用后门户接口一律 403）
    tenant_id    INT  NOT NULL DEFAULT 0,
    created_by   INT  NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
