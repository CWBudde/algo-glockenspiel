import { useEffect, useRef, useState } from "react";

import type { FitSnapshot } from "../../api/types";

/** One sample of the cost curve, keyed by the optimizer's own iteration count. */
export interface CostPoint {
  iteration: number;
  best: number;
  current: number;
}

export interface FitEvents {
  /** The most recent whole snapshot, or null before the first event arrives. */
  snapshot: FitSnapshot | null;
  /** The curve so far. Replaced, never mutated, so a memo on it fires. */
  points: CostPoint[];
  /** Whether a stream is currently open. */
  streaming: boolean;
  /**
   * Something worth telling the user about. A 404 before any fit has started is
   * not one of those, so it never lands here.
   */
  streamError: string | null;
}

const empty: FitEvents = {
  snapshot: null,
  points: [],
  streaming: false,
  streamError: null,
};

/**
 * Subscribes to `GET /api/fit/events` for the lifetime of one job.
 *
 * The stream's contract (`internal/server/events.go`) shapes all of this:
 *
 *   - Event names are `progress` while the job runs, `done` once it reaches a
 *     terminal state, and `shutdown` when the server is going away.
 *   - Every `data:` line is a whole `FitSnapshot`, never a delta, and the
 *     current one is written the moment the stream opens -- so attaching
 *     mid-run, or after the run already finished, paints immediately.
 *   - There is no `id:` and no `retry:`, so there is no Last-Event-ID replay to
 *     lean on. A source left open after a terminal event is reconnected by the
 *     browser, handed the same terminal snapshot, and reconnected again,
 *     forever. Closing on `done` and on `shutdown` is therefore not tidiness,
 *     it is the only thing that ends the loop.
 *   - Heartbeat comment lines (`: keep-alive`, every 15 s) are not events;
 *     EventSource drops them without telling anyone, which is what we want.
 *
 * The URL is relative because the bundle is built with `base: "./"` and is
 * served both from `/` by `glockenspiel serve` and from a project sub-path on
 * Pages. An absolute `/api/...` would work in exactly one of the two.
 *
 * @param jobId the job to watch; null closes any open stream and clears the
 *   curve. Passing a new id starts a fresh curve, which is what makes a second
 *   fit draw from zero rather than continuing the first one's line.
 */
export function useFitEvents(jobId: string | null): FitEvents {
  // What arrived is stamped with the job it arrived for, so that switching
  // jobs needs no state reset: a stamp that no longer matches is simply not
  // read. Resetting instead would mean a setState in the effect body, which is
  // a cascading render and which the hooks lint rightly refuses.
  const [tracked, setTracked] = useState<{
    jobId: string | null;
    events: FitEvents;
  }>({ jobId: null, events: empty });

  // The points array is accumulated here rather than read back out of state, so
  // that a burst of events at reportEvery: 1 cannot interleave two functional
  // updates against a stale array.
  const pointsRef = useRef<CostPoint[]>([]);

  useEffect(() => {
    pointsRef.current = [];

    if (jobId === null) {
      return;
    }

    const setState = (next: (previous: FitEvents) => FitEvents) => {
      setTracked((previous) => ({
        jobId,
        events: next(previous.jobId === jobId ? previous.events : empty),
      }));
    };

    const source = new EventSource("api/fit/events");

    const apply = (event: MessageEvent<string>, terminal: boolean) => {
      let snapshot: FitSnapshot;

      try {
        snapshot = JSON.parse(event.data) as FitSnapshot;
      } catch {
        setState((previous) => ({
          ...previous,
          streamError: "the progress stream sent something that is not JSON",
        }));

        return;
      }

      pointsRef.current = appendPoint(pointsRef.current, snapshot);

      setState(() => ({
        snapshot,
        points: pointsRef.current,
        streaming: !terminal,
        streamError: null,
      }));

      if (terminal) {
        source.close();
      }
    };

    const onProgress = (event: MessageEvent<string>) => {
      apply(event, false);
    };

    const onDone = (event: MessageEvent<string>) => {
      apply(event, true);
    };

    const onShutdown = () => {
      // The server names its reason before dropping the connection, and it is
      // not coming back, so the source must not be left to reconnect into a
      // closed port.
      source.close();

      setState((previous) => ({
        ...previous,
        streaming: false,
        streamError: "the server is shutting down; the stream ended",
      }));
    };

    const onError = () => {
      // EventSource reports a failed connect, a dropped connection and the end
      // of a stream through this one event, with no status code attached. The
      // most likely cause by far is a 404 -- no fit has been started yet, or
      // the one being watched has been replaced -- and that is a normal state
      // of the world rather than something to put in front of the user. So the
      // stream is marked as not running and nothing is said; anything that
      // really went wrong shows up as a stalled curve beside a "running" state,
      // which the status panel already displays.
      setState((previous) => ({ ...previous, streaming: false }));
    };

    source.addEventListener("progress", onProgress);
    source.addEventListener("done", onDone);
    source.addEventListener("shutdown", onShutdown);
    source.addEventListener("error", onError);

    return () => {
      source.removeEventListener("progress", onProgress);
      source.removeEventListener("done", onDone);
      source.removeEventListener("shutdown", onShutdown);
      source.removeEventListener("error", onError);
      source.close();
    };
  }, [jobId]);

  if (tracked.jobId !== jobId) {
    // Nothing has arrived for this job yet. A stream is about to open for it,
    // which is what "streaming" says while the first snapshot is in flight.
    return jobId === null ? empty : { ...empty, streaming: true };
  }

  return tracked.events;
}

/**
 * Adds one snapshot to the curve.
 *
 * Under a slow reader the server coalesces intermediate reports away -- a
 * subscriber is woken, not queued -- so consecutive events can jump several
 * optimizer iterations, and the terminal event can repeat the iteration count
 * of the report before it. Points are therefore appended when the count has
 * moved and overwritten when it has not, which keeps the x axis monotonic
 * without dropping the final, most accurate reading.
 */
function appendPoint(points: CostPoint[], snapshot: FitSnapshot): CostPoint[] {
  const point: CostPoint = {
    iteration: snapshot.optimizerIterations,
    best: snapshot.bestCost,
    current: snapshot.currentCost,
  };

  const last = points[points.length - 1];

  if (last !== undefined && point.iteration <= last.iteration) {
    return [...points.slice(0, -1), point];
  }

  return [...points, point];
}
