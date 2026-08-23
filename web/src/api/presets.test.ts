import { describe, expect, it } from "vitest";

import { DEFAULT_SOUND_PRESET_ID, SOUND_PRESETS } from "./presets.generated";

// The table is generated, so these are not assertions about its contents. They
// are the two properties the picker depends on and that a regeneration could
// quietly break -- the file is written from a directory listing, and neither
// the generator nor the Go side has a reason to notice either one.
describe("SOUND_PRESETS", () => {
  it("offers more than one sound", () => {
    expect(SOUND_PRESETS.length).toBeGreaterThan(1);
  });

  it("has unique ids", () => {
    const ids = SOUND_PRESETS.map((preset) => preset.id);

    expect(new Set(ids).size).toBe(ids.length);
  });

  it("has a label for every sound", () => {
    for (const preset of SOUND_PRESETS) {
      expect(preset.label).not.toBe("");
    }
  });

  // Without this the select would mount with a value none of its options
  // carries, which browsers render as a blank field rather than as an error.
  it("contains the default the picker starts on", () => {
    const ids = SOUND_PRESETS.map((preset) => preset.id);

    expect(ids).toContain(DEFAULT_SOUND_PRESET_ID);
  });
});
