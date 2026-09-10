import { defineConfig } from "vitest/config";

// A configuration of its own rather than a `test` block on vite.config.ts.
//
// That file describes how the frontend is served and built, including the port
// the Wails CLI picks and the output directory the Go binary embeds. None of it
// applies to a test run, and a single file would make every change to one a
// change to the other.
//
// The environment is node, not jsdom. What is tested here is the logic the
// window is drawn from — which rows are visible, what a level is allowed to do
// next — and that is deliberately pure: a test that had to mount a component to
// ask whether a collapsed node may be asked for again would be testing React.
export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
});
