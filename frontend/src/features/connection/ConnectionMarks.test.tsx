// @vitest-environment happy-dom

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { ConnectionMarks, isProduction } from "./ConnectionMarks";

afterEach(cleanup);

describe("the marks on a connection", () => {
  it("says nothing about a connection with nothing to say", () => {
    const { container } = render(<ConnectionMarks environment="" readOnly={false} />);

    expect(container.innerHTML).toBe("");
  });

  it("writes the three the Go side offers in words", () => {
    render(<ConnectionMarks environment="prod" readOnly />);

    expect(screen.getByText("Production")).not.toBeNull();
    expect(screen.getByText("Read-only")).not.toBeNull();
  });

  it("writes an unfamiliar label as it came", () => {
    render(<ConnectionMarks environment="qa" readOnly={false} />);

    expect(screen.getByText("qa")).not.toBeNull();
  });

  /**
   * A label is looked up on the map, not on everything the map inherits.
   *
   * `wording[environment]` walks the prototype chain, so a value like
   * "constructor" answers with a function — which React renders by throwing,
   * and there is no error boundary above this, so the window goes with it.
   *
   * Nothing can produce that value today: Go validates the label on the way in
   * and again when the file is read. But the type that guarantees it is erased
   * at runtime, this component is drawn in three places now, and the fix is one
   * call.
   */
  it("does not answer with something the map merely inherits", () => {
    render(<ConnectionMarks environment="constructor" readOnly={false} />);

    expect(screen.getByText("constructor")).not.toBeNull();
  });

  it("knows which one is the one nobody wants to be wrong about", () => {
    expect(isProduction("prod")).toBe(true);
    expect(isProduction("staging")).toBe(false);
    expect(isProduction("constructor")).toBe(false);
  });
});
