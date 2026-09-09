import { useEffect, useState } from "react";

import { ddl, properties, type ObjectRef, type PropertiesView } from "../../api/object";
import { wasCancelled, type CancellablePromise } from "../../api/tree";

type Tab = "properties" | "ddl";

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
  const [shown, setShown] = useState<PropertiesView | null>(null);
  const [script, setScript] = useState("");
  const [failure, setFailure] = useState("");

  // Nothing is reset when the question changes: what is on screen stays until
  // the answer to the new one lands, which is the same rule the tree follows
  // for a level being refreshed. It is also the only shape the rule against
  // setting state in the body of an effect leaves — and the rule is right, a
  // reset here is a second render before anything has been asked.
  useEffect((): (() => void) => {
    const request: CancellablePromise<PropertiesView | string> =
      tab === "properties" ? properties(connectionId, object) : ddl(connectionId, object);

    request
      .then((answer): void => {
        setFailure("");

        if (typeof answer === "string") {
          setScript(answer);
        } else {
          setShown(answer);
        }
      })
      .catch((err: unknown): void => {
        if (!wasCancelled(err)) {
          setFailure(String(err));
        }
      });

    return (): void => {
      void request.cancel();
    };
  }, [connectionId, object, tab]);

  // There is nothing to show yet for the tab being looked at. It is derived
  // rather than held, so it cannot disagree with what is on screen.
  const nothingYet = tab === "properties" ? shown === null : script === "";

  return (
    <section className="panel" aria-label={object.name}>
      <header className="panel__header">
        <h3>{object.name}</h3>
        <p className="panel__where">
          {object.database} · {object.schema}
        </p>
      </header>

      <div className="panel__tabs" role="tablist">
        <TabButton current={tab} value="properties" onPick={setTab}>
          Properties
        </TabButton>
        <TabButton current={tab} value="ddl" onPick={setTab}>
          DDL
        </TabButton>
      </div>

      {failure !== "" && <p className="panel__error">{failure}</p>}
      {nothingYet && failure === "" && <p className="panel__note">Reading…</p>}

      {tab === "properties" && shown !== null && <Properties shown={shown} />}
      {tab === "ddl" && script !== "" && <pre className="panel__ddl">{script}</pre>}
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

/** The columns and everything else declared on the object. */
function Properties({ shown }: { shown: PropertiesView }): React.JSX.Element {
  return (
    <div className="panel__properties">
      {shown.notes !== null && shown.notes.length > 0 && (
        <ul className="panel__notes">
          {shown.notes.map((note) => (
            <li key={note}>{note}</li>
          ))}
        </ul>
      )}

      {shown.columns !== null && shown.columns.length > 0 && (
        <table className="panel__columns">
          <thead>
            <tr>
              <th>Column</th>
              <th>Type</th>
              <th>Null</th>
              <th>Default</th>
            </tr>
          </thead>
          <tbody>
            {shown.columns.map((column) => (
              <tr key={column.name}>
                <td>{column.name}</td>
                <td>{column.type}</td>
                <td>{column.notNull ? "not null" : ""}</td>
                <td>{column.generated === "" ? column.default : column.generated}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <Listing title="Constraints" items={shown.constraints} />
      <Listing title="Indexes" items={shown.indexes} />
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
    <div className="panel__listing">
      <h4>{title}</h4>
      <ul>
        {items.map((item) => (
          <li key={item}>{item}</li>
        ))}
      </ul>
    </div>
  );
}
