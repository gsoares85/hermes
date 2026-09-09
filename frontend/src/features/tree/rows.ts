import type { NodeRef, NodeView } from "../../api/tree";

/**
 * How a level is doing.
 *
 * "asking" is a level being fetched, and it is per node rather than per window:
 * expanding one schema must not make the rest of the tree look busy.
 */
export type LevelState = "asking" | "ready" | "failed";

export interface Level {
  state: LevelState;
  nodes: NodeView[];
  error: string;
}

/** Every level the tree has asked for, by the key of the node it belongs to. */
export type Levels = Record<string, Level>;

/**
 * One drawn line of the tree.
 *
 * The tree is drawn from a flat list rather than from nested components, and
 * that is the structure rather than a rendering trick: a virtualiser can only
 * skip work it can count, and what it counts is the rows a person can actually
 * see. Nesting is carried by depth.
 */
export interface Row {
  key: string;
  node: NodeView;
  depth: number;
  expanded: boolean;
  level: Level | undefined;
}

/**
 * The key of the root, whose children are the databases of the server.
 */
export const rootKey = "";

/**
 * Separator between the parts of a key.
 *
 * A NUL, because PostgreSQL will not let one into an identifier and every
 * printable character is fair game — a schema really can be called `a/b` or
 * `a.b`, and a tree keyed on those would put two different objects in one place.
 */
const separator = "\u0000";

/** The key of a child of the node at parentKey. */
export function keyOf(parentKey: string, name: string): string {
  return parentKey === rootKey ? name : parentKey + separator + name;
}

/**
 * The place a key names, as the Go side addresses it.
 *
 * The first part is the database and the second the schema. An object has a
 * third part and is never expanded, so it never reaches here.
 */
export function refOf(key: string): NodeRef {
  const parts = key.split(separator);

  return { database: parts[0] ?? "", schema: parts[1] ?? "" };
}

/**
 * The rows a person can see, in order, given what has been loaded and what is
 * open.
 *
 * Walks depth first from the root and descends only into open nodes whose
 * children have arrived. A node that is open and still being asked for
 * contributes itself and nothing else, which is what lets the row show that it
 * is working without the tree jumping about when the answer lands.
 */
export function visibleRows(levels: Levels, expanded: ReadonlySet<string>): Row[] {
  const rows: Row[] = [];

  const walk = (parentKey: string, depth: number): void => {
    const level = levels[parentKey];
    if (level === undefined) {
      return;
    }

    for (const node of level.nodes) {
      const key = keyOf(parentKey, node.name);
      const open = expanded.has(key);

      rows.push({ key, node, depth, expanded: open, level: levels[key] });

      if (open) {
        walk(key, depth + 1);
      }
    }
  };

  walk(rootKey, 0);

  return rows;
}
