import type { ColumnView } from "../../api/object";

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
