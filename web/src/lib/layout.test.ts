import { describe, expect, it } from "vitest";

import {
  BAR_PERSPECTIVE_PX,
  computeBarGeometry,
  computeBarSupportGeometry,
  computeKeyMap,
  computeNoteLayout,
  computePlayfieldLayout,
  type BarEntry,
  type BarKind,
} from "./layout";

describe("computer-keyboard hints", () => {
  it("labels every bound bar with the key that strikes its note", () => {
    const layout = computeNoteLayout();
    const keyMap = computeKeyMap();
    const bars = [...layout.naturals, ...layout.accidentals];

    for (const bar of bars) {
      if (bar.keyHint !== "") {
        expect(keyMap.get(bar.keyHint), bar.name).toBe(bar.note);
      }
    }

    expect(bars.filter((bar) => bar.keyHint !== "")).toHaveLength(keyMap.size);
  });
});

describe("mobile playfield geometry", () => {
  it("aligns the 15-unit C4-C6 rack inside the 36-unit C2-C7 keyboard", () => {
    expect(computePlayfieldLayout()).toEqual({
      whiteUnitPx: 44,
      totalWhiteUnits: 36,
      rackWhiteUnits: 15,
      rackOffsetWhiteUnits: 14,
      initialScrollLeft: 616,
    });
  });

  it("derives the initial scroll from the requested pitch width", () => {
    expect(computePlayfieldLayout(52).initialScrollLeft).toBe(728);
  });
});

describe("rack depth geometry", () => {
  const layout = computeNoteLayout();

  function row(kind: BarKind): readonly BarEntry[] {
    return kind === "natural" ? layout.naturals : layout.accidentals;
  }

  it("keeps one constant width per material row", () => {
    const naturalWidths = layout.naturals.map(
      (entry) => computeBarGeometry(entry, "natural").width,
    );
    const accidentalWidths = layout.accidentals.map(
      (entry) => computeBarGeometry(entry, "accidental").width,
    );

    expect(new Set(naturalWidths)).toEqual(new Set([32]));
    expect(new Set(accidentalWidths)).toEqual(new Set([28]));
  });

  it.each<BarKind>(["natural", "accidental"])(
    "drops the %s baseline monotonically by the perspective depth",
    (kind) => {
      const geometries = row(kind).map((entry) =>
        computeBarGeometry(entry, kind),
      );

      for (let index = 1; index < geometries.length; index += 1) {
        expect(geometries[index].baseline).toBeGreaterThan(
          geometries[index - 1].baseline,
        );
      }
      expect(geometries.at(-1)!.baseline - geometries[0].baseline).toBeCloseTo(
        BAR_PERSPECTIVE_PX,
      );
    },
  );

  it.each<BarKind>(["natural", "accidental"])(
    "passes the %s support through every mount center",
    (kind) => {
      const entries = row(kind);
      const support = computeBarSupportGeometry(
        entries,
        kind,
        layout.totalWhiteUnits,
      );

      expect(support.points).toHaveLength(entries.length);
      support.points.forEach((point, index) => {
        const geometry = computeBarGeometry(entries[index], kind);

        expect(point.note).toBe(entries[index].note);
        expect(point.y).toBeCloseTo(geometry.mountCenterY);
        expect(point.x).toBeCloseTo(
          (entries[index].center / layout.totalWhiteUnits) * 100,
        );
      });
    },
  );
});
