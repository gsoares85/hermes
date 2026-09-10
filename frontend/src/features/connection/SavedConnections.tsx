import type { SavedView } from "../../api/connection";

import { ConnectionMarks } from "./ConnectionMarks";

/**
 * The connections that have been saved, in the sidebar.
 *
 * It shows what is in the file and nothing about the keychain. Saying which of
 * them have a stored password would mean reading the keychain once per row,
 * which on macOS and on Linux can be an authorisation dialog per connection for
 * somebody who only wanted to see a list.
 *
 * Picking one opens it for editing. Nothing here removes anything: a button
 * that takes a password out of the keychain, with nothing able to put it back,
 * is better pressed by somebody who has the connection open in front of them
 * than by somebody aiming at a small target beside a name.
 */
export function SavedConnections({
  connections,
  openId,
  notice,
  onPick,
  onNew,
}: {
  connections: SavedView[];
  /** The connection that is open, if it is one of these. */
  openId: string;
  /** What went wrong listing them, if anything did. */
  notice: string;
  onPick: (connection: SavedView) => void;
  onNew: () => void;
}): React.JSX.Element {
  return (
    <div className="saved">
      <div className="saved__header">
        <span>Connections</span>
        <button type="button" className="saved__new" aria-label="New connection" onClick={onNew}>
          +
        </button>
      </div>

      {notice !== "" && <p className="saved__notice">{notice}</p>}

      {connections.length === 0 ? (
        <p className="saved__empty">Nothing saved yet.</p>
      ) : (
        <ul className="saved__list">
          {connections.map((connection): React.JSX.Element => (
            <li key={connection.id}>
              <button
                type="button"
                className={connection.id === openId ? "is-open" : ""}
                onClick={(): void => {
                  onPick(connection);
                }}
              >
                <span className="saved__name">
                  {connection.name === "" ? connection.host : connection.name}
                  <ConnectionMarks
                    environment={connection.environment}
                    readOnly={connection.readOnly}
                  />
                </span>
                <span className="saved__target">
                  {connection.user}@{connection.host}:{connection.port}
                  {connection.database === "" ? "" : `/${connection.database}`}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
