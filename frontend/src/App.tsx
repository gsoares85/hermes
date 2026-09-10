import { useEffect, useState } from "react";

import { fetchAppInfo, unknownAppInfo, type AppInfo } from "./api/appInfo";
import {
  closeConnection,
  formFromSaved,
  openConnection,
  savedConnections,
  serverVersion,
  wasCancelled,
  type SavedView,
} from "./api/connection";
import type { ObjectRef } from "./api/object";
import { Icon } from "./ui/Icon";
import { ConnectionDialog } from "./features/connection/ConnectionDialog";
import { ConnectionForm } from "./features/connection/ConnectionForm";
import { SavedConnections } from "./features/connection/SavedConnections";
import { ObjectPanel } from "./features/tree/ObjectPanel";
import { ObjectTree } from "./features/tree/ObjectTree";
import { EmptyState } from "./features/workspace/EmptyState";
import type { Pane, Side } from "./features/workspace/panes";
import { rememberedPanes, rememberPanes } from "./features/workspace/remembered";
import { showing } from "./features/workspace/regions";
import { StatusBar } from "./features/workspace/StatusBar";
import { TabBar } from "./features/workspace/TabBar";
import {
  activeOf,
  closed,
  noTabs,
  opened as openedTab,
  type Tabs,
} from "./features/workspace/tabs";
import { Toolbar } from "./features/workspace/Toolbar";
import { Workspace } from "./features/workspace/Workspace";

export function App(): React.JSX.Element {
  const [info, setInfo] = useState<AppInfo>(unknownAppInfo);
  // Every connection the window has open, and which of them is in front.
  //
  // The Go side has held a map of open connections since the day it could open
  // one. It was this that held a single one and closed it to open another, and
  // the tab bar is what stops that: opening a second server no longer costs you
  // the first.
  const [tabs, setTabs] = useState<Tabs>(noTabs);
  // The object each connection has selected, by connection.
  //
  // By connection rather than one for the window, because a selection belongs
  // to the server it was made in. Held that way, moving between tabs is
  // something the render works out; held as one value it would be something an
  // effect had to clear, which is a render before anything has been asked.
  const [picked, setPicked] = useState<ReadonlyMap<string, ObjectRef>>(new Map());
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
  // What each open server reports itself to be, by connection — the same
  // argument as the selection above, and the same shape.
  const [versions, setVersions] = useState<ReadonlyMap<string, string>>(new Map());
  // The saved connection being opened, if one is. A server can take as long as
  // it likes to answer and reading a password can be a dialog waiting for a
  // person, so the row says what it is doing and the list stops taking clicks
  // rather than quietly queueing them.
  const [connecting, setConnecting] = useState("");

  const connection = activeOf(tabs);
  const selected = connection === null ? null : (picked.get(connection.id) ?? null);
  const version = connection === null ? "" : (versions.get(connection.id) ?? "");

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

  // Asked once per connection: it reaches the server, and it does not change
  // under one.
  useEffect(() => {
    if (connection === null || versions.has(connection.id)) {
      return;
    }

    let active = true;
    const asked = connection.id;

    serverVersion(asked)
      .then((reported): void => {
        if (active) {
          setVersions((held): ReadonlyMap<string, string> => new Map(held).set(asked, reported));
        }
      })
      .catch((): void => {
        // The server did not answer. The rest of the window still works, and
        // the footer says nothing rather than saying something wrong.
      });

    return (): void => {
      active = false;
    };
  }, [connection, versions]);

  // One answer for the three questions the markup below would otherwise ask
  // separately, because they are not independent: everything on the sides
  // belongs to an open connection, the production mark most of all.
  const shown = showing(connection, selected);

  function openDialog(on: SavedView | null): void {
    setEditing(on);
    setOpened((times): number => times + 1);
    setDialog(true);
  }

  /**
   * Open a saved connection, or come back to it if it is already open.
   *
   * Clicking the name of a server means "show me that server", and a second
   * connection to one already on screen is a second pool, a second cached
   * catalog and a second tab saying what the first one says.
   *
   * The password is not read here and does not come back here: the Go side
   * fetches it from the keychain at the moment it opens the pool, which is why
   * a form carrying an identifier and no password is enough.
   */
  async function connect(saved: SavedView): Promise<void> {
    const already = tabs.open.find((tab): boolean => tab.savedId === saved.id);
    if (already) {
      setTabs((held): Tabs => ({ ...held, activeId: already.id }));

      return;
    }

    setConnecting(saved.id);
    setSavedNotice("");

    try {
      const status = await openConnection(formFromSaved(saved));
      setTabs((held): Tabs => openedTab(held, status));
    } catch (err) {
      setSavedNotice(wasCancelled(err) ? "Connecting was stopped." : String(err));
    } finally {
      setConnecting("");
    }
  }

  // Closing the tab is what closes the connection: the pools per database and
  // the cached catalog on the Go side go with it. The tab goes whether or not
  // the call worked — the only way it fails is the connection being gone
  // already, and a tab pointing at one that is not there is worse than either.
  async function close(id: string): Promise<void> {
    try {
      await closeConnection(id);
    } catch {
      // Already closed. Nothing to say and nothing to go back to.
    }

    setTabs((held): Tabs => closed(held, id));
    setPicked((held): ReadonlyMap<string, ObjectRef> => {
      const next = new Map(held);
      next.delete(id);

      return next;
    });
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
            if (connection !== null) {
              void close(connection.id);
            }
          }}
        />
      }
      objects={
        <>
          <SavedConnections
            connections={saved}
            openIds={tabs.open.map((tab): string => tab.savedId)}
            connecting={connecting}
            notice={savedNotice}
            onConnect={(pick): void => {
              void connect(pick);
            }}
            onManage={openDialog}
            onNew={(): void => {
              openDialog(null);
            }}
          />

          {shown.objects && connection !== null && (
            <>
              <ObjectTree
                key={connection.id}
                connectionId={connection.id}
                onSelect={(object): void => {
                  setPicked((held): ReadonlyMap<string, ObjectRef> => {
                    const next = new Map(held);

                    if (object === null) {
                      next.delete(connection.id);
                    } else {
                      next.set(connection.id, object);
                    }

                    return next;
                  });
                }}
              />

              {/*
                Which server these objects came from. It is the question the
                tree raises and the status bar does not answer: that one says
                what the session is doing, this says where it is.
              */}
              <p className="workspace__origin">
                <Icon name="plug" />
                {connection.user}@{connection.host}:{connection.port}
                {version === "" ? "" : ` · PostgreSQL ${version}`}
              </p>
            </>
          )}
        </>
      }
      main={
        <>
          {tabs.open.length > 0 && (
            <TabBar
              tabs={tabs}
              onPick={(id): void => {
                setTabs((held): Tabs => ({ ...held, activeId: id }));
              }}
              onClose={(id): void => {
                void close(id);
              }}
            />
          )}

          <EmptyState connected={connection !== null} />

          <ConnectionDialog
            open={dialog}
            title={editing === null ? "New connection" : "Edit connection"}
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
                if (status !== null) {
                  setTabs((held): Tabs => openedTab(held, status));
                }
              }}
            />
          </ConnectionDialog>
        </>
      }
      details={
        shown.details && connection !== null && selected !== null ? (
          <ObjectPanel connectionId={connection.id} object={selected} />
        ) : null
      }
      status={<StatusBar connection={connection} info={info} />}
    />
  );
}
