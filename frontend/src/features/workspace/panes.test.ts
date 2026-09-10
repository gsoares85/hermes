import { describe, expect, it } from "vitest";

import { boundsOf, initial, keyed, resized, step, toggled, type Pane } from "./panes";

const bounds = boundsOf("objects");

function pane(width: number, open = true): Pane {
  return { width, open };
}

describe("resizing a pane", () => {
  it("takes the width it is dragged to", () => {
    expect(resized(300, bounds)).toEqual({ width: 300, open: true });
  });

  // Dragged past either end it stops rather than following: a pane of two
  // pixels is a pane nobody can grab again, and one wider than the window
  // leaves nothing to look at.
  it("stops at the narrowest it is allowed to be", () => {
    expect(resized(10, bounds).width).toBe(bounds.min);
  });

  it("stops at the widest it is allowed to be", () => {
    expect(resized(9999, bounds).width).toBe(bounds.max);
  });

  it("rounds to whole pixels, because a pointer reports fractions of one", () => {
    expect(resized(300.6, bounds).width).toBe(301);
  });

  // Resizing something that is not there is the one reading of the gesture that
  // means nothing, so a width always answers a pane that is showing.
  it("always answers a pane that is open", () => {
    expect(resized(300, bounds).open).toBe(true);
  });
});

describe("putting a pane away", () => {
  it("closes an open one and keeps the width it had", () => {
    expect(toggled(pane(300))).toEqual({ width: 300, open: false });
  });

  it("opens a closed one at the width it had", () => {
    expect(toggled(pane(300, false))).toEqual({ width: 300, open: true });
  });
});

describe("the keyboard on a divider", () => {
  it("says nothing about a key that is not its business", () => {
    expect(keyed(pane(252), "a", "objects", bounds)).toBeNull();
    expect(keyed(pane(252), "Tab", "objects", bounds)).toBeNull();
  });

  // The arrows move the divider, and the divider of the right-hand pane is on
  // its left edge — so the same key widens one pane and narrows the other. Which
  // is why the side is an argument rather than something the caller inverts and
  // hopes it got right.
  it("moves the divider, not the pane", () => {
    expect(keyed(pane(252), "ArrowRight", "objects", bounds)?.width).toBe(252 + step);
    expect(keyed(pane(252), "ArrowLeft", "objects", bounds)?.width).toBe(252 - step);

    const other = boundsOf("details");

    expect(keyed(pane(260), "ArrowLeft", "details", other)?.width).toBe(260 + step);
    expect(keyed(pane(260), "ArrowRight", "details", other)?.width).toBe(260 - step);
  });

  it("stops at the ends like a drag does", () => {
    expect(keyed(pane(bounds.max), "ArrowRight", "objects", bounds)?.width).toBe(bounds.max);
    expect(keyed(pane(bounds.min), "ArrowLeft", "objects", bounds)?.width).toBe(bounds.min);
  });

  it("jumps to either end", () => {
    expect(keyed(pane(252), "Home", "objects", bounds)?.width).toBe(bounds.min);
    expect(keyed(pane(252), "End", "objects", bounds)?.width).toBe(bounds.max);
  });

  it("puts the pane away and brings it back", () => {
    expect(keyed(pane(252), "Enter", "objects", bounds)?.open).toBe(false);
    expect(keyed(pane(252, false), " ", "objects", bounds)?.open).toBe(true);
  });

  // A key that would make a pane that is not there narrower has nothing to act
  // on, and answering it would put a pane back on screen to shrink it.
  it("ignores a narrowing key on a pane that is put away", () => {
    expect(keyed(pane(252, false), "ArrowLeft", "objects", bounds)).toBeNull();
  });

  it("brings a pane back when the key would widen it", () => {
    expect(keyed(pane(252, false), "ArrowRight", "objects", bounds)).toEqual({
      width: 252 + step,
      open: true,
    });
  });

  it("brings a pane back when the key jumps to an end", () => {
    expect(keyed(pane(252, false), "Home", "objects", bounds)?.open).toBe(true);
  });
});

describe("the panes a window starts with", () => {
  it("starts both open, within their own bounds", () => {
    for (const side of ["objects", "details"] as const) {
      const started = initial[side];
      const allowed = boundsOf(side);

      expect(started.open).toBe(true);
      expect(started.width).toBeGreaterThanOrEqual(allowed.min);
      expect(started.width).toBeLessThanOrEqual(allowed.max);
    }
  });
});
