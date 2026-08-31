import { AppInfoService } from "../../bindings/github.com/gsoares85/hermes/internal/ui";
import type { Info } from "../../bindings/github.com/gsoares85/hermes/internal/version";

/** Build information of the running binary, produced by internal/version. */
export type AppInfo = Info;

/**
 * Shown when the backend is unreachable, as in a browser-only dev session.
 *
 * Deliberately not the placeholders internal/version falls back to. Repeating
 * "dev" and "none" here would claim the two sides agree on a value while
 * nothing keeps them in step, and would make an unreachable backend look
 * exactly like an unreleased build.
 */
export const unknownAppInfo: AppInfo = {
  version: "unavailable",
  commit: "unavailable",
  date: "unavailable",
  platform: "unavailable",
};

/**
 * Reads the build information from the Go side.
 *
 * The call goes through the generated bindings, so the shape of the payload is
 * the Go struct itself: if internal/version changes, this stops compiling
 * instead of failing at runtime.
 */
export async function fetchAppInfo(): Promise<AppInfo> {
  return await AppInfoService.Info();
}
