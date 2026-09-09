import { useVirtualizer } from "@tanstack/react-virtual";
import { useCallback, useEffect, useRef, useState } from "react";

import {
  children,
  noFilter,
  wasCancelled,
  type CancellablePromise,
  type NodeView,
} from "../../api/tree";

import { refOf, rootKey, visibleRows, type Level, type Levels, type Row } from "./rows";

/**
 * The object tree of one open connection.
 *
 * Every level is asked for when it is opened and never before, which is what
 * makes a server with five thousand tables in a schema open as fast as one with
 * ten: what a level costs is one question, and the question is only asked about
 * the node somebody actually clicked.
 *
 * The rows are virtualised from the first line rather than once it hurts. A
 * tree drawn as nested components has to build every node it has loaded on
 * every repaint, and rewriting it into a flat list later is a rewrite — the
 * flat list is the structure, and depth is a number on a row.
 */
export function ObjectTree({ connectionId }: { connectionId: string }): React.JSX.Element {
  const [levels, setLevels] = useState<Levels>({});
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());

  // The requests in flight, so that collapsing a node or closing the window
  // stops work nobody is waiting for any more.
  const running = useRef(new Map<string, CancellablePromise<NodeView[]>>());

  const ask = useCallback(
    (key: string): void => {
      const inFlight = running.current;
      if (inFlight.has(key)) {
        return;
      }

      setLevels((held): Levels => ({ ...held, [key]: asking(held[key]) }));

      const request = children(connectionId, refOf(key), noFilter);
      inFlight.set(key, request);

      request
        .then((nodes): void => {
          setLevels((held): Levels => ({ ...held, [key]: { state: "ready", nodes, error: "" } }));
        })
        .catch((err: unknown): void => {
          if (wasCancelled(err)) {
            return;
          }

          setLevels((held): Levels => ({
            ...held,
            [key]: { state: "failed", nodes: [], error: String(err) },
          }));
        })
        .finally((): void => {
          inFlight.delete(key);
        });
    },
    [connectionId],
  );

  // The databases of the server, asked for once the tree is on screen. A
  // connection that changes is a different tree, so everything loaded goes.
  useEffect((): (() => void) => {
    const inFlight = running.current;

    setLevels({});
    setExpanded(new Set());
    ask(rootKey);

    return (): void => {
      for (const request of inFlight.values()) {
        void request.cancel();
      }

      inFlight.clear();
    };
  }, [ask]);

  const toggle = useCallback(
    (row: Row): void => {
      if (!row.node.expandable) {
        return;
      }

      setExpanded((open): ReadonlySet<string> => {
        const next = new Set(open);

        if (next.has(row.key)) {
          next.delete(row.key);

          // Whatever was still being fetched for a node nobody is looking at
          // any more is stopped rather than left to arrive.
          void running.current.get(row.key)?.cancel();

          return next;
        }

        next.add(row.key);

        return next;
      });

      // Asked when it is opened, and asked again when what is there failed
      // last time: reopening a node that could not be read is how a person
      // retries, and answering the old failure would make the tree look stuck.
      if (!expanded.has(row.key) && (row.level === undefined || row.level.state === "failed")) {
        ask(row.key);
      }
    },
    [ask, expanded],
  );

  const rows = visibleRows(levels, expanded);
  const root = levels[rootKey];

  const scroller = useRef<HTMLDivElement>(null);
  const virtualiser = useVirtualizer({
    count: rows.length,
    getScrollElement: (): HTMLDivElement | null => scroller.current,
    estimateSize: (): number => 26,
    overscan: 12,
  });

  return (
    <section className="tree" aria-label="Objects">
      {root?.state === "asking" && rows.length === 0 && (
        <p className="tree__note">Reading the server…</p>
      )}
      {root?.state === "failed" && <p className="tree__error">{root.error}</p>}

      <div className="tree__scroller" ref={scroller}>
        <div className="tree__spacer" style={{ height: `${String(virtualiser.getTotalSize())}px` }}>
          {virtualiser.getVirtualItems().map((item) => {
            const row = rows[item.index];
            if (row === undefined) {
              return null;
            }

            return (
              <div
                key={row.key}
                className="tree__row"
                style={{ transform: `translateY(${String(item.start)}px)` }}
                ref={virtualiser.measureElement}
                data-index={item.index}
              >
                <TreeRow row={row} onToggle={toggle} />
              </div>
            );
          })}
        </div>
      </div>
    </section>
  );
}

/** One line: its indentation, whether it can open, and what it is. */
function TreeRow({ row, onToggle }: { row: Row; onToggle: (row: Row) => void }): React.JSX.Element {
  const asking = row.level?.state === "asking";
  const failed = row.level?.state === "failed";

  return (
    <div className="tree__line" style={{ paddingLeft: `${String(row.depth * 16 + 8)}px` }}>
      <button
        type="button"
        className="tree__toggle"
        onClick={(): void => {
          onToggle(row);
        }}
        disabled={!row.node.expandable}
        aria-expanded={row.node.expandable ? row.expanded : undefined}
        // A node that cannot open still draws the space, so names line up
        // rather than shifting by a character depending on their kind.
        aria-label={row.node.expandable ? `Expand ${row.node.name}` : row.node.name}
      >
        {row.node.expandable ? (row.expanded ? "▾" : "▸") : "·"}
      </button>

      <span className={`tree__kind tree__kind--${row.node.kind}`}>{row.node.kind}</span>
      <span className="tree__name">{row.node.name}</span>

      {asking && <span className="tree__working">…</span>}
      {failed && (
        <span className="tree__failed" title={row.level?.error}>
          could not open
        </span>
      )}
    </div>
  );
}

/**
 * The level as it looks while it is being asked for.
 *
 * What was already there is kept: a node being refreshed shows what it held
 * until the answer lands, instead of emptying and filling again.
 */
function asking(held: Level | undefined): Level {
  return { state: "asking", nodes: held?.nodes ?? [], error: "" };
}
