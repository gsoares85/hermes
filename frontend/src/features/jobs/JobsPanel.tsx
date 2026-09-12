import { useCallback, useEffect, useRef, useState } from "react";

import {
  advanced,
  cancelJob,
  forgetJob,
  isOver,
  isStoppable,
  jobLog,
  jobs as listJobs,
  merged,
  ordered,
  remaining,
  watchJobs,
  type JobView,
} from "../../api/job";
import { Icon } from "../../ui/Icon";

/**
 * Everything the window has running, and what it has run.
 *
 * The panel holds two things that arrive by different routes and must not be
 * confused. Events are the fast path: they move a bar without a round trip.
 * The full listing is the truth: it is read when the panel mounts and whenever
 * the window comes back, and it is what corrects a window that missed an
 * event. Without the second, one lost event is a bar stuck at forty per cent
 * for a job that finished ten minutes ago, and nothing in the application ever
 * puts it right. (ADR-0015)
 */
export function JobsPanel({ onClose }: { onClose: () => void }): React.JSX.Element {
  const [held, setHeld] = useState<JobView[]>([]);
  const [opened, setOpened] = useState<string>("");
  // Which job's log is on screen, readable from inside the subscription below
  // without resubscribing every time somebody opens a different one. A ref
  // rather than the state itself: resubscribing would drop events in the gap.
  const openedRef = useRef("");
  const [log, setLog] = useState<readonly string[]>([]);
  const [notice, setNotice] = useState("");

  // The reconciliation. Declared above the effects that call it, for the
  // reason the window's own reload is: a function declaration hoists, so
  // written underneath, nothing would show that the effect captures the copy
  // from the first render.
  const reconcile = useCallback((): void => {
    listJobs()
      .then((truth): void => {
        setHeld(truth);
      })
      .catch((err: unknown): void => {
        // Running outside the desktop shell, or the Go side is gone. An empty
        // panel that says why beats a panel that looks like nothing is
        // running.
        setNotice(String(err));
      });
  }, []);

  useEffect((): void => {
    reconcile();
  }, [reconcile]);

  // The window coming back is the moment a missed event matters most: it is
  // exactly when the panel was not being drawn and the events went nowhere.
  useEffect((): (() => void) => {
    window.addEventListener("focus", reconcile);

    return (): void => {
      window.removeEventListener("focus", reconcile);
    };
  }, [reconcile]);

  useEffect((): (() => void) => {
    return watchJobs({
      state: (view): void => {
        setHeld((current): JobView[] => merged(current, view));
      },
      // Progress carries what moved and not the whole job. Merging it as a
      // whole job would blank the title and the times, and the row would go
      // empty for a job that is simply advancing.
      progress: (view): void => {
        setHeld((current): JobView[] => advanced(current, view));
      },
      log: (view): void => {
        setLog((current): readonly string[] =>
          view.id === openedRef.current ? [...current, ...view.lines] : current,
        );
      },
    });
  }, []);

  function open(id: string): void {
    const next = id === opened ? "" : id;
    setOpened(next);
    openedRef.current = next;
    setLog([]);

    if (next === "") {
      return;
    }

    jobLog(next)
      .then((lines): void => {
        // Somebody opened another job while this was being asked for, or
        // closed this one. Two answers are in flight and the older of them
        // must not land in the newer one's place.
        if (openedRef.current !== next) {
          return;
        }

        // What arrived by event while the asking was in flight is kept: the
        // whole log answers what was there when it was asked, and the lines
        // that came after it have already been counted as sent.
        setLog((current): readonly string[] => [...lines, ...current]);
      })
      .catch((): void => {
        // The job was forgotten while the log was being asked for. An empty
        // log is the truth about a job that is gone.
      });
  }

  function stop(id: string): void {
    cancelJob(id).catch((err: unknown): void => {
      setNotice(String(err));
    });
  }

  function forget(id: string): void {
    forgetJob(id)
      .then((): void => {
        setHeld((current): JobView[] => current.filter((job): boolean => job.id !== id));
        if (id === opened) {
          setOpened("");
          setLog([]);
        }
      })
      .catch((err: unknown): void => {
        setNotice(String(err));
      });
  }

  // Kept in step for anything that changes what is open without going through
  // open() — closing the panel on a job that was forgotten, for one. open()
  // writes it itself, because the answer it is waiting for has to be checked
  // against what is open now rather than against what it was a render ago.
  useEffect((): void => {
    openedRef.current = opened;
  }, [opened]);

  const shown = ordered(held);

  return (
    <section className="jobs" aria-label="Jobs">
      <header className="jobs__head">
        <h2 className="jobs__title">Jobs</h2>
        <button type="button" className="jobs__close" aria-label="Close jobs" onClick={onClose}>
          <Icon name="close" />
        </button>
      </header>

      {notice !== "" && <p className="jobs__notice">{notice}</p>}

      {shown.length === 0 ? (
        <p className="jobs__empty">
          Nothing is running. Backups, restores and transfers appear here while they work, and stay
          afterwards so you can read what they said.
        </p>
      ) : (
        <ul className="jobs__list">
          {shown.map((job): React.JSX.Element => (
            <li key={job.id} className={`jobs__job jobs__job--${job.state}`}>
              <div className="jobs__row">
                <span className="jobs__kind">{job.kind}</span>
                <span className="jobs__name">{job.title}</span>
                <span className="jobs__state">{job.state}</span>

                {isStoppable(job.state) && (
                  <button
                    type="button"
                    aria-label={`Stop ${job.title}`}
                    onClick={(): void => {
                      stop(job.id);
                    }}
                  >
                    Stop
                  </button>
                )}

                {isOver(job.state) && (
                  <button
                    type="button"
                    aria-label={`Forget ${job.title}`}
                    onClick={(): void => {
                      forget(job.id);
                    }}
                  >
                    Forget
                  </button>
                )}

                <button
                  type="button"
                  aria-expanded={opened === job.id}
                  aria-label={`Log of ${job.title}`}
                  onClick={(): void => {
                    open(job.id);
                  }}
                >
                  Log
                </button>
              </div>

              <Bar job={job} />

              {job.error !== "" && <p className="jobs__error">{job.error}</p>}

              {opened === job.id && <Log job={job} lines={log} />}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/**
 * How much of a log is drawn at once.
 *
 * The end of it, because that is where a failure is and where a running job
 * is. A full log is five thousand lines, and drawing the whole of one means
 * rejoining and reconciling all of them on every event that arrives — ten
 * times a second for a verbose restore — to show a thousand lines nobody is
 * reading. What a person wants from a log that long is to follow it.
 */
const shownLines = 500;

/**
 * What a job said, as much of it as is worth drawing.
 *
 * It says when it is showing only the end, for the same reason the queue says
 * how many lines it dropped: a log that silently begins in the middle reads as
 * an operation that began there.
 */
function Log({ job, lines }: { job: JobView; lines: readonly string[] }): React.JSX.Element {
  const shown = lines.slice(-shownLines);
  const hidden = lines.length - shown.length;

  const said = [
    ...(job.dropped > 0 ? [`… ${String(job.dropped)} earlier lines were dropped`] : []),
    ...(hidden > 0 ? [`… ${String(hidden)} earlier lines are not shown`] : []),
    ...shown,
  ];

  return <pre className="jobs__log">{said.join("\n")}</pre>;
}

/**
 * The bar, and what it says beside itself.
 *
 * A job whose progress cannot be known draws a bar that moves rather than one
 * that fills. Drawing nought per cent instead would say the work has failed to
 * advance, which is a different and worse thing to tell somebody.
 */
function Bar({ job }: { job: JobView }): React.JSX.Element | null {
  if (isOver(job.state)) {
    return null;
  }

  const left = remaining(job.progress);

  return (
    <div className="jobs__progress">
      <div
        className={job.progress.indeterminate ? "jobs__bar jobs__bar--unknown" : "jobs__bar"}
        role="progressbar"
        aria-label={`Progress of ${job.title}`}
        aria-valuemin={0}
        aria-valuemax={100}
        {...(job.progress.indeterminate
          ? {}
          : { "aria-valuenow": Math.round(job.progress.fraction * 100) })}
      >
        <span
          className="jobs__fill"
          style={{ width: `${String(Math.round(job.progress.fraction * 100))}%` }}
        />
      </div>

      <span className="jobs__said">
        {job.progress.step}
        {left !== "" && ` · ${left}`}
      </span>
    </div>
  );
}
