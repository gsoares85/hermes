import type { AppInfo } from "../../api/appInfo";
import type { StatusView } from "../../api/connection";

/**
 * The strip across the bottom: what the connection is doing, and what build is
 * doing it.
 *
 * What it does not carry is as deliberate as what it does. The design it is
 * drawn from also shows the position of the cursor, the state of the
 * transaction and the time the last query took — three things that belong to a
 * SQL editor that does not exist yet. A status bar with places held for them
 * would be a row of dashes claiming to be information.
 */
export function StatusBar({
  connection,
  info,
}: {
  connection: StatusView | null;
  info: AppInfo;
}): React.JSX.Element {
  return (
    <>
      {connection === null ? (
        <span className="status__idle">Not connected</span>
      ) : (
        <>
          <span className={`status__state status__state--${connection.state}`}>
            <span className="status__dot" aria-hidden="true" />
            {connection.state}
          </span>
          <span>
            {connection.database} · {connection.user}
          </span>
        </>
      )}

      {/*
        The build that is running, named. A bare version string is ambiguous the
        moment anything else in the window has one, and the commit and the date
        stay in the tooltip: enough to identify a build exactly, without a status
        bar that reads like a changelog.
      */}
      <span className="status__build" title={`${info.commit} · ${info.date}`}>
        Hermes {info.version} · {info.platform}
      </span>
    </>
  );
}
