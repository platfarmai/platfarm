import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

export default defineConfig({
  base: "/api/openapi/console/",
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
});
