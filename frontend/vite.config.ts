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
  build: {
    outDir: "dist",
    emptyOutDir: false,
  },
});
