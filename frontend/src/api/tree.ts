import { CatalogService } from "../../bindings/github.com/gsoares85/hermes/internal/ui";
import type {
  NodeRef,
  NodeView,
  TreeFilter,
} from "../../bindings/github.com/gsoares85/hermes/internal/ui/models";

import { wasCancelled, type CancellablePromise } from "./connection";

export type { CancellablePromise, NodeRef, NodeView, TreeFilter };
export { wasCancelled };

/**
 * What a node of the object tree holds.
 *
 * The frontend names a place and gets back what is there. It never assembles a
 * query and never says how to find anything: the pattern someone types is sent
 * as a value beside the query the Go side wrote, which is why a name with a
 * quote in it is a name and not an incident.
 *
 * The promise carries the handle that stops it, so expanding a schema of five
 * thousand objects and changing your mind stops the work instead of waiting for
 * an answer nobody will read.
 */
export function children(
  connectionId: string,
  node: NodeRef,
  filter: TreeFilter,
): CancellablePromise<NodeView[]> {
  // A level with nothing in it comes back as null rather than as an empty
  // list, because that is how Go's nil slice crosses. The tree draws an empty
  // node either way, and the difference is not one the window should carry.
  return CatalogService.Children(connectionId, node, filter).then(
    (found): NodeView[] => found ?? [],
  );
}

/** The root of the tree: the server itself, whose children are its databases. */
export const rootNode: NodeRef = { database: "", schema: "" };

/** A filter that narrows nothing. */
export const noFilter: TreeFilter = { pattern: "", system: false };
