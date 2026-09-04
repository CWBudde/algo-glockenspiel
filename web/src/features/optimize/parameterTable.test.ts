import { describe, expect, it } from "vitest";

import type { BarParams, PinnedDimension } from "../../api/types";
import {
  describePin,
  formatParamNumber,
  harmonicGainRows,
  indexPinned,
  modeRows,
  scalarRows,
} from "./parameterTable";

function barParams(overrides?: Partial<BarParams>): BarParams {
  return {
    input_mix: 0.5,
    filter_frequency: 4000,
    base_frequency: 440,
    modes: [
      { amplitude: 1, frequency: 440, decay_ms: 300 },
      { amplitude: 0.4, frequency: 880.3, decay_ms: 120 },
    ],
    chebyshev: { enabled: false, harmonic_gains: [] },
    ...overrides,
  };
}

describe("indexPinned", () => {
  it("indexes by the codec's own dimension name", () => {
    const pinned: PinnedDimension[] = [
      { name: "modes[1].frequency", value: 880.3, bound: "max", limit: 900 },
    ];

    const map = indexPinned(pinned);

    expect(map.get("modes[1].frequency")).toEqual({ bound: "max", limit: 900 });
    expect(map.get("modes[0].frequency")).toBeUndefined();
  });

  it("is empty for an undefined list, rather than throwing", () => {
    expect(indexPinned(undefined).size).toBe(0);
  });
});

describe("modeRows", () => {
  it("reads as modes: one row per mode, each carrying its own pin", () => {
    const pinnedByName = indexPinned([
      { name: "modes[0].decay_ms", value: 300, bound: "min", limit: 300 },
    ]);

    const rows = modeRows(barParams(), pinnedByName);

    expect(rows).toHaveLength(2);
    expect(rows[0]).toMatchObject({
      index: 0,
      frequencyHz: 440,
      amplitude: 1,
      decayMs: 300,
      frequencyPin: null,
      amplitudePin: null,
    });
    expect(rows[0].decayPin).toEqual({ bound: "min", limit: 300 });
    expect(rows[1].decayPin).toBeNull();
  });
});

describe("scalarRows", () => {
  it("carries base_frequency with no pin, since it is never a search dimension", () => {
    const rows = scalarRows(barParams(), indexPinned(undefined));
    const base = rows.find((row) => row.key === "base_frequency");

    expect(base?.pin).toBeNull();
    expect(base?.unit).toBe("Hz");
  });

  it("picks up a pin on input_mix or filter_frequency by name", () => {
    const pinnedByName = indexPinned([
      { name: "filter_frequency", value: 20000, bound: "max", limit: 20000 },
    ]);

    const rows = scalarRows(barParams(), pinnedByName);

    expect(rows.find((row) => row.key === "filter_frequency")?.pin).toEqual({
      bound: "max",
      limit: 20000,
    });
    expect(rows.find((row) => row.key === "input_mix")?.pin).toBeNull();
  });
});

describe("harmonicGainRows", () => {
  it("is empty when the Chebyshev stage is disabled", () => {
    expect(harmonicGainRows(barParams(), indexPinned(undefined))).toEqual([]);
  });

  it("lists each gain, pinned or not, when the stage is enabled", () => {
    const parameters = barParams({
      chebyshev: { enabled: true, harmonic_gains: [0.1, 2] },
    });
    const pinnedByName = indexPinned([
      { name: "chebyshev.harmonic_gains[1]", value: 2, bound: "max", limit: 2 },
    ]);

    const rows = harmonicGainRows(parameters, pinnedByName);

    expect(rows).toEqual([
      { index: 0, value: 0.1, pin: null },
      { index: 1, value: 2, pin: { bound: "max", limit: 2 } },
    ]);
  });
});

describe("formatParamNumber", () => {
  it("renders six significant digits", () => {
    expect(formatParamNumber(880.3)).toBe("880.300");
  });

  it("is a dash for a non-finite value", () => {
    expect(formatParamNumber(Number.NaN)).toBe("-");
  });
});

describe("describePin", () => {
  it("names the edge in words, not just a colour", () => {
    expect(describePin({ bound: "min", limit: 20 })).toBe(
      "pinned at its lower bound (20.0000)",
    );
    expect(describePin({ bound: "max", limit: 20000 })).toBe(
      "pinned at its upper bound (20000.0)",
    );
  });
});
