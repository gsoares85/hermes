import { describe, expect, it } from "vitest";

import type { ColumnView } from "../../api/object";

import { saidOf } from "./columns";

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
