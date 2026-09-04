import type { Page } from "@playwright/test";

/**
 * Replaces the audio engine worker with its stable loaded state.
 *
 * App starts the Play tab's WASM worker unconditionally on mount, so every
 * spec pays for the real module load whether or not it touches the Play tab.
 * The specs that exercise the Optimize tab against the Go server have no
 * reason to, and a worker that answers "loaded" and nothing else is the whole
 * of what they need from it.
 *
 * It is a module of its own rather than a copy per spec because two specs now
 * want it and Playwright would run the tests of a spec file imported for its
 * helper a second time. A file that is not itself a spec cannot do that.
 */
export async function installStableEngine(page: Page): Promise<void> {
  await page.addInitScript(() => {
    class StableEngineWorker {
      onmessage: ((event: MessageEvent<unknown>) => void) | null = null;
      onerror: ((event: ErrorEvent) => void) | null = null;

      postMessage(message: { type?: string }): void {
        if (message.type !== "load") {
          return;
        }

        window.queueMicrotask(() => {
          this.onmessage?.(
            new MessageEvent("message", { data: { type: "loaded" } }),
          );
        });
      }

      terminate(): void {
        // The production hook deliberately keeps its worker for the app's life.
      }
    }

    Object.defineProperty(window, "Worker", {
      configurable: true,
      value: StableEngineWorker,
    });
  });
}
