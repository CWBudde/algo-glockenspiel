import { describe, expect, it } from "vitest";

import type { FitMetrics, FitProfile } from "../../api/types";
import {
  SCORE_TERMS,
  formatTermShare,
  formatTermValue,
  rawTerms,
  termContributions,
} from "./termBars";

function metrics(overrides?: Partial<FitMetrics>): FitMetrics {
  return {
    partial_cents: 5,
    partial_level_db: 3,
    partial_decay_octaves: 0.25,
    partial_missing: 0,
    partial_extra: 0,
    spectral_fine_db: 10,
    spectral_coarse_db: null,
    envelope_db: 1.5,
    onset_db: 12,
    decay_slope_dbps: 2,
    waveform: 0.1,
    gain_db: 0,
    waveform_gain_db: -40,
    lag: 0,
    overlap: 1000,
    reference_partials: 8,
    model_partials: 8,
    matched: 8,
    ...overrides,
  };
}

// Mirrors ProfileBalanced's shape (internal/optimizer/metrics.go) closely
// enough to exercise the arithmetic, without pinning this test to the exact
// weights, which are Go's to change.
const profile: FitProfile = {
  name: "balanced",
  terms: [
    { term: "partial_cents", weight: 0.5, norm: 10 },
    { term: "spectral_fine_db", weight: 0.3, norm: 10 },
    { term: "spectral_coarse_db", weight: 0.2, norm: 10 },
  ],
};

describe("termContributions", () => {
  it("scales a measured term by x/(1+x) against the transmitted norm", () => {
    const [cents] = termContributions(metrics(), profile);

    // 5 / 10 = 0.5; 0.5 / 1.5 = 1/3.
    expect(cents.scaled).toBeCloseTo(1 / 3, 10);
    expect(cents.measured).toBe(true);
  });

  it("renormalises weight over only the measured terms, so shares sum to the score", () => {
    const contributions = termContributions(metrics(), profile);
    const totalShare = contributions.reduce((sum, c) => sum + c.share, 0);

    // spectral_coarse_db is null in `metrics()`, so its 0.2 weight drops out
    // of the denominator and the other two shares still sum to the whole
    // measured score -- the same renormalisation optimizer.Metrics.Score does.
    const coarse = contributions.find((c) => c.term === "spectral_coarse_db");

    expect(coarse?.measured).toBe(false);
    expect(coarse?.share).toBe(0);

    // Recompute independently, the way optimizer.Metrics.Score would.
    const weightTotal = 0.5 + 0.3;
    const expected =
      (0.5 * (5 / 10 / (1 + 5 / 10)) + 0.3 * (10 / 10 / (1 + 10 / 10))) /
      weightTotal;

    expect(totalShare).toBeCloseTo(expected, 10);
  });

  it("zeroes every measure of an unmeasured term rather than inventing one", () => {
    const contributions = termContributions(metrics(), profile);
    const coarse = contributions.find((c) => c.term === "spectral_coarse_db");

    expect(coarse).toMatchObject({ value: null, scaled: 0, share: 0, measured: false });
  });

  it("keeps the profile's own reporting order", () => {
    const contributions = termContributions(metrics(), profile);

    expect(contributions.map((c) => c.term)).toEqual([
      "partial_cents",
      "spectral_fine_db",
      "spectral_coarse_db",
    ]);
  });
});

describe("rawTerms", () => {
  it("lists every score term with its raw value and no scaling", () => {
    const terms = rawTerms(metrics());
    const cents = terms.find((t) => t.term === "partial_cents");

    expect(cents?.value).toBe(5);

    // Against SCORE_TERMS rather than a count, so that adding a term to the
    // objective fails here only when rawTerms actually drops it.
    expect(terms.map((t) => t.term)).toEqual(SCORE_TERMS);
  });

  it("reports an unmeasured term as null rather than zero", () => {
    const terms = rawTerms(metrics());
    const coarse = terms.find((t) => t.term === "spectral_coarse_db");

    expect(coarse?.value).toBeNull();
  });
});

describe("formatTermValue", () => {
  it("renders six significant digits, and a dash for the unmeasured", () => {
    expect(formatTermValue(5)).toBe("5.00000");
    expect(formatTermValue(null)).toBe("-");
  });
});

describe("formatTermShare", () => {
  it("reads as a percentage of the score for a measured term", () => {
    const [cents] = termContributions(metrics(), profile);

    expect(formatTermShare(cents)).toMatch(/% of score$/);
  });

  it("says a term was not measured rather than showing 0%", () => {
    const contributions = termContributions(metrics(), profile);
    const coarse = contributions.find((c) => c.term === "spectral_coarse_db");

    expect(coarse && formatTermShare(coarse)).toBe("not measured");
  });
});
