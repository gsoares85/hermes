// @vitest-environment happy-dom

import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { StatusView } from "../../api/connection";

import { TabBar } from "./TabBar";
import { noTabs, opened, type Tabs } from "./tabs";

afterEach(cleanup);

function status(id: string, name: string): StatusView {
  return {
    id,
    savedId: `saved-${id}`,
    state: "open",
    diagnosis: { failed: false, class: "", summary: "", cause: "", nextStep: "", detail: "" },
    name,
    environment: "",
    readOnly: false,
    host: "db.example.com",
    port: 5432,
    database: "shop",
    user: "reader",
  };
}

const three: Tabs = ["one", "two", "three"].reduce(
  (held, id, at): Tabs => opened(held, status(id, ["First", "Second", "Third"][at] ?? "")),
  noTabs,
);

/** The strip, controlled the way the window controls it. */
function Harness({ onClose = vi.fn() }: { onClose?: () => void }): React.JSX.Element {
  const [tabs, setTabs] = useState<Tabs>({ ...three, activeId: "one" });

  return (
    <TabBar
      tabs={tabs}
      onPick={(id): void => {
        setTabs((held): Tabs => ({ ...held, activeId: id }));
      }}
      onClose={onClose}
    />
  );
}

describe("the tab bar", () => {
  it("gives the tablist the tabs themselves", () => {
    render(<Harness />);

    const strip = screen.getByRole("tablist");

    // A tab wrapped in a plain element is a tab the tablist does not own, and
    // a reader told this is a tablist stops being told "tab 2 of 3".
    expect(within(strip).getAllByRole("tab")).toHaveLength(3);
    for (const tab of within(strip).getAllByRole("tab")) {
      expect(tab.parentElement?.getAttribute("role")).toBe("presentation");
    }
  });

  it("makes only the tab in front a stop on the way through the window", () => {
    render(<Harness />);

    const tabs = screen.getAllByRole("tab");

    expect(tabs.map((tab): string | null => tab.getAttribute("tabindex"))).toEqual([
      "0",
      "-1",
      "-1",
    ]);
  });

  /**
   * In a roving tabindex, focus follows selection or the strip is broken.
   *
   * Moving the selection alone leaves the focus ring on the tab that was just
   * given tabIndex -1 while another tab is the selected one. The two are then
   * in different places, and the next Tab press leaves the strip from
   * somewhere nobody chose.
   */
  it("moves the focus with the selection", () => {
    render(<Harness />);

    const [first] = screen.getAllByRole("tab");
    first?.focus();

    fireEvent.keyDown(first as HTMLElement, { key: "ArrowRight" });

    const second = screen.getAllByRole("tab")[1];
    expect(second?.getAttribute("aria-selected")).toBe("true");
    expect(document.activeElement).toBe(second);
  });

  it("moves the focus to the end and back to the start", () => {
    render(<Harness />);

    const [first] = screen.getAllByRole("tab");
    first?.focus();

    fireEvent.keyDown(document.activeElement as HTMLElement, { key: "End" });
    expect(document.activeElement).toBe(screen.getAllByRole("tab")[2]);

    fireEvent.keyDown(document.activeElement as HTMLElement, { key: "Home" });
    expect(document.activeElement).toBe(screen.getAllByRole("tab")[0]);
  });

  // Clicking is not the keyboard, and taking the focus after a click would
  // move it for somebody who never asked for it to move.
  it("leaves the focus alone when a tab is clicked", () => {
    render(<Harness />);

    const outside = document.createElement("button");
    document.body.append(outside);
    outside.focus();

    fireEvent.click(screen.getAllByRole("tab")[1] as HTMLElement);

    expect(screen.getAllByRole("tab")[1]?.getAttribute("aria-selected")).toBe("true");
    expect(document.activeElement).toBe(outside);
    outside.remove();
  });

  /**
   * Home on the first tab, End on the last, either arrow with one tab open:
   * the key is handled and the selection does not move. Nothing should be left
   * armed by that, or the next selection change — a click, a connection
   * opening — takes the focus on behalf of a key pressed some time ago.
   */
  it("leaves nothing armed by a key that moves the selection nowhere", () => {
    render(<Harness />);

    const [first] = screen.getAllByRole("tab");
    first?.focus();

    fireEvent.keyDown(first as HTMLElement, { key: "Home" });

    const outside = document.createElement("button");
    document.body.append(outside);
    outside.focus();

    fireEvent.click(screen.getAllByRole("tab")[1] as HTMLElement);

    expect(screen.getAllByRole("tab")[1]?.getAttribute("aria-selected")).toBe("true");
    expect(document.activeElement).toBe(outside);
    outside.remove();
  });

  it("closes from the control beside the name", () => {
    const onClose = vi.fn();
    render(<Harness onClose={onClose} />);

    fireEvent.click(screen.getByRole("button", { name: "Close Second" }));

    expect(onClose).toHaveBeenCalledWith("two");
  });
});
