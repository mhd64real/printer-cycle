import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  build: {
    // Embedded into the dashboard binary by web/embed.go, so the output has to
    // stay inside this directory.
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    // During development the page is served by Vite and the API by the
    // dashboard binary, so calls are proxied rather than hard-coded to a port
    // that only exists in development.
    proxy: {
      "/api": "http://127.0.0.1:6311",
      "/healthz": "http://127.0.0.1:6311",
    },
  },
});
