# 019 tasks

- [x] promtail: `platfarm-services` scrape job (all containers, service/container labels, best-effort JSON level/requestId)
- [x] grafana container (observability profile, 127.0.0.1:18002) + provisioning: Loki datasource, dashboard "Platfarm · 日志总览", alert "网关 5xx 突增"
- [x] verified: grafana healthy, datasource/dashboard/alert provisioned, Loki `service` labels cover all 13 containers
