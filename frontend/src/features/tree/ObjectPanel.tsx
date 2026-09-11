import { useEffect, useState } from "react";

import {
  ddl,
  properties,
  refreshObject,
  type ObjectRef,
  type PropertiesView,
} from "../../api/object";
import { wasCancelled, type CancellablePromise } from "../../api/tree";
import { Icon } from "../../ui/Icon";

import { glyphOf, saidOf } from "./columns";

type Tab = "properties" | "ddl";

/**
 * What has been answered, and which object it was answered about.
 *
 * The second half is the point. Two objects clicked in a row are two questions
 * with one answer between them for as long as the second is in flight, and
 * without a name on the answer the panel draws the first object's columns under
 * the second object's title — which is worse than an empty panel, because
 * nothing on screen says it is wrong.
 */
interface Answer {
  of: string;
  shown: PropertiesView | null;
  script: string;
  failure: string;
}

const nothing: Answer = { of: "", shown: null, script: "", failure: "" };

/**
 * An object as a value, so that two references to the same object are the same
 * thing. A NUL joins the parts for the reason the tree keys on one: every
 * printable character is legal in an identifier.
 */
function nameOf(object: ObjectRef): string {
  return [object.database, object.schema, object.name].join("\u0000");
}

/**
 * What the selected object is, beside the tree.
 *
 * Two tabs over one selection: what it holds, and the statements that would
 * build it. Both are asked for when the tab is looked at rather than when the
 * object is clicked — reading a schema is the expensive question in this
 * product, and paying it because a row was highlighted would make moving
 * through the tree with the arrow keys cost a reading per row.
 */
export function ObjectPanel({
  connectionId,
  object,
}: {
  connectionId: string;
  object: ObjectRef;
}): React.JSX.Element {
  const [tab, setTab] = useState<Tab>("properties");
  const [answer, setAnswer] = useState<Answer>(nothing);
  // Bumped by the Refresh button, and nothing else reads it. It is what turns
  // "ask the same question again" into a change the effect below can see: the
  // object and the tab are the same, so without it the answer already on screen
  // is the answer to the new question too.
  const [asked, setAsked] = useState(0);

  // The object as a value rather than as an object, because the effect below
  // depends on it and the tree builds a fresh ObjectRef on every click. Depending
  // on the reference made clicking the highlighted row ask the server for what
  // was already on screen.
  const wanted = nameOf(object);

  // What is held says which object it is about, so that the answer to the last
  // question cannot be drawn under the title of the next one. Resetting in the
  // body of the effect would be the other way, and the rule against it is right:
  // it is a second render before anything has been asked.
  useEffect((): (() => void) => {
    const request: CancellablePromise<PropertiesView | string> =
      tab === "properties" ? properties(connectionId, object) : ddl(connectionId, object);

    request
      .then((got): void => {
        setAnswer((held): Answer => {
          const base = held.of === wanted ? held : nothing;

          return typeof got === "string"
            ? { ...base, of: wanted, script: got, failure: "" }
            : { ...base, of: wanted, shown: got, failure: "" };
        });
      })
      .catch((err: unknown): void => {
        if (!wasCancelled(err)) {
          setAnswer((held): Answer => ({
            ...(held.of === wanted ? held : nothing),
            of: wanted,
            failure: String(err),
          }));
        }
      });

    return (): void => {
      void request.cancel();
    };
    // object is left out on purpose: wanted is the value of it, and the
    // reference changes on every click of the same row.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connectionId, wanted, tab, asked]);

  async function onRefresh(): Promise<void> {
    try {
      await refreshObject(connectionId, object);
      setAsked((times): number => times + 1);
    } catch (err) {
      if (!wasCancelled(err)) {
        setAnswer((held): Answer => ({ ...held, of: wanted, failure: String(err) }));
      }
    }
  }

  // Only what was answered about the object on screen. Everything below reads
  // this rather than the state, so the panel cannot show one object's DDL under
  // another's name while the answer is on its way.
  const held = answer.of === wanted ? answer : nothing;

  // There is nothing to show yet for the tab being looked at. It is derived
  // rather than held, so it cannot disagree with what is on screen.
  const nothingYet = tab === "properties" ? held.shown === null : held.script === "";

  return (
    <section className="panel" aria-label={object.name}>
      <header className="panel__header">
        <div className="panel__title">
          <p className="panel__where">
            {object.database} · {object.schema}
          </p>
          <h3>{object.name}</h3>
        </div>
        {/*
          The tree reads the server every time a node is opened and this panel
          reads through a cache, so after something changes the database from
          elsewhere the two disagree. This is the way to say "look again", and
          it forgets one schema rather than the whole connection.
        */}
        <button type="button" className="panel__refresh" onClick={(): void => void onRefresh()}>
          <Icon name="refresh" />
          Refresh
        </button>
      </header>

      <div className="panel__tabs" role="tablist">
        <TabButton current={tab} value="properties" onPick={setTab}>
          Properties
        </TabButton>
        <TabButton current={tab} value="ddl" onPick={setTab}>
          DDL
        </TabButton>
      </div>

      {held.failure !== "" && <p className="panel__error">{held.failure}</p>}
      {nothingYet && held.failure === "" && <p className="panel__note">Reading…</p>}

      {tab === "properties" && held.shown !== null && <Properties shown={held.shown} />}
      {tab === "ddl" && held.script !== "" && <pre className="panel__ddl">{held.script}</pre>}
    </section>
  );
}

function TabButton({
  current,
  value,
  onPick,
  children,
}: {
  current: Tab;
  value: Tab;
  onPick: (tab: Tab) => void;
  children: React.ReactNode;
}): React.JSX.Element {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={current === value}
      className={current === value ? "is-selected" : ""}
      onClick={(): void => {
        onPick(value);
      }}
    >
      {children}
    </button>
  );
}

/*
 * One name for one number.
 *
 * The card counted every constraint the model read and called the total
 * "Keys", while the list underneath rendered the same array under the heading
 * "Constraints" — so a table with three CHECK constraints and no key at all
 * reported three keys. Both now read the name from here, which is what stops
 * them saying different things about one array again.
 */
const constraintsTitle = "Constraints";
const indexesTitle = "Indexes";

/** The columns and everything else declared on the object. */
function Properties({ shown }: { shown: PropertiesView }): React.JSX.Element {
  const columns = shown.columns ?? [];
  const constraints = shown.constraints ?? [];
  const indexes = shown.indexes ?? [];

  return (
    <div className="panel__properties">
      {/*
        What the model actually read. The design this is drawn from also counts
        rows and measures the table on disk; those come from the statistics of
        the server rather than from its catalog, and a card that showed a dash
        where a number belongs would be worse than a card with three facts on it.
      */}
      <div className="panel__stats">
        <Stat label="Columns" value={columns.length} />
        <Stat label={constraintsTitle} value={constraints.length} />
        <Stat label={indexesTitle} value={indexes.length} />
      </div>

      {shown.notes !== null && shown.notes.length > 0 && (
        <ul className="panel__notes">
          {shown.notes.map((note) => (
            <li key={note}>{note}</li>
          ))}
        </ul>
      )}

      {columns.length > 0 && (
        <div className="panel__section">
          <h4>Columns</h4>
          <ul className="panel__columns">
            {columns.map((column) => (
              <li key={column.name}>
                <span className="panel__column-name">
                  {column.primaryKey ? (
                    <Icon name={glyphOf(column)} className="panel__key" />
                  ) : (
                    <Icon name={glyphOf(column)} />
                  )}
                  {column.name}
                </span>
                <span className="panel__column-type">
                  {column.type}
                  {saidOf(column).map((said) => (
                    <span key={said} className="panel__flag">
                      {" "}
                      {said}
                    </span>
                  ))}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}

      <Listing title={constraintsTitle} items={shown.constraints} />
      <Listing title={indexesTitle} items={shown.indexes} />
    </div>
  );
}

function Stat({ label, value }: { label: string; value: number }): React.JSX.Element {
  return (
    <div className="panel__stat">
      <span className="panel__stat-label">{label}</span>
      <span className="panel__stat-value">{value}</span>
    </div>
  );
}

function Listing({
  title,
  items,
}: {
  title: string;
  items: string[] | null;
}): React.JSX.Element | null {
  if (items === null || items.length === 0) {
    return null;
  }

  return (
    <div className="panel__section panel__listing">
      <h4>{title}</h4>
      <ul>
        {items.map((item) => (
          <li key={item}>{item}</li>
        ))}
      </ul>
    </div>
  );
}
