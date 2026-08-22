import { describe, expect, it } from "vitest";

import {
  BAR_NODE_RATIO,
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
      viewportWhiteUnits: 7,
      viewportWidth: 308,
    });
  });

  it("derives the initial scroll from the requested pitch width", () => {
    const layout = computePlayfieldLayout(52, 8);

    expect(layout.initialScrollLeft).toBe(728);
    expect(layout.viewportWidth).toBe(416);
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
    expect(new Set(accidentalWidths)).toEqual(new Set([32]));
    expect(new Set([...naturalWidths, ...accidentalWidths])).toEqual(
      new Set([32]),
    );
  });

  it.each<BarKind>(["natural", "accidental"])(
    "centers the %s row so its top and bottom form a symmetric trapezoid",
    (kind) => {
      const geometries = row(kind).map((entry) =>
        computeBarGeometry(entry, kind),
      );

      for (let index = 1; index < geometries.length; index += 1) {
        expect(geometries[index].top).toBeGreaterThan(
          geometries[index - 1].top,
        );
        expect(geometries[index].baseline).toBeLessThan(
          geometries[index - 1].baseline,
        );
      }

      const first = geometries[0];
      const last = geometries.at(-1)!;
      expect(last.top - first.top).toBeCloseTo(
        first.baseline - last.baseline,
      );
      expect(
        new Set(geometries.map(({ top, baseline }) => top + baseline)).size,
      ).toBe(1);
    },
  );

  it.each<BarKind>(["natural", "accidental"])(
    "passes both %s supports through the two fundamental node points",
    (kind) => {
      const entries = row(kind);
      const supportGeometry = computeBarSupportGeometry(
        entries,
        kind,
        layout.totalWhiteUnits,
      );

      expect(supportGeometry.supports.map(({ position }) => position)).toEqual([
        "upper",
        "lower",
      ]);
      supportGeometry.supports.forEach((support, mountIndex) => {
        expect(support.points).toHaveLength(entries.length);
        support.points.forEach((point, index) => {
          const entry = entries[index];
          const geometry = computeBarGeometry(entry, kind);

          expect(point.note).toBe(entry.note);
          expect(point.y).toBeCloseTo(geometry.mountCenterYs[mountIndex]);
          expect(point.x).toBeCloseTo(
            (entry.center / layout.totalWhiteUnits) * 100,
          );
          const distanceFromNearestEnd =
            mountIndex === 0
              ? point.y - geometry.top
              : geometry.baseline - point.y;
          expect(distanceFromNearestEnd / entry.length).toBeCloseTo(
            BAR_NODE_RATIO,
          );
        });
      });
    },
  );
});
