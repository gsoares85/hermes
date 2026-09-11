// @vitest-environment happy-dom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { CancellablePromise, NodeRef, NodeView } from "../../api/tree";

import { ObjectTree } from "./ObjectTree";

const asked = vi.hoisted(() => vi.fn());

vi.mock("../../api/tree", async (real) => {
  const actual = await real<typeof import("../../api/tree")>();

  return {
    ...actual,
    children: (connectionId: string, node: NodeRef): CancellablePromise<NodeView[]> => {
      asked(connectionId, node.database, node.schema);

      return Object.assign(
        Promise.resolve([{ kind: "database", name: "shop", expandable: true }]),
        { cancel: (): Promise<void> => Promise.resolve() },
      ) as CancellablePromise<NodeView[]>;
    },
  };
});

afterEach(cleanup);
beforeEach(() => {
  asked.mockClear();
});

/**
 * Two connections, drawn the way the window draws them.
 *
 * What is counted is questions to the server, not rows on screen. The tree
 * virtualises from the first line and a virtualiser draws what fits in the
 * height of its scroller — outside a browser nothing has a height, so nothing
 * is drawn. The questions are the honest evidence either way: a tree that was
 * taken down and built again asks the root for a second time, and that is the
 * defect, not the redraw.
 */
function Harness(): React.JSX.Element {
  const [front, setFront] = useState("one");

  return (
    <>
      <button
        type="button"
        onClick={(): void => {
          setFront((held): string => (held === "one" ? "two" : "one"));
        }}
      >
        switch
      </button>

      {["one", "two"].map((id): React.JSX.Element => (
        <ObjectTree key={id} connectionId={id} showing={id === front} onSelect={vi.fn()} />
      ))}
    </>
  );
}

function trees(): HTMLElement[] {
  // Not by role: an element with the hidden attribute is out of the
  // accessibility tree, which is exactly what is wanted at runtime and makes
  // the hidden one unfindable here.
  return [...document.querySelectorAll<HTMLElement>("section.tree")];
}

describe("the object tree of a tab that is not in front", () => {
  it("is hidden rather than taken down", async () => {
    render(<Harness />);

    await waitFor((): void => {
      expect(asked).toHaveBeenCalledTimes(2);
    });

    const drawn = trees();

    expect(drawn).toHaveLength(2);
    expect(drawn[0]?.hasAttribute("hidden")).toBe(false);
    expect(drawn[1]?.hasAttribute("hidden")).toBe(true);
    // The one in front is the one a reader is offered.
    expect(screen.getAllByRole("region", { name: "Objects" })).toHaveLength(1);
  });

  it("keeps what it has read when another comes forward and back", async () => {
    render(<Harness />);

    await waitFor((): void => {
      expect(asked).toHaveBeenCalledTimes(2);
    });

    const before = asked.mock.calls.filter((call): boolean => call[0] === "one").length;

    fireEvent.click(screen.getByRole("button", { name: "switch" }));
    fireEvent.click(screen.getByRole("button", { name: "switch" }));

    // Nothing was asked again. A tree that is remounted cancels what is in
    // flight, empties every level it had read and asks for the root once more —
    // on every switch, which is the one interaction the tab bar exists for.
    await waitFor((): void => {
      expect(asked.mock.calls.filter((call): boolean => call[0] === "one")).toHaveLength(before);
    });

    expect(trees()[0]?.hasAttribute("hidden")).toBe(false);
  });
});
