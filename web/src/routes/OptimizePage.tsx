/**
 * The Optimize tab.
 *
 * The page composes the pieces two parallel branches fill in and owns nothing
 * but the job the tab is currently watching. The form and the API client are
 * one PR; the progress stream, the cost chart and the audition another. Each
 * remaining slot below names the file that will replace it.
 */

import { useCallback, useEffect, useState } from "react";

import { FitApiError, getFitStatus } from "../api/fit";
import type { FitSnapshot } from "../api/types";
import { FitForm } from "../features/optimize/FitForm";
import { useApiAvailable } from "../features/optimize/useApiAvailable";

/** The command that makes the fit API reachable. */
const SERVE_COMMAND = "glockenspiel serve";

export function OptimizePage() {
  const { availability, version } = useApiAvailable();
  const [snapshot, setSnapshot] = useState<FitSnapshot | null>(null);

  const onSnapshot = useCallback((next: FitSnapshot) => {
    setSnapshot(next);
  }, []);

  // A fit outlives the page: the server holds one slot and a reload lands back
  // on whatever is running. Reading the status once on mount is what makes the
  // Cancel button reachable after a refresh instead of stranding the slot.
  useEffect(() => {
    if (availability !== "available") {
      return;
    }

    let cancelled = false;

    getFitStatus()
      .then((current) => {
        if (!cancelled) {
          setSnapshot(current);
        }
      })
      .catch((cause: unknown) => {
        // A 404 -- "no fit has been started" -- is the ordinary answer on a
        // fresh server and is not worth a word. Anything else is a real
        // failure: a 500, or a server that stopped answering between the
        // version probe and this request. Swallowing those too would leave the
        // page looking healthy while the status it shows is simply absent.
        if (cause instanceof FitApiError && cause.isNotFound) {
          return;
        }

        console.error("Reading the current fit failed", cause);
      });

    return () => {
      cancelled = true;
    };
  }, [availability]);

  /*
   * A stopgap until the SSE hook lands: while a fit runs, re-read the status so
   * the page notices the terminal state and re-enables Start. Cancel needs no
   * polling -- its 200 already means the slot is free, because the handler
   * blocks on the job's done channel -- and this whole effect is replaced by
   * features/optimize/useFitEvents.ts, which receives a whole snapshot per
   * event instead.
   */
  const running = snapshot?.state === "running";

  useEffect(() => {
    if (!running) {
      return;
    }

    let cancelled = false;

    const timer = window.setInterval(() => {
      getFitStatus()
        .then((current) => {
          if (!cancelled) {
            setSnapshot(current);
          }
        })
        .catch(() => {
          // A transient failure is not a reason to stop watching.
        });
    }, 1000);

    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [running]);

  return (
    <section className="optimize-panel" aria-labelledby="optimize-heading">
      <header className="optimize-header">
        <h2 id="optimize-heading">Optimize</h2>
        <p className="optimize-lead">
          Fit the instrument model against a reference recording, watch the cost
          fall, then audition and download the result.
        </p>
      </header>

      <div aria-live="polite" className="optimize-availability">
        {availability === "probing" ? (
          <p className="optimize-placeholder">Looking for the fit API…</p>
        ) : null}

        {availability === "unavailable" ? (
          <>
            <p className="optimize-placeholder">
              This page is served without the fit API, which is what the hosted
              build looks like: fitting is CPU-bound work the Go binary does,
              and there is no binary behind a static host. Run the local server
              and open the address it prints to use this tab.
            </p>

            <pre className="optimize-command">
              <code>{SERVE_COMMAND}</code>
            </pre>

            <p className="optimize-placeholder">
              Fitting entirely in the browser, through the WebAssembly build, is
              deferred. Until then a fit can also be run without a browser at
              all:
            </p>

            <pre className="optimize-command">
              <code>
                glockenspiel fit --reference recording.wav --output preset.json
              </code>
            </pre>
          </>
        ) : null}
      </div>

      {availability === "available" ? (
        <>
          <p className="optimize-lead">
            Connected to glockenspiel {version ?? "(unknown version)"}.
          </p>

          <FitForm onSnapshot={onSnapshot} snapshot={snapshot} />

          <div aria-live="polite" className="optimize-status">
            {snapshot === null ? (
              <p className="optimize-placeholder">
                No fit has been started yet.
              </p>
            ) : (
              <p className="optimize-placeholder">
                Fit {snapshot.jobId} is {snapshot.state}
                {snapshot.stopReason === undefined
                  ? ""
                  : ` (${snapshot.stopReason})`}
                : {snapshot.optimizerIterations} optimizer iterations,{" "}
                {snapshot.evaluations} evaluations, best cost{" "}
                {snapshot.bestCost.toPrecision(6)} after{" "}
                {(snapshot.elapsedMs / 1000).toFixed(1)}s.
                {snapshot.error === undefined ? "" : ` ${snapshot.error}`}
              </p>
            )}
          </div>

          {/* slot: the cost chart -- features/optimize/CostChart.tsx */}
          {/* slot: audition and download -- features/optimize/Audition.tsx */}
        </>
      ) : null}
    </section>
  );
}
