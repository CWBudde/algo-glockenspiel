import { readFileSync } from "node:fs";

import { describe, expect, it, vi } from "vitest";

import {
  applyPreference,
  isThemePreference,
  readStoredPreference,
  resolvePreference,
  storePreference,
  THEME_PREFERENCES,
  THEME_STORAGE_KEY,
} from "./theme";

function fakeStorage(initial: Record<string, string> = {}) {
  const values = new Map(Object.entries(initial));

  return {
    values,
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => {
      values.set(key, value);
    },
    removeItem: (key: string) => {
      values.delete(key);
    },
  };
}

function fakeRoot() {
  const attributes = new Map<string, string>();

  return {
    attributes,
    setAttribute: (name: string, value: string) => {
      attributes.set(name, value);
    },
    removeAttribute: (name: string) => {
      attributes.delete(name);
    },
  } as unknown as Element & { attributes: Map<string, string> };
}

describe("theme preferences", () => {
  it("offers auto, light and dark in switch order", () => {
    expect(THEME_PREFERENCES).toEqual(["auto", "light", "dark"]);
  });

  it("accepts only the three known preferences", () => {
    expect(THEME_PREFERENCES.every(isThemePreference)).toBe(true);
    expect(isThemePreference("system")).toBe(false);
    expect(isThemePreference(null)).toBe(false);
  });

  it("resolves auto against the system and otherwise ignores it", () => {
    expect(resolvePreference("auto", true)).toBe("dark");
    expect(resolvePreference("auto", false)).toBe("light");
    expect(resolvePreference("light", true)).toBe("light");
    expect(resolvePreference("dark", false)).toBe("dark");
  });
});

describe("remembering the choice", () => {
  it("reads back a stored preference", () => {
    const storage = fakeStorage({ [THEME_STORAGE_KEY]: "dark" });

    expect(readStoredPreference(storage)).toBe("dark");
  });

  it("falls back to auto without storage, without a value, or on junk", () => {
    expect(readStoredPreference(null)).toBe("auto");
    expect(readStoredPreference(fakeStorage())).toBe("auto");
    expect(
      readStoredPreference(fakeStorage({ [THEME_STORAGE_KEY]: "sepia" })),
    ).toBe("auto");
  });

  it("falls back to auto when the store itself throws", () => {
    const storage = {
      getItem: vi.fn(() => {
        throw new Error("site data blocked");
      }),
    };

    expect(readStoredPreference(storage)).toBe("auto");
  });

  it("stores an explicit choice and forgets auto", () => {
    const storage = fakeStorage();

    storePreference("dark", storage);
    expect(storage.values.get(THEME_STORAGE_KEY)).toBe("dark");

    storePreference("auto", storage);
    expect(storage.values.has(THEME_STORAGE_KEY)).toBe(false);
  });

  /*
   * index.html applies the stored theme in a blocking inline script, before
   * the bundle exists, so it cannot import the key and spells it out instead.
   * Renaming the key here without renaming it there would silently cost every
   * returning visitor their choice for one frame of the wrong palette.
   */
  it("shares its key with the no-flash script in index.html", () => {
    const html = readFileSync(
      new URL("../../index.html", import.meta.url),
      "utf8",
    );

    expect(html).toContain(`localStorage.getItem("${THEME_STORAGE_KEY}")`);
  });

  /*
   * The pack status page served by `glockenspiel-campaign pack status --serve`
   * reads the key the same way, from Go, so that a visitor who chose dark here
   * is dark there when the two share an origin. Same reasoning, third copy.
   */
  it("shares its key with the pack status page", () => {
    const page = readFileSync(
      new URL("../../../internal/pack/statuspage.go", import.meta.url),
      "utf8",
    );

    expect(page).toContain(`themeStorageKey = "${THEME_STORAGE_KEY}"`);
  });

  it("survives a store that refuses to write", () => {
    const storage = {
      setItem: vi.fn(() => {
        throw new Error("quota exceeded");
      }),
      removeItem: vi.fn(),
    };

    expect(() => {
      storePreference("light", storage);
    }).not.toThrow();
  });
});

describe("applying the choice to the document", () => {
  it("writes an explicit theme onto the root", () => {
    const root = fakeRoot();

    applyPreference("dark", root);
    expect(root.attributes.get("data-theme")).toBe("dark");

    applyPreference("light", root);
    expect(root.attributes.get("data-theme")).toBe("light");
  });

  it("spells auto as the absence of the attribute", () => {
    const root = fakeRoot();

    applyPreference("dark", root);
    applyPreference("auto", root);

    expect(root.attributes.has("data-theme")).toBe(false);
  });
});
