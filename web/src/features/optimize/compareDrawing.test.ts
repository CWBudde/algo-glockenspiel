import { describe, expect, it } from "vitest";

import type { FitSpectrogram, FitWaveform } from "../../api/types";
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
} from "./compareDrawing";

function waveform(min: number[], max: number[]): FitWaveform {
  return { columns: min.length, min, max };
}

function spectrogram(peakDb: number): FitSpectrogram {
  return {
    frames: 2,
    bins: 2,
    frameSize: 2048,
    hop: 512,
    peakDb,
    maxHz: 12000,
    db: [
      [-40, -20],
      [-30, peakDb],
    ],
  };
}

describe("sharedAmplitudeRange", () => {
  it("is symmetric about the louder side's peak, whichever side that is", () => {
    const reference = waveform([-0.2, -0.1], [0.2, 0.1]);
    const render = waveform([-0.8, -0.3], [0.5, 0.3]);

    expect(sharedAmplitudeRange(reference, render)).toEqual({
      min: -0.8,
      max: 0.8,
    });
  });

  it("falls back to +/-1 for a silent pair rather than a zero-width range", () => {
    const silence = waveform([0, 0], [0, 0]);

    expect(sharedAmplitudeRange(silence, silence)).toEqual({
      min: -1,
      max: 1,
    });
  });

  it("ignores a non-finite sample rather than letting it dominate the range", () => {
    const reference = waveform([-0.3], [0.3]);
    const render = waveform([Number.NaN], [Number.POSITIVE_INFINITY]);

    expect(sharedAmplitudeRange(reference, render)).toEqual({
      min: -0.3,
      max: 0.3,
    });
  });
});

describe("waveformPoints", () => {
  it("centres each column and maps the shared range to the full height", () => {
    const points = waveformPoints(waveform([-1, 0], [1, 0]), 100, 50, {
      min: -1,
      max: 1,
    });

    expect(points).toHaveLength(2);
    // Column 0 spans [0, 50), centred at x = 25; column 1 at x = 75.
    expect(points[0].x).toBeCloseTo(25);
    expect(points[1].x).toBeCloseTo(75);
    // -1 maps to the bottom (y = height), +1 to the top (y = 0).
    expect(points[0].yMin).toBeCloseTo(50);
    expect(points[0].yMax).toBeCloseTo(0);
    // A silent column sits on the centre line.
    expect(points[1].yMin).toBeCloseTo(25);
    expect(points[1].yMax).toBeCloseTo(25);
  });

  it("puts the same span of two differently-sized waveforms at the same x range", () => {
    // A four-column side and an eight-column side covering the same seconds:
    // column 1 of 4 (the second quarter, [25, 50) of the width) must span
    // exactly what columns 2 and 3 of 8 span together, because both sides
    // cover the same total time and neither is stretched to the other's
    // column count.
    const four = waveformPoints(waveform([0, 0, 0, 0], [0, 0, 0, 0]), 100, 10, {
      min: -1,
      max: 1,
    });
    const eight = waveformPoints(
      waveform([0, 0, 0, 0, 0, 0, 0, 0], [0, 0, 0, 0, 0, 0, 0, 0]),
      100,
      10,
      { min: -1, max: 1 },
    );

    const fourCellWidth = 100 / 4;
    const eightCellWidth = 100 / 8;
    const fourColumn1Left = four[1].x - fourCellWidth / 2;
    const fourColumn1Right = four[1].x + fourCellWidth / 2;
    const eightColumn2Left = eight[2].x - eightCellWidth / 2;
    const eightColumn3Right = eight[3].x + eightCellWidth / 2;

    expect(eightColumn2Left).toBeCloseTo(fourColumn1Left);
    expect(eightColumn3Right).toBeCloseTo(fourColumn1Right);
  });

  it("answers no points for an empty waveform rather than dividing by zero", () => {
    expect(
      waveformPoints(waveform([], []), 100, 50, { min: -1, max: 1 }),
    ).toEqual([]);
  });
});

describe("sharedSpectrogramPeakDb", () => {
  it("takes the louder of the two sides' peaks", () => {
    expect(
      sharedSpectrogramPeakDb(-60, spectrogram(-10), spectrogram(-25)),
    ).toBe(-10);
    expect(
      sharedSpectrogramPeakDb(-60, spectrogram(-25), spectrogram(-10)),
    ).toBe(-10);
  });

  it("falls back to the floor when neither side has a spectrogram", () => {
    expect(sharedSpectrogramPeakDb(-60, undefined, undefined)).toBe(-60);
  });

  it("uses whichever one side has a spectrogram", () => {
    expect(sharedSpectrogramPeakDb(-60, spectrogram(-12), undefined)).toBe(-12);
  });
});

describe("dbToIntensity", () => {
  it("maps the floor to 0 and the peak to 1", () => {
    expect(dbToIntensity(-60, -60, -10)).toBe(0);
    expect(dbToIntensity(-10, -60, -10)).toBe(1);
    expect(dbToIntensity(-35, -60, -10)).toBeCloseTo(0.5);
  });

  it("clamps a value outside the range instead of extrapolating", () => {
    expect(dbToIntensity(-70, -60, -10)).toBe(0);
    expect(dbToIntensity(0, -60, -10)).toBe(1);
  });

  it("treats a degenerate (zero-span) range as fully on or off", () => {
    expect(dbToIntensity(-30, -30, -30)).toBe(0);
    expect(dbToIntensity(-20, -30, -30)).toBe(1);
  });
});

describe("spectrogramColumnExtent and spectrogramRowExtent", () => {
  it("divides the width into equal columns left to right", () => {
    expect(spectrogramColumnExtent(0, 4, 100)).toEqual({ x: 0, w: 25 });
    expect(spectrogramColumnExtent(3, 4, 100)).toEqual({ x: 75, w: 25 });
  });

  it("places row 0 at the bottom, since row 0 is the lowest frequency", () => {
    expect(spectrogramRowExtent(0, 4, 100)).toEqual({ y: 75, h: 25 });
    expect(spectrogramRowExtent(3, 4, 100)).toEqual({ y: 0, h: 25 });
  });

  it("answers an empty extent rather than dividing by zero", () => {
    expect(spectrogramColumnExtent(0, 0, 100)).toEqual({ x: 0, w: 0 });
    expect(spectrogramRowExtent(0, 0, 100)).toEqual({ y: 0, h: 0 });
  });
});

describe("lerpColor", () => {
  it("interpolates channel by channel", () => {
    expect(lerpColor([0, 0, 0], [200, 100, 50], 0.5)).toBe("rgb(100, 50, 25)");
  });

  it("clamps t outside [0, 1]", () => {
    expect(lerpColor([10, 10, 10], [20, 20, 20], -1)).toBe("rgb(10, 10, 10)");
    expect(lerpColor([10, 10, 10], [20, 20, 20], 2)).toBe("rgb(20, 20, 20)");
  });
});

describe("waveformSummary and spectrogramSummary", () => {
  it("reports the shared scale, not a side's own extremes", () => {
    const text = waveformSummary("Reference", 2.5, waveform([-0.2], [0.2]), {
      min: -0.8,
      max: 0.8,
    });

    expect(text).toContain("2.50 s");
    expect(text).toContain("±0.80");
    expect(text).toContain("±0.20");
  });

  it("reports the shared dB range alongside this side's own peak", () => {
    const text = spectrogramSummary("Render", 2.5, spectrogram(-18), -60, -10);

    expect(text).toContain("-60.0 to -10.0 dB");
    expect(text).toContain("-18.0 dB");
    expect(text).toContain("12000 Hz");
  });
});
