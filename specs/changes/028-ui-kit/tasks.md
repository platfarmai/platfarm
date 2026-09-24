# 028 tasks

- [x] platform/ui/v1/: pf-ui.css（令牌+基础类）+ pf-ui.js（custom elements + pfToast/pfConfirm）+ demo.html 活样例（unspecified-high 代理交付）
- [x] pctl serve: /platform/ui/* 静态分发（Cache-Control 1h）；renderKong 公开路由 platform-ui
- [x] docs/ui-spec.md 规范（令牌铁律/组件契约/升级策略/接入要求）
- [x] demo.html 浏览器验收：亮/暗双主题全组件渲染
- [x] 迁移 svc-users web + svc-openapi web（Vue：isCustomElement + pf-topbar/table/pager/secret/confirm，删私有样式）
- [x] 迁移 console.html（暗色壳：变体切 tab、pf-dot 状态、令牌化 pre）+ svc-demo 内联 + account/（pf-button/alert/badge/kv，Enter 提交经隐藏原生 submit）
- [x] 全面重建 + Playwright 逐面 QA（壳/Users/OpenAPI/Demo/账号中心 五面截图与交互）+ pctl check --e2e 8 服务全绿
- [ ] kit v1.1 待办（迁移反馈的缺口）：pf-button 提交变体（type=submit）、日志/pre 表面类、checkbox/chip 原语、裸 input 的非 .pf-field 样式
