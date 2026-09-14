import { Events } from "@wailsio/runtime";

import { JobService } from "../../bindings/github.com/gsoares85/hermes/internal/ui";
import type {
  HistoryView,
  JobProgressView,
  JobView,
} from "../../bindings/github.com/gsoares85/hermes/internal/ui/models";

export type { HistoryView, JobProgressView, JobView };

/**
 * What a job has said: the whole of it, or what is new since the window was
 * last told.
 *
 * `seq` is how many lines the job has said in total up to the end of these,
 * counting from when it started and including the ones the log's ceiling has
 * dropped since. The whole log and the events that follow it are read from the
 * same buffer at different moments, each keeping its own place, so it is the
 * only thing that says how the two fit together — the lines cannot, because a
 * log repeats itself all the time.
 */
export interface JobLogView {
  id: string;
  lines: string[];
  seq: number;
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

/**
 * The jobs that ended before this one, a page at a time.
 *
 * `jobs()` answers what is running plus the most recent page of what has run,
 * which is the panel on opening. This is how somebody reaches further back
 * than that. An empty answer is the beginning of the history, not a failure.
 */
export async function olderJobs(after: JobView): Promise<JobView[]> {
  return (await JobService.Older({ endedAt: after.endedAt, id: after.id })) ?? [];
}

/**
 * Whether what runs in this session will still be there tomorrow.
 *
 * The warning is empty when the history is being written to its file. When it
 * is not — a file that could not be opened, a disk that filled — it says so in
 * words, and the panel puts them in front of the person: a history that
 * quietly stopped being kept is found out on the morning somebody looks for
 * the backup that ran overnight.
 */
export async function historyStatus(): Promise<HistoryView> {
  return await JobService.HistoryStatus();
}

/** Everything a job has said, and how far along its log that reaches. */
export async function jobLog(id: string): Promise<JobLogView> {
  const read = await JobService.Log(id);

  return { id, lines: read.lines ?? [], seq: read.seq };
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
 *
 * What arrives is whatever the framework was handed, so all three payloads are
 * checked before they are passed on rather than asserted into shape. Nothing
 * on this boundary is checked against Go at build time: the generator knows
 * the signatures of methods and says nothing about the payload of an event, so
 * a field renamed on one side is a panel that draws blanks on the other. An
 * event that does not look like what it claims to be is dropped, which is what
 * `jobs()` exists to correct.
 */
export function watchJobs(on: {
  state: (view: JobView) => void;
  progress: (view: JobView) => void;
  log: (view: JobLogView) => void;
}): () => void {
  const stop = [
    Events.On(stateEvent, (event): void => {
      const view = jobViewIn(event.data);
      if (view !== null) {
        on.state(view);
      }
    }),
    Events.On(progressEvent, (event): void => {
      const view = jobViewIn(event.data);
      if (view !== null) {
        on.progress(view);
      }
    }),
    Events.On(logEvent, (event): void => {
      const view = jobLogViewIn(event.data);
      if (view !== null) {
        on.log(view);
      }
    }),
  ];

  return (): void => {
    for (const off of stop) {
      off();
    }
  };
}

/**
 * The job in an event payload, or null when it does not carry one.
 *
 * An identifier and progress, and nothing beyond them: a progress event
 * carries where a job got to and nothing else about it, while a state event
 * carries the whole row, and what the caller does with either is merge it into
 * what it already has.
 *
 * Progress is required because the panel reads into it for every job that has
 * not ended — the bar asks whether it is indeterminate — so an event that
 * arrived without it would not draw a row with a missing bar, it would throw
 * while rendering and take the panel with it.
 */
function jobViewIn(data: unknown): JobView | null {
  if (typeof data !== "object" || data === null) {
    return null;
  }

  const { id, progress } = data as Record<string, unknown>;
  if (typeof id !== "string" || id === "") {
    return null;
  }

  return typeof progress === "object" && progress !== null ? (data as JobView) : null;
}

/** The lines in a log event, or null when it does not carry any. */
function jobLogViewIn(data: unknown): JobLogView | null {
  if (typeof data !== "object" || data === null) {
    return null;
  }

  const { id, lines, seq } = data as Record<string, unknown>;
  if (typeof id !== "string" || id === "" || !Array.isArray(lines) || typeof seq !== "number") {
    return null;
  }

  return lines.every((line): boolean => typeof line === "string")
    ? { id, lines: lines as string[], seq }
    : null;
}

/**
 * A log with what arrived after it put on the end, and nothing put on twice.
 *
 * The whole log and the events that follow it are two readings of one buffer,
 * taken at different moments and each keeping its own place: the whole log is
 * read when somebody opens it, and the events are sent from wherever the
 * window's sampling had got to — which is usually further back. So an event
 * ordinarily repeats lines that are already on screen, and only the count says
 * which ones.
 *
 * An event entirely behind what is held adds nothing. One that straddles the
 * end adds only its tail. One that begins after a gap — the ceiling took what
 * was in between — is added whole, because those lines are gone from the
 * buffer and the count of what was dropped is what says so.
 */
export function appended(
  held: readonly string[],
  seq: number,
  arrived: JobLogView,
): { lines: string[]; seq: number } {
  if (arrived.seq <= seq) {
    return { lines: [...held], seq };
  }

  const begins = arrived.seq - arrived.lines.length;
  const already = Math.max(0, Math.min(seq - begins, arrived.lines.length));

  return { lines: [...held, ...arrived.lines.slice(already)], seq: arrived.seq };
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
 *
 * What is running is ordered by when it began, and what has ended by when it
 * ended. They are different questions: of two jobs still going, the one that
 * started first has been going longest, and of two that are over, the one that
 * ended last is the one somebody just watched finish. Ordering the finished
 * half by its beginning also puts a job cancelled before it ever started at
 * the very bottom, because it has no beginning to compare at all.
 *
 * Every time crosses in UTC, so comparing them as text compares the instants.
 */
export function ordered(held: readonly JobView[]): JobView[] {
  return [...held].sort((left, right): number => {
    const leftOver = isOver(left.state);
    if (leftOver !== isOver(right.state)) {
      return leftOver ? 1 : -1;
    }

    return leftOver
      ? right.endedAt.localeCompare(left.endedAt)
      : right.startedAt.localeCompare(left.startedAt);
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
 *
 * The state is not among what it carries, and is not taken from here even if
 * it were. A sample is read a moment before it arrives, so one taken just
 * before a job ended lands after the state change that announced the end —
 * and taking it would put a Stop button back on a job that has finished.
 */
export function advanced(held: readonly JobView[], one: JobView): JobView[] {
  return held.map((job): JobView =>
    job.id === one.id ? { ...job, progress: one.progress, dropped: one.dropped } : job,
  );
}
