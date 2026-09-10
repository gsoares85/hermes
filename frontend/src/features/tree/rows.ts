import type { ObjectRef } from "../../api/object";
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

/**
 * Every level the tree has asked for, by the key of the node it belongs to.
 *
 * A Map rather than an object, because the keys come from the server. A database
 * really can be called `constructor`, `toString` or `__proto__` — they are legal
 * quoted identifiers — and the key of a database is its bare name. Held in a
 * plain object, a level nobody has loaded for one of those answers whatever
 * Object.prototype has under that name: the walk below would descend into a
 * function and read undefined nodes, and needsAsking would see something that is
 * neither missing nor failed and ask for nothing.
 */
export type Levels = ReadonlyMap<string, Level>;

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
    const level = levels.get(parentKey);
    if (level === undefined) {
      return [];
    }

    const rows: Row[] = [];

    for (const node of level.nodes) {
      const key = keyOf(parentKey, node.name);
      const open = expanded.has(key);
      const below = open ? walk(key, depth + 1) : [];

      if (matches(node.name, text) || below.length > 0) {
        rows.push({ key, node, depth, expanded: open, level: levels.get(key) });
        rows.push(...below);
      }
    }

    return rows;
  };

  return walk(rootKey, 0);
}

/**
 * The object a key names, for the panel that shows one.
 *
 * Three parts: the database, the schema and the object. A key with fewer names
 * a level rather than an object, and the panel has nothing to show for one.
 */
export function objectOf(key: string): ObjectRef | null {
  const parts = key.split(separator);
  const [database, schema, name] = parts;

  if (parts.length !== 3 || database === undefined || schema === undefined || name === undefined) {
    return null;
  }

  return { database, schema, name };
}

/**
 * The level a node is in while its children are on the way.
 *
 * Whatever it already held stays: a level that emptied itself to say it was
 * working would make the tree jump every time somebody changed the filter, and
 * the rows it is showing are still the ones the server sent last time.
 */
export function asking(held: Level | undefined): Level {
  return { state: "asking", nodes: held?.nodes ?? [], error: "" };
}

/**
 * What a level becomes when nobody is waiting for it any more.
 *
 * Collapsing a node stops the request it had in flight, and the answer never
 * arrives — so something has to take the level out of "asking", or it stays
 * there for the rest of the session. A level left that way is worse than a
 * failed one: needsAsking finds it neither missing nor failed, so reopening the
 * node asks for nothing at all and the row shows a spinner forever.
 *
 * A level that had rows keeps them and goes back to ready; one that never had
 * any is dropped, so that opening the node asks again.
 */
export function abandoned(held: Level | undefined): Level | undefined {
  if (held === undefined || held.nodes.length === 0) {
    return undefined;
  }

  return { state: "ready", nodes: held.nodes, error: "" };
}

/**
 * What clicking the arrow on a row does.
 *
 * A value rather than a branch inside the component, because the interesting
 * case is a sequence: collapsing a node stops its request, and reopening it
 * before the cancellation lands has to ask again rather than find a level that
 * still says it is being fetched.
 */
export type Toggling = "nothing" | "collapse" | "open" | "open-and-ask";

export function toggling(row: Row, expanded: ReadonlySet<string>): Toggling {
  if (!row.node.expandable) {
    return "nothing";
  }

  if (expanded.has(row.key)) {
    return "collapse";
  }

  return needsAsking(row.level) ? "open-and-ask" : "open";
}

/**
 * Whether the children of a node have to be asked for.
 *
 * A level nobody has asked for, and one whose last attempt failed — reopening a
 * node that could not be read is how a person retries, and answering the old
 * failure would make the tree look stuck.
 */
export function needsAsking(level: Level | undefined): boolean {
  return level === undefined || level.state === "failed";
}
