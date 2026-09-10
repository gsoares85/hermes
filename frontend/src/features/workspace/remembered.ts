import { boundsOf, initial, resized, type Pane, type Side } from "./panes";

/**
 * How wide the panes were last time.
 *
 * It is a preference of this window on this machine, not a profile: it has
 * nothing to do with how a server is reached, so it has no business in the
 * connections file, which is meant to be read, copied between machines and
 * committed to a repository. Local storage is the right size of place for it.
 *
 * Everything that touches storage goes through the two functions at the bottom,
 * and the two above them are pure. The day this becomes part of a saved
 * workspace, the storage moves and the parsing does not.
 */
const where = "hermes.panes";

/**
 * The panes a stored string describes.
 *
 * Every failure lands on the default for that side rather than on an exception,
 * and the list of failures is longer than it looks: no key at all on a first
 * run, a truncated write, a file somebody opened and edited, a width that was
 * legal when it was written and is outside the bounds this build allows.
 *
 * Per side and not for the whole thing, so that one nonsense value does not
 * throw away a width that was fine.
 */
export function panesFrom(stored: string | null): Record<Side, Pane> {
  const held = parse(stored);

  return {
    objects: paneFrom(held?.["objects"], "objects"),
    details: paneFrom(held?.["details"], "details"),
  };
}

/** What to store for these panes. */
export function panesTo(panes: Record<Side, Pane>): string {
  return JSON.stringify(panes);
}

/**
 * What was stored, or the defaults.
 *
 * Reading local storage is not merely a lookup that can miss: in a private
 * window, with site data blocked, or in a context that has no storage at all —
 * a test runner, a thumbnail — the access itself throws. None of that can stop
 * the window drawing, so all of it lands on the defaults.
 */
export function rememberedPanes(): Record<Side, Pane> {
  try {
    return panesFrom(localStorage.getItem(where));
  } catch {
    return { ...initial };
  }
}

/** Remember them, and say nothing when there is nowhere to remember them. */
export function rememberPanes(panes: Record<Side, Pane>): void {
  try {
    localStorage.setItem(where, panesTo(panes));
  } catch {
    // Nowhere to write. The window works, it just starts at the default widths
    // next time, and there is nothing useful to tell somebody about that.
  }
}

function parse(stored: string | null): Record<string, unknown> | null {
  if (stored === null || stored === "") {
    return null;
  }

  try {
    const held: unknown = JSON.parse(stored);

    return typeof held === "object" && held !== null ? (held as Record<string, unknown>) : null;
  } catch {
    return null;
  }
}

function paneFrom(held: unknown, side: Side): Pane {
  if (typeof held !== "object" || held === null) {
    return initial[side];
  }

  const { width, open } = held as Record<string, unknown>;
  if (typeof width !== "number" || !Number.isFinite(width) || typeof open !== "boolean") {
    return initial[side];
  }

  // Clamped rather than refused. A width outside the bounds is what a build that
  // moved them leaves behind, and putting the pane back near where somebody had
  // it beats putting it back where it started.
  return { ...resized(width, boundsOf(side)), open };
}
