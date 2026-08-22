import { describe, expect, it } from "vitest";

import {
  computeKeyMap,
  computeNoteLayout,
  computePlayfieldLayout,
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
