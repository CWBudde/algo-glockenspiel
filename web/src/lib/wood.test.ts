import { describe, expect, it, vi } from "vitest";

import { applyWoodTexture, getWoodSpeciesOptions } from "./wood";

describe("wood textures", () => {
  it("exposes every baked species in preset order", () => {
    expect(getWoodSpeciesOptions()).toEqual([
      expect.objectContaining({ id: "beech", label: "Beech" }),
      expect.objectContaining({ id: "walnut", label: "Walnut" }),
      expect.objectContaining({ id: "oak", label: "Oak" }),
      expect.objectContaining({ id: "maple", label: "Maple" }),
    ]);
  });

  it("applies a static asset URL", () => {
    const setProperty = vi.fn();
    const target = {
      style: { setProperty },
      dataset: {},
    } as unknown as HTMLElement;

    applyWoodTexture(target, "oak");

    expect(setProperty).toHaveBeenCalledWith(
      "--wood-panel-texture",
      expect.stringMatching(/^url\(".*oak\.png"\)$/),
    );
    expect(target.dataset.woodSpecies).toBe("oak");
  });

  it("uses walnut by default", () => {
    const setProperty = vi.fn();
    const target = {
      style: { setProperty },
      dataset: {},
    } as unknown as HTMLElement;

    applyWoodTexture(target);

    expect(setProperty).toHaveBeenCalledWith(
      "--wood-panel-texture",
      expect.stringMatching(/^url\(".*walnut\.png"\)$/),
    );
    expect(target.dataset.woodSpecies).toBe("walnut");
  });

  it("falls back to walnut for an unknown species", () => {
    const setProperty = vi.fn();
    const target = {
      style: { setProperty },
      dataset: {},
    } as unknown as HTMLElement;

    applyWoodTexture(target, "ash");

    expect(setProperty).toHaveBeenCalledWith(
      "--wood-panel-texture",
      expect.stringMatching(/^url\(".*walnut\.png"\)$/),
    );
    expect(target.dataset.woodSpecies).toBe("walnut");
  });
});
