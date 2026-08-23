import { useEffect } from "react";

import type { FitSnapshot } from "../../api/types";
import { Audition } from "./Audition";
import { CostChart } from "./CostChart";
import { FitStatus } from "./FitStatus";
import { useFitEvents, type FitEvents } from "./useFitEvents";
import type { FitArtifacts } from "./Audition";

export interface FitProgressProps {
  /**
   * The job to watch. Null closes the stream and clears the curve; a new id
   * starts a fresh curve, so a second fit draws from zero rather than
   * continuing the first one's line.
   */
  jobId: string | null;
  /**
   * The `maxIterations` the fit was started with, for the "n of m" reading.
   * The snapshot does not carry it -- the server does not echo the request
   * back -- so the form that sent it supplies it. Null when it is not known.
   */
  maxIterations: number | null;
  /**
   * Called with every whole snapshot the stream delivers. The page composing
   * the tab needs them too -- the form's Start and Cancel buttons follow the
   * job's state -- and the stream is the only thing that sees a run reach a
   * terminal state on its own.
   */
  onSnapshot?: (snapshot: FitSnapshot) => void;
  /** In-memory progress from the browser worker; absent for the HTTP service. */
  events?: FitEvents | undefined;
  /** In-memory artifacts from the browser worker; absent for the HTTP service. */
  artifacts?: FitArtifacts | undefined;
  /**
   * Makes the fitted preset playable in the Play tab and returns the name it
   * was listed under. Absent leaves the button out entirely.
   */
  onUseInPlay?:
    | ((document: string, jobId: string | null) => string)
    | undefined;
}

/**
 * Everything that watches a running fit: the stream, the curve, the numbers and
 * the audition.
 *
 * It is one component so that the page composing the Optimize tab mounts it in
 * one line and owns none of the stream's lifetime. Everything it shows comes
 * from the snapshots the stream delivers, which are whole status objects, so
 * there is no second source of truth to keep in step.
 */
export function FitProgress({
  jobId,
  maxIterations,
  onSnapshot,
  events,
  artifacts,
  onUseInPlay,
}: FitProgressProps) {
  const serverEvents = useFitEvents(events === undefined ? jobId : null);
  const { snapshot, points, revision, streaming, streamError } =
    events ?? serverEvents;

  useEffect(() => {
    if (snapshot !== null) {
      onSnapshot?.(snapshot);
    }
  }, [snapshot, onSnapshot]);

  if (jobId === null) {
    return (
      <section className="fit-progress" aria-labelledby="fit-results-heading">
        <header className="fit-results-header">
          <h2 id="fit-results-heading">Results</h2>
        </header>

        <div className="fit-results-empty">
          <p>
            Start a fit to see live progress, the cost curve, and audition
            controls.
          </p>
        </div>
      </section>
    );
  }

  return (
    <section className="fit-progress" aria-labelledby="fit-results-heading">
      <header className="fit-results-header">
        <h2 id="fit-results-heading">Results</h2>
      </header>

      <FitStatus
        snapshot={snapshot}
        maxIterations={maxIterations}
        streaming={streaming}
        streamError={streamError}
      />

      <div className="fit-chart-area">
        {points.length === 0 ? (
          <p className="fit-chart-waiting">Waiting for first cost report…</p>
        ) : (
          <CostChart points={points} revision={revision} />
        )}
      </div>

      <Audition
        snapshot={snapshot}
        artifacts={artifacts}
        onUseInPlay={onUseInPlay}
      />
    </section>
  );
}
