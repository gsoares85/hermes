// @vitest-environment happy-dom

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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

describe("asking what the server is", () => {
  /**
   * The question is abandoned with the tab that asked it.
   *
   * It reaches the server, and the binding is cancellable the whole way down.
   * Awaiting it throws the handle away, so closing the tab left the query
   * running on the Go side until its own timeout — the pattern every other
   * call in api/connection is deliberately written to avoid.
   */
  it("stops asking when the tab that asked is closed", async () => {
    const abandon = vi.fn(() => Promise.resolve());
    backend.serverVersion.mockReturnValue(
      Object.assign(new Promise<string>((): void => {}), { cancel: abandon }),
    );

    render(<App />);
    await connectTo("First");

    fireEvent.click(screen.getByRole("button", { name: "Close First" }));

    await waitFor((): void => {
      expect(abandon).toHaveBeenCalled();
    });
  });
});

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

/**
 * A connection opened before it was saved.
 *
 * The state that comes back from opening one carries no saved identifier —
 * there was nothing saved to point at. Saving it afterwards mints one, and if
 * the tab is not told, the row that has just appeared in the sidebar belongs to
 * a server that is already open and nothing knows it: clicking it opens a
 * second connection, a second pool and a second tab.
 */
describe("saving a connection that was opened first", () => {
  it("tells the tab which saved connection it came from", async () => {
    backend.saved.mockReturnValue(cancellable([]));
    backend.open.mockReturnValue(cancellable(status("c-new", "", "Ad hoc")));
    backend.save.mockReturnValue(cancellable(saved("s9", "Ad hoc")));

    render(<App />);

    // The toolbar's, not the one at the head of the sidebar list.
    fireEvent.click(
      within(await screen.findByRole("banner")).getByRole("button", { name: "New connection" }),
    );
    fireEvent.click(await screen.findByRole("button", { name: "Connect" }));
    await screen.findByRole("tab", { name: /Ad hoc/ });

    backend.saved.mockReturnValue(cancellable([saved("s9", "Ad hoc")]));
    fireEvent.click(screen.getByRole("button", { name: "Save connection" }));

    fireEvent.click(await screen.findByRole("button", { name: "Close" }));

    const row = await screen.findByRole("button", { name: /^Ad hoc/ });
    backend.open.mockClear();
    fireEvent.click(row);

    await waitFor((): void => {
      expect(screen.getAllByRole("tab", { name: /Ad hoc/ })).toHaveLength(1);
    });
    expect(backend.open).not.toHaveBeenCalled();
  });
});

/**
 * The password does not outlive the dialog it was typed into.
 *
 * The form was a child of a <dialog> that is always rendered, so closing the
 * dialog took it off the screen without unmounting it: what was typed stayed
 * in React's state and in the value of the input, and only a later reopen —
 * which remounts the form — cleared it. Someone who types a password, fails to
 * connect and closes the dialog left the secret in the window for the rest of
 * the session.
 */
describe("the connection dialog", () => {
  it("forgets a typed password when it closes", async () => {
    render(<App />);

    fireEvent.click(
      within(await screen.findByRole("banner")).getByRole("button", { name: "New connection" }),
    );

    const field = await screen.findByLabelText("Password");
    fireEvent.change(field, { target: { value: "s3cr3t" } });
    expect((field as HTMLInputElement).value).toBe("s3cr3t");

    fireEvent.click(screen.getByRole("button", { name: "Close" }));

    expect(screen.queryByLabelText("Password")).toBeNull();
    expect(document.body.innerHTML).not.toContain("s3cr3t");
  });
});
