// @vitest-environment happy-dom

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { SavedView } from "../../api/connection";

import { SavedConnections } from "./SavedConnections";

afterEach(cleanup);

function saved(over: Partial<SavedView> = {}): SavedView {
  return {
    id: "one",
    name: "Reporting",
    params: null,
    options: null,
    host: "db.example.com",
    port: 5432,
    database: "reporting",
    user: "reader",
    sslMode: "prefer",
    rootCert: "",
    cert: "",
    key: "",
    environment: "",
    readOnly: false,
    archived: false,
    ...over,
  };
}

function draw(over: Partial<Parameters<typeof SavedConnections>[0]> = {}): {
  onConnect: ReturnType<typeof vi.fn>;
  onManage: ReturnType<typeof vi.fn>;
  onStop: ReturnType<typeof vi.fn>;
} {
  const handlers = { onConnect: vi.fn(), onManage: vi.fn(), onStop: vi.fn() };

  render(
    <SavedConnections
      connections={[saved()]}
      openIds={[]}
      connecting=""
      notice=""
      onNew={vi.fn()}
      {...handlers}
      {...over}
    />,
  );

  return handlers;
}

describe("the saved connections", () => {
  it("connects from the row", () => {
    const { onConnect, onManage } = draw();

    fireEvent.click(screen.getByRole("button", { name: /^Reporting/ }));

    expect(onConnect).toHaveBeenCalledWith(saved());
    expect(onManage).not.toHaveBeenCalled();
  });

  it("opens the form from the control beside the name", () => {
    const { onConnect, onManage } = draw();

    fireEvent.click(screen.getByRole("button", { name: "Manage Reporting" }));

    expect(onManage).toHaveBeenCalledWith(saved());
    expect(onConnect).not.toHaveBeenCalled();
  });

  /**
   * The row that is opening has a way out, and it is not decoration.
   *
   * A server can take as long as it likes to answer — a host behind a firewall
   * that drops rather than refuses runs to the operating system's timeout, and
   * reading a password can be a keychain dialog waiting for a person. During
   * all of it the list stops taking clicks, so without this the window has a
   * row saying "Connecting…" and nothing anybody can do about it.
   *
   * The form next door already learned this: its Stop button carries the
   * comment "Without this the window has a spinner and no way out."
   */
  it("offers a way to stop the one that is opening", () => {
    const { onStop } = draw({ connecting: "one" });

    fireEvent.click(screen.getByRole("button", { name: "Stop connecting to Reporting" }));

    expect(onStop).toHaveBeenCalled();
  });

  it("offers it only while something is opening", () => {
    draw();

    expect(screen.queryByRole("button", { name: /Stop connecting/ })).toBeNull();
  });

  it("says which row is the one that is opening", () => {
    draw({ connecting: "one" });

    expect(screen.getByText("Connecting…")).not.toBeNull();
  });
});
