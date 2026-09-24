# Platfarm UI 规范（specs/028）

平台所有管理界面（首方 console、账号中心、管理壳，以及自愿接入的第三方插件 console）共用一套设计令牌与组件库 **pf-ui**。

## 1. 引入方式（唯一正确姿势）

```html
<link rel="stylesheet" href="/platform/ui/v1/pf-ui.css">
<script src="/platform/ui/v1/pf-ui.js" defer></script>
```

- 经网关同源提供（公开路由，无需 token），所有 console 与静态页一行引入。
- **禁止**复制 pf-ui 文件进服务目录（会产生漂移）；禁止引外部 CDN 字体/图标/框架。
- Vue SPA 同样引上述两行（index.html），模板直接写 `<pf-button>` 等自定义元素；vite 需配置 `isCustomElement: tag => tag.startsWith('pf-')`。

## 2. 设计令牌（取值铁律）

任何颜色、字号、圆角、阴影、间距**必须**取自令牌，禁止硬编码：

| 类别 | 令牌 |
|---|---|
| 色彩 | `--pf-accent / --pf-accent-weak / --pf-success / --pf-danger / --pf-warning / --pf-bg / --pf-surface / --pf-surface-2 / --pf-border / --pf-text / --pf-text-muted` |
| 字体 | `--pf-font`（系统无衬线栈）/ `--pf-font-mono`；字号 `--pf-fs-12/13/14/16/20` |
| 形状 | `--pf-radius`(6px) / `--pf-radius-lg`(10px)；阴影 `--pf-shadow-1/2` |
| 间距 | 4px 网格（所有 padding/margin 为 4 的倍数） |

主题：默认亮色；`<html data-pf-theme="dark">` 切暗色（管理壳用暗色）。

## 3. 组件清单

**元素**（Web Components，浅 DOM，零依赖）：
`pf-button`（variant/size/loading）、`pf-badge`（tone）、`pf-alert`（tone/closable）、`pf-tabs`+`pf-tab`（active/hash 同步）、`pf-dialog`（.show/.close）、`pf-secret`（一次性凭据展示+复制）、`pf-pager`（total/limit/offset，`pf-change` 事件）、`pf-empty`、`pf-spinner`、`pf-topbar`。
**全局助手**：`pfToast(msg, tone)`、`pfConfirm(msg, {danger}) → Promise<boolean>`。
**样式类**：`.pf-container / .pf-table / .pf-form / .pf-field / .pf-toolbar / .pf-muted / .pf-code / .pf-kv / .pf-dot / .pf-link`。

活样例（即视觉验收标准）：`/platform/ui/v1/demo.html`。

## 4. 升级策略

- 版本化目录：破坏性变更发 `/platform/ui/v2/`，各面按节奏迁移，v1 保留到全部迁完。
- 非破坏性修补直接改 v1（同源缓存 1h）。
- 组件契约（元素名/属性/事件）变更必须走 specs 提案。

## 5. 接入要求

- 新建 console（`pctl new` 起步或手写）：**必须**引 pf-ui，禁止自带全局样式表（组件内局部样式除外）。
- 第三方插件 console：文档引导、不强制（沙箱内自担风格一致性）。
- 文案 zh-CN、不用 emoji、可交互元素必须有 `:focus-visible` 可见焦点（pf-ui 自带）。
