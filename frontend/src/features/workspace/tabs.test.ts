import { describe, expect, it } from "vitest";

import type { StatusView } from "../../api/connection";

import { activeOf, closed, moved, noTabs, opened, released, type Tabs } from "./tabs";

function status(id: string): StatusView {
  return {
    id,
    savedId: `saved-${id}`,
    state: "connected",
    diagnosis: { failed: false, class: "", summary: "", cause: "", nextStep: "", detail: "" },
    name: id,
    environment: "",
    readOnly: false,
    host: "db.example.com",
    port: 5432,
    database: "app",
    user: "reporting",
  };
}

function withOpen(...ids: string[]): Tabs {
  return ids.reduce((tabs, id): Tabs => opened(tabs, status(id)), noTabs);
}

function ids(tabs: Tabs): string[] {
  return tabs.open.map((tab): string => tab.id);
}

describe("opening a connection", () => {
  it("adds a tab and makes it the one in front", () => {
    const tabs = withOpen("a", "b");

    expect(ids(tabs)).toEqual(["a", "b"]);
    expect(tabs.activeId).toBe("b");
  });

  // Opening the same connection again is the same tab, not a second one, and
  // it keeps the place it had: a tab that jumped to the end when its state was
  // refreshed would move under the pointer.
  it("replaces a tab it already has, in place", () => {
    const again = opened(withOpen("a", "b"), { ...status("a"), state: "down" });

    expect(ids(again)).toEqual(["a", "b"]);
    expect(again.activeId).toBe("a");
    expect(again.open[0]?.state).toBe("down");
  });
});

describe("closing a tab", () => {
  it("takes it away", () => {
    expect(ids(closed(withOpen("a", "b", "c"), "b"))).toEqual(["a", "c"]);
  });

  // The neighbour on the right, which is what every editor does: closing a tab
  // leaves you where the work was going rather than where it had been.
  it("moves to the next one along when the one in front is closed", () => {
    expect(closed(withOpen("a", "b", "c"), "b").activeId).toBe("c");
  });

  it("moves back when the last one is closed", () => {
    const tabs = closed(withOpen("a", "b", "c"), "c");

    expect(tabs.activeId).toBe("b");
  });

  // Closing one that is not in front leaves the front alone.
  it("keeps the tab in front when another is closed", () => {
    const tabs = closed({ ...withOpen("a", "b", "c"), activeId: "a" }, "c");

    expect(tabs.activeId).toBe("a");
  });

  it("leaves nothing in front when the last tab goes", () => {
    expect(closed(withOpen("a"), "a")).toEqual(noTabs);
  });

  it("says nothing about a tab it does not have", () => {
    const tabs = withOpen("a", "b");

    expect(closed(tabs, "nope")).toEqual(tabs);
  });
});

describe("which tab is in front", () => {
  it("is the one the identifier names", () => {
    expect(activeOf(withOpen("a", "b"))?.id).toBe("b");
  });

  it("is nothing when there are no tabs", () => {
    expect(activeOf(noTabs)).toBeNull();
  });

  // The identifier and the list are two pieces of state and can disagree for a
  // render. Answering a tab that is not there would have the window draw a
  // connection nobody has open.
  it("is nothing when the identifier names no tab", () => {
    expect(activeOf({ ...withOpen("a"), activeId: "gone" })).toBeNull();
  });
});

describe("moving between tabs with the keyboard", () => {
  it("steps along and wraps at both ends", () => {
    const tabs = { ...withOpen("a", "b", "c"), activeId: "b" };

    expect(moved(tabs, "ArrowRight")).toBe("c");
    expect(moved(tabs, "ArrowLeft")).toBe("a");
    expect(moved({ ...tabs, activeId: "c" }, "ArrowRight")).toBe("a");
    expect(moved({ ...tabs, activeId: "a" }, "ArrowLeft")).toBe("c");
  });

  it("jumps to either end", () => {
    const tabs = { ...withOpen("a", "b", "c"), activeId: "b" };

    expect(moved(tabs, "Home")).toBe("a");
    expect(moved(tabs, "End")).toBe("c");
  });

  it("says nothing about a key that is not its business", () => {
    expect(moved(withOpen("a", "b"), "x")).toBeNull();
  });

  it("says nothing when there are no tabs to move between", () => {
    expect(moved(noTabs, "ArrowRight")).toBeNull();
  });
});

/**
 * A saved connection has one tab, whichever door it was opened by.
 *
 * The sidebar knows to come back to the tab a server already has. The dialog
 * did not: opening Manage on a connection that is already open and pressing
 * Connect gave a second tab onto the same server, with a second pool of its
 * own and a second cached catalog — the duplication the saved identifier was
 * put on the state to prevent, and twice the resting memory it was budgeted
 * for.
 */
describe("opening a connection that is already open by another route", () => {
  it("replaces the tab it already had rather than adding one", () => {
    const first = status("a");
    const again = { ...status("b"), savedId: first.savedId };

    const tabs = opened(opened(noTabs, first), again);

    expect(ids(tabs)).toEqual(["b"]);
    expect(tabs.activeId).toBe("b");
  });

  it("keeps the place the tab had", () => {
    const middle = status("b");
    const again = { ...status("d"), savedId: middle.savedId };

    const tabs = opened(withOpen("a", "b", "c"), again);

    expect(ids(tabs)).toEqual(["a", "d", "c"]);
  });

  /**
   * Answered by the replacement itself rather than asked beforehand.
   *
   * Two connections can be opening at once — a row in the sidebar and the
   * dialog — and each answers from the tab list it was started with. Asking
   * first and replacing afterwards let the second opening land in between, so
   * the first one replaced a tab it had been told was not there and the
   * connection that lost it stayed open with nothing able to close it.
   */
  it("queues the connection that lost its tab, so it can be released", () => {
    const first = status("a");
    const again = { ...status("b"), savedId: first.savedId };

    expect(opened(opened(noTabs, first), again).releasing).toEqual(["a"]);
  });

  it("queues nothing when the tab is the same connection refreshed", () => {
    const held = status("a");

    expect(opened(opened(noTabs, held), held).releasing).toEqual([]);
  });

  it("queues nothing when the connection is opening for the first time", () => {
    expect(opened(noTabs, status("a")).releasing).toEqual([]);
  });

  it("keeps a queued release that a second replacement has not made yet", () => {
    const first = status("a");
    const again = { ...status("b"), savedId: first.savedId };
    const third = { ...status("c"), savedId: first.savedId };

    expect(opened(opened(opened(noTabs, first), again), third).releasing).toEqual(["a", "b"]);
  });

  it("keeps a queued release through the closing of another tab", () => {
    const first = status("a");
    const again = { ...status("b"), savedId: first.savedId };
    const other = status("z");

    const tabs = opened(opened(opened(noTabs, first), other), again);

    expect(closed(tabs, "z").releasing).toEqual(["a"]);
  });

  it("takes a connection out of the queue once it has been released", () => {
    const first = status("a");
    const again = { ...status("b"), savedId: first.savedId };

    expect(released(opened(opened(noTabs, first), again), "a").releasing).toEqual([]);
  });

  it("leaves the rest of the queue alone", () => {
    const first = status("a");
    const again = { ...status("b"), savedId: first.savedId };
    const third = { ...status("c"), savedId: first.savedId };
    const tabs = opened(opened(opened(noTabs, first), again), third);

    expect(released(tabs, "a").releasing).toEqual(["b"]);
  });

  // Answering with the same object is what stops the effect that drains the
  // queue from setting state it has already set, every time it runs.
  it("answers with the tabs themselves when the connection is not queued", () => {
    const tabs = withOpen("a");

    expect(released(tabs, "a")).toBe(tabs);
  });

  // A connection opened from a form that was never saved has no saved
  // identifier, and two of them are two different servers as far as anything
  // here can tell. Matching on the empty string would fold every unsaved
  // connection into one tab.
  it("does not fold two unsaved connections together", () => {
    const one = { ...status("a"), savedId: "" };
    const two = { ...status("b"), savedId: "" };

    const tabs = opened(opened(noTabs, one), two);

    expect(ids(tabs)).toEqual(["a", "b"]);
    expect(tabs.releasing).toEqual([]);
  });
});

/**
 * The identifier and the list are two pieces of state and can disagree for a
 * render. activeOf already answers "no tab" when they do; this answered a tab,
 * and answered a different one per key: ArrowRight gave the first, which looks
 * deliberate, and ArrowLeft gave the second from the end, which is arithmetic
 * on a position of -1 rather than a decision anybody made.
 */
describe("moving between tabs when the one in front is unknown", () => {
  const adrift: Tabs = { ...withOpen("a", "b", "c"), activeId: "gone" };

  it("moves nowhere, whichever key it was", () => {
    for (const key of ["ArrowRight", "ArrowLeft", "Home", "End"]) {
      expect(moved(adrift, key)).toBeNull();
    }
  });

  it("still moves when the one in front is known", () => {
    const tabs = withOpen("a", "b", "c");

    expect(moved(tabs, "ArrowRight")).toBe("a");
    expect(moved(tabs, "ArrowLeft")).toBe("b");
    expect(moved(tabs, "Home")).toBe("a");
    expect(moved(tabs, "End")).toBe("c");
  });
});
