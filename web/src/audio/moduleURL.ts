/**
 * wasmModuleURL resolves the URL to fetch the module from, appending the
 * content hash that scripts/build-wasm.sh records in manifest.json.
 *
 * The artifact keeps its fixed name -- internal/server hard-codes
 * "glockenspiel.wasm" to recognise a missing build and answer with the command
 * that produces it -- so the fingerprint travels in the query string instead of
 * the file name. A cache keyed on the full URL still sees a new resource per
 * build, which is the point: the module is the one file here big enough that a
 * stale copy matters, and the one whose staleness is invisible (old audio code,
 * current UI).
 *
 * Both names are resolved against the page's base URL rather than fetched
 * relative to whatever is asking, because the asking is now done from a worker
 * that a bundler serves out of assets/: a relative "manifest.json" there means
 * assets/manifest.json, which is a 404, and a 404 here is silently survivable
 * (see below), so it would have cost the cache busting with no error to show
 * for it.
 *
 * A missing or unreadable manifest is not fatal. A checkout built before this
 * script existed, or served by something that does not hand out .json, should
 * still load the demo; it just falls back to plain revalidation.
 */
export async function wasmModuleURL(
  baseURL: string,
  fetchImpl: typeof fetch = fetch,
): Promise<string> {
  const url = new URL("glockenspiel.wasm", baseURL).href;

  try {
    const response = await fetchImpl(new URL("manifest.json", baseURL).href, {
      cache: "no-store",
    });
    if (!response.ok) {
      return url;
    }

    const manifest = (await response.json()) as { hash?: unknown };
    if (typeof manifest.hash === "string" && manifest.hash.length > 0) {
      return `${url}?v=${encodeURIComponent(manifest.hash)}`;
    }
  } catch (error) {
    console.warn(
      "No build manifest; fetching the module unfingerprinted",
      error,
    );
  }

  return url;
}
