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
 * The statements that would build the selected object.
 *
 * Written by the generator the structure sync is built on, over a model holding
 * that one object — so what is on screen is what the product would run, not a
 * rendering of it made for reading.
 */
export function ddl(connectionId: string, object: ObjectRef): CancellablePromise<string> {
  return CatalogService.DDL(connectionId, object);
}
