import { Separator } from "./Separator";
import type { Pane, Side } from "./panes";

/**
 * The four regions of the window.
 *
 * A toolbar across the top, a body of up to three columns, and a status bar
 * across the bottom. Each region is a slot: this decides where things go and
 * how wide and tall they are, and nothing about what they contain.
 *
 * The height chain is the part worth being careful about. The tree draws
 * virtualised rows, and a virtualiser can only skip work it can count — what it
 * counts is how many rows fit in the height of its scroller. A column that grew
 * with its content would give it an unbounded height, it would draw every row it
 * has loaded, and the budget of expanding a schema of five thousand tables would
 * be gone with nothing turning red: the benchmark in CI measures the listing on
 * the Go side, not the drawing. So every box from here down to that scroller
 * says min-height: 0, and the scroller itself keeps `contain: strict`, which
 * makes it impossible for its content to size it at all.
 */
export function Workspace({
  toolbar,
  objects,
  main,
  details,
  status,
  production,
  panes,
  onPane,
}: {
  toolbar: React.ReactNode;
  objects: React.ReactNode | null;
  main: React.ReactNode;
  details: React.ReactNode | null;
  status: React.ReactNode;
  production: boolean;
  panes: Record<Side, Pane>;
  onPane: (side: Side, pane: Pane) => void;
}): React.JSX.Element {
  return (
    <div
      className={production ? "workspace workspace--production" : "workspace"}
      // The widths live here rather than in a stylesheet because they are state:
      // the drag writes the same two variables straight onto this element, and
      // this is what puts the settled value back after it.
      style={
        {
          "--objects-width": `${String(panes.objects.width)}px`,
          "--details-width": `${String(panes.details.width)}px`,
        } as React.CSSProperties
      }
    >
      <header className="workspace__toolbar">{toolbar}</header>

      <div className="workspace__body">
        {/*
          The divider is drawn whenever the pane could be shown, including when
          it is put away — it is the only way back to a pane that is not there.
        */}
        {objects !== null && (
          <>
            {panes.objects.open && (
              <nav className="workspace__objects" aria-label="Navigator">
                {objects}
              </nav>
            )}
            <Separator
              side="objects"
              pane={panes.objects}
              onChange={(pane): void => {
                onPane("objects", pane);
              }}
            />
          </>
        )}

        <main className="workspace__main">{main}</main>

        {details !== null && (
          <>
            <Separator
              side="details"
              pane={panes.details}
              onChange={(pane): void => {
                onPane("details", pane);
              }}
            />
            {panes.details.open && (
              <aside className="workspace__details" aria-label="Details">
                {details}
              </aside>
            )}
          </>
        )}
      </div>

      <footer className="workspace__status">{status}</footer>
    </div>
  );
}
