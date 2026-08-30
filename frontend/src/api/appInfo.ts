import { AppInfoService } from "../../bindings/github.com/gsoares85/hermes/internal/ui";
import type { Info } from "../../bindings/github.com/gsoares85/hermes/internal/version";

/** Build information of the running binary, produced by internal/version. */
export type AppInfo = Info;

/** Shown when the backend is unreachable, as in a browser-only dev session. */
export const unknownAppInfo: AppInfo = {
  version: "dev",
  commit: "none",
  date: "unknown",
  platform: "unknown",
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
