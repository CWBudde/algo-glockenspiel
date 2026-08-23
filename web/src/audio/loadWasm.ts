import { wasmModuleURL, type WasmArtifact } from "./moduleURL";
import {
  WASM_NAMESPACE,
  WASM_READY_CALLBACK,
  WASM_READY_TIMEOUT_MS,
} from "./wasmTypes";

interface WorkerScope {
  setTimeout: typeof setTimeout;
  clearTimeout: typeof clearTimeout;
  [WASM_READY_CALLBACK]?: (api?: unknown) => void;
  [WASM_NAMESPACE]?: unknown;
}

export interface LoadedWasm<T> {
  api: T;
  memory: WebAssembly.Memory;
}

const scope = self as unknown as WorkerScope;

function waitForWasmReady(): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const timer = scope.setTimeout(() => {
      delete scope[WASM_READY_CALLBACK];
      reject(
        new Error(
          `WASM module did not signal ready within ${WASM_READY_TIMEOUT_MS} ms`,
        ),
      );
    }, WASM_READY_TIMEOUT_MS);

    scope[WASM_READY_CALLBACK] = (ready) => {
      scope.clearTimeout(timer);
      delete scope[WASM_READY_CALLBACK];
      resolve(ready ?? scope[WASM_NAMESPACE]);
    };
  });
}

/** Loads one Go runtime in the worker that calls this function. */
export async function loadWasm<T>(
  baseURL: string,
  artifact: WasmArtifact,
  accepts: (api: unknown) => api is T,
  onRuntimeError: (error: unknown) => void,
): Promise<LoadedWasm<T>> {
  await import(/* @vite-ignore */ new URL("wasm_exec.js", baseURL).href);

  const go = new Go();
  const moduleURL = await wasmModuleURL(baseURL, artifact);
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
    const bytes = await response.arrayBuffer();
    result = await WebAssembly.instantiate(bytes, go.importObject);
  }

  const exports = result.instance.exports;
  const wasmMemory = exports.mem ?? exports.memory;
  if (!(wasmMemory instanceof WebAssembly.Memory)) {
    throw new Error("WASM memory export not found");
  }

  const ready = waitForWasmReady();
  void go.run(result.instance).catch(onRuntimeError);

  const api = await ready;
  if (!accepts(api)) {
    throw new Error(`${WASM_NAMESPACE} is missing its ${artifact} exports`);
  }

  return { api, memory: wasmMemory };
}
