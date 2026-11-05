# 029 tasks
- [x] services/svc-ai：providers/aliases/keys/usage + Messages 透传网关 + 管理 API + console UI
- [x] 集成：pf_svc_ai 建库 + .env（AI_SEED_ANTHROPIC_KEY 可选）+ pctl sync
- [x] svc-gitpulse 切换：SVC_GITPULSE_ANTHROPIC_BASE_URL=http://svc-ai:8080/api/ai + 虚拟 key
      （注意：宿主机环境变量 ANTHROPIC_BASE_URL 会在 compose 替换时遮蔽 .env 同名项，故用服务级变量）
- [x] QA：虚拟 key 鉴权 / 别名路由 / 透传 / 计量落表（含上游错误 502 计量）/ 401 / 403 / 契约三件套
- [ ] 待办：上游 claude.web3tm.top 中转 /v1/messages 持续 500（models 列表正常）——
      需确认该 cr_ token 的可用模型/权限，或在 AI 中心换一个可用 provider
