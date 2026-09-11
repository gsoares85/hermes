import { useEffect, useRef, useState } from "react";

import { fetchAppInfo, unknownAppInfo, type AppInfo } from "./api/appInfo";
import {
  closeConnection,
  formFromSaved,
  openConnection,
  savedConnections,
  serverVersion,
  wasCancelled,
  type CancellablePromise,
  type SavedView,
  type StatusView,
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
  released,
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
  // The request that is opening it, so that it can be abandoned. A ref rather
  // than state: nothing on screen is drawn from it, and a render per keystroke
  // of a value only a handler reads is work for nothing.
  const connectingWith = useRef<CancellablePromise<StatusView> | null>(null);
  // The connections the window has already asked the Go side to release. A ref
  // for the same reason as the one above: nothing on screen is drawn from it.
  const letting = useRef(new Set<string>());

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
    const request = serverVersion(asked);

    request
      .then((reported): void => {
        if (active) {
          setVersions((held): ReadonlyMap<string, string> => new Map(held).set(asked, reported));
        }
      })
      .catch((): void => {
        // The server did not answer, or the question was abandoned. The rest
        // of the window still works, and the footer says nothing rather than
        // saying something wrong.
      });

    return (): void => {
      active = false;
      // The question is abandoned with the tab that asked it: it reaches the
      // server, and a flag alone only stops the answer being used.
      void request.cancel();
    };
  }, [connection, versions]);

  // Written from an effect rather than from inside the updater above.
  //
  // An updater has to be pure, and StrictMode calls it twice in development to
  // say so — which made every arrow key on a divider two synchronous writes to
  // storage, with key repeat running at about thirty a second.
  //
  // Not on the first render: what is in state then is what was just read out of
  // storage, and writing it straight back is a write that says nothing.
  const remembered = useRef(true);

  useEffect((): void => {
    if (remembered.current) {
      remembered.current = false;

      return;
    }

    rememberPanes(panes);
  }, [panes]);

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
   * Put an open connection in front.
   *
   * A server gets one tab whichever door it was opened by. The sidebar already
   * came back to a tab rather than opening a second connection; the dialog did
   * not, so opening Manage on a connection that is already open and pressing
   * Connect gave a second tab onto the same server — a second pool, a second
   * cached catalog, and twice the resting memory the window is budgeted for.
   *
   * Taking the tab is only half of it, and the other half is not done here:
   * which connection this pushes out is a question for the tab list at the
   * moment it is replaced, not for the one this render was drawn from. The
   * effect below releases what the answer names.
   */
  function show(status: StatusView): void {
    setTabs((held): Tabs => openedTab(held, status));
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

    // Held rather than awaited straight away: the binding is cancellable the
    // whole way down to the driver, and awaiting it here would throw the only
    // handle away. Without it the row says "Connecting…" for as long as the
    // server takes, the list refuses clicks for all of it, and there is
    // nothing anybody can do — the state the form's Stop button was written to
    // prevent.
    const opening = openConnection(formFromSaved(saved));
    connectingWith.current = opening;

    try {
      show(await opening);
    } catch (err) {
      setSavedNotice(wasCancelled(err) ? "Connecting was stopped." : String(err));
    } finally {
      connectingWith.current = null;
      setConnecting("");
    }
  }

  function stopConnecting(): void {
    void connectingWith.current?.cancel();
  }

  /**
   * Closing the tab is what closes the connection: the pools per database and
   * the cached catalog on the Go side go with it.
   *
   * The tab goes first, and the connection is released behind it. Close is not
   * cancellable and closes the pools, which blocks until the connections in use
   * come back — so waiting for it would leave the × doing nothing visible for
   * as long as a listing of a large schema takes to finish, on the interaction
   * people do fastest.
   *
   * Nothing is waited for afterwards either. The only way releasing fails is
   * the connection being gone already, and a tab pointing at one that is not
   * there is worse than either.
   */
  function close(id: string): void {
    setTabs((held): Tabs => closed(held, id));
    forget(id);

    closeConnection(id).catch((): void => {
      // Already closed. Nothing to say and nothing to go back to.
    });
  }

  /**
   * Everything the window holds keyed by a connection, dropped together.
   *
   * Two maps are keyed by it — what is selected, and what the server said it
   * is — and one of them was being cleared. Neither can be reached again once
   * the tab is gone, so the leak is a string per closed connection and nothing
   * on screen is wrong. It is the asymmetry that is the defect: two maps with
   * the same key and one rule between them is how the next map keyed by a
   * connection ends up never cleared either.
   */
  function forget(id: string): void {
    setPicked((held): ReadonlyMap<string, ObjectRef> => {
      const next = new Map(held);
      next.delete(id);

      return next;
    });
    setVersions((held): ReadonlyMap<string, string> => {
      const next = new Map(held);
      next.delete(id);

      return next;
    });
  }

  /**
   * The connections that lost their tab, released once the tab list says which.
   *
   * Asked before the replacement instead, from the list the render held, the
   * answer went stale the moment a second server was opening at the same time:
   * the sidebar row and the dialog each start one, and the slower of the two
   * replaced a tab it had been told was not there. What it pushed out then
   * stayed open on the Go side — a pool per database and a cached catalog —
   * with no tab on screen able to close it.
   *
   * The queue says what is owed, not what is in flight, and this runs again
   * whenever another connection joins it. What has already been asked for is
   * what the set is for: without it, a second displacement would ask the Go
   * side to close the first one twice.
   */
  useEffect((): void => {
    for (const id of tabs.releasing) {
      if (letting.current.has(id)) {
        continue;
      }

      letting.current.add(id);

      void closeConnection(id)
        .catch((): void => {
          // Gone already. The tab it had is gone too, which is the part
          // anybody can see.
        })
        .finally((): void => {
          letting.current.delete(id);
          forget(id);
          setTabs((held): Tabs => released(held, id));
        });
    }
  }, [tabs.releasing]);

  return (
    <Workspace
      production={shown.production}
      panes={panes}
      onPane={(side, pane): void => {
        setPanes((held): Record<Side, Pane> => ({ ...held, [side]: pane }));
      }}
      toolbar={
        <Toolbar
          connection={connection}
          onNew={(): void => {
            openDialog(null);
          }}
          onDisconnect={(): void => {
            if (connection !== null) {
              close(connection.id);
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
            onStop={stopConnecting}
            onNew={(): void => {
              openDialog(null);
            }}
          />

          {shown.objects && connection !== null && (
            <>
              {/*
                One tree per open connection, and the ones that are not in
                front are hidden rather than taken down.

                Keyed and mounted only for the active tab, every switch
                unmounted a tree: what it had read was thrown away, what was in
                flight was cancelled, and the root was asked for again. That is
                a question to the server and every expanded node closed, on the
                one interaction the tab bar exists for.
              */}
              {tabs.open.map((tab): React.JSX.Element => (
                <ObjectTree
                  key={tab.id}
                  connectionId={tab.id}
                  showing={tab.id === connection.id}
                  onSelect={(object): void => {
                    setPicked((held): ReadonlyMap<string, ObjectRef> => {
                      const next = new Map(held);

                      if (object === null) {
                        next.delete(tab.id);
                      } else {
                        next.set(tab.id, object);
                      }

                      return next;
                    });
                  }}
                />
              ))}

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
                close(id);
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
            {/*
              Rendered only while the dialog is open, so closing it unmounts
              the form.

              A form that merely goes off screen keeps what was typed into it —
              in React's state and in the value of the input — and only a later
              reopen cleared it, because that is what remounts it. Somebody who
              types a password, fails to connect and closes the dialog left the
              secret in the window for the rest of the session.
            */}
            {dialog && (
              <ConnectionForm
                key={opened}
                editing={editing}
                connection={connection}
                onChanged={(): void => {
                  void reload();
                }}
                onOpened={(status): void => {
                  if (status !== null) {
                    show(status);
                  }
                }}
              />
            )}
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
