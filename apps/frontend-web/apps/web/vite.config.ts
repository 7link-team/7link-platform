import path from "path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig, loadEnv } from "vite"

export default defineConfig(({ mode }) => ({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      // 与源站一致：@/* → apps/web 根（源 tsconfig paths "@/*": ["./*"]），
      // 让迁移文件的 @/lib、@/components、@/hooks 零改动。src/ 仅放 Vite bootstrap。
      "@": path.resolve(__dirname, "."),
      "@workspace/ui": path.resolve(__dirname, "../../packages/ui/src"),
      // The viewer's bundled core resolves this optional Node canvas backend
      // even when its Office plugin is disabled. Keep that parser out of Web.
      "emf-converter": path.resolve(
        __dirname,
        "src/shims/emf-converter-browser.ts"
      ),
    },
  },
  server: {
    port: 8091,
    // Resource OPTIONS requests must reach the API instead of becoming empty 204s.
    cors: { preflightContinue: true },
    proxy: {
      // Browser calls use /api; the backend owns routes from /authn, /iam, and /oauth.
      "/api": {
        target: loadEnv(mode, process.cwd(), "VITE_").VITE_API_PROXY_TARGET ?? "http://127.0.0.1:8080",
        changeOrigin: true,
        rewrite: (requestPath) => requestPath.replace(/^\/api/u, ""),
      },
    },
  },
}))
