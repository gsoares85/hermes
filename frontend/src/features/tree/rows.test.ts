import { describe, expect, it } from "vitest";

import type { NodeView } from "../../api/tree";

import { around, keyOf, matches, objectOf, refOf, rootKey, visibleRows, type Levels } from "./rows";

function node(name: string, expandable = false): NodeView {
  return { kind: expandable ? "schema" : "table", name, expandable };
}

function ready(nodes: NodeView[]): Levels[string] {
  return { state: "ready", nodes, error: "" };
}

/** The tree the rest of these tests walk: one database, two schemas, one table. */
function loaded(): Levels {
  return {
    [rootKey]: ready([node("analytics", true)]),
    analytics: ready([node("reporting", true), node("public", true)]),
    [keyOf("analytics", "reporting")]: ready([node("daily_revenue"), node("invoice_id_seq")]),
  };
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
    const levels: Levels = {
      [rootKey]: ready([node("analytics", true)]),
      analytics: { state: "asking", nodes: [], error: "" },
    };

    expect(names(levels, ["analytics"], "")).toEqual(["analytics"]);
  });

  // The level on a row is the one holding that node's children, whether or not
  // the node is open: it is what the row draws its working and failed marks
  // from, and a row that only learned about it on expansion could not say that
  // the last attempt to open it had failed.
  it("hands each row the level of what is under it, and undefined until it arrives", () => {
    const unopened: Levels = { [rootKey]: ready([node("analytics", true)]) };

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
    expect(names({}, [], "")).toEqual([]);
  });
});
