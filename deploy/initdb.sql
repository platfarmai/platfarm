-- Platfarm bundled-db 首次初始化（docker-entrypoint-initdb.d，仅新数据卷生效）。
-- 第一方服务的自有库（specs/015：一服务一库，pf_ 前缀）。
-- 存量卷 / 外部数据库模式：手工执行等价语句（见 .env.example 模式 B/C 注释）。
CREATE DATABASE pf_svc_file OWNER platfarm;
CREATE DATABASE pf_svc_notify OWNER platfarm;
CREATE DATABASE pf_svc_users OWNER platfarm;
CREATE DATABASE pf_svc_jobs OWNER platfarm;
CREATE DATABASE pf_svc_grants OWNER platfarm;
