import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

export default defineConfig({
  base: "/api/openapi/console/",
  plugins: [vue()],
  build: { outDir: "dist", emptyOutDir: true },
});
