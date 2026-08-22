import { describe, expect, it } from "vitest";

import { computeKeyMap, computeNoteLayout } from "./layout";

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
