# 029: svc-ai — 平台 AI 管理中心（LLM 网关）

## Motivation

svc-gitpulse 引入了第一个 LLM 消费场景：key/base_url/模型散落在各服务 .env，无全局用量视图。
后续每个要用 AI 的服务都会重复这套配置。平台需要一个核心能力服务统一管 provider 凭据、
模型路由与按调用方计量——服务只持有一个平台签发的虚拟 key。

## Design

对等能力服务 `/api/ai`，自有库 `pf_svc_ai`（015 迁移规范）。协议 = **Anthropic Messages 透传**：
`POST /api/ai/v1/messages`，请求头 `x-api-key: <虚拟 key>`。调用方无需任何 SDK 改造——
凡是支持 `ANTHROPIC_BASE_URL` 重定向的客户端（含 svc-gitpulse）改两个 env 即接入。

- **providers**：上游凭据（kind=anthropic，base_url + api_key，可多个，enabled 开关）。
  启动时若表空且环境有 `AI_SEED_ANTHROPIC_KEY` 则自动种默认 provider（dev 降级）。
- **model_aliases**：`alias → (provider, model)` 路由；未命中别名时透传原 model 至默认
  （首个 enabled）provider，故零配置即可用。
- **ai_keys**：平台签发虚拟 key（`pfk-` 前缀，SHA-256 入库，明文仅创建时返回一次），
  按 key 维度计量与禁用。
- **usage**：每次调用记 `key_name/alias/model/input_tokens/output_tokens/status/latency_ms`，
  从上游响应 `usage` 字段解析；上游报错也计量（tokens=0, status=上游码）。
- 管理面（admin 角色，经网关 JWT）：providers / aliases / keys CRUD + usage 汇总，
  console 内嵌管理页（admin_ui，pf-ui）。
- 内网调用 `http://svc-ai:8080/api/ai/v1/messages` 不经网关，由虚拟 key 鉴权；
  经网关调用则额外多一层平台 JWT，两者皆可。

## Blast radius

新增 `services/svc-ai/`；svc-gitpulse 仅改 .env（ANTHROPIC_BASE_URL→svc-ai，key→虚拟 key），
代码零改动。不动平台契约与其他服务。

## Acceptance

虚拟 key 调 /v1/messages → 别名解析/透传 → 上游转发 → usage 落表（含上游 4xx 也计量）；
无效 key 401；非 admin 管理接口 403；gitpulse 经 svc-ai 完成一次真实日报生成；契约三件套绿。
