import { describe, expect, it } from "vitest";

import { readFittedPreset } from "./fittedPreset";

const document = JSON.stringify({
  version: 2,
  name: "Fitted Bar",
  note: 72,
  parameters: { input_mix: 1 },
});

describe("readFittedPreset", () => {
  it("names an entry after the document and the job that produced it", () => {
    const fitted = readFittedPreset(document, "fit-3", 1);

    expect(fitted.label).toBe("Fitted Bar · fit-3");
    expect(fitted.note).toBe(72);
    expect(fitted.document).toBe(document);
  });

  it("gives every registration its own id, job id or not", () => {
    const first = readFittedPreset(document, "fit-3", 1);
    const second = readFittedPreset(document, "fit-3", 2);

    expect(first.id).not.toBe(second.id);
    expect(readFittedPreset(document, null, 4).label).toBe(
      "Fitted Bar · fit 4",
    );
  });

  it("never takes an id a built-in sound already answers to", () => {
    // The module refuses an id that shadows an embedded preset, so this is the
    // page's half of that contract rather than a cosmetic prefix.
    expect(readFittedPreset(document, "fit-1", 1).id).toBe("fitted-1");
  });

  it("falls back when the document names neither a sound nor a note", () => {
    const fitted = readFittedPreset(
      JSON.stringify({ parameters: {}, name: "  ", note: 999 }),
      "fit-1",
      1,
    );

    expect(fitted.label).toBe("Fitted preset · fit-1");
    expect(fitted.note).toBe(69);
  });

  it("refuses what is not a preset document", () => {
    expect(() => readFittedPreset("not json", "fit-1", 1)).toThrow(
      /not valid JSON/,
    );
    expect(() => readFittedPreset("[]", "fit-1", 1)).toThrow(/no parameters/);
    expect(() => readFittedPreset('{"name":"x"}', "fit-1", 1)).toThrow(
      /no parameters/,
    );
  });
});
