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
export type WasmArtifact = "audio" | "fit";

export async function wasmModuleURL(
  baseURL: string,
  artifact: WasmArtifact = "audio",
  fetchImpl: typeof fetch = fetch,
): Promise<string> {
  const fallbackName =
    artifact === "audio" ? "glockenspiel.wasm" : "glockenspiel-fit.wasm";

  try {
    const response = await fetchImpl(new URL("manifest.json", baseURL).href, {
      cache: "no-store",
    });
    if (!response.ok) {
      return new URL(fallbackName, baseURL).href;
    }

    const manifest = (await response.json()) as {
      wasm?: unknown;
      hash?: unknown;
      fitWasm?: unknown;
      fitHash?: unknown;
    };
    const name = artifact === "audio" ? manifest.wasm : manifest.fitWasm;
    const hash = artifact === "audio" ? manifest.hash : manifest.fitHash;
    const url = new URL(
      typeof name === "string" && name.length > 0 ? name : fallbackName,
      baseURL,
    ).href;
    if (typeof hash === "string" && hash.length > 0) {
      return `${url}?v=${encodeURIComponent(hash)}`;
    }

    return url;
  } catch (error) {
    console.warn(
      "No build manifest; fetching the module unfingerprinted",
      error,
    );
  }

  return new URL(fallbackName, baseURL).href;
}
