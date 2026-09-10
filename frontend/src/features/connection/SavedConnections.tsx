import type { SavedView } from "../../api/connection";
import { Icon } from "../../ui/Icon";

import { ConnectionMarks } from "./ConnectionMarks";

/**
 * The connections that have been saved, in the sidebar.
 *
 * **A row connects.** That is what somebody who clicks the name of a server
 * wants, and it was worth saying out loud because the row used to open a form
 * about the server instead — one more click and a screenful of fields between
 * wanting to look at a database and looking at it. Editing it is a thing you do
 * occasionally, so it is a small control of its own rather than what the whole
 * row does.
 *
 * It shows what is in the file and nothing about the keychain. Saying which of
 * these have a stored password would mean reading the keychain once per row,
 * which on macOS and on Linux can be an authorisation dialog per connection for
 * somebody who only wanted to see a list.
 */
export function SavedConnections({
  connections,
  openIds,
  connecting,
  notice,
  onConnect,
  onManage,
  onNew,
}: {
  connections: SavedView[];
  /** The saved connections that already have a tab. */
  openIds: readonly string[];
  /** The one being opened, if any: a server can take its time answering. */
  connecting: string;
  /** What went wrong listing them, or opening one. */
  notice: string;
  onConnect: (connection: SavedView) => void;
  onManage: (connection: SavedView) => void;
  onNew: () => void;
}): React.JSX.Element {
  return (
    <div className="saved">
      <div className="saved__header">
        <span>Connections</span>
        <button type="button" className="saved__new" aria-label="New connection" onClick={onNew}>
          <Icon name="plus" />
        </button>
      </div>

      {notice !== "" && <p className="saved__notice">{notice}</p>}

      {connections.length === 0 ? (
        <p className="saved__empty">Nothing saved yet.</p>
      ) : (
        <ul className="saved__list">
          {connections.map((connection): React.JSX.Element => {
            const name = connection.name === "" ? connection.host : connection.name;
            const open = openIds.includes(connection.id);
            const opening = connecting === connection.id;

            return (
              <li key={connection.id} className={open ? "is-open" : ""}>
                <button
                  type="button"
                  className="saved__open"
                  disabled={connecting !== ""}
                  onClick={(): void => {
                    onConnect(connection);
                  }}
                >
                  <span className="saved__name">
                    <Icon name="database" className="saved__icon" />
                    {name}
                    <ConnectionMarks
                      environment={connection.environment}
                      readOnly={connection.readOnly}
                    />
                  </span>
                  <span className="saved__target">
                    {opening
                      ? "Connecting…"
                      : `${connection.user}@${connection.host}:${String(connection.port)}${
                          connection.database === "" ? "" : `/${connection.database}`
                        }`}
                  </span>
                </button>

                <button
                  type="button"
                  className="saved__manage"
                  aria-label={`Manage ${name}`}
                  title={`Manage ${name}`}
                  onClick={(): void => {
                    onManage(connection);
                  }}
                >
                  <Icon name="manage" />
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
