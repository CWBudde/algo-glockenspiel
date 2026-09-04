/**
 * Pure helpers behind `ParameterTable`.
 *
 * `optimizer.ParamCodec.DimensionNames` (internal/optimizer/params.go) names
 * every encoded dimension `"input_mix"`, `"filter_frequency"`,
 * `"modes[i].amplitude"`, `"modes[i].frequency"`, `"modes[i].decay_ms"` and
 * `"chebyshev.harmonic_gains[i]"`, and `ParamCodec.Pinned` reuses those exact
 * names for `PinnedDimension.name`. This module reconstructs the same names
 * from a decoded `BarParams` so a pinned dimension can be looked up rather
 * than re-derived: the name a search pins is the surface, not a value this
 * module invents.
 */

import type { BarParams, PinnedDimension } from "../../api/types";

/** One dimension's pin, once it has been looked up by name. */
export interface PinInfo {
  bound: "min" | "max";
  limit: number;
}

/** Indexes a snapshot's pinned dimensions by the codec's own name. */
export function indexPinned(
  pinned: readonly PinnedDimension[] | undefined,
): Map<string, PinInfo> {
  const map = new Map<string, PinInfo>();

  for (const dimension of pinned ?? []) {
    map.set(dimension.name, { bound: dimension.bound, limit: dimension.limit });
  }

  return map;
}

/** One mode, read as a mode rather than as three entries of a flat vector. */
export interface ModeRow {
  index: number;
  frequencyHz: number;
  frequencyPin: PinInfo | null;
  amplitude: number;
  amplitudePin: PinInfo | null;
  decayMs: number;
  decayPin: PinInfo | null;
}

/** Every mode of a fitted preset, in the order the preset already writes them. */
export function modeRows(
  parameters: BarParams,
  pinnedByName: Map<string, PinInfo>,
): ModeRow[] {
  return parameters.modes.map((mode, index) => ({
    index,
    frequencyHz: mode.frequency,
    frequencyPin: pinnedByName.get(`modes[${String(index)}].frequency`) ?? null,
    amplitude: mode.amplitude,
    amplitudePin: pinnedByName.get(`modes[${String(index)}].amplitude`) ?? null,
    decayMs: mode.decay_ms,
    decayPin: pinnedByName.get(`modes[${String(index)}].decay_ms`) ?? null,
  }));
}

/** One scalar (non-mode) parameter of the bar. */
export interface ScalarRow {
  key: string;
  label: string;
  value: number;
  unit: string;
  pin: PinInfo | null;
}

/**
 * The bar's own scalars: the dry/wet mix, the excitation lowpass, and the
 * base frequency the note was struck at.
 *
 * `base_frequency` is never a search dimension -- it is fixed by the note the
 * fit ran against, not fitted -- so it never carries a pin; it is included
 * here anyway because it is part of the preset a reader wants beside the
 * modes it seeded. `output_gain_db` is the same shape for the same reason: the
 * objective solves the level and subtracts it, so it is measured after the
 * search rather than found by it, and it never carries a pin either.
 */
export function scalarRows(
  parameters: BarParams,
  pinnedByName: Map<string, PinInfo>,
): ScalarRow[] {
  return [
    {
      key: "input_mix",
      label: "Input mix",
      value: parameters.input_mix,
      unit: "",
      pin: pinnedByName.get("input_mix") ?? null,
    },
    {
      key: "filter_frequency",
      label: "Filter frequency",
      value: parameters.filter_frequency,
      unit: "Hz",
      pin: pinnedByName.get("filter_frequency") ?? null,
    },
    {
      key: "base_frequency",
      label: "Base frequency",
      value: parameters.base_frequency,
      unit: "Hz",
      pin: null,
    },
    {
      key: "output_gain_db",
      label: "Output gain",
      value: parameters.output_gain_db ?? 0,
      unit: "dB",
      pin: null,
    },
  ];
}

/** One harmonic gain of the Chebyshev waveshaper. */
export interface HarmonicGainRow {
  index: number;
  value: number;
  pin: PinInfo | null;
}

/**
 * The waveshaper's harmonic gains, or an empty list when the stage is
 * disabled -- a preset that never turned it on has nothing here worth a row.
 */
export function harmonicGainRows(
  parameters: BarParams,
  pinnedByName: Map<string, PinInfo>,
): HarmonicGainRow[] {
  if (!parameters.chebyshev.enabled) {
    return [];
  }

  return parameters.chebyshev.harmonic_gains.map((value, index) => ({
    index,
    value,
    pin: pinnedByName.get(`chebyshev.harmonic_gains[${String(index)}]`) ?? null,
  }));
}

/** Six significant digits, the precision the rest of this UI reports values at. */
export function formatParamNumber(value: number): string {
  if (!Number.isFinite(value)) {
    return "-";
  }

  return value.toPrecision(6);
}

/**
 * The pin as words, so a screen reader (or a reader who cannot see colour)
 * gets the same fact a sighted reader gets from the badge: which edge the
 * search pushed against, and where that edge sits.
 */
export function describePin(pin: PinInfo): string {
  return `pinned at its ${pin.bound === "min" ? "lower" : "upper"} bound (${formatParamNumber(pin.limit)})`;
}
