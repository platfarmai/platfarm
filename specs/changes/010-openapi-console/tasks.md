# 010 Implementation Checklist

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] auth: refactor register logic; internal apps API (list/create/update/rotate) with service-token + admin OBO gate
- [x] auth: create/rotate return secret once; list/update never return secret
- [x] svc-openapi backend (Go): /api/openapi/* admin-only, calls auth internal apps (service token + X-PF-User-Token), scope catalog
- [x] svc-openapi Vue SPA: list/create(secret once)/edit scope+rate/enable-disable/rotate; cookie SSO
- [x] plugin.yaml: admin_ui + accept_service_tokens; multi-stage Dockerfile
- [x] QA: create→secret once→token→open 200; remove scope→403; disable→401; rotate→old 401; non-admin 403
- [x] docs ADR #25; commit/push; bump submodule
