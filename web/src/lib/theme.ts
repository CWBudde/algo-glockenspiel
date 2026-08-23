import { useCallback, useEffect, useState, useSyncExternalStore } from "react";

/**
 * What the user asked for. `auto` is the default and defers to the operating
 * system; the other two override it and are remembered across visits.
 */
export type ThemePreference = "auto" | "light" | "dark";

/** What the page actually paints, once `auto` has been resolved. */
export type ResolvedTheme = "light" | "dark";

export const THEME_PREFERENCES: readonly ThemePreference[] = [
  "auto",
  "light",
  "dark",
];

/**
 * The key the preference is remembered under.
 *
 * index.html reads the same key in a tiny inline script before the bundle
 * loads, so that a dark visitor never sees a frame of the light palette. The
 * two copies have to agree; this constant is the one that is tested.
 */
export const THEME_STORAGE_KEY = "algo-glockenspiel:theme";

/** The media query `auto` resolves against. */
export const DARK_MEDIA_QUERY = "(prefers-color-scheme: dark)";

export function isThemePreference(value: unknown): value is ThemePreference {
  return value === "auto" || value === "light" || value === "dark";
}

/**
 * Reads the remembered preference, falling back to `auto`.
 *
 * Storage is read defensively: a browser with site data blocked throws on the
 * property access itself, and a value written by an older build (or by hand)
 * is not trusted to be one of the three.
 */
export function readStoredPreference(
  storage: Pick<Storage, "getItem"> | null | undefined,
): ThemePreference {
  if (!storage) {
    return "auto";
  }

  let stored: string | null = null;

  try {
    stored = storage.getItem(THEME_STORAGE_KEY);
  } catch {
    return "auto";
  }

  return isThemePreference(stored) ? stored : "auto";
}

/**
 * Remembers the preference, or forgets it when it is back to `auto`.
 *
 * `auto` is stored as the absence of a value rather than as the string, so a
 * visitor who never touches the switch and one who returns to it are the same
 * visitor as far as the next page load is concerned.
 */
export function storePreference(
  preference: ThemePreference,
  storage: Pick<Storage, "setItem" | "removeItem"> | null | undefined,
): void {
  if (!storage) {
    return;
  }

  try {
    if (preference === "auto") {
      storage.removeItem(THEME_STORAGE_KEY);
    } else {
      storage.setItem(THEME_STORAGE_KEY, preference);
    }
  } catch {
    // A full or blocked store costs the visitor the memory of the choice, not
    // the choice itself, so there is nothing useful to report here.
  }
}

export function resolvePreference(
  preference: ThemePreference,
  prefersDark: boolean,
): ResolvedTheme {
  if (preference === "auto") {
    return prefersDark ? "dark" : "light";
  }

  return preference;
}

/**
 * Writes the preference onto the root element.
 *
 * `auto` removes the attribute rather than writing "auto": the stylesheet's
 * dark block is a `prefers-color-scheme` media query narrowed by
 * `:root:not([data-theme="light"])`, so the absence of the attribute is
 * already spelled "follow the system" in CSS and needs no third branch.
 */
export function applyPreference(
  preference: ThemePreference,
  root: Element = document.documentElement,
): void {
  if (preference === "auto") {
    root.removeAttribute("data-theme");
  } else {
    root.setAttribute("data-theme", preference);
  }
}

function matchDark(): MediaQueryList | null {
  if (typeof window === "undefined" || !window.matchMedia) {
    return null;
  }

  return window.matchMedia(DARK_MEDIA_QUERY);
}

/*
 * The system preference and the root's attribute are both state that lives
 * outside React, so they are read through useSyncExternalStore rather than
 * mirrored into a useState that an effect keeps in step. Each snapshot below
 * returns a primitive, which is what makes the store's identity check work.
 */
function subscribeToSystem(onChange: () => void): () => void {
  const media = matchDark();

  media?.addEventListener("change", onChange);

  return () => {
    media?.removeEventListener("change", onChange);
  };
}

function getPrefersDark(): boolean {
  return matchDark()?.matches ?? false;
}

/** Server-side there is no media query to ask, and light is the default. */
function getPrefersDarkOnServer(): boolean {
  return false;
}

/**
 * The theme the page is currently painting, for the parts of the UI that are
 * drawn rather than styled.
 *
 * A canvas cannot inherit a custom property, so the cost chart has to re-read
 * the palette when the theme changes. It watches the same two things the
 * stylesheet does -- the root's `data-theme` and the system preference -- and
 * therefore stays right whether the switch moved or the operating system did,
 * without the switch having to know that a chart exists.
 */
export function useResolvedTheme(): ResolvedTheme {
  return useSyncExternalStore(
    subscribeToTheme,
    readResolvedTheme,
    getLightTheme,
  );
}

function subscribeToTheme(onChange: () => void): () => void {
  const unsubscribeFromSystem = subscribeToSystem(onChange);
  const observer = new MutationObserver(onChange);

  observer.observe(document.documentElement, {
    attributeFilter: ["data-theme"],
  });

  return () => {
    unsubscribeFromSystem();
    observer.disconnect();
  };
}

function getLightTheme(): ResolvedTheme {
  return "light";
}

function readResolvedTheme(): ResolvedTheme {
  if (typeof document === "undefined") {
    return "light";
  }

  const attribute = document.documentElement.getAttribute("data-theme");

  return resolvePreference(
    isThemePreference(attribute) ? attribute : "auto",
    matchDark()?.matches ?? false,
  );
}

export interface Theme {
  /** What the switch shows: the choice, not its outcome. */
  preference: ThemePreference;
  /** What the page paints, for the parts of the UI that are drawn, not styled. */
  resolved: ResolvedTheme;
  setPreference: (preference: ThemePreference) => void;
}

/**
 * The color theme, as the switch sees it.
 *
 * The attribute is what the stylesheet actually reads; `resolved` exists for
 * the canvas-drawn chart, which cannot inherit a custom property and has to be
 * told when to re-read the palette.
 */
export function useTheme(): Theme {
  const [preference, setPreferenceState] = useState<ThemePreference>(() =>
    readStoredPreference(
      typeof window === "undefined" ? null : window.localStorage,
    ),
  );
  const prefersDark = useSyncExternalStore(
    subscribeToSystem,
    getPrefersDark,
    getPrefersDarkOnServer,
  );

  useEffect(() => {
    applyPreference(preference);
  }, [preference]);

  const setPreference = useCallback((next: ThemePreference) => {
    storePreference(
      next,
      typeof window === "undefined" ? null : window.localStorage,
    );
    setPreferenceState(next);
  }, []);

  return {
    preference,
    resolved: resolvePreference(preference, prefersDark),
    setPreference,
  };
}
