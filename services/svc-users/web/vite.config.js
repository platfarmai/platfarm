import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

export default defineConfig({
  base: "/api/users/console/",
  plugins: [vue()],
  build: { outDir: "dist", emptyOutDir: true },
});
