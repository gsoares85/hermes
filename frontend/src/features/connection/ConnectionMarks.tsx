/**
 * What a connection is, drawn wherever the connection is.
 *
 * The two marks answer the two questions somebody about to run something needs
 * answered without asking: which server am I on, and can I break it. They are
 * drawn from the connection rather than from the screen showing it, so every
 * place that shows a connection shows the same thing — which is the whole
 * requirement, because a production server that is unmistakable on one screen
 * and unlabelled on the next is not marked at all.
 */

/**
 * The words for the three labels the Go side offers.
 *
 * Only the wording lives here. Which values exist, and which of them a saved
 * connection may carry, is decided in Go and asked for at startup — this map
 * says how to write them down, and an unfamiliar value is written as it came
 * rather than swallowed.
 */
const wording: Record<string, string> = {
  dev: "Development",
  staging: "Staging",
  prod: "Production",
};

export function ConnectionMarks({
  environment,
  readOnly,
}: {
  environment: string;
  readOnly: boolean;
}): React.JSX.Element | null {
  if (environment === "" && !readOnly) {
    return null;
  }

  return (
    <span className="marks">
      {environment !== "" && (
        <span className={`mark mark--${environment}`}>{wording[environment] ?? environment}</span>
      )}
      {readOnly && <span className="mark mark--read-only">Read-only</span>}
    </span>
  );
}

/** Whether a connection is the one nobody wants to be wrong about. */
export function isProduction(environment: string): boolean {
  return environment === "prod";
}
