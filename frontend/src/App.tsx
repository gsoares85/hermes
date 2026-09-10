import { useEffect, useState } from "react";

import { fetchAppInfo, unknownAppInfo, type AppInfo } from "./api/appInfo";
import {
  closeConnection,
  savedConnections,
  type SavedView,
  type StatusView,
} from "./api/connection";
import type { ObjectRef } from "./api/object";
import { ConnectionDialog } from "./features/connection/ConnectionDialog";
import { ConnectionForm } from "./features/connection/ConnectionForm";
import { SavedConnections } from "./features/connection/SavedConnections";
import { ObjectPanel } from "./features/tree/ObjectPanel";
import { ObjectTree } from "./features/tree/ObjectTree";
import type { Pane, Side } from "./features/workspace/panes";
import { rememberedPanes, rememberPanes } from "./features/workspace/remembered";
import { Toolbar } from "./features/workspace/Toolbar";
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
  // connection opened in this window — and, through the storage below, this run
  // of it. Read lazily: it touches storage, and doing that on every render to
  // throw the answer away is work for nothing.
  const [panes, setPanes] = useState<Record<Side, Pane>>(rememberedPanes);
  // The saved connections, listed in the sidebar. They live here rather than in
  // the form because the sidebar shows them and the form changes them, and two
  // copies of a list are two answers to the same question.
  const [saved, setSaved] = useState<SavedView[]>([]);
  const [savedNotice, setSavedNotice] = useState("");
  // The connection the dialog is about, and the number of times it has been
  // opened. The count is the key of the form, so opening the dialog on another
  // connection builds a fresh one rather than trying to keep fields somebody is
  // typing into in step with a prop.
  const [editing, setEditing] = useState<SavedView | null>(null);
  const [opened, setOpened] = useState(0);
  const [dialog, setDialog] = useState(false);

  // Declared above the effect that calls it for the reason the chain below is
  // written out rather than awaited: a function declaration hoists, so written
  // underneath nothing would show that the effect captures the copy from the
  // first render.
  function reload(): Promise<void> {
    return savedConnections()
      .then((connections): void => {
        setSaved(connections);
        setSavedNotice("");
      })
      .catch((err: unknown): void => {
        // A file somebody broke by hand is reported rather than swallowed: an
        // empty list looks exactly like never having saved anything.
        setSavedNotice(String(err));
      });
  }

  useEffect(() => {
    void reload();
  }, []);

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

  function openDialog(on: SavedView | null): void {
    setEditing(on);
    setOpened((times): number => times + 1);
    setDialog(true);
  }

  // Releasing it is what closing the pools and the cached catalog on the Go
  // side depends on, so the state goes to null whether or not the call worked:
  // the only way it fails is the connection being gone already, and leaving the
  // window pointing at one that is not there would be worse than either.
  async function disconnect(): Promise<void> {
    if (connection === null) {
      return;
    }

    try {
      await closeConnection(connection.id);
    } catch {
      // Already closed. Nothing to say and nothing to go back to.
    }

    setConnection(null);
    setSelected(null);
  }

  return (
    <Workspace
      production={shown.production}
      panes={panes}
      onPane={(side, pane): void => {
        setPanes((held): Record<Side, Pane> => {
          const next = { ...held, [side]: pane };
          rememberPanes(next);

          return next;
        });
      }}
      toolbar={
        <Toolbar
          connection={connection}
          onNew={(): void => {
            openDialog(null);
          }}
          onDisconnect={(): void => {
            void disconnect();
          }}
        />
      }
      objects={
        <>
          <SavedConnections
            connections={saved}
            openId={connection?.id ?? ""}
            notice={savedNotice}
            onPick={openDialog}
            onNew={(): void => {
              openDialog(null);
            }}
          />

          {shown.objects && connection !== null && (
            <ObjectTree connectionId={connection.id} onSelect={setSelected} />
          )}
        </>
      }
      main={
        <ConnectionDialog
          open={dialog}
          onClose={(): void => {
            setDialog(false);
          }}
        >
          <ConnectionForm
            key={opened}
            editing={editing}
            connection={connection}
            onChanged={(): void => {
              void reload();
            }}
            onOpened={(status): void => {
              // The one that was open is released rather than dropped. Replacing
              // the state alone would leave it open on the Go side, with its pools
              // per database and its cached catalog, and nothing left out here
              // holding the identifier that could close it. The form disables
              // Connect while one is open, so this is the belt to that braces —
              // and it is the half that does not depend on a second component
              // agreeing about what is open.
              setConnection((held): StatusView | null => {
                if (held !== null && held.id !== status?.id) {
                  void closeConnection(held.id).catch((): void => {
                    // Already gone, or the window is closing. There is nothing
                    // useful to say and nothing to go back to.
                  });
                }

                return status;
              });
              setSelected(null);
            }}
          />
        </ConnectionDialog>
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
