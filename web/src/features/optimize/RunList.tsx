import { useCallback, useEffect, useState } from "react";

import { listFitJobs } from "../../api/fit";
import type { FitJobListEntry } from "../../api/types";
import {
  formatRunCost,
  formatRunElapsed,
  hasActiveRun,
  runElapsedMs,
  runStateLabel,
  sortRunsNewestFirst,
} from "./runList";

/**
 * How often the list re-reads `api/fit/jobs` while something in it is queued
 * or running.
 *
 * The active job's own numbers already stream over SSE through
 * `useFitEvents`; this list is not a second copy of that stream, it is the
 * history around it, so it does not need that stream's cadence. Three
 * seconds is fast enough that a run finishing, or a queued one starting, shows
 * up within about one glance, while `job.listing()` on the server only copies
 * a handful of fields under a lock -- there is no computation here worth
 * budgeting for, so the interval is chosen for a human's patience rather than
 * for the server's.
 */
const POLL_MS = 3000;

export interface RunListProps {
  /** The job currently loaded into the results panel, or null for none. */
  selectedJobId: string | null;
  /** Called with a job's id when its row is chosen. */
  onSelect: (jobId: string) => void;
}

/**
 * The run history: every job the server has kept, newest first, with the
 * currently selected one marked.
 *
 * A queued or running row is not read differently from a finished one -- the
 * state column already says which it is -- but the list as a whole refreshes
 * while any row is unsettled and stops once every row is terminal, which is
 * what makes a fit that just finished appear here without a reload.
 */
export function RunList({ selectedJobId, onSelect }: RunListProps) {
  const [jobs, setJobs] = useState<FitJobListEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  // Stamped alongside `jobs` rather than read with Date.now() at render time,
  // which React's purity rule refuses: a component body has to be a function
  // of its state, not of the wall clock. A running row's elapsed time is
  // therefore only as fresh as the last read, which is exactly as fresh as
  // the rest of the row already is.
  const [now, setNow] = useState<number>(() => Date.now());

  const refresh = useCallback(() => {
    listFitJobs()
      .then((body) => {
        setJobs(sortRunsNewestFirst(body.jobs));
        setNow(Date.now());
        setError(null);
      })
      .catch((cause: unknown) => {
        setError(
          cause instanceof Error
            ? cause.message
            : "the run history could not be read",
        );
      });
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const active = jobs !== null && hasActiveRun(jobs);

  useEffect(() => {
    if (!active) {
      return;
    }

    const id = window.setInterval(refresh, POLL_MS);

    return () => {
      window.clearInterval(id);
    };
  }, [active, refresh]);

  return (
    <section className="run-list" aria-labelledby="run-list-heading">
      <h3 id="run-list-heading">Runs</h3>

      {error !== null && <p className="fit-status-error">{error}</p>}

      {jobs === null ? (
        <p className="optimize-note">Loading run history…</p>
      ) : jobs.length === 0 ? (
        <p className="optimize-note">No fits have been started yet.</p>
      ) : (
        <div className="run-list-table">
          <div className="run-list-head" aria-hidden="true">
            <span>State</span>
            <span>Best cost</span>
            <span>Optimizer</span>
            <span>Metric</span>
            <span>Note</span>
            <span>Elapsed</span>
          </div>

          <div className="run-list-body" role="list">
            {jobs.map((job) => (
              <button
                key={job.jobId}
                type="button"
                role="listitem"
                className="run-list-row"
                data-state={job.state}
                // Not aria-pressed: that attribute is only allowed on
                // role="button", and this row's role is "listitem" so it
                // nests correctly inside the "list" above it. aria-current
                // is the one selection signal every role accepts, which is
                // what a real run list (Task 9 caught this against actual
                // job history; every prior test's list was empty) needs.
                aria-current={job.jobId === selectedJobId ? "true" : undefined}
                onClick={() => {
                  onSelect(job.jobId);
                }}
              >
                <span className="run-list-cell run-list-state">
                  {runStateLabel(job.state)}
                </span>
                <span className="run-list-cell run-list-numeric">
                  {formatRunCost(job)}
                </span>
                <span className="run-list-cell">{job.optimizer}</span>
                <span className="run-list-cell">{job.metric}</span>
                <span className="run-list-cell run-list-numeric">
                  {job.note}
                </span>
                <span className="run-list-cell run-list-numeric">
                  {formatRunElapsed(runElapsedMs(job, now))}
                </span>
              </button>
            ))}
          </div>
        </div>
      )}
    </section>
  );
}
