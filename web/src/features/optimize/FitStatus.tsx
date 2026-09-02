import type { FitMetrics, FitSnapshot } from "../../api/types";

export interface FitStatusProps {
  snapshot: FitSnapshot | null;
  /**
   * The `maxIterations` the fit was started with. It is not part of the
   * snapshot -- the server does not echo the request back -- so the form that
   * sent it supplies it here. Null when it is not known.
   */
  maxIterations: number | null;
  /** Whether a stream is currently open, for the "live" hint. */
  streaming: boolean;
  /** Something the stream wants to say; usually null. */
  streamError: string | null;
}

function formatCost(cost: number): string {
  if (!Number.isFinite(cost)) {
    return "-";
  }

  return cost.toPrecision(6);
}

function formatElapsed(elapsedMs: number): string {
  const seconds = elapsedMs / 1000;

  if (seconds < 60) {
    return `${seconds.toFixed(1)} s`;
  }

  const minutes = Math.floor(seconds / 60);

  return `${String(minutes)} m ${(seconds - minutes * 60).toFixed(1)} s`;
}

/**
 * What the fit is doing, in words.
 *
 * The region is `aria-live="polite"`, so a screen reader hears the run
 * progress without the focus being stolen from whatever the user is doing --
 * which for a fit that may last minutes is the only usable setting.
 */
/** The terms in reporting order, with the unit each is measured in. */
const METRIC_ROWS: readonly [keyof FitMetrics, string, string][] = [
  ["partial_cents", "Partial pitch", "cents"],
  ["partial_level_db", "Partial level", "dB"],
  ["partial_decay_octaves", "Partial decay", "oct"],
  ["partial_missing", "Partials missing", ""],
  ["partial_extra", "Partials extra", ""],
  ["spectral_fine_db", "Spectrum, fine", "dB"],
  ["spectral_coarse_db", "Spectrum, coarse", "dB"],
  ["envelope_db", "Envelope", "dB"],
  ["decay_slope_dbps", "Decay slope", "dB/s"],
  ["waveform", "Waveform residual", ""],
  ["gain_db", "Gain applied", "dB"],
];

function formatMetric(value: number | null | undefined, unit: string): string {
  if (value === null || value === undefined) {
    return "n/a";
  }

  const digits = Math.abs(value) >= 100 ? 0 : Math.abs(value) >= 10 ? 1 : 2;
  const text = value.toFixed(digits);

  return unit === "" ? text : `${text} ${unit}`;
}

export function FitStatus({
  snapshot,
  maxIterations,
  streaming,
  streamError,
}: FitStatusProps) {
  return (
    <section
      className="fit-status"
      aria-labelledby="fit-status-heading"
      aria-live="polite"
    >
      <h3 id="fit-status-heading">Status</h3>

      {snapshot === null ? (
        <p className="optimize-note">
          {streaming
            ? "Waiting for the first report."
            : "No fit is being watched."}
        </p>
      ) : (
        <dl className="fit-status-grid">
          <div>
            <dt>State</dt>
            <dd data-state={snapshot.state}>
              {snapshot.state}
              {streaming ? " (live)" : ""}
            </dd>
          </div>

          <div>
            <dt>Job</dt>
            <dd>{snapshot.jobId}</dd>
          </div>

          <div>
            <dt>Best cost</dt>
            <dd>{formatCost(snapshot.bestCost)}</dd>
          </div>

          <div>
            <dt>Current cost</dt>
            <dd>{formatCost(snapshot.currentCost)}</dd>
          </div>

          <div>
            {/*
              Two counts, two rows, both named for what they are. `iteration`
              counts progress reports -- it moves once per `reportEvery` -- and
              only `optimizerIterations` is the backend's own count and the one
              `maxIterations` bounds. Showing either without the other invites
              reading a report count as progress towards the limit.

              The label follows the backend, because the unit does: a mayfly
              iteration is one generation of the whole swarm -- roughly fifty
              renders -- so "3 of 100" is far more work than the same numbers
              mean under the simple optimizer.
            */}
            <dt>
              {snapshot.optimizer === "mayfly"
                ? "Generations"
                : "Optimizer iterations"}
            </dt>
            <dd>
              {snapshot.optimizerIterations}
              {maxIterations === null ? "" : ` of ${String(maxIterations)}`}
            </dd>
          </div>

          {snapshot.restart !== undefined && (
            <div>
              {/*
                Only CMA-ES restarts, and the field is absent for the run that
                has not: the row appears when a fit is on its second cold run
                or later, which is the only time the number says anything.
              */}
              <dt>Restart</dt>
              <dd>{snapshot.restart}</dd>
            </div>
          )}

          <div>
            <dt>Progress reports</dt>
            <dd>{snapshot.iteration}</dd>
          </div>

          <div>
            <dt>Evaluations</dt>
            <dd>{snapshot.evaluations}</dd>
          </div>

          <div>
            <dt>Elapsed</dt>
            <dd>{formatElapsed(snapshot.elapsedMs)}</dd>
          </div>

          <div>
            <dt>Optimizer</dt>
            <dd>
              {snapshot.optimizer} / {snapshot.metric}
            </dd>
          </div>

          {snapshot.stopReason !== undefined && snapshot.stopReason !== "" && (
            <div>
              {/*
                An opaque string, shown as it came: gonum reports statuses like
                "FunctionConvergence" and mayfly reports "time_budget", and
                mapping them to friendlier words here would either lose the
                distinction or go stale the next time a backend is added.
              */}
              <dt>Stop reason</dt>
              <dd>{snapshot.stopReason}</dd>
            </div>
          )}

          {snapshot.mayflyVariant !== undefined &&
            snapshot.mayflyVariant !== "" && (
              <div>
                {/*
                  What the mayfly backend settled on, which is not always what
                  was asked for: a preset chooses a dialect without naming it,
                  and this row is the only place that choice is ever reported.
                */}
                <dt>Mayfly variant</dt>
                <dd>{snapshot.mayflyVariant}</dd>
              </div>
            )}

          {snapshot.mayflySeed !== undefined && snapshot.mayflySeed !== "" && (
            <div>
              {/*
                Rendered verbatim, never through Number(): the seed is an int64
                and a JS number stops representing every integer past 2^53, so
                a round trip through one could display a seed that was never
                used and cannot reproduce the run.
              */}
              <dt>Mayfly seed</dt>
              <dd>{snapshot.mayflySeed}</dd>
            </div>
          )}

          <div>
            <dt>Starting modes</dt>
            <dd>
              {snapshot.seededModes > 0
                ? `${snapshot.seededModes} from the reference's partials`
                : "the starting preset's own"}
            </dd>
          </div>

          <div>
            <dt>Fitted preset</dt>
            <dd>{snapshot.hasPreset ? "available" : "not yet"}</dd>
          </div>
        </dl>
      )}

      {snapshot?.pinned !== undefined && snapshot.pinned.length > 0 && (
        <p className="fit-status-pinned">
          {/*
            A dimension that finished on a bound is one the search wanted to
            push past the box, which is worth knowing before the preset is
            trusted.
          */}
          On a bound:{" "}
          {snapshot.pinned
            .map((dimension) => `${dimension.name} at ${dimension.bound}`)
            .join(", ")}
        </p>
      )}

      {snapshot?.metrics !== undefined && (
        <dl className="fit-status-grid fit-status-metrics">
          {/*
            The breakdown of the best point so far: one raw term per thing the
            composite objective measures, in physical units, whatever metric
            the run scores by. A term the reference was too short to measure
            arrives as null and is shown as n/a rather than hidden, so the
            list keeps its shape from one report to the next.
          */}
          {METRIC_ROWS.map(([key, label, unit]) => (
            <div key={key}>
              <dt>{label}</dt>
              <dd>{formatMetric(snapshot.metrics?.[key], unit)}</dd>
            </div>
          ))}
        </dl>
      )}

      {snapshot?.error !== undefined && snapshot.error !== "" && (
        <p className="fit-status-error" role="alert">
          {snapshot.error}
        </p>
      )}

      {streamError !== null && (
        <p className="fit-status-error">{streamError}</p>
      )}
    </section>
  );
}
