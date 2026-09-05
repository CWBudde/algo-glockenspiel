import { useEffect, useState } from "react";

import { getFitJob, getFitJobTrace } from "../../api/fit";
import type { FitSnapshot } from "../../api/types";
import { parseTraceToPoints } from "./costCompare";
import type { FitEvents } from "./useFitEvents";

/**
 * How often a run watched this way is re-read.
 *
 * It is slower than the SSE stream on purpose. This is the fallback path, not
 * the main one: it costs a status read plus the whole trace file per tick,
 * where the stream costs one push per report. Two seconds keeps a followed
 * run's curve visibly alive -- the server itself only reads the trace once a
 * second -- while a run at `reportEvery: 1` still arrives in whole batches
 * rather than one line per request.
 */
const POLL_MS = 2000;

/** The state before the first read lands. */
const empty: FitEvents = {
  snapshot: null,
  points: [],
  revision: 0,
  streaming: false,
  streamError: null,
  displaced: false,
};

/**
 * Watches one job by polling, for every run the event stream cannot carry.
 *
 * `api/fit/events` only ever answers about whichever job the server considers
 * active -- the most recently recorded one, which since Phase 8.8 includes a
 * `glockenspiel fit` the server adopted out of its work directory. So a job
 * named by the run list, and any job the stream has been taken away from, has
 * to be read rather than listened to.
 *
 * The two sources are read so that they produce the same thing: the snapshot
 * is the same document the stream would have pushed, and the curve is built
 * from `trace.jsonl` by `parseTraceToPoints`, the same parser the compare
 * overlay uses. A followed run therefore paints the same status panel, the
 * same cost curve and the same term bars as a fit this server started; only
 * the cadence differs.
 *
 * The whole trace is re-read each tick rather than ranged over. It is one
 * short line per report and a request every two seconds, and the alternative
 * -- a byte offset held on the client -- would put a second implementation of
 * the server's own tail here, with the same partial-line hazard and none of
 * its tests.
 *
 * @param jobId the job to read; null stops the polling and clears the curve.
 */
export function useTracedFit(jobId: string | null): FitEvents {
  // Stamped with the job it was read for, exactly as useFitEvents stamps what
  // arrives: switching jobs then needs no state reset, because an answer for
  // a job that is no longer wanted is simply not read.
  const [tracked, setTracked] = useState<{
    jobId: string | null;
    events: FitEvents;
  }>({ jobId: null, events: empty });

  useEffect(() => {
    if (jobId === null) {
      return;
    }

    let stopped = false;
    let timer: number | null = null;

    const read = async () => {
      let snapshot: FitSnapshot;

      try {
        snapshot = await getFitJob(jobId);
      } catch (cause: unknown) {
        if (!stopped) {
          setTracked({
            jobId,
            events: {
              ...empty,
              streamError:
                cause instanceof Error
                  ? cause.message
                  : "this run could not be read",
            },
          });
        }

        return;
      }

      // A trace that cannot be read is not a failure worth showing: a run
      // that has not written its first line yet answers with one, and the
      // status beside the chart is already correct. The curve simply stays
      // empty until there is something to draw.
      let points: FitEvents["points"] = [];

      try {
        points = parseTraceToPoints(await getFitJobTrace(jobId));
      } catch {
        points = [];
      }

      if (stopped) {
        return;
      }

      setTracked((previous) => ({
        jobId,
        events: {
          snapshot,
          points,
          revision:
            (previous.jobId === jobId ? previous.events.revision : 0) + 1,
          // "Live" here means the run is still moving and this hook is still
          // reading it, which is what the status panel's (live) marker says
          // for a streamed job too.
          streaming:
            snapshot.state === "queued" || snapshot.state === "running",
          streamError: null,
          displaced: false,
        },
      }));

      // A terminal run has nothing more to say, so the loop ends on the read
      // that saw it end rather than going on asking a finished job how it is.
      if (snapshot.state === "queued" || snapshot.state === "running") {
        timer = window.setTimeout(() => void read(), POLL_MS);
      }
    };

    void read();

    return () => {
      stopped = true;

      if (timer !== null) {
        window.clearTimeout(timer);
      }
    };
  }, [jobId]);

  if (tracked.jobId !== jobId) {
    // Nothing has been read for this job yet. The first read is in flight,
    // which is the same "waiting for the first report" the stream shows while
    // its opening snapshot is on its way.
    return jobId === null ? empty : { ...empty, streaming: true };
  }

  return tracked.events;
}
