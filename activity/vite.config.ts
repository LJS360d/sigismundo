import { defineConfig, loadEnv } from "vite";
import tailwindcss from "@tailwindcss/vite";

// `pnpm dev` serves the Activity and forwards the API (and the WebSocket) to
// the server; config in .env (see .env.example).
export default defineConfig(({ mode }) => {
  const env = { ...loadEnv(mode, process.cwd(), ""), ...process.env };
  return {
    plugins: [tailwindcss()],
    server: {
      port: Number(env.PORT ?? 5173),
      // reached through a tunnel when testing inside Discord
      allowedHosts: true,
      proxy: {
        "/api": { target: env.API_URL ?? "http://127.0.0.1:8080", ws: true },
      },
    },
    build: { outDir: "dist", emptyOutDir: true },
  };
});
