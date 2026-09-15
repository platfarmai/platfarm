# 006 实施清单

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] auth：login/refresh 写 `pf_access` Cookie；logout 清除（Bearer 保留）
- [x] pctl manifest：解析+校验 `admin_ui { path, embed, title, icon }`
- [x] pctl sync：Kong 全局 `pre-function`（Cookie→Bearer）+ `/platform/console` 路由到 console
- [x] pctl console：应用切换壳（读 `admin_ui`）+ iframe；登录写 Cookie；`/auth/me` 判断会话
- [x] svc-demo（或 CMS）声明 `admin_ui` 作为首个嵌入样例
- [x] QA：Cookie 登录、网关 console、iframe 免二次登录、Cookie→Bearer 200、logout 401
- [x] 文档：adding-a-service 加 `admin_ui`；ADR #21
