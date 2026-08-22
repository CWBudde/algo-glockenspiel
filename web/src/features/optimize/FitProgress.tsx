import { useEffect } from "react";

import type { FitSnapshot } from "../../api/types";
import { Audition } from "./Audition";
import { CostChart } from "./CostChart";
import { FitStatus } from "./FitStatus";
import { useFitEvents } from "./useFitEvents";

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
}: FitProgressProps) {
  const { snapshot, points, streaming, streamError } = useFitEvents(jobId);

  useEffect(() => {
    if (snapshot !== null) {
      onSnapshot?.(snapshot);
    }
  }, [snapshot, onSnapshot]);

  return (
    <div className="fit-progress">
      <CostChart points={points} />

      <FitStatus
        snapshot={snapshot}
        maxIterations={maxIterations}
        streaming={streaming}
        streamError={streamError}
      />

      <Audition snapshot={snapshot} />
    </div>
  );
}
