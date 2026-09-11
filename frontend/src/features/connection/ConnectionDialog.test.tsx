// @vitest-environment happy-dom

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ConnectionDialog } from "./ConnectionDialog";

afterEach(cleanup);

/**
 * The dialog, in a DOM.
 *
 * Everything this frame is worth using for — focus staying inside it, Escape
 * closing it, the page behind it being inert — is behaviour of the element
 * switched on by one call, and nothing that compiles, lints or type-checks can
 * see whether that call was reached. The stylesheet test next door proves the
 * CSS does not un-hide a closed dialog; whether an open one is a modal is a
 * different question, and it is the one that shipped wrong twice with every
 * gate green.
 *
 * `showModal` and `show` are watched rather than the state they leave behind,
 * because no DOM implementation outside a browser distinguishes the two: both
 * set the `open` attribute, and only one of them makes a modal. Which of the
 * two is called *is* the contract.
 */
describe("the connection dialog", () => {
  let showModal: ReturnType<typeof vi.spyOn>;
  let show: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    showModal = vi.spyOn(HTMLDialogElement.prototype, "showModal");
    show = vi.spyOn(HTMLDialogElement.prototype, "show");
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("opens as a modal rather than merely visible", () => {
    render(
      <ConnectionDialog open title="New connection" onClose={vi.fn()}>
        <p>fields</p>
      </ConnectionDialog>,
    );

    expect(showModal).toHaveBeenCalled();
    // `show` gives a dialog that is on screen with the page behind it still
    // reachable: no backdrop, nothing inert, no focus trap, and Escape doing
    // nothing. It is the failure this component exists to make impossible.
    expect(show).not.toHaveBeenCalled();
  });

  it("is not in the document until it is asked to open", () => {
    render(
      <ConnectionDialog open={false} title="New connection" onClose={vi.fn()}>
        <p>fields</p>
      </ConnectionDialog>,
    );

    expect(showModal).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("hears the element closing itself", () => {
    const onClose = vi.fn();

    render(
      <ConnectionDialog open title="New connection" onClose={onClose}>
        <p>fields</p>
      </ConnectionDialog>,
    );

    // Escape closes a modal without asking anybody, and what it produces is
    // this event. The state has to hear it, or the two disagree and the next
    // attempt to open the dialog finds the element already closed and the
    // state already true, and does nothing at all.
    fireEvent(screen.getByRole("dialog"), new Event("close"));

    expect(onClose).toHaveBeenCalled();
  });

  it("closes from the corner", () => {
    const onClose = vi.fn();

    render(
      <ConnectionDialog open title="New connection" onClose={onClose}>
        <p>fields</p>
      </ConnectionDialog>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Close" }));

    expect(onClose).toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("closes from the backdrop, and not from the fields", () => {
    const onClose = vi.fn();

    render(
      <ConnectionDialog open title="New connection" onClose={onClose}>
        <p>fields</p>
      </ConnectionDialog>,
    );

    fireEvent.click(screen.getByText("fields"));
    expect(onClose).not.toHaveBeenCalled();

    // A click on the backdrop lands on the dialog element itself. It is the
    // one way to tell the two apart without measuring where the pointer was.
    fireEvent.click(screen.getByRole("dialog"));
    expect(onClose).toHaveBeenCalled();
  });

  it("closes the element when the state alone says it is closed", () => {
    function Harness(): React.JSX.Element {
      const [open, setOpen] = useState(true);

      return (
        <>
          <button
            type="button"
            onClick={(): void => {
              setOpen(false);
            }}
          >
            elsewhere
          </button>
          <ConnectionDialog open={open} title="New connection" onClose={vi.fn()}>
            <p>fields</p>
          </ConnectionDialog>
        </>
      );
    }

    render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "elsewhere" }));

    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("names what it is about", () => {
    render(
      <ConnectionDialog open title="Edit connection" onClose={vi.fn()}>
        <p>fields</p>
      </ConnectionDialog>,
    );

    expect(screen.getByRole("dialog").getAttribute("aria-labelledby")).toBe("dialog-title");
    expect(screen.getByRole("heading", { name: "Edit connection" }).id).toBe("dialog-title");
  });
});
