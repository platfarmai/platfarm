-- svc-gitpulse 初始结构：仓库 / 提交元数据 / AI 分析 / 问题闭环
CREATE TABLE repos (
    id             BIGSERIAL PRIMARY KEY,
    owner          TEXT NOT NULL,
    name           TEXT NOT NULL,
    token          TEXT NOT NULL DEFAULT '',   -- 留空 = 使用全局 GITHUB_TOKEN
    default_branch TEXT NOT NULL DEFAULT '',
    poll_enabled   BOOLEAN NOT NULL DEFAULT TRUE,
    last_polled_at TIMESTAMPTZ,
    last_commit_at TIMESTAMPTZ,
    created_by     TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner, name)
);

CREATE TABLE commits (
    id              BIGSERIAL PRIMARY KEY,
    repo_id         BIGINT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    sha             TEXT NOT NULL,
    author_name     TEXT NOT NULL DEFAULT '',
    author_login    TEXT NOT NULL DEFAULT '',
    message         TEXT NOT NULL DEFAULT '',
    committed_at    TIMESTAMPTZ NOT NULL,
    additions       INT NOT NULL DEFAULT 0,
    deletions       INT NOT NULL DEFAULT 0,
    files_changed   INT NOT NULL DEFAULT 0,
    files           JSONB NOT NULL DEFAULT '[]',  -- 改动文件路径列表
    convention_ok   BOOLEAN,                      -- NULL = 未检查（如 merge commit）
    convention_note TEXT NOT NULL DEFAULT '',
    ai_note         TEXT NOT NULL DEFAULT '',     -- 时间线上的 AI 注解
    UNIQUE (repo_id, sha)
);
CREATE INDEX idx_commits_repo_time ON commits (repo_id, committed_at DESC);

CREATE TABLE analyses (
    id           BIGSERIAL PRIMARY KEY,
    repo_id      BIGINT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL,                 -- daily_digest | weekly_trend
    period_start TIMESTAMPTZ NOT NULL,
    period_end   TIMESTAMPTZ NOT NULL,
    summary      TEXT NOT NULL DEFAULT '',      -- AI 中文报告（markdown）
    data         JSONB NOT NULL DEFAULT '{}',   -- 结构化指标
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_analyses_repo_kind ON analyses (repo_id, kind, created_at DESC);

CREATE TABLE issues (
    id                BIGSERIAL PRIMARY KEY,
    repo_id           BIGINT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    title             TEXT NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'open', -- open | ai_matched | verified | closed
    created_by        TEXT NOT NULL DEFAULT '',
    created_by_name   TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    verified_at       TIMESTAMPTZ,
    lead_time_seconds BIGINT
);
CREATE INDEX idx_issues_repo_status ON issues (repo_id, status);

CREATE TABLE issue_commits (
    id         BIGSERIAL PRIMARY KEY,
    issue_id   BIGINT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    commit_id  BIGINT NOT NULL REFERENCES commits(id) ON DELETE CASCADE,
    source     TEXT NOT NULL,                  -- message_ref | ai_match
    confidence REAL NOT NULL DEFAULT 1.0,
    status     TEXT NOT NULL DEFAULT 'pending',-- pending | confirmed | rejected
    reason     TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (issue_id, commit_id)
);
