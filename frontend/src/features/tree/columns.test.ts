import { describe, expect, it } from "vitest";

import type { ColumnView } from "../../api/object";

import { glyphOf, saidOf } from "./columns";

function column(over: Partial<ColumnView> = {}): ColumnView {
  return {
    primaryKey: false,
    name: "id",
    type: "bigint",
    notNull: false,
    default: "",
    identity: "",
    generated: "",
    collation: "",
    ...over,
  };
}

describe("what is said about a column", () => {
  it("says nothing about a column with nothing declared on it", () => {
    expect(saidOf(column())).toEqual([]);
  });

  it("says it cannot be empty", () => {
    expect(saidOf(column({ notNull: true }))).toEqual(["not null"]);
  });

  it("says what it falls back to", () => {
    expect(saidOf(column({ default: "now()" }))).toEqual(["default now()"]);
  });

  // The three are alternatives, not additions: a column is filled by an
  // identity, or by an expression, or by a default, and the catalog can hold
  // the leftovers of the others. Saying two of them would describe a table
  // nobody can write.
  it("prefers the identity to a default", () => {
    expect(saidOf(column({ identity: "always", default: "nextval('s')" }))).toEqual([
      "identity always",
    ]);
  });

  it("prefers what generates it to a default", () => {
    expect(saidOf(column({ generated: "stored", default: "1" }))).toEqual(["generated stored"]);
  });

  it("says what it is collated as", () => {
    expect(saidOf(column({ collation: "en_US" }))).toEqual(["collate en_US"]);
  });

  it("says everything true of the same column, in one order", () => {
    expect(saidOf(column({ notNull: true, identity: "by default", collation: "C" }))).toEqual([
      "not null",
      "identity by default",
      "collate C",
    ]);
  });
});

describe("the glyph beside a column", () => {
  it("marks the key before anything else about it", () => {
    expect(glyphOf(column({ primaryKey: true, type: "text" }))).toBe("keyFill");
  });

  it("knows a number when the whole name is one", () => {
    for (const type of ["bigint", "integer", "numeric(10,2)", "double precision", "money"]) {
      expect(glyphOf(column({ type }))).toBe("sequence");
    }
  });

  it("knows a moment", () => {
    for (const type of ["date", "timestamp with time zone", "timestamptz", "interval"]) {
      expect(glyphOf(column({ type }))).toBe("calendar");
    }
  });

  /**
   * A type is recognised by its name, not by what its name contains.
   *
   * Matching "int" anywhere made `point` a number, and it is the shape of
   * mistake that grows: every type added to the pattern is another substring
   * that can turn up inside a name nobody was thinking about.
   */
  it("does not read a number out of the middle of a name", () => {
    for (const type of ["point", "inet", "interval_kind", "printer"]) {
      expect(glyphOf(column({ type }))).not.toBe("sequence");
    }

    expect(glyphOf(column({ type: "point" }))).toBe("text");
  });

  it("gives an unfamiliar type the glyph for words", () => {
    expect(glyphOf(column({ type: "hstore" }))).toBe("text");
  });

  it("reads an array as what it is an array of", () => {
    expect(glyphOf(column({ type: "bigint[]" }))).toBe("sequence");
  });
});
