// @vitest-environment happy-dom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { SavedView, StatusView } from "../../api/connection";

import { ConnectionForm } from "./ConnectionForm";

/**
 * The form, over a backend that is a set of functions.
 *
 * Mocked at the bindings rather than at `api/connection`, for the reason the
 * window's own test gives: what the form does with an answer is the part worth
 * testing, and mocking `api/` would replace exactly that.
 */
const backend = vi.hoisted(() => ({
  sslModes: vi.fn(),
  environments: vi.fn(),
  vault: vi.fn(),
  save: vi.fn(),
  open: vi.fn(),
  databases: vi.fn(),
}));

/** A promise the Wails bindings' shape: awaitable, and abandonable. */
function cancellable<T>(value: T | Promise<T>): Promise<T> & { cancel: () => Promise<void> } {
  return Object.assign(Promise.resolve(value), {
    cancel: (): Promise<void> => Promise.resolve(),
  });
}

vi.mock("../../../bindings/github.com/gsoares85/hermes/internal/ui", () => ({
  ConnectionService: {
    SSLModes: (): unknown => backend.sslModes(),
    Environments: (): unknown => backend.environments(),
    VaultStatus: (): unknown => backend.vault(),
    Save: (...args: unknown[]): unknown => backend.save(...args),
    Open: (...args: unknown[]): unknown => backend.open(...args),
    Databases: (...args: unknown[]): unknown => backend.databases(...args),
  },
}));

function saved(id: string, name: string): SavedView {
  return {
    id,
    name,
    params: null,
    options: null,
    host: `${name.toLowerCase()}.example.com`,
    port: 5432,
    database: "shop",
    user: "reader",
    sslMode: "prefer",
    rootCert: "",
    cert: "",
    key: "",
    environment: "",
    readOnly: false,
    archived: false,
  };
}

function status(id: string, savedId: string, name: string): StatusView {
  return {
    id,
    savedId,
    state: "open",
    diagnosis: { failed: false, class: "", summary: "", cause: "", nextStep: "", detail: "" },
    name,
    environment: "",
    readOnly: false,
    host: `${name.toLowerCase()}.example.com`,
    port: 5432,
    database: "shop",
    user: "reader",
  };
}

afterEach(cleanup);

beforeEach(() => {
  vi.clearAllMocks();

  backend.sslModes.mockReturnValue(cancellable(["prefer"]));
  backend.environments.mockReturnValue(cancellable([]));
  backend.vault.mockReturnValue(cancellable({ available: true, backend: "test", detail: "" }));
  backend.databases.mockReturnValue(cancellable([]));
});

describe("saving a connection", () => {
  /**
   * The connection that comes in is the one the window has in front, which is
   * not necessarily the one this dialog is about.
   *
   * Saving used to hand the stored identifier to whatever was in `connection`
   * whenever the two identifiers differed — so editing Second while First was
   * the open tab wrote Second's saved identifier onto First's connection. The
   * sidebar row for Second then pointed at a connection to First, and clicking
   * it came back to the wrong server rather than opening the right one.
   */
  it("leaves a connection this form did not open alone", async () => {
    const onOpened = vi.fn();
    backend.save.mockReturnValue(cancellable(saved("saved-b", "Second")));

    render(
      <ConnectionForm
        editing={saved("saved-b", "Second")}
        connection={status("conn-a", "saved-a", "First")}
        onOpened={onOpened}
        onChanged={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await screen.findByText("Saved “Second”.");
    expect(onOpened).not.toHaveBeenCalled();
  });

  /**
   * The half that has to keep working.
   *
   * A connection opened before it was saved comes back with no saved
   * identifier, because there was nothing saved to point at. Saving is what
   * creates one, and the tab has to be told — without it the row that has just
   * appeared in the sidebar belongs to a server that is already open and
   * nothing knows it, so clicking it opens a second connection.
   */
  it("links the connection it opened itself once there is a row to link it to", async () => {
    const onOpened = vi.fn();
    backend.open.mockReturnValue(cancellable(status("conn-new", "", "First")));
    backend.save.mockReturnValue(cancellable(saved("saved-new", "First")));

    render(
      <ConnectionForm editing={null} connection={null} onOpened={onOpened} onChanged={vi.fn()} />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Connect" }));
    await waitFor((): void => {
      expect(onOpened).toHaveBeenCalledWith(status("conn-new", "", "First"));
    });

    fireEvent.click(screen.getByRole("button", { name: "Save connection" }));

    await waitFor((): void => {
      expect(onOpened).toHaveBeenLastCalledWith({
        ...status("conn-new", "", "First"),
        savedId: "saved-new",
      });
    });
  });
});
