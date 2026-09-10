import { useEffect, useState } from "react";

import { fetchAppInfo, unknownAppInfo, type AppInfo } from "./api/appInfo";
import type { StatusView } from "./api/connection";
import type { ObjectRef } from "./api/object";
import { ConnectionForm } from "./features/connection/ConnectionForm";
import { ConnectionMarks, isProduction } from "./features/connection/ConnectionMarks";
import { ObjectPanel } from "./features/tree/ObjectPanel";
import { ObjectTree } from "./features/tree/ObjectTree";

export function App(): React.JSX.Element {
  const [info, setInfo] = useState<AppInfo>(unknownAppInfo);
  // The connection whose objects are on screen. Null until one is opened, and
  // null again when it closes: the tree belongs to a connection and there is
  // nothing to draw without one.
  //
  // The whole state is kept rather than the identifier alone, because the mark
  // saying which server this is belongs around the whole window and not inside
  // the panel that happens to have asked for it.
  const [connection, setConnection] = useState<StatusView | null>(null);
  // The object whose properties are on screen. Null until one is picked, and
  // null again when the connection goes, because it belonged to that server.
  const [selected, setSelected] = useState<ObjectRef | null>(null);

  useEffect(() => {
    let active = true;

    fetchAppInfo()
      .then((loaded): void => {
        if (active) {
          setInfo(loaded);
        }
      })
      .catch((): void => {
        // Running outside the desktop shell: keep the placeholder rather than
        // breaking the window over build metadata.
      });

    return (): void => {
      active = false;
    };
  }, []);

  // Drawn on the window rather than on a panel, and that is the requirement
  // rather than a flourish: a production server has to be unmistakable wherever
  // someone is looking, and every panel is inside this.
  const production = connection !== null && isProduction(connection.environment);

  return (
    <div className={production ? "app app--production" : "app"}>
      <header className="app__header">
        <div>
          <h1>Hermes</h1>
          <p>PostgreSQL, without the license.</p>
        </div>

        {connection !== null && (
          <p className="app__connection">
            {connection.name !== "" && (
              <span className="app__connection-name">{connection.name}</span>
            )}
            <ConnectionMarks environment={connection.environment} readOnly={connection.readOnly} />
          </p>
        )}
      </header>

      <main className="app__main">
        <ConnectionForm
          onOpened={(opened): void => {
            setConnection(opened);
            setSelected(null);
          }}
        />

        {connection !== null && (
          <div className="app__browser">
            <ObjectTree connectionId={connection.id} onSelect={setSelected} />

            {selected !== null && <ObjectPanel connectionId={connection.id} object={selected} />}
          </div>
        )}
      </main>

      {/*
        The build that is running, named. A bare version string is ambiguous the
        moment anything else in the window has one, and the commit and the date
        stay in the tooltip: enough to identify a build exactly, without a
        status bar that reads like a changelog.
      */}
      <footer className="app__status" title={`${info.commit} · ${info.date}`}>
        <span>Hermes {info.version}</span>
        <span>{info.platform}</span>
      </footer>
    </div>
  );
}
