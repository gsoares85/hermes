import { useEffect, useState } from "react";

import { fetchAppInfo, unknownAppInfo, type AppInfo } from "./api/appInfo";
import { closeConnection, type StatusView } from "./api/connection";
import type { ObjectRef } from "./api/object";
import { ConnectionForm } from "./features/connection/ConnectionForm";
import { ConnectionMarks } from "./features/connection/ConnectionMarks";
import { ObjectPanel } from "./features/tree/ObjectPanel";
import { ObjectTree } from "./features/tree/ObjectTree";
import { initial, type Pane, type Side } from "./features/workspace/panes";
import { showing } from "./features/workspace/regions";
import { Workspace } from "./features/workspace/Workspace";

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
  // How wide the side panes are and whether they are showing. It is the layout
  // of the window rather than anything about a connection, so it outlives every
  // connection opened in this window.
  const [panes, setPanes] = useState<Record<Side, Pane>>(initial);

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

  // One answer for the three questions the markup below would otherwise ask
  // separately, because they are not independent: everything on the sides
  // belongs to an open connection, the production mark most of all.
  const shown = showing(connection, selected);

  return (
    <Workspace
      production={shown.production}
      panes={panes}
      onPane={(side, pane): void => {
        setPanes((held): Record<Side, Pane> => ({ ...held, [side]: pane }));
      }}
      toolbar={
        <>
          <span className="workspace__brand">Hermes</span>

          {connection !== null && (
            <p className="workspace__connection">
              {connection.name !== "" && (
                <span className="workspace__connection-name">{connection.name}</span>
              )}
              <ConnectionMarks
                environment={connection.environment}
                readOnly={connection.readOnly}
              />
            </p>
          )}
        </>
      }
      objects={
        shown.objects && connection !== null ? (
          <ObjectTree connectionId={connection.id} onSelect={setSelected} />
        ) : null
      }
      main={
        <ConnectionForm
          onOpened={(opened): void => {
            // The one that was open is released rather than dropped. Replacing
            // the state alone would leave it open on the Go side, with its pools
            // per database and its cached catalog, and nothing left out here
            // holding the identifier that could close it. The form disables
            // Connect while one is open, so this is the belt to that braces —
            // and it is the half that does not depend on a second component
            // agreeing about what is open.
            setConnection((held): StatusView | null => {
              if (held !== null && held.id !== opened?.id) {
                void closeConnection(held.id).catch((): void => {
                  // Already gone, or the window is closing. There is nothing
                  // useful to say and nothing to go back to.
                });
              }

              return opened;
            });
            setSelected(null);
          }}
        />
      }
      details={
        shown.details && connection !== null && selected !== null ? (
          <ObjectPanel connectionId={connection.id} object={selected} />
        ) : null
      }
      status={
        // The build that is running, named. A bare version string is ambiguous
        // the moment anything else in the window has one, and the commit and the
        // date stay in the tooltip: enough to identify a build exactly, without
        // a status bar that reads like a changelog.
        <span className="workspace__build" title={`${info.commit} · ${info.date}`}>
          Hermes {info.version} · {info.platform}
        </span>
      }
    />
  );
}
