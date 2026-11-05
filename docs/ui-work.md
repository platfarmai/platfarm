# UI 工作说明

## 目标

管理界面看起来像 shadcn-admin：中性底、白卡片、细边框、左侧导航、主按钮对比清楚。

不引入 shadcn-admin，也不引入 shadcn-vue / React。那个模板是整站（React 或 Vue + Tailwind + 文件路由），平台页面还有纯静态 HTML 和 Python 内联页，接不进去。

做法是继续用 pf-ui（`/platform/ui/v1/pf-ui.css` + `pf-ui.js`）。所有页面只引这一份。Vue 控制台写自定义元素，静态页写 class。不复制样式进各个服务。

已引入 Tailwind CSS：standalone CLI（不需要 Node 工程）扫描页面产出 `/platform/ui/v1/tw.css`，和 pf-ui.css 一起由 console 容器静态分发，产物提交进 git。页面先引 pf-ui.css 再引 tw.css，不从 CDN 拉。

- 源文件：`platform/ui/src/tw.css`（只出 utilities，无 preflight；颜色/圆角映射 `--pf-*` 令牌，默认调色板已禁用）。
- 构建：仓库根目录执行 `./tools/tailwindcss.exe -i platform/ui/src/tw.css -o platform/ui/v1/tw.css --minify`（二进制从 tailwindcss GitHub releases 下载，已 gitignore）。
- 级联规则：utilities 在 `@layer` 里，永远输给 pf-ui.css 的未分层规则。工具类只做排版（flex/gap/margin/width），要约束 pf 组件尺寸时把类加在外层容器（如 `.pf-field w-20`），不要硬压组件规则。

## 已经做了什么

- 五个第一方面已引用 pf-ui + tw：管理壳、svc-users、svc-openapi、账号中心、svc-demo。
- 令牌改成接近 shadcn zinc：亮色底 `#fafafa`，字 `#09090b`，边框 `#e4e4e7`，圆角 8px / 12px。暗色同理。
- 主按钮用 `--pf-accent` / `--pf-accent-fg`，不再把暗色文字写死（pf-ui.css 里原先的 `color:#fff` + 暗色覆盖已删）。
- CSS 里有壳层 class：`.pf-shell`、`.pf-side`、`.pf-nav`、`.pf-main`、`.pf-card`。
- 管理壳 `tools/pctl/console.html` 改成左栏加内容区。登录、服务表、日志、审计在右栏。导航脚本没改。
- pf-ui.css 加了全局 `[hidden]{display:none!important}` 兜底：组件普遍显式给 display（`pf-button` 等），否则压过 UA 的 `[hidden]`——这正是控制台登录卡和面板叠在一起的根因，已修。
- `pf-ui.css` 重复的 `.pf-card`（壳层区 + demo helpers 区各一份）已合并成一处，padding 统一 20px。
- console.html 局部 `<style>` 清到只剩根元素高度和卡片标题两条；布局全部换成 tw 工具类 + pf-ui class；`pre.pf-log` 局部副本删掉改用 kit 的 `.pf-log`。
- svc-users / svc-openapi 的 App.vue：表格+工具栏套进 `.pf-card`，局部 `<style>` 基本清空（openapi 只留 `.chip[data-on]` 状态色），布局辅助类换 tw。
- 账号中心 `article.card` 改为 `pf-card card`，style.css 里与 pf-card 重复的边框/底色/圆角删掉。
- svc-demo 内联后台页内容区套 `.pf-card`，删掉局部 pf-log 副本。

## 主题切换（亮 / 暗 / 跟随系统）

- pf-ui.js 内置主题管理：偏好存 `localStorage("pf-theme")`（`light` / `dark`，缺省 = 跟随系统），同源各页共享；auto 模式监听 `prefers-color-scheme` 实时跟随，`storage` 事件做跨标签页同步。暴露 `window.pfTheme.get()/set(mode)`。
- 新组件 `<pf-theme-toggle>`：点击循环 跟随系统 → 亮色 → 暗色。已放进：控制台侧栏底部、svc-users / svc-openapi topbar、账号中心头部、demo.html topbar（demo 原自制切换按钮已删）。
- 原先写死的主题入口已清除：console.html 与 svc-demo 的 `data-pf-theme="dark"`、账号中心的 `<meta name="color-scheme" content="light">`。svc-demo 内联页不放切换按钮，跟随共享偏好。

## 浏览器验证结果（2026-10-09，Chrome，经网关 18000）

- `/platform/console/`：登出态只显示登录卡；登录态左栏导航 + 服务表/日志/审计三卡片正常，启停/日志/SSO iframe 嵌入均可用。
- svc-users / svc-openapi / svc-demo 后台、账号中心：同一套卡片壳渲染正常，交互（搜索、分页、限流改值、TOTP 区块）可用。
- demo.html 亮/暗两档：按钮、徽章、警告条、标签页、表格、分页、表单、对话框、确认、Toast、密钥、空态全部正常；暗色主按钮为浅底深字（accent-fg 生效）。
- 主题切换：三态循环与 localStorage 记忆 ✓；控制台设暗色后账号中心/Users 页同步变暗 ✓；auto 模式用 DevTools 模拟系统深浅色实时跟随 ✓；账号中心暗色下徽章/标签页/卡片均可读。
- 改动尚未提交。组件契约不变，不用新开 `/platform/ui/v2/`。svc-file 容器 Exited(1) 是本机缺 bundled-s3 的环境问题，与 UI 无关。

## 子模块用 Vue / React

可行且已有先例（svc-users / svc-openapi 是 Vue 3 + Vite）。硬约束、反模式、框架适配和后续增量见 [ui-frameworks.md](ui-frameworks.md)。

## 明确不做

- 不把控制台改成 React 或 shadcn-vue 工程。
- 不从 CDN 引 Tailwind、字体或图标（Tailwind 可以用，但走 standalone CLI 产出静态文件，见上）。
- 第三方插件控制台不强制改，文档里说明可以引同一份 pf-ui。
