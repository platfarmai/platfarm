import { writeFileSync } from "node:fs";
import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

const BASE = "/api/pay/console/";

// go:embed all:web/dist 要求目录至少有一个文件；dist/.gitkeep 入库，emptyOutDir 清空后再补回。
const keepGitkeep = () => ({
  name: "keep-gitkeep",
  closeBundle() {
    writeFileSync("dist/.gitkeep", "");
  },
});

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
    keepGitkeep(),
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
