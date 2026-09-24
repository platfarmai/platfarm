-- svc-jobs 初始表：jobs（异步任务队列）+ schedules（cron 调度定义）。
CREATE TABLE jobs (
    id           BIGSERIAL PRIMARY KEY,
    type         TEXT NOT NULL,
    payload      TEXT NOT NULL DEFAULT '',
    webhook      TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending', -- pending|running|done|failed|dead
    attempts     INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 5,
    run_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error   TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    done_at      TIMESTAMPTZ
);

-- 投递 worker 取件热路径：仅 pending 行入索引。
CREATE INDEX jobs_pending_run_at_idx ON jobs (run_at) WHERE status='pending';

CREATE TABLE schedules (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT UNIQUE NOT NULL,
    cron        TEXT NOT NULL,
    type        TEXT NOT NULL,
    payload     TEXT NOT NULL DEFAULT '',
    webhook     TEXT NOT NULL,
    enabled     BOOL NOT NULL DEFAULT true,
    last_run_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
