/**
 * The four regions of the window.
 *
 * A toolbar across the top, a body of up to three columns, and a status bar
 * across the bottom. Each region is a slot: this decides where things go and
 * how tall they are, and nothing about what they contain.
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
}: {
  toolbar: React.ReactNode;
  objects: React.ReactNode | null;
  main: React.ReactNode;
  details: React.ReactNode | null;
  status: React.ReactNode;
  production: boolean;
}): React.JSX.Element {
  return (
    <div className={production ? "workspace workspace--production" : "workspace"}>
      <header className="workspace__toolbar">{toolbar}</header>

      <div className="workspace__body">
        {objects !== null && (
          <nav className="workspace__objects" aria-label="Navigator">
            {objects}
          </nav>
        )}

        <main className="workspace__main">{main}</main>

        {details !== null && (
          <aside className="workspace__details" aria-label="Details">
            {details}
          </aside>
        )}
      </div>

      <footer className="workspace__status">{status}</footer>
    </div>
  );
}
