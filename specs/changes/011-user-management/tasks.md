# 011 Implementation Checklist

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] auth: internal users API (list/create/patch/reset-password) + admin OBO gate
- [x] auth: `POST /auth/change-password` (verify old, revoke current token)
- [x] auth: anti-lockout (last admin can't be demoted/disabled; no self disable/demote); disable revokes tokens
- [x] auth: create/reset return password once; list never returns hash; whitelist svc-users
- [x] svc-users backend (Go): /api/users/* proxy (service token + OBO) + self change-password passthrough
- [x] svc-users Vue SPA: admin list/create/role/enable-disable/reset + self change-password; cookie SSO
- [x] plugin.yaml admin_ui + multi-stage Dockerfile; register svc-users client creds
- [x] QA acceptance matrix; docs ADR #26; commit/push
