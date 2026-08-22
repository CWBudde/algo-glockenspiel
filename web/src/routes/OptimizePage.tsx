/**
 * The Optimize tab.
 *
 * The page owns nothing but the job the tab is currently watching: the form
 * starts and cancels it, and FitProgress subscribes to the server's event
 * stream for it. Every snapshot -- from the status read on mount, from a start
 * or a cancel, and from the stream -- is one whole status object, so there is a
 * single shape of truth and no reconciliation to do.
 */

import { useCallback, useEffect, useState } from "react";

import { getFitStatus } from "../api/fit";
import type { FitSnapshot } from "../api/types";
import { FitForm } from "../features/optimize/FitForm";
import { FitProgress } from "../features/optimize/FitProgress";
import { useApiAvailable } from "../features/optimize/useApiAvailable";

/** The command that makes the fit API reachable. */
const SERVE_COMMAND = "glockenspiel serve";

export function OptimizePage() {
  const { availability, version } = useApiAvailable();
  const [snapshot, setSnapshot] = useState<FitSnapshot | null>(null);

  // What the running fit was started with. The server does not echo the
  // request back, so only the form knows it, and only for a fit this page
  // started: a run picked up from the status read on mount has no known limit.
  const [maxIterations, setMaxIterations] = useState<number | null>(null);

  const onSnapshot = useCallback((next: FitSnapshot, startedWith?: number) => {
    setSnapshot(next);

    if (startedWith !== undefined) {
      setMaxIterations(startedWith);
    }
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
      .catch(() => {
        // A 404 -- "no fit has been started" -- is the ordinary answer on a
        // fresh server, and there is nothing else worth saying here: the form
        // reports the failures that belong to an action the user took.
      });

    return () => {
      cancelled = true;
    };
  }, [availability]);

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

          <FitProgress
            jobId={snapshot?.jobId ?? null}
            maxIterations={maxIterations}
            onSnapshot={onSnapshot}
          />
        </>
      ) : null}
    </section>
  );
}
