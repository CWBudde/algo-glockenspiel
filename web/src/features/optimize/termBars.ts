/**
 * Pure helpers behind `TermBars`.
 *
 * `optimizer.Metrics.Contributions` (internal/optimizer/metrics.go) is the
 * arithmetic a displayed bar must agree with: each measured term is divided
 * by its norm and passed through `x / (1 + x)`, then weighted and divided by
 * the total weight of the terms that were actually measured, so the shares
 * sum to the score. Nothing here re-derives a norm or a weight -- both travel
 * on `FitSnapshot.profile`, written by `profileEchoFor` in
 * internal/server/job.go from the very `optimizer.Profile` the score was
 * computed against -- so a norm that changes in Go changes what this module
 * scales by in the same release, rather than needing a matching edit here.
 */

import type { FitMetrics, FitProfile } from "../../api/types";

/** Mirrors `optimizer.saturate`: zero below zero, otherwise x/(1+x). */
function saturate(x: number): number {
  return x <= 0 ? 0 : x / (1 + x);
}

/** The reading-order label for one term, for the axis a bar sits on. */
const TERM_LABELS: Record<keyof FitMetrics, string> = {
  partial_cents: "Partial pitch",
  partial_level_db: "Partial level",
  partial_decay_octaves: "Partial decay",
  partial_missing: "Partial missing",
  partial_extra: "Partial extra",
  spectral_fine_db: "Spectral fine",
  spectral_coarse_db: "Spectral coarse",
  envelope_db: "Envelope",
  onset_db: "Onset",
  decay_slope_dbps: "Decay slope",
  waveform: "Waveform",
  gain_db: "Gain",
  waveform_gain_db: "Waveform gain",
  lag: "Lag",
  overlap: "Overlap",
  reference_partials: "Reference partials",
  model_partials: "Model partials",
  matched: "Matched",
};

/** The eleven terms a composite profile can weight, in `optimizer.Terms()` order. */
export const SCORE_TERMS: (keyof FitMetrics)[] = [
  "partial_cents",
  "partial_level_db",
  "partial_decay_octaves",
  "partial_missing",
  "partial_extra",
  "spectral_fine_db",
  "spectral_coarse_db",
  "envelope_db",
  "onset_db",
  "decay_slope_dbps",
  "waveform",
];

/** One term's contribution to the score, scaled the way the score scaled it. */
export interface TermContribution {
  term: keyof FitMetrics;
  label: string;
  unit?: string | undefined;
  value: number | null;
  weight: number;
  norm: number;
  /** The term after the norm and the saturation, in [0, 1). Zero when unmeasured. */
  scaled: number;
  /** Weight times scaled, over the total weight of the measured terms. */
  share: number;
  measured: boolean;
}

/** Whether a wire value is a real, finite measurement rather than an absent one. */
function isMeasured(value: number | null | undefined): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

/**
 * Breaks the score down term by term, in the profile's own reporting order.
 *
 * `profile.terms` already carries only the weighted terms -- `profileEchoFor`
 * skips a term of weight zero -- so every entry here is one that counted
 * toward the score, exactly as `Metrics.Contributions` builds its list.
 */
export function termContributions(
  metrics: FitMetrics,
  profile: FitProfile,
): TermContribution[] {
  const raw = profile.terms.map((term) => {
    const wire = metrics[term.term];
    const value = isMeasured(wire) ? wire : null;
    const scaled = value !== null && term.norm > 0 ? saturate(value / term.norm) : 0;

    return {
      term: term.term,
      label: TERM_LABELS[term.term],
      unit: term.unit,
      value,
      weight: term.weight,
      norm: term.norm,
      scaled,
      measured: value !== null,
    };
  });

  const weightTotal = raw.reduce(
    (sum, entry) => sum + (entry.measured ? entry.weight : 0),
    0,
  );

  return raw.map((entry) => ({
    ...entry,
    share:
      entry.measured && weightTotal > 0
        ? (entry.weight * entry.scaled) / weightTotal
        : 0,
  }));
}

/** One term shown with no scaling at all: the WASM-without-profile fallback. */
export interface RawTerm {
  term: keyof FitMetrics;
  label: string;
  value: number | null;
}

/**
 * Lists the ten score terms with their raw physical value and nothing else.
 *
 * Used only when a snapshot carries `metrics` but no `profile` -- the browser
 * worker's contract, which has no profile block to send. Inventing a norm or
 * a weight here to draw a bar anyway is exactly what the brief that produced
 * this module forbids: a term with nothing to scale it by is shown as a
 * number, not as a bar that would be scaled by a constant made up in
 * TypeScript.
 */
export function rawTerms(metrics: FitMetrics): RawTerm[] {
  return SCORE_TERMS.map((term) => ({
    term,
    label: TERM_LABELS[term],
    value: isMeasured(metrics[term]) ? metrics[term] : null,
  }));
}

/** Six significant digits, matching the rest of the fit UI's number formatting. */
export function formatTermValue(value: number | null): string {
  if (value === null || !Number.isFinite(value)) {
    return "-";
  }

  return value.toPrecision(6);
}

/** A term's share of the score, as a percentage string, or a dash if unmeasured. */
export function formatTermShare(contribution: TermContribution): string {
  if (!contribution.measured) {
    return "not measured";
  }

  return `${(contribution.share * 100).toFixed(1)}% of score`;
}
