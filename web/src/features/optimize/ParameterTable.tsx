import { useEffect, useState } from "react";

import { FitApiError, getFitJobPreset } from "../../api/fit";
import type { BarParams, PinnedDimension, Preset } from "../../api/types";
import type { FitArtifacts } from "./Audition";
import {
  describePin,
  formatParamNumber,
  harmonicGainRows,
  indexPinned,
  modeRows,
  scalarRows,
  type PinInfo,
} from "./parameterTable";

export interface ParameterTableProps {
  /** The job to read parameters for, or null to show nothing. */
  jobId: string | null;
  /**
   * Whether that job has a fitted preset yet, the same gate `Audition` and
   * `Comparison` use: a run cancelled after its first report still has one.
   */
  hasPreset: boolean;
  /**
   * The dimensions of the result sitting on a bound, from
   * `FitSnapshot.pinned` / the WASM snapshot's own `pinned`. Names are
   * `optimizer.ParamCodec`'s own -- see parameterTable.ts.
   */
  pinned?: PinnedDimension[] | undefined;
  /** In-memory artifacts from the browser worker; absent for the HTTP service. */
  artifacts?: FitArtifacts | undefined;
}

/**
 * One value cell, with a text-and-glyph badge for a pinned dimension.
 *
 * The badge is never colour alone: `data-bound` on the cell drives a border
 * treatment the stylesheet paints, but the word "min" or "max" is printed
 * text, and `describePin` spells the whole fact out again for anyone reading
 * through a screen reader rather than looking at a border.
 */
function ParamCell({
  value,
  unit,
  pin,
}: {
  value: number;
  unit?: string;
  pin: PinInfo | null;
}) {
  return (
    <td className="param-cell" data-pinned={pin !== null} data-bound={pin?.bound}>
      {formatParamNumber(value)}
      {unit !== undefined && unit !== "" ? ` ${unit}` : ""}
      {pin !== null && (
        <>
          {" "}
          <span className="param-pin-badge" aria-hidden="true">
            {pin.bound}
          </span>
          <span className="visually-hidden">, {describePin(pin)}</span>
        </>
      )}
    </td>
  );
}

/**
 * Reads the fitted preset's parameters back as plain text, from whichever
 * backend produced it -- the same two-path split `Audition.fittedDocument`
 * uses, for the same reason: neither is a new endpoint.
 */
async function readParameters(
  jobId: string,
  artifacts: FitArtifacts | undefined,
): Promise<BarParams> {
  if (artifacts === undefined) {
    return (await getFitJobPreset(jobId)).parameters;
  }

  const text = await (await artifacts.preset()).text();

  return (JSON.parse(text) as Preset).parameters;
}

/**
 * Every fitted parameter, read as a preset reads: the bar's own scalars, then
 * one row per mode carrying its frequency, amplitude and half-life together,
 * then the waveshaper's gains if it is on. A parameter that finished on a
 * bound of the search box is marked, in text as well as in style, so the
 * table stays legible to axe and to anyone who cannot see colour.
 */
export function ParameterTable({
  jobId,
  hasPreset,
  pinned,
  artifacts,
}: ParameterTableProps) {
  const [read, setRead] = useState<{
    jobId: string;
    parameters: BarParams | null;
    error: string | null;
  } | null>(null);

  useEffect(() => {
    if (jobId === null || !hasPreset) {
      return;
    }

    let cancelled = false;

    readParameters(jobId, artifacts)
      .then((parameters) => {
        if (!cancelled) {
          setRead({ jobId, parameters, error: null });
        }
      })
      .catch((cause: unknown) => {
        if (cancelled) {
          return;
        }

        const message =
          cause instanceof FitApiError
            ? cause.message
            : cause instanceof Error
              ? cause.message
              : "the parameters could not be read";

        setRead({ jobId, parameters: null, error: message });
      });

    return () => {
      cancelled = true;
    };
  }, [jobId, hasPreset, artifacts]);

  if (jobId === null || !hasPreset) {
    return (
      <section className="fit-parameters" aria-labelledby="parameter-table-heading">
        <h3 id="parameter-table-heading">Parameters</h3>
        <p className="optimize-note">
          There is nothing to show yet. The fitted parameters become available
          as soon as the fit has reported once, even if it is later cancelled.
        </p>
      </section>
    );
  }

  if (read === null || read.jobId !== jobId) {
    return (
      <section className="fit-parameters" aria-labelledby="parameter-table-heading">
        <h3 id="parameter-table-heading">Parameters</h3>
        <p className="optimize-note">Loading the fitted parameters…</p>
      </section>
    );
  }

  if (read.error !== null || read.parameters === null) {
    return (
      <section className="fit-parameters" aria-labelledby="parameter-table-heading">
        <h3 id="parameter-table-heading">Parameters</h3>
        <p className="fit-status-error">
          {read.error ?? "the parameters could not be read"}
        </p>
      </section>
    );
  }

  const parameters = read.parameters;
  const pinnedByName = indexPinned(pinned);
  const scalars = scalarRows(parameters, pinnedByName);
  const modes = modeRows(parameters, pinnedByName);
  const gains = harmonicGainRows(parameters, pinnedByName);

  return (
    <section className="fit-parameters" aria-labelledby="parameter-table-heading">
      <h3 id="parameter-table-heading">Parameters</h3>

      <p className="optimize-note">
        Every fitted parameter. One marked “min” or “max” finished on the edge
        of the search box: the search wanted to go further than its bounds
        allowed.
      </p>

      <table className="param-table">
        <caption className="visually-hidden">Bar-level parameters</caption>
        <thead>
          <tr>
            <th scope="col">Parameter</th>
            <th scope="col">Value</th>
          </tr>
        </thead>
        <tbody>
          {scalars.map((row) => (
            <tr key={row.key}>
              <th scope="row">{row.label}</th>
              <ParamCell value={row.value} unit={row.unit} pin={row.pin} />
            </tr>
          ))}
        </tbody>
      </table>

      <table className="param-table param-table-modes">
        <caption className="visually-hidden">Per-mode parameters</caption>
        <thead>
          <tr>
            <th scope="col">Mode</th>
            <th scope="col">Frequency (Hz)</th>
            <th scope="col">Amplitude</th>
            <th scope="col">Half-life (ms)</th>
          </tr>
        </thead>
        <tbody>
          {modes.map((mode) => (
            <tr key={mode.index}>
              <th scope="row">{mode.index + 1}</th>
              <ParamCell value={mode.frequencyHz} pin={mode.frequencyPin} />
              <ParamCell value={mode.amplitude} pin={mode.amplitudePin} />
              <ParamCell value={mode.decayMs} pin={mode.decayPin} />
            </tr>
          ))}
        </tbody>
      </table>

      {gains.length > 0 && (
        <table className="param-table">
          <caption className="visually-hidden">Chebyshev harmonic gains</caption>
          <thead>
            <tr>
              <th scope="col">Harmonic</th>
              <th scope="col">Gain</th>
            </tr>
          </thead>
          <tbody>
            {gains.map((gain) => (
              <tr key={gain.index}>
                <th scope="row">{gain.index + 1}</th>
                <ParamCell value={gain.value} pin={gain.pin} />
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
