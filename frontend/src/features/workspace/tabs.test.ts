import { describe, expect, it } from "vitest";

import type { StatusView } from "../../api/connection";

import { activeOf, closed, moved, noTabs, opened, type Tabs } from "./tabs";

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
