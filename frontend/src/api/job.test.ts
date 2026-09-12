import { describe, expect, it } from "vitest";

import {
  advanced,
  duration,
  isOver,
  isStoppable,
  merged,
  ordered,
  remaining,
  type JobProgressView,
  type JobView,
} from "./job";

function progress(fields: Partial<JobProgressView> = {}): JobProgressView {
  return {
    step: "",
    unit: "",
    done: 0,
    total: 0,
    fraction: 0,
    indeterminate: true,
    elapsedMs: 0,
    remainingMs: 0,
    ...fields,
  };
}

function view(id: string, fields: Partial<JobView> = {}): JobView {
  return {
    id,
    kind: "backup",
    title: `job ${id}`,
    state: "running",
    error: "",
    progress: progress(),
    dropped: 0,
    startedAt: "2026-09-11T10:00:00Z",
    endedAt: "",
    ...fields,
  };
}

describe("whether a job is over", () => {
  it("names the three ends and nothing else", () => {
    expect(["done", "failed", "cancelled"].map(isOver)).toEqual([true, true, true]);
    expect(["pending", "running", "cancelling"].map(isOver)).toEqual([false, false, false]);
  });

  // Cancelling is already stopping: a second press has nothing to ask for, and
  // a button that stays live says otherwise.
  it("can be asked to stop only while it has not been", () => {
    expect(["pending", "running"].map(isStoppable)).toEqual([true, true]);
    expect(["cancelling", "done", "failed", "cancelled"].map(isStoppable)).toEqual([
      false,
      false,
      false,
      false,
    ]);
  });
});

describe("how long is left", () => {
  it("says nothing when nothing can be said", () => {
    expect(remaining(progress({ indeterminate: true, remainingMs: 5000 }))).toBe("");
    expect(remaining(progress({ indeterminate: false, remainingMs: 0 }))).toBe("");
  });

  it("says it in the largest unit that still means something", () => {
    expect(duration(4_000)).toBe("4s");
    expect(duration(95_000)).toBe("1m 35s");
    expect(duration(3_900_000)).toBe("1h 5m");
  });

  it("reads as time left rather than as a number", () => {
    expect(remaining(progress({ indeterminate: false, remainingMs: 45_000 }))).toBe("45s left");
  });
});

describe("the order the panel draws them in", () => {
  it("puts what is running above what has finished", () => {
    const held = [
      view("finished", { state: "done", endedAt: "2026-09-11T10:05:00Z" }),
      view("running"),
    ];

    expect(ordered(held).map((job): string => job.id)).toEqual(["running", "finished"]);
  });

  it("puts the newest first among those that finished", () => {
    const held = [
      view("older", { state: "done", endedAt: "2026-09-11T09:00:00Z" }),
      view("newer", { state: "done", endedAt: "2026-09-11T10:00:00Z" }),
    ];

    expect(ordered(held).map((job): string => job.id)).toEqual(["newer", "older"]);
  });

  /**
   * Of two jobs that are over, the one somebody just watched finish is the one
   * that ended last — whichever of them was started first. A long backup begun
   * in the morning and a short one begun at noon can end in either order.
   */
  it("orders what has finished by when it ended, not by when it began", () => {
    const held = [
      view("started first, ended last", {
        state: "done",
        startedAt: "2026-09-11T09:00:00Z",
        endedAt: "2026-09-11T12:00:00Z",
      }),
      view("started last, ended first", {
        state: "done",
        startedAt: "2026-09-11T11:00:00Z",
        endedAt: "2026-09-11T11:05:00Z",
      }),
    ];

    expect(ordered(held).map((job): string => job.id)).toEqual([
      "started first, ended last",
      "started last, ended first",
    ]);
  });

  /**
   * A job cancelled before it ever started has no beginning at all, so
   * ordering the finished half by one would send it to the bottom of the list
   * whatever time it was actually called off at.
   */
  it("does not banish a job that never started", () => {
    const held = [
      view("ran and finished", {
        state: "done",
        startedAt: "2026-09-11T09:00:00Z",
        endedAt: "2026-09-11T09:30:00Z",
      }),
      view("called off before it began", {
        state: "cancelled",
        startedAt: "",
        endedAt: "2026-09-11T10:00:00Z",
      }),
    ];

    expect(ordered(held)[0]?.id).toBe("called off before it began");
  });

  it("leaves the list it was given alone", () => {
    const held = [view("b"), view("a")];
    ordered(held);

    expect(held.map((job): string => job.id)).toEqual(["b", "a"]);
  });
});

describe("what arrives from the Go side", () => {
  it("replaces a job the panel already has", () => {
    const held = [view("one"), view("two")];
    const next = merged(held, view("two", { state: "done" }));

    expect(next).toHaveLength(2);
    expect(next[1]?.state).toBe("done");
  });

  it("adds a job the panel has never seen", () => {
    expect(merged([view("one")], view("two"))).toHaveLength(2);
  });

  /**
   * A progress event carries what moved, not the whole job.
   *
   * Merging it as if it were a whole job blanks the title and the times, and
   * the row goes empty for a job that is simply advancing — which looks like
   * the job disappearing at the exact moment it is working hardest.
   */
  it("applies progress without blanking the rest of the row", () => {
    const held = [view("one", { title: "shop on db.example.com" })];

    const next = advanced(held, {
      ...view("one", { title: "", startedAt: "" }),
      progress: progress({ indeterminate: false, fraction: 0.5, done: 5, total: 10 }),
    });

    expect(next[0]?.title).toBe("shop on db.example.com");
    expect(next[0]?.startedAt).toBe("2026-09-11T10:00:00Z");
    expect(next[0]?.progress.fraction).toBe(0.5);
  });

  it("says nothing about a job it does not have", () => {
    const held = [view("one")];

    expect(advanced(held, view("other"))).toEqual(held);
  });

  /**
   * A sample is read a moment before it arrives, so one taken just before a
   * job ended lands after the state change that announced the end. Taking the
   * state from it would put a Stop button back on a job that has finished,
   * until the next sample took it away again.
   */
  it("leaves the state to the event that announces it", () => {
    const held = [view("one", { state: "done" })];

    const next = advanced(held, { ...view("one", { state: "running" }), progress: progress() });

    expect(next[0]?.state).toBe("done");
  });
});
