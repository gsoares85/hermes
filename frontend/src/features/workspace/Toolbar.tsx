import type { StatusView } from "../../api/connection";
import { ConnectionMarks } from "../connection/ConnectionMarks";

/**
 * The strip across the top of the window: who you are connected to, and the
 * few things you can do to that connection.
 *
 * It carries only actions that exist. A row of buttons for a query editor and
 * an importer that have not been built, greyed out and waiting, is a window
 * making promises on behalf of work nobody has started — and the greying is the
 * same signal a control uses when it is temporarily unavailable, so it says the
 * wrong thing twice.
 *
 * The marks of the open connection live here because the requirement they exist
 * for is that a production server be unmistakable *wherever* somebody is
 * looking. This strip is above every panel and scrolls away from none of them.
 */
export function Toolbar({
  connection,
  onNew,
  onDisconnect,
}: {
  connection: StatusView | null;
  onNew: () => void;
  onDisconnect: () => void;
}): React.JSX.Element {
  return (
    <>
      <span className="workspace__brand">Hermes</span>

      {/*
        Also reachable from the + of the navigator, and not a duplicate for the
        sake of it: the navigator can be put away, and with it the only way of
        opening a connection at all.
      */}
      <button type="button" onClick={onNew}>
        New connection
      </button>

      {connection !== null && (
        <button type="button" onClick={onDisconnect}>
          Disconnect
        </button>
      )}

      {connection !== null && (
        <p className="workspace__connection">
          {connection.name !== "" && (
            <span className="workspace__connection-name">{connection.name}</span>
          )}
          <ConnectionMarks environment={connection.environment} readOnly={connection.readOnly} />
        </p>
      )}
    </>
  );
}
