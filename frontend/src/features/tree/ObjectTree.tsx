import { useVirtualizer } from "@tanstack/react-virtual";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  children,
  noFilter,
  wasCancelled,
  type CancellablePromise,
  type NodeView,
  type TreeFilter,
} from "../../api/tree";

import type { ObjectRef } from "../../api/object";
import { Icon, type IconName } from "../../ui/Icon";

import {
  abandoned,
  around,
  askAgain,
  asking,
  objectOf,
  parentOf,
  refOf,
  rootKey,
  toggling,
  visibleRows,
  type Level,
  type Levels,
  type Row,
} from "./rows";

/**
 * How long typing settles before the levels are asked for again.
 *
 * The tree filters what it holds on every keystroke, so this is not what makes
 * the box feel quick; it is what stops a level of fifty thousand names crossing
 * whole when somebody has already said which of them they want.
 */
const settleDelay = 300;

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
export function ObjectTree({
  connectionId,
  onSelect,
}: {
  connectionId: string;
  onSelect: (object: ObjectRef | null) => void;
}): React.JSX.Element {
  const [levels, setLevels] = useState<Levels>(new Map());
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const [text, setText] = useState("");
  const [system, setSystem] = useState(false);

  // What the levels were last asked with. Typing filters what is on screen at
  // once; this is the copy that goes back to the server once the typing stops.
  const [settled, setSettled] = useState<TreeFilter>(noFilter);

  // The requests in flight, so that collapsing a node or closing the window
  // stops work nobody is waiting for any more.
  const running = useRef(new Map<string, CancellablePromise<NodeView[]>>());

  // How many times each node has been asked about.
  //
  // A handler writes the level only when its own request is still the current
  // one for that node. Without it, replacing a request lets the older one's
  // rejection land after the newer one has already set the level, and the level
  // ends up describing a question nobody asked.
  const era = useRef(new Map<string, number>());

  // Marks whatever is in flight for a node as no longer the current question,
  // so its handlers write nothing when they land.
  const supersede = useCallback((key: string): number => {
    const next = (era.current.get(key) ?? 0) + 1;
    era.current.set(key, next);

    return next;
  }, []);

  const ask = useCallback(
    (key: string, filter: TreeFilter): void => {
      const inFlight = running.current;

      // A request already running for this node is replaced rather than left to
      // win. Dropping the new one was the earlier shape, and it meant that
      // widening the filter — deleting a character — left the level holding the
      // answer to the narrower question until the next keystroke.
      void inFlight.get(key)?.cancel();

      const mine = supersede(key);
      const current = (): boolean => era.current.get(key) === mine;

      setLevels((held): Levels => written(held, key, asking(held.get(key))));

      const request = children(connectionId, refOf(key), filter);
      inFlight.set(key, request);

      request
        .then((nodes): void => {
          if (current()) {
            setLevels((held): Levels => written(held, key, { state: "ready", nodes, error: "" }));
          }
        })
        .catch((err: unknown): void => {
          if (!current()) {
            return;
          }

          // Nobody is waiting for this any more. The level must not be left
          // saying it is still being asked for: reopening the node would find
          // it neither missing nor failed, ask for nothing, and show a spinner
          // for the rest of the session.
          if (wasCancelled(err)) {
            setLevels((held): Levels => giveUp(held, key));

            return;
          }

          setLevels((held): Levels =>
            written(held, key, { state: "failed", nodes: [], error: String(err) }),
          );
        })
        .finally((): void => {
          if (inFlight.get(key) === request) {
            inFlight.delete(key);
          }
        });
    },
    [connectionId, supersede],
  );

  // The databases of the server, asked for once the tree is on screen. A
  // connection that changes is a different tree, so everything loaded goes.
  useEffect((): (() => void) => {
    const inFlight = running.current;

    setLevels(new Map());
    setExpanded(new Set());
    ask(rootKey, noFilter);

    return (): void => {
      for (const request of inFlight.values()) {
        void request.cancel();
      }

      inFlight.clear();
    };
  }, [ask]);

  // The row that was last picked, kept so that the tree can say which one is
  // selected. A reader that is told a list is a tree and then that none of its
  // items is selected is being told less than a plain list would say.
  const [picked, setPicked] = useState("");

  const pick = useCallback(
    (row: Row): void => {
      setPicked(row.key);

      // Only an object has properties to show. A database or a schema opens
      // instead, which is what the click on those already does.
      onSelect(row.node.expandable ? null : objectOf(row.key));
    },
    [onSelect],
  );

  const toggle = useCallback(
    (row: Row): void => {
      const what = toggling(row, expanded);
      if (what === "nothing") {
        return;
      }

      setExpanded((open): ReadonlySet<string> => {
        const next = new Set(open);

        if (what === "collapse") {
          next.delete(row.key);
        } else {
          next.add(row.key);
        }

        return next;
      });

      if (what === "collapse") {
        // Whatever was still being fetched for a node nobody is looking at any
        // more is stopped — and the level is given up here rather than when the
        // rejection lands. Between the two, a quick reopen used to find a level
        // that still said it was being asked for, ask for nothing, and then
        // have the rejection take the level away from under an open node.
        void running.current.get(row.key)?.cancel();
        supersede(row.key);
        setLevels((held): Levels => giveUp(held, row.key));

        return;
      }

      // Asked when it is opened, and asked again when what is there failed last
      // time: reopening a node that could not be read is how a person retries,
      // and answering the old failure would make the tree look stuck.
      if (what === "open-and-ask") {
        ask(row.key, settled);
      }
    },
    [ask, expanded, settled, supersede],
  );

  // What is open and what has been loaded, so that the effect below can read
  // both without running every time somebody expands something.
  const openNow = useRef<ReadonlySet<string>>(expanded);
  const heldNow = useRef<Levels>(levels);

  useEffect((): void => {
    openNow.current = expanded;
    heldNow.current = levels;
  }, [expanded, levels]);

  // Typing settles, and then the open levels are asked for again with what was
  // typed. This is the half of the filter the server does, and it is a
  // different job from the one above: the tree hides rows on every keystroke so
  // that typing feels like typing, and this stops a level of fifty thousand
  // names crossing whole once somebody has said which of them they want.
  //
  // The levels that are closed are dropped rather than refreshed. Reopening one
  // then asks with the filter in force, instead of showing what the answer to
  // some earlier question happened to hold.
  useEffect((): (() => void) => {
    const timer = setTimeout((): void => {
      const filter: TreeFilter = { pattern: text, system };
      const open = openNow.current;

      setSettled(filter);
      setLevels((held): Levels => {
        const kept = new Map<string, Level>();

        for (const key of [rootKey, ...open]) {
          const level = held.get(key);
          if (level !== undefined) {
            kept.set(key, level);
          }
        }

        return kept;
      });

      for (const key of askAgain(heldNow.current, open)) {
        ask(key, filter);
      }
    }, settleDelay);

    return (): void => {
      clearTimeout(timer);
    };
  }, [ask, system, text]);

  // Memoised because the virtualiser renders on every frame of a scroll, and
  // this walks everything that has been loaded. With five thousand objects open
  // that is five thousand rows rebuilt per frame to draw the twenty that moved.
  const rows = useMemo((): Row[] => visibleRows(levels, expanded, text), [levels, expanded, text]);
  const root = levels.get(rootKey);

  // The row the keyboard is on. Only rendered rows exist in the DOM, so Tab
  // cannot reach an offscreen one and every row being tabbable would mean
  // tabbing through a schema of five thousand: one row is tabbable, the arrows
  // move which, and the virtualiser is told to render the target first.
  const [active, setActive] = useState(0);
  const focused = rows.length === 0 ? -1 : Math.min(active, rows.length - 1);

  // Set when the keyboard moved, so that focus follows the row rather than
  // being stolen from wherever somebody clicked.
  const following = useRef(false);

  const scroller = useRef<HTMLDivElement>(null);
  // The rule warns that the React Compiler will skip memoizing a component that
  // calls this, because the virtualiser returns functions that cannot be
  // memoized safely. Three things make it moot here and one of them is decisive:
  // this build does not run the compiler at all — the React plugin is
  // configured without it — the skip would be confined to this component, and
  // nothing the virtualiser returns is handed to a memoized hook. Left as a
  // warning it prints a block on every lint run, which is how people learn to
  // stop reading lint output.
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualiser = useVirtualizer({
    count: rows.length,
    getScrollElement: (): HTMLDivElement | null => scroller.current,
    estimateSize: (): number => 26,
    overscan: 12,
  });

  const moveTo = useCallback(
    (index: number): void => {
      if (rows.length === 0) {
        return;
      }

      const bounded = Math.max(0, Math.min(index, rows.length - 1));

      following.current = true;
      setActive(bounded);
      virtualiser.scrollToIndex(bounded);
    },
    [rows.length, virtualiser],
  );

  // Runs after every render rather than on a change of the active row, and that
  // is what makes it work: scrolling to a row that was offscreen renders it one
  // or more paints later, and an effect that only watched the index would look
  // for it before it existed. The flag is what stops this stealing focus back
  // on every unrelated render.
  useEffect((): void => {
    if (!following.current) {
      return;
    }

    const line = scroller.current?.querySelector<HTMLElement>(
      `[data-index="${String(focused)}"] .tree__line`,
    );

    if (line) {
      following.current = false;
      line.focus();
    }
  });

  const onKey = useCallback(
    (event: React.KeyboardEvent, row: Row, index: number): void => {
      const move = (to: number): void => {
        event.preventDefault();
        moveTo(to);
      };

      switch (event.key) {
        case "Enter":
        case " ":
          event.preventDefault();
          pick(row);

          return;
        case "ArrowDown":
          move(index + 1);

          return;
        case "ArrowUp":
          move(index - 1);

          return;
        case "Home":
          move(0);

          return;
        case "End":
          move(rows.length - 1);

          return;
        case "ArrowRight":
          event.preventDefault();

          // Opens a closed node, and steps into an open one — which is the row
          // below it, because a child follows its parent in the list.
          if (row.expanded) {
            moveTo(index + 1);
          } else {
            toggle(row);
          }

          return;
        case "ArrowLeft":
          event.preventDefault();

          // Closes an open node, and steps out of a closed one.
          if (row.expanded) {
            toggle(row);
          } else {
            moveTo(parentOf(rows, index));
          }

          return;
        default:
          return;
      }
    },
    [moveTo, pick, rows, toggle],
  );

  return (
    <section className="tree" aria-label="Objects">
      <div className="tree__filter">
        <Icon name="search" className="tree__filter-icon" />
        <input
          type="search"
          value={text}
          placeholder="Filter by name"
          aria-label="Filter by name"
          onChange={(event): void => {
            setText(event.target.value);
          }}
        />

        <label className="tree__system">
          <input
            type="checkbox"
            checked={system}
            onChange={(event): void => {
              setSystem(event.target.checked);
            }}
          />
          System schemas
        </label>
      </div>

      {root?.state === "asking" && rows.length === 0 && (
        <p className="tree__note">Reading the server…</p>
      )}
      {root?.state === "failed" && <p className="tree__error">{root.error}</p>}

      <div className="tree__scroller" ref={scroller}>
        {/*
          The rows are the tree, and each one carries its own depth: a
          virtualised list has no nesting to read the structure from, so the
          level is stated rather than implied by the markup.
        */}
        <div
          className="tree__spacer"
          role="tree"
          style={{ height: `${String(virtualiser.getTotalSize())}px` }}
        >
          {virtualiser.getVirtualItems().map((item) => {
            const row = rows[item.index];
            if (row === undefined) {
              return null;
            }

            return (
              <div
                key={row.key}
                className="tree__row"
                // The positioning wrapper is the virtualiser's, not part of the
                // tree: without this it sits between the tree and its items.
                role="presentation"
                style={{ transform: `translateY(${String(item.start)}px)` }}
                ref={virtualiser.measureElement}
                data-index={item.index}
              >
                <TreeRow
                  row={row}
                  text={text}
                  selected={row.key === picked}
                  tabbable={item.index === focused}
                  onToggle={toggle}
                  onPick={(picking): void => {
                    setActive(item.index);
                    pick(picking);
                  }}
                  onKey={(event): void => {
                    onKey(event, row, item.index);
                  }}
                />
              </div>
            );
          })}
        </div>
      </div>
    </section>
  );
}

/**
 * What each kind of node is drawn as.
 *
 * A kind the Go side learns to answer before this learns to draw it falls back
 * to the table glyph rather than to nothing: a row with no icon looks broken,
 * and the word beside it in the accessible name is still right.
 */
function glyphOf(kind: string): IconName {
  switch (kind) {
    case "database":
      return "database";
    case "schema":
      return "folder";
    case "view":
      return "view";
    case "sequence":
      return "sequence";
    default:
      return "table";
  }
}

/** One line: its indentation, whether it can open, and what it is. */
function TreeRow({
  row,
  text,
  selected,
  tabbable,
  onToggle,
  onPick,
  onKey,
}: {
  row: Row;
  text: string;
  selected: boolean;
  tabbable: boolean;
  onToggle: (row: Row) => void;
  onPick: (row: Row) => void;
  onKey: (event: React.KeyboardEvent) => void;
}): React.JSX.Element {
  const asking = row.level?.state === "asking";
  const failed = row.level?.state === "failed";

  return (
    <div
      className="tree__line"
      style={{ paddingLeft: `${String(row.depth * 16 + 8)}px` }}
      onClick={(): void => {
        onPick(row);
      }}
      // A row is not a control, but clicking one selects it, and a person who
      // cannot use a mouse has to be able to do the same thing — and to reach
      // every row, including the ones the virtualiser has not drawn.
      onKeyDown={onKey}
      role="treeitem"
      tabIndex={tabbable ? 0 : -1}
      aria-level={row.depth + 1}
      aria-selected={selected}
    >
      <button
        type="button"
        className="tree__toggle"
        // The click stops here rather than reaching the row behind it. Without
        // this, opening a schema also counts as picking it, and picking a
        // schema clears the panel — so expanding a node threw away the object
        // somebody was reading, from a click that meant "show me more".
        onClick={(event): void => {
          event.stopPropagation();
          onToggle(row);
        }}
        disabled={!row.node.expandable}
        aria-expanded={row.node.expandable ? row.expanded : undefined}
        // A node that cannot open still draws the space, so names line up
        // rather than shifting by a character depending on their kind.
        aria-label={row.node.expandable ? `Expand ${row.node.name}` : row.node.name}
      >
        {row.node.expandable ? <Icon name="caret" /> : <span className="tree__leaf" />}
      </button>

      <span className="tree__kind">
        <Icon name={glyphOf(row.node.kind)} />
        <span className="visually-hidden">{row.node.kind}</span>
      </span>
      <span className="tree__name">
        <Marked name={row.node.name} text={text} />
      </span>

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
 * A name with the part that matched marked.
 *
 * The mark is what makes an incremental filter readable: with a dozen rows left
 * on screen, showing why each one survived is the difference between a filter
 * and a shorter list.
 */
function Marked({ name, text }: { name: string; text: string }): React.JSX.Element {
  const { before, match, after } = around(name, text);

  if (match === "") {
    return <>{name}</>;
  }

  return (
    <>
      {before}
      <mark>{match}</mark>
      {after}
    </>
  );
}

/**
 * The level as it looks while it is being asked for.
 *
 * What was already there is kept: a node being refreshed shows what it held
 * until the answer lands, instead of emptying and filling again.
 */
/** A copy of the levels with one of them set. */
function written(held: Levels, key: string, level: Level): Levels {
  const next = new Map(held);
  next.set(key, level);

  return next;
}

/** Applies to the map what a level becomes once nobody is waiting for it. */
function giveUp(held: Levels, key: string): Levels {
  const level = abandoned(held.get(key));
  if (level !== undefined) {
    return written(held, key, level);
  }

  // Removed rather than left behind: what makes the node ask again is there
  // being no level for it at all.
  const next = new Map(held);
  next.delete(key);

  return next;
}
