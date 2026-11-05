import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

const BASE = "/api/pay/console/";

export default defineConfig({
  base: BASE,
  plugins: [
    vue({
      template: {
        compilerOptions: {
          isCustomElement: (tag) => tag.startsWith("pf-"),
        },
      },
    }),
  ],
  build: { outDir: "dist", emptyOutDir: true },
  server: {
    proxy: {
      "/api": "http://localhost:18000",
      "/auth": "http://localhost:18000",
      "/platform": "http://localhost:18000",
    },
  },
});
