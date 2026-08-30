import { Call } from "@wailsio/runtime";

/** Build information of the running binary, produced by internal/version. */
export interface AppInfo {
  version: string;
  commit: string;
  date: string;
  platform: string;
}

/** Shown when the backend is unreachable, as in a browser-only dev session. */
export const unknownAppInfo: AppInfo = {
  version: "dev",
  commit: "none",
  date: "unknown",
  platform: "unknown",
};

const infoMethod = "ui.AppInfoService.Info";

function isAppInfo(value: unknown): value is AppInfo {
  if (typeof value !== "object" || value === null) {
    return false;
  }
  const candidate = value as Record<string, unknown>;
  return (
    typeof candidate["version"] === "string" &&
    typeof candidate["commit"] === "string" &&
    typeof candidate["date"] === "string" &&
    typeof candidate["platform"] === "string"
  );
}

/**
 * Reads the build information from the Go side.
 *
 * The value crossing the boundary is untrusted input as far as the compiler is
 * concerned, so it is validated instead of cast.
 */
export async function fetchAppInfo(): Promise<AppInfo> {
  const result: unknown = await Call.ByName(infoMethod);
  if (!isAppInfo(result)) {
    throw new TypeError(`${infoMethod} returned an unexpected payload`);
  }
  return result;
}
