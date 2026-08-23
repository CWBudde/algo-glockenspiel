/**
 * The Optimize tab.
 *
 * The page selects the native service or browser worker and composes the form
 * with its progress. Every snapshot -- from an HTTP response, SSE, or a worker
 * message -- is one whole status object, so both backends paint the same UI.
 */

import { useCallback, useEffect, useMemo, useState } from "react";

import { FitApiError, getFitStatus } from "../api/fit";
import type { FitSnapshot } from "../api/types";
import { FitForm, type FitActions } from "../features/optimize/FitForm";
import { FitProgress } from "../features/optimize/FitProgress";
import type { ApiProbe } from "../features/optimize/useApiAvailable";
import type { WasmFitWorker } from "../features/optimize/useWasmFitWorker";

/** The command that makes the fit API reachable. */
const SERVE_COMMAND = "glockenspiel serve";

/**
 * A started fit's iteration limit, stamped with the job it was sent for.
 *
 * Written on one line rather than inline in the useState type argument because
 * prettier 3.8 and 3.9 break a multi-line type differently and each rewrites
 * the other's output; see the same note on `BuiltBody` in FitForm.
 */
type StartLimit = { jobId: string; maxIterations: number };

export interface OptimizePageProps {
  api: ApiProbe;
  wasm: WasmFitWorker;
}

export function OptimizePage({ api, wasm }: OptimizePageProps) {
  const { availability, version } = api;
  const [serverSnapshot, setServerSnapshot] = useState<FitSnapshot | null>(
    null,
  );

  // What a fit was started with, stamped with the job it was sent for. The
  // server does not echo the request back, so only the form knows the limit,
  // and only for a fit this page started: a run picked up from the status read
  // on mount, or from the stream after the slot was reused, has none. Keeping
  // the job id alongside it is what stops the previous run's limit from being
  // read against the new run's count; the status panel then shows the bare
  // iteration count rather than "n of m" against an m that is not this fit's.
  const [limit, setLimit] = useState<StartLimit | null>(null);

  const onSnapshot = useCallback((next: FitSnapshot, startedWith?: number) => {
    setServerSnapshot(next);

    if (startedWith !== undefined) {
      setLimit({ jobId: next.jobId, maxIterations: startedWith });
    }
  }, []);

  const browserMode = availability === "unavailable";
  const snapshot = browserMode ? wasm.events.snapshot : serverSnapshot;
  const wasmActions = useMemo<FitActions | undefined>(() => {
    const local = wasm.client;
    if (local === null) {
      return undefined;
    }

    return {
      start: (form) => local.start(form),
      cancel: (jobId) => local.cancel(jobId ?? ""),
    };
  }, [wasm.client]);

  const limitApplies = limit !== null && limit.jobId === snapshot?.jobId;
  const maxIterations = limitApplies ? limit.maxIterations : null;
  const serviceStatus =
    availability === "probing"
      ? "Checking fit service"
      : availability === "available"
        ? `Fit service connected · ${version ?? "unknown version"}`
        : wasm.error
          ? "Browser optimizer unavailable"
          : wasm.client === null
            ? "Loading browser optimizer"
            : "Browser optimizer ready · WebAssembly";

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
          setServerSnapshot(current);
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

  return (
    <section className="optimize-panel" aria-labelledby="optimize-heading">
      <header className="optimize-header">
        <div className="optimize-heading-row">
          <h2 id="optimize-heading">Optimize</h2>

          <div
            aria-label={serviceStatus}
            aria-live="polite"
            className="optimize-api-status"
            data-state={
              availability === "probing" ||
              (browserMode && wasm.client === null && !wasm.error)
                ? "probing"
                : availability === "available" || wasm.client !== null
                  ? "available"
                  : "unavailable"
            }
            role="status"
          >
            {serviceStatus}
          </div>
        </div>

        <p className="optimize-lead">
          Fit the instrument model against a reference recording, watch the cost
          fall, then audition and download the result.
        </p>
      </header>

      <div className="optimize-availability">
        {browserMode ? (
          <>
            <p className="optimize-placeholder">
              This static version runs fitting locally in WebAssembly. It is
              slower than the native service, but uses the same objectives,
              parameter bounds and optimizer backends.
            </p>

            {wasm.error ? (
              <>
                <p className="optimize-placeholder">
                  {wasm.status}. For native fitting, run:
                </p>
                <pre className="optimize-command">
                  <code>{SERVE_COMMAND}</code>
                </pre>
              </>
            ) : wasm.client === null ? (
              <p className="optimize-placeholder">{wasm.status}</p>
            ) : null}
          </>
        ) : null}
      </div>

      {availability === "available" || wasmActions !== undefined ? (
        <div className="optimize-workspace">
          <FitForm
            actions={browserMode ? wasmActions : undefined}
            onSnapshot={onSnapshot}
            snapshot={snapshot}
          />

          <FitProgress
            artifacts={browserMode ? (wasm.client ?? undefined) : undefined}
            events={browserMode ? wasm.events : undefined}
            jobId={snapshot?.jobId ?? null}
            maxIterations={maxIterations}
            onSnapshot={onSnapshot}
          />
        </div>
      ) : null}
    </section>
  );
}
