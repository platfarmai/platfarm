# web-vue —— 服务后台 UI 脚手架（Vue 3 + Vite + pf-ui）

给需要 CRUD 后台的服务用的参考前端。硬约束与反模式见 `docs/ui-frameworks.md`（同源分发、构建期编译、设计系统只有 pf-ui 一份、hash 路由）。1–2 屏的简单页面不要用本模板，直接 vanilla + pf-ui。

## 用法

1. 拷贝本目录到你的服务：`services/<svc-id>/web/`（去掉本 README 可选）。
2. 全局替换 `svc-hello` 为你的服务 id：`vite.config.js` 的 `BASE`、`src/api.js` 的 `BASE`、`index.html` 标题。
3. `service.yaml` 声明后台入口：

   ```yaml
   admin_ui:
     path: /console
     embed: true   # 在平台控制台壳里以 iframe 嵌入
     title: Hello
   ```

4. Dockerfile 加 node 构建阶段（照抄 svc-users，产物进镜像、容器里不留 Node）：

   ```dockerfile
   FROM node:22-alpine AS web
   WORKDIR /web
   COPY web/package.json ./
   RUN npm install --no-audit --no-fund --registry=https://registry.npmmirror.com
   COPY web/ ./
   RUN npm run build
   # 之后在服务构建阶段 COPY --from=web /web/dist ./web/dist
   ```

5. 服务端把 `web/dist` 挂到 `<mount>/console` 下。Go 参考 `services/svc-users/main.go`（`//go:embed all:web/dist` + mountConsole）；其他语言用等价的静态文件路由即可。

## 本地开发（热更新，不用重建镜像）

```bash
cd services/<svc-id>/web
npm install --registry=https://registry.npmmirror.com
npm run dev
```

先在 `http://localhost:18000/platform/console/` 登录一次（Cookie 不区分端口），再开 vite 的地址开发；`/api` `/auth` `/platform` 已代理到本机网关，fetch 自动带 `pf_access`。

## 约定速查

- pf-* 是原生自定义元素：事件直接 `@pf-change`，`isCustomElement` 已配好。
- 样式：pf-ui 组件类 + `/platform/ui/v1/tw.css` 工具类，组件内不写局部 `<style>`（状态选择器除外）。
- 要路由时用 `createWebHashHistory`，不要 browser history。
- 禁止引入 Element Plus / AntDV / Naive UI 等第二套组件库。
