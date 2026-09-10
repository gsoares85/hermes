import { CatalogService } from "../../bindings/github.com/gsoares85/hermes/internal/ui";
import type {
  ColumnView,
  ObjectRef,
  PropertiesView,
} from "../../bindings/github.com/gsoares85/hermes/internal/ui/models";

import type { CancellablePromise } from "./connection";

export type { ColumnView, ObjectRef, PropertiesView };

/**
 * What the selected object is: its columns, what is declared on it, and the
 * facts about it that are not a column.
 *
 * The Go side decides what is worth showing and hands over text. The window
 * prints it and does not interpret it, which is what keeps a change to the
 * model from becoming a change to this screen.
 */
export function properties(
  connectionId: string,
  object: ObjectRef,
): CancellablePromise<PropertiesView> {
  return CatalogService.Properties(connectionId, object);
}

/**
 * Forgets what was read about the schema this object is in.
 *
 * The tree asks the server on every expansion and this panel reads through a
 * cache, so that the second object clicked in a schema costs nothing. That is
 * the right trade until something changes the database from somewhere else, and
 * this is how a person says "look again". One schema and not the connection:
 * throwing away everything because one table changed would make every other
 * schema be waited for a second time.
 */
export function refreshObject(connectionId: string, object: ObjectRef): CancellablePromise<void> {
  return CatalogService.Refresh(connectionId, object);
}

/**
 * The statements that would build the selected object.
 *
 * Written by the generator the structure sync is built on, over a model holding
 * that one object — so what is on screen is what the product would run, not a
 * rendering of it made for reading.
 */
export function ddl(connectionId: string, object: ObjectRef): CancellablePromise<string> {
  return CatalogService.DDL(connectionId, object);
}
