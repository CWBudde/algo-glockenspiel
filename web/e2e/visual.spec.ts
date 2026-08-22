import { expect, test, type Page } from "@playwright/test";

const ENGINE_READY = "WASM loaded. Strike a bar to start audio.";

/**
 * Replaces the audio worker with its stable loaded state. Visual tests exercise
 * layout, not the Go runtime, and therefore do not need a built WASM artifact.
 */
async function installStableEngine(page: Page): Promise<void> {
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

/** Makes Optimize render its connected, no-prior-job state without a Go server. */
async function installStableFitApi(page: Page): Promise<void> {
  await page.route("**/api/version", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ version: "visual-test" }),
    });
  });

  await page.route("**/api/fit", async (route) => {
    await route.fulfill({
      status: 404,
      contentType: "application/json",
      body: JSON.stringify({ error: "no fit has been started" }),
    });
  });
}

async function waitForStablePaint(page: Page): Promise<void> {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.evaluate(async () => {
    await document.fonts.ready;
  });
}

for (const viewport of [
  { width: 1440, height: 1000 },
  { width: 1024, height: 768 },
  { width: 390, height: 844 },
]) {
  test(`Play at ${viewport.width}x${viewport.height}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await installStableEngine(page);
    await page.goto("/#/play");

    await expect(page.getByText(ENGINE_READY, { exact: true })).toBeVisible();
    await waitForStablePaint(page);
    await expect(page).toHaveScreenshot(
      `play-${viewport.width}x${viewport.height}.png`,
    );
  });
}

test("Optimize at 1440x1000", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await installStableEngine(page);
  await installStableFitApi(page);
  await page.goto("/#/optimize");

  await expect(
    page.getByText("Connected to glockenspiel visual-test.", { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Start fit" })).toBeVisible();
  await waitForStablePaint(page);
  await expect(page).toHaveScreenshot("optimize-1440x1000.png");
});
