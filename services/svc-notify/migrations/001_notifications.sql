-- 通知统一表：email/webhook 为 outbox（worker 投递），inbox 为站内信（直接可读）。
CREATE TABLE notifications (
    id              BIGSERIAL PRIMARY KEY,
    channel         TEXT NOT NULL,                        -- email | webhook | inbox
    recipient       TEXT NOT NULL,                        -- email 地址 / webhook URL / inbox: userId
    subject         TEXT NOT NULL DEFAULT '',
    body            TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'pending',      -- pending|sent|failed（inbox: unread|read）
    attempts        INT  NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      TEXT NOT NULL DEFAULT '',             -- 发起方（svc id 或 username）
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at         TIMESTAMPTZ
);
CREATE INDEX notifications_pending_idx ON notifications (next_attempt_at) WHERE status = 'pending';
CREATE INDEX notifications_inbox_idx ON notifications (recipient) WHERE channel = 'inbox';
