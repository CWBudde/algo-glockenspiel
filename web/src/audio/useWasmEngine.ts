import { useEffect, useRef, useState, type RefObject } from "react";

import {
  hasEveryExport,
  WASM_NAMESPACE,
  WASM_READY_CALLBACK,
  WASM_READY_TIMEOUT_MS,
  type GlockenspielWasm,
} from "./wasmTypes";

export interface WasmEngine {
  /** The module's exports once it has signalled ready, otherwise null. */
  wasm: GlockenspielWasm | null;
  /**
   * Go's linear memory, the buffer processBlock hands back pointers into.
   *
   * A ref rather than state on purpose: the audio callback reads it on every
   * block and must see the live value, not whatever a render closed over.
   */
  memoryRef: RefObject<WebAssembly.Memory | null>;
  /** The last thing worth telling the user, ready or not. */
  status: string;
  error: boolean;
}

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
 * Both URLs are relative to the page, which is what lets the same bundle run at
 * the server root and under the GitHub Pages project sub-path.
 *
 * A missing or unreadable manifest is not fatal. A checkout built before this
 * script existed, or served by something that does not hand out .json, should
 * still load the demo; it just falls back to plain revalidation.
 */
async function wasmModuleURL(): Promise<string> {
  const url = "glockenspiel.wasm";

  try {
    const response = await fetch("manifest.json", { cache: "no-store" });
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

/**
 * waitForWasmReady installs the hook the Go module invokes once its exports are
 * published, and resolves with the namespace object it passes.
 *
 * This replaces a `setTimeout(resolve, 50)` after `go.run(...)` followed by a
 * typeof check. That sleep was a guess about how long a machine needs to get
 * through the Go runtime's start-up, and it is wrong in both directions: too
 * short on a loaded CI box or a cold cache, where the page reported "WASM
 * exports not found" for a module that was seconds from being ready, and 50 ms
 * of dead time on every load where it was not.
 *
 * The hook has to be installed before the runtime starts, because Go calls it
 * from inside `go.run(...)` -- the module's main runs synchronously up to the
 * point where it blocks -- so there is no later moment at which registering it
 * would still be in time.
 */
function waitForWasmReady(): Promise<GlockenspielWasm | undefined> {
  return new Promise((resolve, reject) => {
    const timer = window.setTimeout(() => {
      delete window[WASM_READY_CALLBACK];
      reject(
        new Error(
          `WASM module did not signal ready within ${WASM_READY_TIMEOUT_MS} ms`,
        ),
      );
    }, WASM_READY_TIMEOUT_MS);

    window[WASM_READY_CALLBACK] = (api) => {
      window.clearTimeout(timer);
      delete window[WASM_READY_CALLBACK];
      resolve(api ?? window[WASM_NAMESPACE]);
    };
  });
}

/**
 * useWasmEngine loads the module once for the lifetime of the page and reports
 * how far it got. It deliberately does not tear the module down on unmount:
 * the Go runtime cannot be unloaded, so a teardown would only cost the page its
 * engine with no way to get it back. It is called from App, which outlives
 * every tab switch.
 */
export function useWasmEngine(): WasmEngine {
  const [wasm, setWasm] = useState<GlockenspielWasm | null>(null);
  const [status, setStatus] = useState("Loading WebAssembly...");
  const [error, setError] = useState(false);
  const memoryRef = useRef<WebAssembly.Memory | null>(null);
  const startedRef = useRef(false);

  useEffect(() => {
    // React 19 runs effects twice in development StrictMode. Instantiating the
    // Go runtime twice would leave two modules fighting over the ready hook,
    // so the load is guarded rather than cleaned up.
    //
    // For the same reason there is no cancellation flag and no cleanup: the
    // load has to publish its result whatever happens to the effect that
    // started it. A flag set by StrictMode's simulated unmount would be read by
    // the still-running first load -- the second setup is skipped by the guard,
    // so nothing restarts it -- and the page would sit on "Loading
    // WebAssembly..." forever. Setting state after an unmount is a no-op React
    // has not warned about since 18, and this hook lives in App, which is
    // mounted for the lifetime of the page anyway.
    if (startedRef.current) {
      return;
    }

    startedRef.current = true;

    const load = async () => {
      const go = new Go();
      const moduleURL = await wasmModuleURL();
      const response = await fetch(moduleURL);
      if (!response.ok) {
        throw new Error(`Failed to fetch WASM: ${response.status}`);
      }

      let result: WebAssembly.WebAssemblyInstantiatedSource;
      try {
        result = await WebAssembly.instantiateStreaming(
          response.clone(),
          go.importObject,
        );
      } catch {
        // A server that does not send application/wasm refuses streaming
        // instantiation; the buffered path works regardless of content type.
        const bytes = await response.arrayBuffer();
        result = await WebAssembly.instantiate(bytes, go.importObject);
      }

      const exports = result.instance.exports;
      const memory = exports.mem ?? exports.memory;
      if (!(memory instanceof WebAssembly.Memory)) {
        throw new Error("WASM memory export not found");
      }

      memoryRef.current = memory;

      const ready = waitForWasmReady();
      // go.run resolves when the Go program exits, which for this module means
      // it died: main blocks forever on purpose. Reporting that beats leaving an
      // unhandled rejection in the console next to a page that stopped working.
      go.run(result.instance).catch((runtimeError: unknown) => {
        console.error("Go runtime stopped", runtimeError);
        setStatus(`WASM runtime stopped: ${messageOf(runtimeError)}`);
        setError(true);
      });

      const api = await ready;
      if (!hasEveryExport(api)) {
        throw new Error(`${WASM_NAMESPACE} is missing its exports`);
      }

      setWasm(api);
      setStatus("WASM loaded. Strike a bar to start audio.");
      setError(false);
    };

    load().catch((loadError: unknown) => {
      console.error("Failed to load WASM demo", loadError);
      setStatus(messageOf(loadError));
      setError(true);
    });
  }, []);

  return { wasm, memoryRef, status, error };
}

export function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
