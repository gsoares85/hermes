import type { ColumnView } from "../../api/object";
import type { IconName } from "../../ui/Icon";

/**
 * What is declared on a column, beyond its name and its type.
 *
 * The model reads all of this out of the catalog whether or not anybody draws
 * it, so a panel that shows only the type is paying for facts it throws away —
 * and sending whoever wanted to know what a column defaults to round to the
 * DDL tab to find out.
 *
 * Identity, generated and default are alternatives rather than additions. A
 * column is filled by an identity, or by an expression, or by a default, and
 * the catalog can hold the leftovers of the others alongside the one in force:
 * an identity column carries the `nextval` of its own sequence as its default.
 * Saying both would describe a table nobody can write.
 */
export function saidOf(column: ColumnView): string[] {
  const said: string[] = [];

  if (column.notNull) {
    said.push("not null");
  }

  if (column.identity !== "") {
    said.push(`identity ${column.identity}`);
  } else if (column.generated !== "") {
    said.push(`generated ${column.generated}`);
  } else if (column.default !== "") {
    said.push(`default ${column.default}`);
  }

  if (column.collation !== "") {
    said.push(`collate ${column.collation}`);
  }

  return said;
}

/**
 * The types that hold a number, and the types that hold a moment.
 *
 * Named in full rather than matched as substrings. The pattern this replaces
 * looked for "int" anywhere in the name, which made `point` a number — and
 * that is the shape of mistake that grows, because every type added to it is
 * another substring that can turn up inside a name nobody was thinking about.
 */
const numbers = new Set([
  "smallint",
  "integer",
  "int",
  "int2",
  "int4",
  "int8",
  "bigint",
  "decimal",
  "numeric",
  "real",
  "double precision",
  "float",
  "float4",
  "float8",
  "money",
  "smallserial",
  "serial",
  "serial2",
  "serial4",
  "serial8",
  "bigserial",
]);

const moments = new Set([
  "date",
  "time",
  "timetz",
  "timestamp",
  "timestamptz",
  "interval",
  "time with time zone",
  "time without time zone",
  "timestamp with time zone",
  "timestamp without time zone",
]);

/**
 * The name of the type, without what a declaration adds to it.
 *
 * A length or a precision is in parentheses, and an array is the element type
 * with brackets after it — a column of bigints is still a column of numbers.
 */
function baseOf(type: string): string {
  return type
    .toLowerCase()
    .replace(/\[[\s\d]*\]/g, "")
    .replace(/\([^)]*\)/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

/**
 * What glyph stands beside a column.
 *
 * The key first, because it is the one thing about a column that changes how
 * you read the rest of the table. After that it is only the shape of the
 * value: a moment, a number, or words. It is presentation and nothing branches
 * on it, so a type this does not recognise gets the glyph for words — which is
 * what an unfamiliar type usually holds.
 */
export function glyphOf(column: ColumnView): IconName {
  if (column.primaryKey) {
    return "keyFill";
  }

  const base = baseOf(column.type);

  if (moments.has(base)) {
    return "calendar";
  }

  return numbers.has(base) ? "sequence" : "text";
}
