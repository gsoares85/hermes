// @vitest-environment happy-dom

import { cleanup, fireEvent, render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { initial } from "./panes";
import { Separator } from "./Separator";

afterEach(cleanup);

/**
 * The divider, inside the two elements it walks up to find.
 *
 * Nothing outside a browser has a layout, so the body's box is stubbed: the
 * arithmetic under test is what a pointer position means, not what the browser
 * measured.
 */
function draw(width: number): {
  handle: HTMLElement;
  root: HTMLElement;
  onChange: ReturnType<typeof vi.fn>;
} {
  const onChange = vi.fn();

  const { container } = render(
    <div className="workspace">
      <div className="workspace__body">
        <Separator side="objects" pane={{ ...initial.objects, width }} onChange={onChange} />
      </div>
    </div>,
  );

  const body = container.querySelector<HTMLElement>(".workspace__body");
  const handle = container.querySelector<HTMLElement>(".workspace__separator");

  Object.defineProperty(body, "getBoundingClientRect", {
    configurable: true,
    value: (): DOMRect => ({ left: 0, right: 900 }) as DOMRect,
  });

  // Pointer capture is a browser API happy-dom does not implement, and the
  // drag is built on it. Stubbed rather than worked around: what is under test
  // is what a pointer position means, and capture is how the divider keeps
  // hearing about a pointer that has left it.
  const captured = new Set<number>();
  Object.assign(handle as HTMLElement, {
    setPointerCapture: (id: number): void => void captured.add(id),
    hasPointerCapture: (id: number): boolean => captured.has(id),
    releasePointerCapture: (id: number): void => {
      if (!captured.delete(id)) {
        // What a browser does: releasing a pointer that is not captured is an
        // error, not a no-op.
        throw new DOMException("no such pointer", "NotFoundError");
      }
    },
  });

  return {
    handle: handle as HTMLElement,
    root: container.querySelector<HTMLElement>(".workspace") as HTMLElement,
    onChange,
  };
}

function dragTo(handle: HTMLElement, clientX: number): void {
  fireEvent.pointerDown(handle, { pointerId: 1, clientX: 252 });
  fireEvent.pointerMove(handle, { pointerId: 1, clientX });
}

describe("dragging the divider", () => {
  it("settles the width when the pointer is let go", () => {
    const { handle, onChange } = draw(252);

    dragTo(handle, 320);
    fireEvent.pointerUp(handle, { pointerId: 1, clientX: 320 });

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ width: 320, open: true }));
  });

  it("writes the width straight onto the element while the pointer moves", () => {
    const { handle, root, onChange } = draw(252);

    dragTo(handle, 300);

    // The drag does not go through React: a render per pointer move is a
    // render of a tree of up to five thousand rows.
    expect(root.style.getPropertyValue("--objects-width")).toBe("300px");
    expect(onChange).not.toHaveBeenCalled();
  });

  /**
   * A cancelled drag is a drag that did not happen.
   *
   * The browser cancels a drag for reasons nobody chose — the pointer is taken
   * by something else, the touch becomes a scroll. Treating that as a release
   * commits whatever width the pointer happened to be over, and because a
   * settled width is always an open pane, it can put back a pane that was
   * deliberately closed.
   */
  it("puts the width back when the drag is cancelled", () => {
    const { handle, root, onChange } = draw(252);

    dragTo(handle, 400);
    fireEvent.pointerCancel(handle, { pointerId: 1, clientX: 400 });

    expect(onChange).not.toHaveBeenCalled();
    expect(root.style.getPropertyValue("--objects-width")).toBe("252px");
  });

  it("does nothing on a second cancel", () => {
    const { handle, onChange } = draw(252);

    dragTo(handle, 400);
    fireEvent.pointerCancel(handle, { pointerId: 1, clientX: 400 });
    fireEvent.pointerCancel(handle, { pointerId: 1, clientX: 400 });

    expect(onChange).not.toHaveBeenCalled();
  });
});
