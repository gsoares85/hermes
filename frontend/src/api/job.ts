import { Events } from "@wailsio/runtime";

import { JobService } from "../../bindings/github.com/gsoares85/hermes/internal/ui";
import type {
  JobProgressView,
  JobView,
} from "../../bindings/github.com/gsoares85/hermes/internal/ui/models";

export type { JobProgressView, JobView };

/**
 * What a job has said since the window was last told.
 *
 * Written out here rather than imported from the generated models, because the
 * generator only knows the signatures of methods and this type crosses as the
 * payload of an event. It is the one shape on this boundary that nothing
 * checks against Go, so it is kept to the two fields it has: a wider one would
 * be a wider thing to get quietly wrong.
 */
export interface JobLogView {
  id: string;
  lines: string[];
}

/**
 * The names the Go side pushes under. They are the contract between the two
 * halves, and a typo in one of them is a panel that silently never updates —
 * so they are written once, here, and nowhere else.
 */
const stateEvent = "job:state";
const progressEvent = "job:progress";
const logEvent = "job:log";

/** Every job the window knows about, running and finished. */
export async function jobs(): Promise<JobView[]> {
  return (await JobService.List()) ?? [];
}

/** Everything a job has said. */
export async function jobLog(id: string): Promise<string[]> {
  return (await JobService.Log(id)) ?? [];
}

/** Asks a job to stop. It returns when the job has been told, not when it has stopped. */
export async function cancelJob(id: string): Promise<void> {
  await JobService.Cancel(id);
}

/** Drops a job that has ended from the list. */
export async function forgetJob(id: string): Promise<void> {
  await JobService.Forget(id);
}

/**
 * Listens for what the Go side pushes, and answers a function that stops
 * listening.
 *
 * Three separate events rather than one with a kind, because the three carry
 * different things and the panel does different work with each: a state change
 * replaces a row, progress updates a bar, and a log arrives as the lines that
 * are new since the last telling.
 *
 * None of them is a source of truth on its own. That is `jobs()`, and the
 * panel asks it whenever it mounts and whenever the window comes back — the
 * reconciliation half of the decision, without which one missed event is a bar
 * stuck at forty per cent for a job that finished ten minutes ago.
 */
export function watchJobs(on: {
  state: (view: JobView) => void;
  progress: (view: JobView) => void;
  log: (view: JobLogView) => void;
}): () => void {
  const stop = [
    Events.On(stateEvent, (event): void => {
      on.state(event.data as JobView);
    }),
    Events.On(progressEvent, (event): void => {
      on.progress(event.data as JobView);
    }),
    Events.On(logEvent, (event): void => {
      on.log(event.data as JobLogView);
    }),
  ];

  return (): void => {
    for (const off of stop) {
      off();
    }
  };
}

/** Whether a job has reached an end it cannot leave. */
export function isOver(state: string): boolean {
  return state === "done" || state === "failed" || state === "cancelled";
}

/** Whether a job can still be asked to stop. */
export function isStoppable(state: string): boolean {
  return state === "pending" || state === "running";
}

/**
 * How long is left, in the words somebody reads.
 *
 * Nothing is said when nothing can be said. An estimate that is guessed rather
 * than derived is worse than no estimate, because a person plans around it.
 */
export function remaining(progress: JobProgressView): string {
  if (progress.indeterminate || progress.remainingMs <= 0) {
    return "";
  }

  return `${duration(progress.remainingMs)} left`;
}

/** A duration in the largest unit that still says something useful. */
export function duration(ms: number): string {
  const seconds = Math.round(ms / 1000);
  if (seconds < 60) {
    return `${String(seconds)}s`;
  }

  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return `${String(minutes)}m ${String(seconds % 60)}s`;
  }

  return `${String(Math.floor(minutes / 60))}h ${String(minutes % 60)}m`;
}

/**
 * The list in the order a person wants it: what is running first, and the rest
 * newest first.
 *
 * Decided here rather than in Go, because it is a question about what somebody
 * is looking for rather than about what the queue holds — and the queue
 * deliberately answers unordered so that this choice is made in one place.
 */
export function ordered(held: readonly JobView[]): JobView[] {
  return [...held].sort((left, right): number => {
    const leftOver = isOver(left.state);
    if (leftOver !== isOver(right.state)) {
      return leftOver ? 1 : -1;
    }

    return right.startedAt.localeCompare(left.startedAt);
  });
}

/** Replaces a job in a list, or adds it when the list has never seen it. */
export function merged(held: readonly JobView[], one: JobView): JobView[] {
  const at = held.findIndex((job): boolean => job.id === one.id);
  if (at < 0) {
    return [...held, one];
  }

  const next = [...held];
  next[at] = one;

  return next;
}

/**
 * Applies progress to the job it belongs to, keeping everything else.
 *
 * A progress event carries what moved and not the whole job, so it must not be
 * merged as if it were one: doing that blanks the title and the times, and the
 * row goes empty for a job that is simply advancing.
 */
export function advanced(held: readonly JobView[], one: JobView): JobView[] {
  return held.map((job): JobView =>
    job.id === one.id
      ? { ...job, state: one.state, progress: one.progress, dropped: one.dropped }
      : job,
  );
}
