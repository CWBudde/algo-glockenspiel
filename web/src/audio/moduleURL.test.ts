import { describe, expect, it, vi } from "vitest";

import { wasmModuleURL } from "./moduleURL";

const BASE = "https://cwbudde.github.io/algo-glockenspiel/";

function respond(body: unknown, ok = true): Response {
  return {
    ok,
    json: () => Promise.resolve(body),
  } as Response;
}

/** stub types the vi.fn as the fetch the function under test accepts. */
function stub(
  implementation: () => Promise<Response>,
): [typeof fetch, ReturnType<typeof vi.fn>] {
  const mock = vi.fn(implementation);

  return [mock, mock];
}

describe("wasmModuleURL", () => {
  it("fingerprints the module with the manifest hash", async () => {
    const [fetchImpl, mock] = stub(() =>
      Promise.resolve(respond({ hash: "0badc0de" })),
    );

    await expect(wasmModuleURL(BASE, fetchImpl)).resolves.toBe(
      `${BASE}glockenspiel.wasm?v=0badc0de`,
    );

    // The manifest is read relative to the page, not to whoever is asking: the
    // worker that calls this is served out of the bundle's asset directory.
    expect(mock).toHaveBeenCalledWith(`${BASE}manifest.json`, {
      cache: "no-store",
    });
  });

  it("falls back to the bare name when the manifest is missing", async () => {
    const [fetchImpl] = stub(() => Promise.resolve(respond(null, false)));

    await expect(wasmModuleURL(BASE, fetchImpl)).resolves.toBe(
      `${BASE}glockenspiel.wasm`,
    );
  });

  it("falls back when the manifest carries no hash", async () => {
    const [fetchImpl] = stub(() => Promise.resolve(respond({ bytes: 42 })));

    await expect(wasmModuleURL(BASE, fetchImpl)).resolves.toBe(
      `${BASE}glockenspiel.wasm`,
    );
  });

  it("survives a fetch that rejects", async () => {
    const [fetchImpl] = stub(() => Promise.reject(new Error("offline")));
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);

    await expect(wasmModuleURL(BASE, fetchImpl)).resolves.toBe(
      `${BASE}glockenspiel.wasm`,
    );
    expect(warn).toHaveBeenCalled();

    warn.mockRestore();
  });
});
