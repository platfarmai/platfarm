import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

// 改成你的服务挂载路径 + admin_ui.path（见 service.yaml），结尾保留斜杠
const BASE = "/api/svc-hello/console/";

export default defineConfig({
  base: BASE,
  plugins: [
    vue({
      template: {
        compilerOptions: {
          // pf-ui 是原生自定义元素（docs/ui-frameworks.md）
          isCustomElement: (tag) => tag.startsWith("pf-"),
        },
      },
    }),
  ],
  build: { outDir: "dist", emptyOutDir: true },
  // 开发期代理：vite dev server 把平台路径转给本机网关。
  // Cookie 不区分端口：先在 http://localhost:18000/platform/console/ 登录一次，
  // 然后 `npm run dev` 热更新开发，fetch 自动带 pf_access。
  server: {
    proxy: {
      "/api": "http://localhost:18000",
      "/auth": "http://localhost:18000",
      "/platform": "http://localhost:18000",
    },
  },
});
