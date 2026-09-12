// @vitest-environment happy-dom

import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { JobView } from "../../api/job";

import { JobsPanel } from "./JobsPanel";

/**
 * The panel, over a backend that is a set of functions.
 *
 * Mocked at the bindings and at the event runtime, because what is being
 * tested is exactly what the panel does with what arrives: which route wins,
 * and what corrects what.
 */
const backend = vi.hoisted(() => ({
  list: vi.fn(),
  log: vi.fn(),
  cancel: vi.fn(),
  forget: vi.fn(),
}));

const listeners = vi.hoisted(() => new Map<string, (event: { data: unknown }) => void>());

function cancellable<T>(value: T | Promise<T>): Promise<T> & { cancel: () => Promise<void> } {
  return Object.assign(Promise.resolve(value), {
    cancel: (): Promise<void> => Promise.resolve(),
  });
}

vi.mock("../../../bindings/github.com/gsoares85/hermes/internal/ui", () => ({
  JobService: {
    List: (): unknown => backend.list(),
    Log: (...args: unknown[]): unknown => backend.log(...args),
    Cancel: (...args: unknown[]): unknown => backend.cancel(...args),
    Forget: (...args: unknown[]): unknown => backend.forget(...args),
  },
}));

vi.mock("@wailsio/runtime", () => ({
  Events: {
    On: (name: string, callback: (event: { data: unknown }) => void): (() => void) => {
      listeners.set(name, callback);

      return (): void => {
        listeners.delete(name);
      };
    },
  },
}));

/** Pushes an event the way the Go side does. */
function push(name: string, data: unknown): void {
  listeners.get(name)?.({ data });
}

function job(id: string, fields: Partial<JobView> = {}): JobView {
  return {
    id,
    kind: "backup",
    title: `shop ${id}`,
    state: "running",
    error: "",
    progress: {
      step: "",
      unit: "",
      done: 0,
      total: 0,
      fraction: 0,
      indeterminate: true,
      elapsedMs: 0,
      remainingMs: 0,
    },
    dropped: 0,
    startedAt: "2026-09-11T10:00:00Z",
    endedAt: "",
    ...fields,
  };
}

afterEach(cleanup);

beforeEach(() => {
  vi.clearAllMocks();
  listeners.clear();

  backend.list.mockReturnValue(cancellable([]));
  backend.log.mockReturnValue(cancellable([]));
  backend.cancel.mockReturnValue(cancellable(undefined));
  backend.forget.mockReturnValue(cancellable(undefined));
});

describe("the jobs panel", () => {
  it("says what it is for when nothing is running", async () => {
    render(<JobsPanel onClose={vi.fn()} />);

    expect(await screen.findByText(/Nothing is running/)).toBeTruthy();
  });

  it("asks for the whole list when it opens", async () => {
    backend.list.mockReturnValue(cancellable([job("one")]));

    render(<JobsPanel onClose={vi.fn()} />);

    expect(await screen.findByText("shop one")).toBeTruthy();
  });

  it("adds a job it hears about without having asked", async () => {
    render(<JobsPanel onClose={vi.fn()} />);
    await screen.findByText(/Nothing is running/);

    push("job:state", job("new"));

    expect(await screen.findByText("shop new")).toBeTruthy();
  });

  /**
   * The half of ADR-0015 that only exists because events can be lost.
   *
   * An event is fire and forget. A window that was minimised, or whose panel
   * had not mounted, never hears one — and without a full read to fall back
   * on, the first missed event leaves a bar at forty per cent for a job that
   * finished ten minutes ago, with nothing in the application ever correcting
   * it.
   */
  it("is corrected by the next full read when an event is lost", async () => {
    backend.list.mockReturnValue(cancellable([job("one", { state: "running" })]));

    render(<JobsPanel onClose={vi.fn()} />);
    await screen.findByText("shop one");
    expect(screen.getByText("running")).toBeTruthy();

    // The job finished and the window never heard: no event is pushed here on
    // purpose. What the Go side holds has moved on.
    backend.list.mockReturnValue(
      cancellable([job("one", { state: "done", endedAt: "2026-09-11T10:05:00Z" })]),
    );

    // The window comes back, which is exactly when a missed event matters:
    // it is when the panel was not being drawn.
    fireEvent(window, new Event("focus"));

    await waitFor((): void => {
      expect(screen.getByText("done")).toBeTruthy();
    });
  });

  // A progress event carries what moved, not the whole job. Applied as if it
  // were a whole job, the row goes blank at the moment the work is busiest.
  it("keeps the rest of the row when only progress arrives", async () => {
    backend.list.mockReturnValue(cancellable([job("one", { title: "shop on db.example.com" })]));

    render(<JobsPanel onClose={vi.fn()} />);
    await screen.findByText("shop on db.example.com");

    push("job:progress", {
      ...job("one", { title: "", startedAt: "" }),
      progress: {
        step: "copying public.orders",
        unit: "rows",
        done: 5,
        total: 10,
        fraction: 0.5,
        indeterminate: false,
        elapsedMs: 1000,
        remainingMs: 1000,
      },
    });

    expect(await screen.findByText("copying public.orders", { exact: false })).toBeTruthy();
    expect(screen.getByText("shop on db.example.com")).toBeTruthy();
  });

  it("offers Stop only while there is something to stop", async () => {
    backend.list.mockReturnValue(cancellable([job("running"), job("finished", { state: "done" })]));

    render(<JobsPanel onClose={vi.fn()} />);
    await screen.findByText("shop running");

    expect(screen.queryByRole("button", { name: "Stop shop running" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Stop shop finished" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Forget shop finished" })).toBeTruthy();
  });

  it("asks the Go side to stop a job", async () => {
    backend.list.mockReturnValue(cancellable([job("one")]));

    render(<JobsPanel onClose={vi.fn()} />);
    await screen.findByText("shop one");

    fireEvent.click(screen.getByRole("button", { name: "Stop shop one" }));

    await waitFor((): void => {
      expect(backend.cancel).toHaveBeenCalledWith("one");
    });
  });

  it("reads the whole log when one is opened, and appends what arrives after", async () => {
    backend.list.mockReturnValue(cancellable([job("one")]));
    backend.log.mockReturnValue(cancellable(["pg_dump: dumping public.orders"]));

    render(<JobsPanel onClose={vi.fn()} />);
    await screen.findByText("shop one");

    fireEvent.click(screen.getByRole("button", { name: "Log of shop one" }));

    expect(await screen.findByText(/dumping public.orders/)).toBeTruthy();

    push("job:log", { id: "one", lines: ["pg_dump: dumping public.customers"] });

    expect(await screen.findByText(/dumping public.customers/)).toBeTruthy();
  });

  /**
   * Two logs asked for in quick succession are two answers in flight, and the
   * slower one can land after the faster. Written into whatever is open at the
   * time, the log of the job somebody left shows under the name of the job
   * they went to.
   */
  it("does not write the log of one job under the name of another", async () => {
    backend.list.mockReturnValue(cancellable([job("one"), job("two")]));

    let answerForOne = (): void => undefined;
    backend.log.mockImplementation((id: string) => {
      if (id === "one") {
        return cancellable(
          new Promise<string[]>((resolve): void => {
            answerForOne = (): void => {
              resolve(["the log of the first job"]);
            };
          }),
        );
      }

      return cancellable(["the log of the second job"]);
    });

    render(<JobsPanel onClose={vi.fn()} />);
    await screen.findByText("shop one");

    fireEvent.click(screen.getByRole("button", { name: "Log of shop one" }));
    fireEvent.click(screen.getByRole("button", { name: "Log of shop two" }));

    expect(await screen.findByText(/the log of the second job/)).toBeTruthy();

    // And now the first one answers, too late. Flushed inside act, so that
    // what it does to the panel has happened by the time it is asked about:
    // asserting on an update that has not been applied yet passes whatever
    // the update was.
    await act(async (): Promise<void> => {
      answerForOne();
      await Promise.resolve();
    });

    expect(screen.queryByText(/the log of the first job/)).toBeNull();
    expect(screen.queryByText(/the log of the second job/)).toBeTruthy();
  });

  // A log truncated in silence is read as the beginning of the operation.
  it("says when the log dropped its beginning", async () => {
    backend.list.mockReturnValue(cancellable([job("one", { dropped: 42 })]));
    backend.log.mockReturnValue(cancellable(["the line that survived"]));

    render(<JobsPanel onClose={vi.fn()} />);
    await screen.findByText("shop one");

    fireEvent.click(screen.getByRole("button", { name: "Log of shop one" }));

    expect(await screen.findByText(/42 earlier lines were dropped/)).toBeTruthy();
  });

  // A job that cannot say how far along it is draws a bar that moves rather
  // than one that fills. Nought per cent would claim it has failed to advance.
  it("draws a bar with no value when there is nothing to know", async () => {
    backend.list.mockReturnValue(cancellable([job("one")]));

    render(<JobsPanel onClose={vi.fn()} />);
    await screen.findByText("shop one");

    const bar = screen.getByRole("progressbar", { name: "Progress of shop one" });
    expect(bar.getAttribute("aria-valuenow")).toBeNull();
  });

  it("draws the value when there is one", async () => {
    backend.list.mockReturnValue(
      cancellable([
        job("one", {
          progress: {
            step: "copying",
            unit: "rows",
            done: 1,
            total: 4,
            fraction: 0.25,
            indeterminate: false,
            elapsedMs: 1000,
            remainingMs: 3000,
          },
        }),
      ]),
    );

    render(<JobsPanel onClose={vi.fn()} />);
    await screen.findByText("shop one");

    const bar = screen.getByRole("progressbar", { name: "Progress of shop one" });
    expect(bar.getAttribute("aria-valuenow")).toBe("25");
    expect(screen.getByText(/3s left/)).toBeTruthy();
  });

  it("takes a forgotten job off the list", async () => {
    backend.list.mockReturnValue(cancellable([job("one", { state: "done" })]));

    render(<JobsPanel onClose={vi.fn()} />);
    await screen.findByText("shop one");

    fireEvent.click(screen.getByRole("button", { name: "Forget shop one" }));

    await waitFor((): void => {
      expect(screen.queryByText("shop one")).toBeNull();
    });
  });

  it("reports what the Go side refused", async () => {
    backend.list.mockReturnValue(cancellable([job("one")]));
    // Built when it is called, not when it is arranged: a rejected promise
    // created at setup has nobody watching it until the click, and Node
    // reports that as an unhandled rejection.
    backend.cancel.mockImplementation(() =>
      Object.assign(Promise.reject(new Error("no such job")), {
        cancel: (): Promise<void> => Promise.resolve(),
      }),
    );

    render(<JobsPanel onClose={vi.fn()} />);
    await screen.findByText("shop one");

    fireEvent.click(screen.getByRole("button", { name: "Stop shop one" }));

    expect(await screen.findByText(/no such job/)).toBeTruthy();
  });

  it("stops listening when it goes away", async () => {
    render(<JobsPanel onClose={vi.fn()} />);
    await screen.findByText(/Nothing is running/);

    expect(listeners.size).toBe(3);

    cleanup();

    expect(listeners.size).toBe(0);
  });

  it("closes from the corner", async () => {
    const onClose = vi.fn();
    render(<JobsPanel onClose={onClose} />);
    await screen.findByText(/Nothing is running/);

    fireEvent.click(
      within(screen.getByRole("region", { name: "Jobs" })).getByRole("button", {
        name: "Close jobs",
      }),
    );

    expect(onClose).toHaveBeenCalled();
  });
});
