import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The Go binary embeds frontend/dist, so the build output directory is part of
// the contract with the backend and must not be renamed.
//
// emptyOutDir stays off on purpose: dist/.gitkeep is tracked so that a fresh
// clone compiles before anyone runs npm, and wiping the directory would delete
// it. Stale hashed assets are harmless — dist is ignored by Git and index.html
// always points at the current build. Use `npm run clean` to reset it.
export default defineConfig({
  plugins: [react()],
  // Dev mode runs Vite as the asset server and the Wails CLI picks the port,
  // so it has to be honoured exactly rather than fall back to another one.
  server: {
    host: "127.0.0.1",
    port: Number(process.env["WAILS_VITE_PORT"]) || 9245,
    strictPort: true,
  },
  build: {
    outDir: "dist",
    emptyOutDir: false,
  },
});
