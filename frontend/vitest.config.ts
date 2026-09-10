import { defineConfig } from "vitest/config";

// A configuration of its own rather than a `test` block on vite.config.ts.
//
// That file describes how the frontend is served and built, including the port
// the Wails CLI picks and the output directory the Go binary embeds. None of it
// applies to a test run, and a single file would make every change to one a
// change to the other.
//
// The default environment is node, because most of what is tested here is the
// logic the window is drawn from — which rows are visible, what a level is
// allowed to do next — and that is deliberately pure: a test that had to mount
// a component to ask whether a collapsed node may be asked for again would be
// testing React.
//
// A file that needs a DOM asks for one with `// @vitest-environment happy-dom` at
// the top. There is a short list of things no pure test can reach — whether a
// <dialog> was opened as a modal, whether focus followed the tab that was
// selected — and every one of them has shipped broken with every other gate
// green.
export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.ts", "src/**/*.test.tsx"],
  },
});
