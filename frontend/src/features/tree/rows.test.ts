import { describe, expect, it } from "vitest";

import type { NodeView } from "../../api/tree";

import {
  abandoned,
  around,
  askAgain,
  asking,
  keyOf,
  matches,
  needsAsking,
  objectOf,
  parentOf,
  refOf,
  rootKey,
  toggling,
  visibleRows,
  type Level,
  type Levels,
  type Row,
} from "./rows";

function node(name: string, expandable = false): NodeView {
  return { kind: expandable ? "schema" : "table", name, expandable };
}

function ready(nodes: NodeView[]): Level {
  return { state: "ready", nodes, error: "" };
}

/** The tree the rest of these tests walk: one database, two schemas, one table. */
function loaded(): Levels {
  return new Map([
    [rootKey, ready([node("analytics", true)])],
    ["analytics", ready([node("reporting", true), node("public", true)])],
    [keyOf("analytics", "reporting"), ready([node("daily_revenue"), node("invoice_id_seq")])],
  ]);
}

function names(levels: Levels, expanded: string[], text: string): string[] {
  return visibleRows(levels, new Set(expanded), text).map((row): string => row.node.name);
}

describe("keys", () => {
  // A NUL separator, because every printable character is legal in an
  // identifier: a schema really can be called `a/b`, and a tree keyed on a
  // printable separator would put two different objects in one place.
  it("keeps two objects apart when their names contain the usual separators", () => {
    expect(keyOf(keyOf("db", "a.b"), "t")).not.toBe(keyOf(keyOf("db", "a"), "b.t"));
  });

  it("addresses a level by the database and schema the key names", () => {
    expect(refOf(rootKey)).toEqual({ database: "", schema: "" });
    expect(refOf("analytics")).toEqual({ database: "analytics", schema: "" });
    expect(refOf(keyOf("analytics", "reporting"))).toEqual({
      database: "analytics",
      schema: "reporting",
    });
  });

  // Only a three-part key is an object. A level has nothing for the panel to
  // show, and answering one would ask the Go side for properties of a schema.
  it("answers an object only for a key that names one", () => {
    expect(objectOf(keyOf(keyOf("analytics", "reporting"), "daily_revenue"))).toEqual({
      database: "analytics",
      schema: "reporting",
      name: "daily_revenue",
    });
    expect(objectOf("analytics")).toBeNull();
    expect(objectOf(keyOf("analytics", "reporting"))).toBeNull();
  });
});

describe("matching", () => {
  it("is a case-insensitive substring, and empty text matches everything", () => {
    expect(matches("Daily_Revenue", "revenue")).toBe(true);
    expect(matches("daily_revenue", "")).toBe(true);
    expect(matches("daily_revenue", "invoice")).toBe(false);
  });

  it("splits a name around the match so the middle can be marked", () => {
    expect(around("daily_revenue", "reve")).toEqual({
      before: "daily_",
      match: "reve",
      after: "nue",
    });
  });

  // The match is echoed from the name rather than from what was typed, so the
  // mark keeps the capitals the object actually has.
  it("marks the name as it is written, not as it was typed", () => {
    expect(around("Daily_Revenue", "revenue").match).toBe("Revenue");
  });

  it("leaves a name that does not match whole and unmarked", () => {
    expect(around("daily_revenue", "invoice")).toEqual({
      before: "daily_revenue",
      match: "",
      after: "",
    });
    expect(around("daily_revenue", "")).toEqual({ before: "daily_revenue", match: "", after: "" });
  });
});

describe("visible rows", () => {
  it("shows only what has been asked for, so a closed node costs nothing", () => {
    expect(names(loaded(), [], "")).toEqual(["analytics"]);
    expect(names(loaded(), ["analytics"], "")).toEqual(["analytics", "reporting", "public"]);
  });

  it("walks depth first, so a child comes directly under its parent", () => {
    expect(names(loaded(), ["analytics", keyOf("analytics", "reporting")], "")).toEqual([
      "analytics",
      "reporting",
      "daily_revenue",
      "invoice_id_seq",
      "public",
    ]);
  });

  it("carries the depth of every row, because nesting is a number and not a shape", () => {
    const rows = visibleRows(loaded(), new Set(["analytics", keyOf("analytics", "reporting")]), "");

    expect(rows.map((row): number => row.depth)).toEqual([0, 1, 2, 2, 1]);
  });

  // A node that is open and still being fetched contributes itself and nothing
  // else, which is what lets the row say it is working without the tree jumping
  // about when the answer lands.
  it("draws a node that is still being asked for without anything under it", () => {
    const levels: Levels = new Map([
      [rootKey, ready([node("analytics", true)])],
      ["analytics", { state: "asking", nodes: [], error: "" }],
    ]);

    expect(names(levels, ["analytics"], "")).toEqual(["analytics"]);
  });

  // The level on a row is the one holding that node's children, whether or not
  // the node is open: it is what the row draws its working and failed marks
  // from, and a row that only learned about it on expansion could not say that
  // the last attempt to open it had failed.
  it("hands each row the level of what is under it, and undefined until it arrives", () => {
    const unopened: Levels = new Map([[rootKey, ready([node("analytics", true)])]]);

    expect(visibleRows(unopened, new Set([]), "")[0]?.level).toBeUndefined();
    expect(visibleRows(loaded(), new Set([]), "")[0]?.level?.state).toBe("ready");
  });

  // The whole point of filtering here rather than at the server: it answers
  // every keystroke, and a round trip per key is a wait rather than a filter.
  it("hides the rows that do not match what was typed", () => {
    expect(names(loaded(), ["analytics"], "publ")).toEqual(["analytics", "public"]);
  });

  // Hiding a schema whose table somebody has just found would hide the answer
  // along with the noise.
  it("keeps a parent whose child matches, even when the parent does not", () => {
    expect(names(loaded(), ["analytics", keyOf("analytics", "reporting")], "invoice")).toEqual([
      "analytics",
      "reporting",
      "invoice_id_seq",
    ]);
  });

  it("drops a branch where nothing matches at any depth", () => {
    expect(names(loaded(), ["analytics", keyOf("analytics", "reporting")], "nothing")).toEqual([]);
  });

  it("is empty until the root has arrived", () => {
    expect(names(new Map(), [], "")).toEqual([]);
  });

  // A database really can be called constructor, toString or __proto__: they are
  // legal quoted identifiers, and the key of a database is its bare name. Held
  // in a plain object, a level nobody has loaded for one of those answers
  // whatever Object.prototype has under that name — so the walk descends into a
  // function, reads undefined nodes and throws, and needsAsking sees something
  // that is neither missing nor failed and asks for nothing.
  it("treats a name that collides with a prototype member as an ordinary key", () => {
    for (const hostile of ["constructor", "toString", "__proto__", "hasOwnProperty"]) {
      const levels: Levels = new Map([[rootKey, ready([node(hostile, true)])]]);

      expect(levels.get(hostile)).toBeUndefined();
      expect(needsAsking(levels.get(hostile))).toBe(true);
      expect(names(levels, [hostile], "")).toEqual([hostile]);
    }
  });
});

describe("what a level may do next", () => {
  it("keeps the rows it already had while the next answer is on the way", () => {
    const held = ready([node("daily_revenue")]);

    expect(asking(held)).toEqual({ state: "asking", nodes: [node("daily_revenue")], error: "" });
    expect(asking(undefined)).toEqual({ state: "asking", nodes: [], error: "" });
  });

  it("asks for a level nobody has asked for, and for one that failed last time", () => {
    expect(needsAsking(undefined)).toBe(true);
    expect(needsAsking({ state: "failed", nodes: [], error: "no" })).toBe(true);
  });

  it("does not ask again for a level it already has", () => {
    expect(needsAsking(ready([node("daily_revenue")]))).toBe(false);
  });

  // The bug this pair exists for. Collapsing a node stops the request, and a
  // level left saying it is still being fetched is a level nothing will ever
  // ask for again: reopening the node finds it neither missing nor failed, so
  // it asks for nothing and the row shows a spinner for the rest of the
  // session.
  it("never leaves a level saying it is still being asked for", () => {
    expect(needsAsking(asking(undefined))).toBe(false);
    expect(abandoned(asking(undefined))).toBeUndefined();
    expect(needsAsking(abandoned(asking(undefined)))).toBe(true);
  });

  // What was already on screen is not thrown away for having been abandoned.
  // The rows are still the ones the server sent; only the answer nobody waited
  // for is gone.
  it("goes back to the rows it had when there were some", () => {
    const abandonedLevel = abandoned(asking(ready([node("daily_revenue")])));

    expect(abandonedLevel).toEqual({ state: "ready", nodes: [node("daily_revenue")], error: "" });
    expect(needsAsking(abandonedLevel)).toBe(false);
  });
});

describe("toggling a node", () => {
  const row = (key: string, expandable: boolean, level?: Level): Row => ({
    key,
    node: node(key, expandable),
    depth: 0,
    expanded: false,
    level,
  });

  it("does nothing to a row that cannot open", () => {
    expect(toggling(row("daily_revenue", false), new Set())).toBe("nothing");
  });

  it("collapses a node that is open", () => {
    expect(toggling(row("analytics", true), new Set(["analytics"]))).toBe("collapse");
  });

  it("opens and asks for a node nobody has opened", () => {
    expect(toggling(row("analytics", true), new Set())).toBe("open-and-ask");
  });

  it("opens without asking again for a node whose children it already has", () => {
    expect(toggling(row("analytics", true, ready([node("public", true)])), new Set())).toBe("open");
  });

  it("opens and asks again for a node whose last attempt failed", () => {
    const failed: Level = { state: "failed", nodes: [], error: "no" };

    expect(toggling(row("analytics", true, failed), new Set())).toBe("open-and-ask");
  });

  // The window between collapsing a node and its cancelled request rejecting.
  // Reopening in that window used to find a level still saying it was being
  // asked for, so it asked for nothing — and the rejection that landed
  // afterwards took the level away from under a node that was open again.
  // Collapsing gives up on the level at once, which is what makes this "ask".
  it("asks again when a collapse gave up on a level mid-question", () => {
    const interrupted = abandoned(asking(undefined));

    expect(toggling(row("analytics", true, interrupted), new Set())).toBe("open-and-ask");
  });
});

describe("what to ask again when typing settles", () => {
  const open = new Set(["analytics", keyOf("analytics", "reporting")]);

  it("asks every open node again, because the filter narrows what a level holds", () => {
    expect(askAgain(loaded(), open).sort()).toEqual([...open].sort());
  });

  // The databases are deliberately not narrowed by the pattern — hiding the
  // database that holds the match would hide the answer — so asking for them
  // again on every settle would be a query per keystroke that cannot change its
  // own answer.
  it("leaves a root that answered alone", () => {
    expect(askAgain(loaded(), open)).not.toContain(rootKey);
  });

  // A root that failed has no other way back: nothing else re-asks it, so
  // without this the tree shows the failure until the connection is reopened.
  it("asks a root that failed again", () => {
    const broken: Levels = new Map([[rootKey, { state: "failed", nodes: [], error: "no" }]]);

    expect(askAgain(broken, new Set())).toEqual([rootKey]);
  });

  it("asks a root that was never loaded", () => {
    expect(askAgain(new Map(), new Set())).toEqual([rootKey]);
  });
});

describe("moving up a level", () => {
  // Arrow Left on a closed row moves to the row that holds it, which in a flat
  // list is the nearest row above at a smaller depth. There is no parent
  // pointer to follow: the tree is a list and nesting is a number on a row.
  const rows = (): Row[] =>
    visibleRows(loaded(), new Set(["analytics", keyOf("analytics", "reporting")]), "");

  it("finds the row that holds a child", () => {
    const shown = rows();

    // analytics · reporting · daily_revenue · invoice_id_seq · public
    expect(parentOf(shown, 2)).toBe(1);
    expect(parentOf(shown, 3)).toBe(1);
    expect(parentOf(shown, 1)).toBe(0);
  });

  it("leaves a row at the top level where it is", () => {
    expect(parentOf(rows(), 0)).toBe(0);
  });

  it("answers the index it was given when there is no such row", () => {
    expect(parentOf([], 3)).toBe(3);
  });
});
