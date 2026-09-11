import type { StatusView } from "../../api/connection";
import type { ObjectRef } from "../../api/object";
import { isProduction } from "../connection/ConnectionMarks";

/**
 * What the window is showing, decided once instead of at four points in the
 * markup.
 *
 * The three answers are not independent, and that is the whole reason this is a
 * function. Everything on the sides belongs to an open connection: without one
 * there are no objects to list, no object to describe, and no server to mark as
 * production. Written as three conditions in the markup they can drift apart —
 * and the one that drifts silently is the production mark, which is the one
 * that has to be right.
 */
export interface Showing {
  /** The objects of the open connection, in the left sidebar. */
  objects: boolean;

  /** The selected object, in the right sidebar. */
  details: boolean;

  /** The mark that the open connection is a production server. */
  production: boolean;
}

const nothing: Showing = { objects: false, details: false, production: false };

export function showing(connection: StatusView | null, selected: ObjectRef | null): Showing {
  if (connection === null) {
    return nothing;
  }

  return {
    objects: true,
    details: selected !== null,
    production: isProduction(connection.environment),
  };
}
