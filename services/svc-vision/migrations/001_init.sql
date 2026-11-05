-- svc-vision 初始 schema（Postgres）。列与索引严格对应 internal/models/models.go 的 GORM tag。
-- 软删除：deleted_at TIMESTAMPTZ（可空）+ 索引。无 users / camera_permissions 表（身份走 PlatFarm JWT）。

CREATE TABLE IF NOT EXISTS cameras (
    id                     BIGSERIAL PRIMARY KEY,
    created_at             TIMESTAMPTZ,
    updated_at             TIMESTAMPTZ,
    deleted_at             TIMESTAMPTZ,

    name                   VARCHAR(100) NOT NULL,
    description            VARCHAR(500),

    protocol               VARCHAR(20) NOT NULL DEFAULT 'rtsp',
    ip                     VARCHAR(45) NOT NULL,
    port                   INTEGER DEFAULT 554,
    username               VARCHAR(50),
    password               VARCHAR(100),
    path                   VARCHAR(200),

    onvif_address          VARCHAR(200),
    onvif_profile_token    VARCHAR(100),
    discovered_stream_uri  VARCHAR(500),
    stream_uri_updated_at  TIMESTAMPTZ,
    device_id              VARCHAR(50),

    manufacturer           VARCHAR(100),
    model                  VARCHAR(100),
    firmware               VARCHAR(50),
    serial_number          VARCHAR(100),

    status                 VARCHAR(20) DEFAULT 'offline',
    last_online            TIMESTAMPTZ,
    error_msg              VARCHAR(500),

    record_enabled         BOOLEAN DEFAULT true,
    record_schedule        VARCHAR(100) DEFAULT '0-23',
    record_type            VARCHAR(20) DEFAULT 'continuous',

    width                  INTEGER DEFAULT 1920,
    height                 INTEGER DEFAULT 1080,
    fps                    INTEGER DEFAULT 25,
    bitrate                INTEGER DEFAULT 4096,
    codec                  VARCHAR(20) DEFAULT 'h264',

    ptz_enabled            BOOLEAN DEFAULT false
);
CREATE INDEX IF NOT EXISTS idx_cameras_name ON cameras (name);
CREATE INDEX IF NOT EXISTS idx_cameras_deleted_at ON cameras (deleted_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_cameras_device_id ON cameras (device_id);

CREATE TABLE IF NOT EXISTS recordings (
    id            BIGSERIAL PRIMARY KEY,
    created_at    TIMESTAMPTZ,
    updated_at    TIMESTAMPTZ,
    deleted_at    TIMESTAMPTZ,

    camera_id     BIGINT NOT NULL,

    start_time    TIMESTAMPTZ NOT NULL,
    end_time      TIMESTAMPTZ NOT NULL,
    duration      INTEGER,

    file_path     VARCHAR(500) NOT NULL,
    file_size     BIGINT,
    segment_index INTEGER,

    record_type   VARCHAR(20),
    status        VARCHAR(20) DEFAULT 'completed',

    index_path    VARCHAR(500),

    storage_type  VARCHAR(20) DEFAULT 'local',
    storage_path  VARCHAR(500)
);
CREATE INDEX IF NOT EXISTS idx_recordings_deleted_at ON recordings (deleted_at);
CREATE INDEX IF NOT EXISTS idx_recordings_camera_id ON recordings (camera_id);
CREATE INDEX IF NOT EXISTS idx_recordings_start_time ON recordings (start_time);
CREATE INDEX IF NOT EXISTS idx_recordings_end_time ON recordings (end_time);

CREATE TABLE IF NOT EXISTS snapshots (
    id           BIGSERIAL PRIMARY KEY,
    created_at   TIMESTAMPTZ,
    updated_at   TIMESTAMPTZ,
    deleted_at   TIMESTAMPTZ,

    camera_id    BIGINT NOT NULL,

    timestamp    TIMESTAMPTZ NOT NULL,
    file_path    VARCHAR(500) NOT NULL,
    file_size    BIGINT,
    type         VARCHAR(20) DEFAULT 'schedule',
    storage_type VARCHAR(20) DEFAULT 'local'
);
CREATE INDEX IF NOT EXISTS idx_snapshots_deleted_at ON snapshots (deleted_at);
CREATE INDEX IF NOT EXISTS idx_snapshots_camera_id ON snapshots (camera_id);
CREATE INDEX IF NOT EXISTS idx_snapshots_timestamp ON snapshots (timestamp);

CREATE TABLE IF NOT EXISTS alerts (
    id            BIGSERIAL PRIMARY KEY,
    created_at    TIMESTAMPTZ,
    updated_at    TIMESTAMPTZ,
    deleted_at    TIMESTAMPTZ,

    camera_id     BIGINT NOT NULL,

    type          VARCHAR(30) NOT NULL,
    level         VARCHAR(10) DEFAULT 'medium',
    message       VARCHAR(500),
    details       TEXT,

    snapshot_path VARCHAR(500),
    video_path    VARCHAR(500),

    status        VARCHAR(20) DEFAULT 'new',
    acked_by      BIGINT,
    acked_at      TIMESTAMPTZ,
    resolved_by   BIGINT,
    resolved_at   TIMESTAMPTZ,

    notified      BOOLEAN DEFAULT false,
    notify_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_alerts_deleted_at ON alerts (deleted_at);
CREATE INDEX IF NOT EXISTS idx_alerts_camera_id ON alerts (camera_id);
CREATE INDEX IF NOT EXISTS idx_alerts_type ON alerts (type);

CREATE TABLE IF NOT EXISTS system_configs (
    id         BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ,

    "key"      VARCHAR(100) NOT NULL,
    value      TEXT,
    "desc"     VARCHAR(200)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_system_configs_key ON system_configs ("key");
