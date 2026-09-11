/**
 * What the middle of the window says while there is nothing in it.
 *
 * It says what the space is for and what to do next, and it does not draw a
 * disabled editor or an empty grid of results. A panel that looks like a
 * feature and does nothing is worse than a sentence: it takes somebody a while
 * to work out that it is not broken.
 */
export function EmptyState({ connected }: { connected: boolean }): React.JSX.Element {
  return (
    <div className="empty">
      {connected ? (
        <>
          <p className="empty__lead">Pick an object in the navigator.</p>
          <p>
            Its columns, constraints and indexes appear on the right, with the DDL that would build
            it.
          </p>
          <p className="empty__later">
            This space is where a query and its results will go, once there is an editor to write
            one in.
          </p>
        </>
      ) : (
        <>
          <p className="empty__lead">Nothing is open.</p>
          <p>
            Open a connection to browse what is on a server — from the navigator, or from the
            toolbar above.
          </p>
        </>
      )}
    </div>
  );
}
