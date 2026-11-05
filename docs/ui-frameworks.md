# 服务 UI 用 Vue / React 的约定

结论：可行，svc-users 和 svc-openapi 已经是 Vue 3 + Vite。平台只认 HTTP + 同源 Cookie + `service.yaml`，前端框架是服务容器内部的实现细节。本文约定"用的时候守住哪几条线"。

## 硬约束（框架无关，违反即打回）

1. **同源分发**。构建产物由服务自己的容器、在自己的挂载路径下提供（如 `/api/users/console/`）。SSO 依赖 `pf_access` Cookie 同源自动携带、Kong 补 Bearer，前端代码永远不碰 token。禁止把前端单独部署到别的域/端口——那会立刻引入 CORS 和 token 管理。
2. **构建期编译，运行期纯静态**。多阶段 Dockerfile：node 阶段 `vite build`，产物 COPY 进最终镜像，容器内不留 Node 运行时。不做 SSR（后台无 SEO 需求，SSR 只换来常驻 Node 进程和故障面）。
3. **设计系统只有一份**。页面 `<link>` 平台分发的 `/platform/ui/v1/pf-ui.css + tw.css + pf-ui.js`，不把 pf-ui 打进 bundle（会双份加载、版本漂移）。组件内部样式用 `var(--pf-*)` 或 tw 工具类。主题免费：pf-ui.js 管 `<html data-pf-theme>`，CSS 变量天然穿透框架组件树。
4. **路由用 hash**（`createWebHashHistory` / `HashRouter`）。browser history 需要服务端 SPA fallback 和网关配合，后台场景不值得。

## 反模式（首方服务禁止）

- 引入第二套组件库（Element / AntD / MUI 等）——会带来第二套设计令牌。
- SSR、CDN 依赖、独立前端域名。
- React 18 及以下：pf-* 自定义元素的 CustomEvent（如 `pf-change`）要手写 ref + addEventListener，成本高。要用 React 必须 ≥19（原生支持 custom elements）。

## 框架适配

- **Vue 3（首选）**：`compilerOptions.isCustomElement: tag => tag.startsWith("pf-")`，事件直接 `@pf-change`。参考 `services/svc-users/web/`（vite base 设为挂载子路径，`credentials: "same-origin"` 的 fetch helper）。
- **React ≥19**：custom elements 属性/事件可用；建议为 `pf-change` 类事件封一个几行的 hook。

## 已评估：fast-crud（2026-10，v1.29.0，结论=暂不引入）

CRUD 框架 [fast-crud](https://github.com/fast-crud/fast-crud) 结构上必须四选一 UI 适配器（ui-element / ui-antdv / ui-antdv4 / ui-naive），分别绑死 Element Plus / AntDV / Naive UI；无 headless 模式，主题透传底层库、不吃 pf 令牌——引入即违反"第二套组件库"反模式。自写 pf-ui 适配器需实现 `UiInterface` 约 55 个组件绑定（Element 参考实现 1100+ 行），且 pf-ui 缺 datePicker/cascader/upload 等组件，成本多周级，不值。

例外通道：将来出现重表单大后台，可在**单个服务内封闭**引入 Naive UI + fast-crud（令牌尽量映射 `--pf-*`），在该服务文档里说明理由；禁止扩散到其他首方服务。

## 什么时候不用框架

1–2 屏的页面（账号中心、svc-demo 后台这类）用 vanilla + pf-ui 组件：没有构建链、改完即生效。经验阈值：有列表 + 表单 + 多视图状态流转的 CRUD 后台才值得上 Vue；一屏静态内容上框架是负资产。

## 后续增量（规划，未做）

- `templates/web-vue/` 参考目录或 `pctl new --ui vue`：index.html 引平台三件、vite.config 配好 base / isCustomElement、api helper。现在新服务靠手抄 svc-users，能用但容易漏配。
- 开发期代理：vite `server.proxy` 把 `/api` `/auth` `/platform` 代理到 `localhost:18000`，开发时登录一次网关、热更新照常。这是目前最大的 DX 空洞（现在改一行 Vue 要重建镜像才能看效果）。
- 第三方插件政策不变：不强制 pf-ui，但文档说明可以引同一份；其 UI 同样必须从自己容器、自己挂载路径下发。
