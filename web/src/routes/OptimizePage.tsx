/**
 * The Optimize tab.
 *
 * The page selects the native service or browser worker and composes the form
 * with its progress. Every snapshot -- from an HTTP response, SSE, or a worker
 * message -- is one whole status object, so both backends paint the same UI.
 */

import { useCallback, useEffect, useMemo, useState } from "react";

import { FitApiError, getFitJob, getFitStatus } from "../api/fit";
import type { FitSnapshot } from "../api/types";
import { FitForm, type FitActions } from "../features/optimize/FitForm";
import { FitProgress } from "../features/optimize/FitProgress";
import { RunList } from "../features/optimize/RunList";
import type { ApiProbe } from "../features/optimize/useApiAvailable";
import type { FitEvents } from "../features/optimize/useFitEvents";
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
  /**
   * Registers a fitted preset as a sound the Play tab can choose, and returns
   * the name it was listed under. It lives in App, which owns both the engine
   * and the session's list of fitted sounds.
   */
  onUseInPlay: (document: string, jobId: string | null) => string;
}

export function OptimizePage({ api, wasm, onUseInPlay }: OptimizePageProps) {
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

  // The job the run list has picked, distinct from the one the form's Start
  // and Cancel buttons act on: those always follow serverSnapshot, the most
  // recent job this page is tracking, however the list is scrolled. Null
  // means "no explicit pick", which reads as "follow the active job" -- the
  // page's behaviour before the list existed, and what a client that never
  // shows the list (the WASM path) always does.
  const [selectedJobId, setSelectedJobId] = useState<string | null>(null);

  // The selected job's own snapshot, read once rather than streamed: SSE
  // (api/fit/events) always answers about whichever job the server considers
  // active, never about an id in the URL, so a history row that is not that
  // job has to be read with getFitJob instead. `jobId` is stamped alongside
  // the answer so a stale fetch for a row the user has since left never
  // overwrites what is now selected.
  const [selectedRead, setSelectedRead] = useState<{
    jobId: string;
    snapshot: FitSnapshot | null;
  } | null>(null);

  const onSnapshot = useCallback((next: FitSnapshot, startedWith?: number) => {
    setServerSnapshot(next);

    if (startedWith !== undefined) {
      setLimit({ jobId: next.jobId, maxIterations: startedWith });
    }
  }, []);

  // FitProgress's onSnapshot prop is required, not optional, when a caller
  // wants the "started with a limit" overload; a picked historical row is
  // read once, has nothing to report back, and must not be allowed to feed
  // its stale snapshot into the active job's state, so it gets this no-op
  // instead of the real callback.
  const ignoreSnapshot = useCallback(() => {}, []);

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

  // "Active" here means the job the page is already tracking through
  // serverSnapshot -- the one the form and the SSE stream follow -- not
  // whatever the server happens to be running: another client's queued job
  // becoming the server's active one is not a reason to yank this page's
  // results panel out from under whatever the user picked. Selecting the
  // page's own active job (or never selecting at all) is the only way back
  // to the live view.
  const viewingActive =
    browserMode || selectedJobId === null || selectedJobId === snapshot?.jobId;
  const selectedSnapshot =
    !viewingActive && selectedRead?.jobId === selectedJobId
      ? selectedRead.snapshot
      : null;
  const progressJobId = viewingActive
    ? (snapshot?.jobId ?? null)
    : selectedJobId;

  // A picked historical row is shown from one read, not a stream: passing a
  // ready-made FitEvents through FitProgress's `events` prop is what lets it
  // skip its own useFitEvents(jobId) call, which would otherwise open
  // api/fit/events and draw whatever job the server currently considers
  // active under the id of the one that was actually picked.
  const historicalEvents: FitEvents | undefined = viewingActive
    ? undefined
    : {
        snapshot: selectedSnapshot,
        points: [],
        revision: 0,
        streaming: false,
        streamError: null,
      };
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

  // Reads the row the run list picked, whenever it names a job other than the
  // one already tracked live. Picking the active job itself needs no read --
  // serverSnapshot already has it -- which is what keeps a click back onto the
  // live row instant instead of round-tripping through the network.
  useEffect(() => {
    if (
      availability !== "available" ||
      selectedJobId === null ||
      selectedJobId === serverSnapshot?.jobId
    ) {
      return;
    }

    let cancelled = false;

    // No "loading" write here: selectedSnapshot below already reads as null
    // for any jobId that does not match selectedRead's, so a newly picked
    // row shows nothing until this read lands rather than briefly showing
    // the row picked before it.
    getFitJob(selectedJobId)
      .then((job) => {
        if (!cancelled) {
          setSelectedRead({ jobId: selectedJobId, snapshot: job });
        }
      })
      .catch((cause: unknown) => {
        if (!cancelled) {
          console.error(`Reading fit ${selectedJobId} failed`, cause);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [availability, selectedJobId, serverSnapshot?.jobId]);

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
        <>
          {/*
            The run list has no meaning in the WASM path: there is no
            filesystem behind it, no history to page through, only the one
            in-memory run the worker is doing right now. Rendering nothing
            here, rather than an empty list, is item 6's contract -- the
            existing Playwright snapshots of that path cover exactly this.
          */}
          {!browserMode && (
            <RunList
              selectedJobId={selectedJobId}
              onSelect={setSelectedJobId}
            />
          )}

          <div className="optimize-workspace">
            <FitForm
              actions={browserMode ? wasmActions : undefined}
              onSnapshot={onSnapshot}
              snapshot={snapshot}
            />

            <FitProgress
              artifacts={browserMode ? (wasm.client ?? undefined) : undefined}
              events={browserMode ? wasm.events : historicalEvents}
              jobId={progressJobId}
              maxIterations={maxIterations}
              onSnapshot={
                browserMode || viewingActive ? onSnapshot : ignoreSnapshot
              }
              onUseInPlay={onUseInPlay}
            />
          </div>
        </>
      ) : null}
    </section>
  );
}
