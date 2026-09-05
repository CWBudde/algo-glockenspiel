import { useEffect } from "react";

import type { FitSnapshot } from "../../api/types";
import { Audition } from "./Audition";
import { Comparison } from "./Comparison";
import { CostChart } from "./CostChart";
import { FitStatus } from "./FitStatus";
import { ParameterTable } from "./ParameterTable";
import { TermBars } from "./TermBars";
import { useFitEvents, type FitEvents } from "./useFitEvents";
import { useTracedFit } from "./useTracedFit";
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
  /**
   * Whether `jobId` is the run `api/fit/events` is expected to be about: the
   * one this page started, or the one it adopted as the active job.
   *
   * The stream carries no job id -- it is always about whatever the server
   * considers active -- so a job named from the run history, including a run
   * the server merely follows, is read through `useTracedFit` instead. False
   * asks for that reading directly; true still falls back to it if the stream
   * turns out to be about somebody else's fit.
   *
   * Ignored when `events` is supplied: the browser worker is its own source.
   */
  streamed?: boolean;
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
  streamed = true,
  events,
  artifacts,
  onUseInPlay,
}: FitProgressProps) {
  const streamable = events === undefined && streamed;
  const streamedEvents = useFitEvents(streamable ? jobId : null);

  // Read rather than streamed in two cases, and they are the same case seen
  // from two sides: a job the stream was never going to be about (a row
  // picked out of the run history, a run the server only follows), and a job
  // the stream turned out not to be about after all, because another run took
  // the active slot while this one was being watched. Either way the run is
  // still there on disk and still moving, so it is read from its own status
  // and its own trace instead of being left frozen.
  const tracedJobId =
    events !== undefined || (streamable && !streamedEvents.displaced)
      ? null
      : jobId;
  const tracedEvents = useTracedFit(tracedJobId);

  const { snapshot, points, revision, streaming, streamError } =
    events ?? (tracedJobId === null ? streamedEvents : tracedEvents);

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
          <CostChart
            points={points}
            revision={revision}
            // The compare picker reads /api/fit/jobs and /trace, neither of
            // which exists in the browser worker's contract -- passing no
            // job id there is what keeps the picker absent rather than
            // broken, the same rule Comparison follows below.
            compareJobId={artifacts === undefined ? jobId : undefined}
          />
        )}
      </div>

      <TermBars metrics={snapshot?.metrics} profile={snapshot?.profile} />

      <ParameterTable
        jobId={jobId}
        hasPreset={snapshot?.hasPreset ?? false}
        pinned={snapshot?.pinned}
        artifacts={artifacts}
      />

      {/*
        The comparison has no counterpart in the browser worker: `artifacts`
        is the WASM path's contract, and there is no per-job compare
        endpoint to serve it -- only the encoded audio the worker already
        holds in memory. It is left out there rather than pointed at a
        request that would 404.
      */}
      {artifacts === undefined && (
        <Comparison jobId={jobId} hasPreset={snapshot?.hasPreset ?? false} />
      )}

      <Audition
        snapshot={snapshot}
        artifacts={artifacts}
        onUseInPlay={onUseInPlay}
      />
    </section>
  );
}
