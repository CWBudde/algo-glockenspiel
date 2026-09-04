import { useEffect, useRef, useState } from "react";

import { FitApiError, getFitJobCompare } from "../../api/fit";
import type { FitCompare, FitCompareSide } from "../../api/types";
import { useResolvedTheme, type ResolvedTheme } from "../../lib/theme";
import {
  dbToIntensity,
  lerpColor,
  sharedAmplitudeRange,
  sharedSpectrogramPeakDb,
  spectrogramColumnExtent,
  spectrogramRowExtent,
  spectrogramSummary,
  waveformPoints,
  waveformSummary,
  type AmplitudeRange,
} from "./compareDrawing";

export interface ComparisonProps {
  /** The job to compare, or null to show nothing. */
  jobId: string | null;
  /**
   * Whether that job has a fitted preset yet. `getFitJobCompare` 409s until
   * it does, and `Audition` gates its own controls the same way -- the two
   * panels reach that state together rather than one of them making a
   * request the other already knows will fail.
   */
  hasPreset: boolean;
}

/** The canvas resolution drawn at; CSS stretches it to the panel's width. */
const CANVAS_WIDTH = 480;
const WAVEFORM_HEIGHT = 120;
const SPECTROGRAM_HEIGHT = 160;

/** Reads one of the palette's custom properties, matching CostChart's helper. */
function cssColor(name: string, fallback: string): string {
  if (typeof window === "undefined") {
    return fallback;
  }

  const value = window
    .getComputedStyle(document.documentElement)
    .getPropertyValue(name)
    .trim();

  return value === "" ? fallback : value;
}

/** Parses a `rgb(r, g, b)` or `#rrggbb` custom property into channels. */
function parseColor(value: string, fallback: [number, number, number]) {
  const rgbMatch = /rgb\(\s*(\d+)[ ,]+(\d+)[ ,]+(\d+)/.exec(value);

  if (rgbMatch) {
    return [Number(rgbMatch[1]), Number(rgbMatch[2]), Number(rgbMatch[3])] as [
      number,
      number,
      number,
    ];
  }

  const hexMatch = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(value);

  if (hexMatch) {
    return [
      Number.parseInt(hexMatch[1], 16),
      Number.parseInt(hexMatch[2], 16),
      Number.parseInt(hexMatch[3], 16),
    ] as [number, number, number];
  }

  return fallback;
}

/** The palette this view paints with, read the same way CostChart reads its own. */
function readPalette(theme: ResolvedTheme) {
  return {
    theme,
    referenceLine: parseColor(
      cssColor("--copper-accent", "#8d562d"),
      [141, 86, 45],
    ),
    renderLine: parseColor(cssColor("--brass", "#e3bb7a"), [227, 187, 122]),
    quiet: parseColor(cssColor("--surface-card", "#f3eee6"), [243, 238, 230]),
    loud: parseColor(cssColor("--copper-accent", "#8d562d"), [141, 86, 45]),
    grid: cssColor("--chart-grid", "rgba(85, 52, 24, 0.16)"),
  };
}

/** Draws one waveform envelope, filled, on a centre line. */
function drawWaveform(
  canvas: HTMLCanvasElement,
  waveform: FitCompareSide["waveform"],
  range: AmplitudeRange,
  fill: [number, number, number],
  grid: string,
) {
  const ctx = canvas.getContext("2d");

  if (ctx === null) {
    return;
  }

  const width = canvas.width;
  const height = canvas.height;

  ctx.clearRect(0, 0, width, height);

  const centre =
    height - ((0 - range.min) / (range.max - range.min || 1)) * height;

  ctx.strokeStyle = grid;
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.moveTo(0, centre);
  ctx.lineTo(width, centre);
  ctx.stroke();

  const points = waveformPoints(waveform, width, height, range);

  if (points.length === 0) {
    return;
  }

  ctx.beginPath();
  ctx.moveTo(points[0].x, points[0].yMax);

  for (const point of points) {
    ctx.lineTo(point.x, point.yMax);
  }

  for (let index = points.length - 1; index >= 0; index -= 1) {
    ctx.lineTo(points[index].x, points[index].yMin);
  }

  ctx.closePath();
  ctx.fillStyle = `rgba(${String(fill[0])}, ${String(fill[1])}, ${String(fill[2])}, 0.75)`;
  ctx.fill();
}

/** Draws one spectrogram, cell by cell, against the shared [floorDb, peakDb]. */
function drawSpectrogram(
  canvas: HTMLCanvasElement,
  spectrogram: NonNullable<FitCompareSide["spectrogram"]>,
  floorDb: number,
  peakDb: number,
  quiet: [number, number, number],
  loud: [number, number, number],
) {
  const ctx = canvas.getContext("2d");

  if (ctx === null) {
    return;
  }

  const width = canvas.width;
  const height = canvas.height;

  ctx.clearRect(0, 0, width, height);

  for (let column = 0; column < spectrogram.frames; column += 1) {
    const { x, w } = spectrogramColumnExtent(column, spectrogram.frames, width);
    const row = spectrogram.db[column];

    for (let bin = 0; bin < spectrogram.bins; bin += 1) {
      const { y, h } = spectrogramRowExtent(bin, spectrogram.bins, height);
      const intensity = dbToIntensity(row[bin], floorDb, peakDb);

      ctx.fillStyle = lerpColor(quiet, loud, intensity);
      // Half a pixel of overlap on the right and bottom edges keeps a seam
      // from showing between adjacent cells at fractional widths.
      ctx.fillRect(x, y, w + 0.5, h + 0.5);
    }
  }
}

/**
 * The comparison view: the reference beside the fit, waveform and
 * spectrogram, drawn from Task 4's `/compare` payload.
 *
 * Every value that the two sides are meant to share -- the amplitude scale,
 * the dB range -- is computed once from both sides by `compareDrawing.ts`
 * and handed to both canvases; see that module for why. This component only
 * fetches the payload, reads the current theme, and turns the shared
 * numbers into pixels and fill colours.
 */
export function Comparison({ jobId, hasPreset }: ComparisonProps) {
  const [read, setRead] = useState<{
    jobId: string;
    compare: FitCompare | null;
    error: string | null;
  } | null>(null);

  useEffect(() => {
    if (jobId === null || !hasPreset) {
      return;
    }

    let cancelled = false;

    getFitJobCompare(jobId)
      .then((compare) => {
        if (!cancelled) {
          setRead({ jobId, compare, error: null });
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
              : "the comparison could not be read";

        setRead({ jobId, compare: null, error: message });
      });

    return () => {
      cancelled = true;
    };
  }, [jobId, hasPreset]);

  const theme = useResolvedTheme();
  const palette = readPalette(theme);

  const referenceWaveformRef = useRef<HTMLCanvasElement | null>(null);
  const renderWaveformRef = useRef<HTMLCanvasElement | null>(null);
  const referenceSpectrogramRef = useRef<HTMLCanvasElement | null>(null);
  const renderSpectrogramRef = useRef<HTMLCanvasElement | null>(null);

  const compare = read !== null && read.jobId === jobId ? read.compare : null;
  const range =
    compare !== null
      ? sharedAmplitudeRange(
          compare.reference.waveform,
          compare.render.waveform,
        )
      : null;
  const peakDb =
    compare?.floorDb !== undefined
      ? sharedSpectrogramPeakDb(
          compare.floorDb,
          compare.reference.spectrogram,
          compare.render.spectrogram,
        )
      : null;

  useEffect(() => {
    if (compare === null || range === null) {
      return;
    }

    const referenceCanvas = referenceWaveformRef.current;
    const renderCanvas = renderWaveformRef.current;

    if (referenceCanvas !== null) {
      drawWaveform(
        referenceCanvas,
        compare.reference.waveform,
        range,
        palette.referenceLine,
        palette.grid,
      );
    }

    if (renderCanvas !== null) {
      drawWaveform(
        renderCanvas,
        compare.render.waveform,
        range,
        palette.renderLine,
        palette.grid,
      );
    }

    if (
      compare.floorDb === undefined ||
      peakDb === null ||
      compare.reference.spectrogram === undefined ||
      compare.render.spectrogram === undefined
    ) {
      return;
    }

    const referenceSpectrogramCanvas = referenceSpectrogramRef.current;
    const renderSpectrogramCanvas = renderSpectrogramRef.current;

    if (referenceSpectrogramCanvas !== null) {
      drawSpectrogram(
        referenceSpectrogramCanvas,
        compare.reference.spectrogram,
        compare.floorDb,
        peakDb,
        palette.quiet,
        palette.loud,
      );
    }

    if (renderSpectrogramCanvas !== null) {
      drawSpectrogram(
        renderSpectrogramCanvas,
        compare.render.spectrogram,
        compare.floorDb,
        peakDb,
        palette.quiet,
        palette.loud,
      );
    }
  }, [compare, range, peakDb, palette]);

  if (jobId === null || !hasPreset) {
    return (
      <section className="fit-comparison" aria-labelledby="comparison-heading">
        <h3 id="comparison-heading">Comparison</h3>
        <p className="optimize-note">
          There is nothing to compare yet. The comparison becomes available as
          soon as the fit has reported once, even if it is later cancelled.
        </p>
      </section>
    );
  }

  if (read === null || read.jobId !== jobId) {
    return (
      <section className="fit-comparison" aria-labelledby="comparison-heading">
        <h3 id="comparison-heading">Comparison</h3>
        <p className="optimize-note">Loading the comparison…</p>
      </section>
    );
  }

  if (read.error !== null) {
    return (
      <section className="fit-comparison" aria-labelledby="comparison-heading">
        <h3 id="comparison-heading">Comparison</h3>
        <p className="fit-status-error">{read.error}</p>
      </section>
    );
  }

  if (compare === null) {
    // Unreachable in practice -- read.error is set whenever compare is not --
    // but typed so the render below can treat compare as present.
    return null;
  }

  const hasSpectrogram =
    compare.floorDb !== undefined &&
    compare.reference.spectrogram !== undefined &&
    compare.render.spectrogram !== undefined;

  return (
    <section className="fit-comparison" aria-labelledby="comparison-heading">
      <h3 id="comparison-heading">Comparison</h3>

      <p className="optimize-note">
        The reference the objective scored, and a render of the fitted preset,
        both drawn on one shared time axis, amplitude scale and dB range.
      </p>

      <div className="fit-comparison-columns">
        <div className="fit-comparison-side">
          <h4>Reference</h4>
          <canvas
            ref={referenceWaveformRef}
            className="fit-comparison-canvas"
            width={CANVAS_WIDTH}
            height={WAVEFORM_HEIGHT}
            role="img"
            aria-label={
              range !== null
                ? waveformSummary(
                    "Reference",
                    compare.seconds,
                    compare.reference.waveform,
                    range,
                  )
                : "Reference waveform"
            }
          />
        </div>

        <div className="fit-comparison-side">
          <h4>Fitted render</h4>
          <canvas
            ref={renderWaveformRef}
            className="fit-comparison-canvas"
            width={CANVAS_WIDTH}
            height={WAVEFORM_HEIGHT}
            role="img"
            aria-label={
              range !== null
                ? waveformSummary(
                    "Fitted render",
                    compare.seconds,
                    compare.render.waveform,
                    range,
                  )
                : "Fitted render waveform"
            }
          />
        </div>
      </div>

      {hasSpectrogram &&
        compare.floorDb !== undefined &&
        peakDb !== null &&
        compare.reference.spectrogram !== undefined &&
        compare.render.spectrogram !== undefined && (
          <div className="fit-comparison-columns">
            <div className="fit-comparison-side">
              <canvas
                ref={referenceSpectrogramRef}
                className="fit-comparison-canvas"
                width={CANVAS_WIDTH}
                height={SPECTROGRAM_HEIGHT}
                role="img"
                aria-label={spectrogramSummary(
                  "Reference",
                  compare.seconds,
                  compare.reference.spectrogram,
                  compare.floorDb,
                  peakDb,
                )}
              />
            </div>

            <div className="fit-comparison-side">
              <canvas
                ref={renderSpectrogramRef}
                className="fit-comparison-canvas"
                width={CANVAS_WIDTH}
                height={SPECTROGRAM_HEIGHT}
                role="img"
                aria-label={spectrogramSummary(
                  "Fitted render",
                  compare.seconds,
                  compare.render.spectrogram,
                  compare.floorDb,
                  peakDb,
                )}
              />
            </div>
          </div>
        )}

      {!hasSpectrogram && (
        <p className="optimize-note">
          The reference is too short for a spectrogram (shorter than one
          analysis frame), so only the waveforms are shown.
        </p>
      )}
    </section>
  );
}
