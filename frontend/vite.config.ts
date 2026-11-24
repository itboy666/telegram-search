import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
export default defineConfig(({ command }) => ({
  plugins: [vue()],
  base: command === "serve" ? "/" : "/build/",
  build: {
    outDir: "../public/build",
    emptyOutDir: true,
    chunkSizeWarningLimit: 1200,
  },
  server: {
    proxy: {
      "/install/api": "http://127.0.0.1:8088",
      "/admin/api": "http://127.0.0.1:8088",
      "/user/api": "http://127.0.0.1:8088",
      "/api": "http://127.0.0.1:8088",
      "/favicon.svg": "http://127.0.0.1:8088",
      "/icons": "http://127.0.0.1:8088",
    },
  },
}));
