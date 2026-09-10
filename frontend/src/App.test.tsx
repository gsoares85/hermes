// @vitest-environment happy-dom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { SavedView, StatusView } from "./api/connection";

import { App } from "./App";

/**
 * The window, over a backend that is a set of functions.
 *
 * One mock at the boundary rather than one per module of `api/`: everything
 * between the two — how a form becomes a request, which promise is held on to,
 * what is done with the answer — is the part that has been getting this wrong,
 * and mocking `api/` would replace exactly that.
 */
const backend = vi.hoisted(() => ({
  saved: vi.fn(),
  open: vi.fn(),
  close: vi.fn(),
  serverVersion: vi.fn(),
  children: vi.fn(),
  sslModes: vi.fn(),
  environments: vi.fn(),
  appInfo: vi.fn(),
  save: vi.fn(),
  remove: vi.fn(),
  test: vi.fn(),
  databases: vi.fn(),
  vault: vi.fn(),
}));

/** A promise the Wails bindings' shape: awaitable, and abandonable. */
function cancellable<T>(value: T | Promise<T>): Promise<T> & { cancel: () => Promise<void> } {
  return Object.assign(Promise.resolve(value), {
    cancel: (): Promise<void> => Promise.resolve(),
  });
}

vi.mock("../bindings/github.com/gsoares85/hermes/internal/ui", () => ({
  AppInfoService: { Get: (): unknown => backend.appInfo() },
  CatalogService: { Children: (...args: unknown[]): unknown => backend.children(...args) },
  ConnectionService: {
    List: (): unknown => backend.saved(),
    Save: (...args: unknown[]): unknown => backend.save(...args),
    Delete: (...args: unknown[]): unknown => backend.remove(...args),
    Test: (...args: unknown[]): unknown => backend.test(...args),
    Databases: (...args: unknown[]): unknown => backend.databases(...args),
    VaultStatus: (): unknown => backend.vault(),
    Open: (...args: unknown[]): unknown => backend.open(...args),
    Close: (...args: unknown[]): unknown => backend.close(...args),
    ServerVersion: (...args: unknown[]): unknown => backend.serverVersion(...args),
    SSLModes: (): unknown => backend.sslModes(),
    Environments: (): unknown => backend.environments(),
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

  backend.appInfo.mockReturnValue(
    cancellable({ version: "0.0.0", commit: "abc", date: "today", platform: "test" }),
  );
  backend.saved.mockReturnValue(cancellable([saved("s1", "First"), saved("s2", "Second")]));
  backend.children.mockReturnValue(cancellable([]));
  backend.serverVersion.mockReturnValue(cancellable("17.2"));
  backend.close.mockReturnValue(cancellable(undefined));
  backend.sslModes.mockReturnValue(cancellable(["prefer"]));
  backend.environments.mockReturnValue(cancellable([]));
  backend.databases.mockReturnValue(cancellable([]));
  backend.vault.mockReturnValue(cancellable({ available: true, backend: "test", detail: "" }));
  backend.open.mockImplementation((form: { id: string }) =>
    cancellable(status(`c-${form.id}`, form.id, form.id === "s1" ? "First" : "Second")),
  );
});

async function connectTo(name: string): Promise<void> {
  fireEvent.click(await screen.findByRole("button", { name: new RegExp(`^${name}`) }));
  await screen.findByRole("tab", { name: new RegExp(name) });
}

describe("closing a tab", () => {
  /**
   * The tab goes first, and the connection is released behind it.
   *
   * Close is not cancellable and closes the pools, which blocks until the
   * connections in use come back. Waiting for it before taking the tab away
   * makes the × do nothing visible for as long as a listing of a large schema
   * takes to finish — on the interaction people do fastest.
   */
  it("takes the tab away before the connection is released", async () => {
    let release = (): void => {};
    backend.close.mockReturnValue(
      Object.assign(
        new Promise<void>((resolve): void => {
          release = resolve;
        }),
        { cancel: (): Promise<void> => Promise.resolve() },
      ),
    );

    render(<App />);
    await connectTo("First");

    fireEvent.click(screen.getByRole("button", { name: "Close First" }));

    await waitFor((): void => {
      expect(screen.queryByRole("tab", { name: /First/ })).toBeNull();
    });

    expect(backend.close).toHaveBeenCalledWith("c-s1");
    release();
  });
});

describe("opening a server that already has a tab", () => {
  it("keeps one tab, and releases the connection that lost it", async () => {
    render(<App />);
    await connectTo("First");

    // What the dialog does: the same saved connection, opened again, coming
    // back as a connection with an identifier of its own.
    backend.open.mockReturnValueOnce(cancellable(status("c-s1-again", "s1", "First")));

    fireEvent.click(await screen.findByRole("button", { name: "Manage First" }));
    fireEvent.click(await screen.findByRole("button", { name: "Connect" }));

    await waitFor((): void => {
      expect(backend.close).toHaveBeenCalledWith("c-s1");
    });

    expect(screen.getAllByRole("tab", { name: /First/ })).toHaveLength(1);
  });
});
