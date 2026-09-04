/**
 * Payload-to-pixel transforms for the comparison view.
 *
 * Everything here is a pure function: given a `FitCompare` payload (or one of
 * its sides), it works out where a mark belongs, never how it looks. Color
 * and the DOM stay in `Comparison.tsx`, which is what keeps this module
 * testable without a canvas or a browser.
 *
 * The one rule every function here exists to uphold: a value that the two
 * sides are meant to share -- the amplitude scale, the dB range -- is
 * computed from *both* sides together and handed back as one range for both
 * to be drawn against. Nothing in this module normalises a side against its
 * own extremes; that is exactly the bug `internal/server/compare.go` was
 * written to prevent, and drawing it independently here would reintroduce it
 * on the canvas.
 */

import type { FitSpectrogram, FitWaveform } from "../../api/types";

/** A symmetric amplitude scale both waveforms are drawn against. */
export interface AmplitudeRange {
  min: number;
  max: number;
}

/**
 * The amplitude scale both waveform canvases share.
 *
 * Symmetric about zero, because a waveform envelope is a min and a max and a
 * scale that was not symmetric would draw the same sample's positive and
 * negative excursions at different distances from the centre line. Computed
 * from both sides at once: a render quieter than its reference is drawn
 * smaller, truthfully, rather than stretched to fill its own canvas the way
 * an independently normalised waveform would be.
 *
 * A silent pair (every sample zero, or no samples at all) falls back to
 * +/-1 so the range has a nonzero span to divide by; the canvas then shows a
 * flat line at the centre, which is what a silent signal actually is.
 */
export function sharedAmplitudeRange(
  reference: FitWaveform,
  render: FitWaveform,
): AmplitudeRange {
  let peak = 0;

  for (const values of [reference.min, reference.max, render.min, render.max]) {
    for (const value of values) {
      if (Number.isFinite(value)) {
        peak = Math.max(peak, Math.abs(value));
      }
    }
  }

  if (peak === 0) {
    peak = 1;
  }

  return { min: -peak, max: peak };
}

/** One column's envelope, in pixel space. */
export interface WaveformPoint {
  x: number;
  yMin: number;
  yMax: number;
}

/**
 * Places one waveform's columns in pixel space.
 *
 * `width` and `height` are the canvas's own drawing size, not CSS pixels.
 * Column `i` of `columns` many is centred at `(i + 0.5) / columns` of the
 * width: since both sides are drawn at this same width and both waveforms
 * cover the same `seconds` (`internal/server/compare.go`'s `fitCompare.
 * Seconds`), a column at a given fraction of its own count lands at the same
 * x on both canvases whether or not the two sides happened to get the same
 * column count -- which is what makes this the shared time axis. `range` is
 * `sharedAmplitudeRange`'s result, so the same amplitude maps to the same y
 * on both sides too.
 *
 * An empty waveform (zero columns) answers no points, which the caller draws
 * as nothing rather than a divide-by-zero.
 */
export function waveformPoints(
  waveform: FitWaveform,
  width: number,
  height: number,
  range: AmplitudeRange,
): WaveformPoint[] {
  const columns = waveform.columns;

  if (columns <= 0) {
    return [];
  }

  const span = range.max - range.min || 1;
  const points: WaveformPoint[] = new Array<WaveformPoint>(columns);

  for (let column = 0; column < columns; column += 1) {
    const x = ((column + 0.5) / columns) * width;
    const yMin = height - ((waveform.min[column] - range.min) / span) * height;
    const yMax = height - ((waveform.max[column] - range.min) / span) * height;

    points[column] = { x, yMin, yMax };
  }

  return points;
}

/**
 * The dB range both spectrogram canvases are painted against.
 *
 * The floor is the comparison's own -- the reference's noise-aware floor,
 * already clamped into every value on both sides by `reduceSpectrogram` --
 * and is passed straight through. The top is the louder of the two sides'
 * own peaks, not either side's alone: using one side's peak as the top of
 * the shared range would still let that side use the whole colour range
 * while the other, if quieter, was compressed into a fraction of it and the
 * other way around, so the picture would show a loudness difference that
 * is really just a difference in which side happened to set the scale.
 * Taking the louder of the two keeps a quieter render visibly and correctly
 * quieter rather than independently normalised to look just as loud.
 *
 * A side with no spectrogram (too short for one analysis frame) contributes
 * nothing to the peak; if neither side has one, the floor itself is
 * returned, which draws a canvas of nothing but the floor colour -- correct
 * for a range that carries no data.
 */
export function sharedSpectrogramPeakDb(
  floorDb: number,
  reference: FitSpectrogram | undefined,
  render: FitSpectrogram | undefined,
): number {
  let peak = floorDb;

  if (reference !== undefined) {
    peak = Math.max(peak, reference.peakDb);
  }

  if (render !== undefined) {
    peak = Math.max(peak, render.peakDb);
  }

  return peak;
}

/** How loud one dB value reads on the shared [floorDb, peakDb] scale, 0 to 1. */
export function dbToIntensity(
  db: number,
  floorDb: number,
  peakDb: number,
): number {
  const span = peakDb - floorDb;

  if (!(span > 0)) {
    return db > floorDb ? 1 : 0;
  }

  const t = (db - floorDb) / span;

  return t < 0 ? 0 : t > 1 ? 1 : t;
}

/** One spectrogram column's horizontal extent in pixel space. */
export function spectrogramColumnExtent(
  column: number,
  frames: number,
  width: number,
): { x: number; w: number } {
  if (frames <= 0) {
    return { x: 0, w: 0 };
  }

  const x = (column / frames) * width;
  const w = width / frames;

  return { x, w };
}

/**
 * One spectrogram row's vertical extent in pixel space.
 *
 * Row 0 is the lowest frequency (`internal/server/compare.go`'s `fitSpectrogram.
 * DB` documents row `r` as covering `r * maxHz / bins` upwards), and a
 * spectrogram is drawn with low frequencies at the bottom, so row 0 is placed
 * at the bottom of the canvas and the row index counts upward from there.
 */
export function spectrogramRowExtent(
  row: number,
  bins: number,
  height: number,
): { y: number; h: number } {
  if (bins <= 0) {
    return { y: 0, h: 0 };
  }

  const h = height / bins;
  const y = height - (row + 1) * h;

  return { y, h };
}

/** Linearly interpolates between two RGB colours at `t` in [0, 1]. */
export function lerpColor(
  from: readonly [number, number, number],
  to: readonly [number, number, number],
  t: number,
): string {
  const clamped = t < 0 ? 0 : t > 1 ? 1 : t;
  const r = Math.round(from[0] + (to[0] - from[0]) * clamped);
  const g = Math.round(from[1] + (to[1] - from[1]) * clamped);
  const b = Math.round(from[2] + (to[2] - from[2]) * clamped);

  return `rgb(${String(r)}, ${String(g)}, ${String(b)})`;
}

/** Rounds a number to a fixed number of decimal places, for display text. */
function fixed(value: number, digits: number): string {
  return Number.isFinite(value) ? value.toFixed(digits) : "-";
}

/**
 * The text alternative for one waveform canvas: the time axis, the shared
 * amplitude scale, and this side's own peak -- everything a sighted reader
 * gets from the picture, spelled out for `aria-label`.
 */
export function waveformSummary(
  label: string,
  seconds: number,
  waveform: FitWaveform,
  range: AmplitudeRange,
): string {
  let peak = 0;

  for (const value of [...waveform.min, ...waveform.max]) {
    if (Number.isFinite(value)) {
      peak = Math.max(peak, Math.abs(value));
    }
  }

  return (
    `${label} waveform over ${fixed(seconds, 2)} s, drawn on a shared ` +
    `amplitude scale of ±${fixed(range.max, 2)} full scale. ` +
    `This side peaks at ±${fixed(peak, 2)}.`
  );
}

/**
 * The text alternative for one spectrogram canvas: the frequency and time
 * axes, the shared dB range, and this side's own peak level.
 */
export function spectrogramSummary(
  label: string,
  seconds: number,
  spectrogram: FitSpectrogram,
  floorDb: number,
  peakDb: number,
): string {
  return (
    `${label} spectrogram over ${fixed(seconds, 2)} s and 0 to ` +
    `${fixed(spectrogram.maxHz, 0)} Hz, painted on a shared range of ` +
    `${fixed(floorDb, 1)} to ${fixed(peakDb, 1)} dB. This side's loudest ` +
    `frame reaches ${fixed(spectrogram.peakDb, 1)} dB.`
  );
}
