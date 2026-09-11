/**
 * How wide the side panes are, and what a drag or a key does to them.
 *
 * All of it is arithmetic, and all of it is here rather than in the component,
 * because the component's job is to know where a pointer is and this is the
 * part that has answers worth pinning: what happens at either end, what a key
 * means on each side of the window, and what either does to a pane that has
 * been put away.
 */

/** Which pane. The side decides what an arrow key means to its divider. */
export type Side = "objects" | "details";

/** How narrow and how wide a pane is allowed to be. */
export interface Bounds {
  min: number;
  max: number;
}

/** A pane: how wide it is, and whether it is showing at all. */
export interface Pane {
  width: number;
  open: boolean;
}

/**
 * How far one press of an arrow moves a divider.
 *
 * Sixteen pixels: one press is visible, and crossing the whole range takes
 * about twenty presses rather than two hundred. Home and End are there for
 * anybody who wants the ends without the journey.
 */
export const step = 16;

/**
 * The bounds are per side because the two panes hold different things. The
 * navigator holds names, which are short; the details pane holds a column
 * called `estimated_delivery_window` beside its type, which is not.
 *
 * The minimums are the point of having bounds at all: a pane dragged to two
 * pixels is a pane nobody can grab again.
 */
const allowed: Record<Side, Bounds> = {
  objects: { min: 180, max: 480 },
  details: { min: 200, max: 560 },
};

export function boundsOf(side: Side): Bounds {
  return allowed[side];
}

/** What the panes are before anybody has moved them — the widths of the design. */
export const initial: Record<Side, Pane> = {
  objects: { width: 252, open: true },
  details: { width: 260, open: true },
};

/**
 * The pane a drag leaves behind.
 *
 * Rounded, because a pointer reports fractions of a pixel and a width that
 * carries them into a CSS variable is a width nobody can compare. Clamped,
 * because past either end the divider stops rather than following. And open,
 * because resizing something that is not there is the one reading of the
 * gesture that means nothing.
 */
export function resized(width: number, bounds: Bounds): Pane {
  return { width: clamp(Math.round(width), bounds), open: true };
}

/** Put a pane away, or bring it back at the width it had. */
export function toggled(pane: Pane): Pane {
  return { ...pane, open: !pane.open };
}

/**
 * What a key pressed on a divider does, or null when the key is not its
 * business — which is what tells the component whether to swallow the event.
 *
 * The arrows move the **divider**, not the pane. The divider of the right-hand
 * pane sits on its left edge, so the same key widens one pane and narrows the
 * other; the side is an argument rather than something each caller inverts and
 * hopes it got right.
 */
export function keyed(pane: Pane, key: string, side: Side, bounds: Bounds): Pane | null {
  if (key === "Enter" || key === " ") {
    return toggled(pane);
  }

  if (key === "Home" || key === "End") {
    return resized(key === "Home" ? bounds.min : bounds.max, bounds);
  }

  const towards = key === "ArrowRight" ? 1 : key === "ArrowLeft" ? -1 : 0;
  if (towards === 0) {
    return null;
  }

  const wider = side === "objects" ? towards : -towards;

  // A key that would narrow a pane nobody can see has nothing to act on, and
  // answering it would put the pane back on screen in order to shrink it.
  if (!pane.open && wider < 0) {
    return null;
  }

  return resized(pane.width + wider * step, bounds);
}

function clamp(width: number, bounds: Bounds): number {
  return Math.min(Math.max(width, bounds.min), bounds.max);
}
