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
 * Whether a name matches what somebody typed.
 *
 * A plain case-insensitive substring, which is what the server matches too. Two
 * definitions of matching would disagree the first time one of them learned
 * something the other had not, and the one people would notice is the tree
 * hiding a row the server had just sent.
 */
export function matches(name: string, text: string): boolean {
  return text === "" || name.toLowerCase().includes(text.toLowerCase());
}

/**
 * A name split around what matched, so the middle can be marked.
 *
 * Three parts and not a list of them: the match is a single substring, and a
 * caller that has to loop is a caller that could put the marks in the wrong
 * place.
 */
export function around(
  name: string,
  text: string,
): { before: string; match: string; after: string } {
  const at = text === "" ? -1 : name.toLowerCase().indexOf(text.toLowerCase());
  if (at < 0) {
    return { before: name, match: "", after: "" };
  }

  return {
    before: name.slice(0, at),
    match: name.slice(at, at + text.length),
    after: name.slice(at + text.length),
  };
}

/**
 * The rows a person can see, in order, given what has been loaded, what is open
 * and what has been typed.
 *
 * Walks depth first from the root and descends only into open nodes whose
 * children have arrived. A node that is open and still being asked for
 * contributes itself and nothing else, which is what lets the row show that it
 * is working without the tree jumping about when the answer lands.
 *
 * The typed text hides rows here, over what is already loaded, because it has
 * to answer every keystroke and a round trip per key is not a filter but a
 * wait. A node stays when it matches or when something under it does — hiding a
 * schema whose table somebody just found would hide the answer along with the
 * noise.
 */
export function visibleRows(levels: Levels, expanded: ReadonlySet<string>, text: string): Row[] {
  const walk = (parentKey: string, depth: number): Row[] => {
    const level = levels[parentKey];
    if (level === undefined) {
      return [];
    }

    const rows: Row[] = [];

    for (const node of level.nodes) {
      const key = keyOf(parentKey, node.name);
      const open = expanded.has(key);
      const below = open ? walk(key, depth + 1) : [];

      if (matches(node.name, text) || below.length > 0) {
        rows.push({ key, node, depth, expanded: open, level: levels[key] });
        rows.push(...below);
      }
    }

    return rows;
  };

  return walk(rootKey, 0);
}
