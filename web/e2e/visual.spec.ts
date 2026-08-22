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

/** Makes the engine expose its fatal status through the deck without WASM. */
async function installFailedEngine(page: Page): Promise<void> {
  await page.addInitScript(() => {
    class FailedEngineWorker {
      onmessage: ((event: MessageEvent<unknown>) => void) | null = null;
      onerror: ((event: ErrorEvent) => void) | null = null;

      postMessage(message: { type?: string }): void {
        if (message.type !== "load") {
          return;
        }

        window.queueMicrotask(() => {
          this.onerror?.(
            new ErrorEvent("error", { message: "visual engine failure" }),
          );
        });
      }

      terminate(): void {
        // The production hook deliberately keeps its worker for the app's life.
      }
    }

    Object.defineProperty(window, "Worker", {
      configurable: true,
      value: FailedEngineWorker,
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

test("performance deck keeps native controls and live engine status", async ({
  page,
}) => {
  await installStableEngine(page);
  await page.goto("/#/play");

  const deck = page.getByRole("region", { name: "Performance controls" });
  const volume = deck.getByRole("slider", { name: "Volume" });
  const velocity = deck.getByRole("slider", { name: "Velocity" });
  const wood = deck.getByRole("combobox", { name: "Wood" });
  const status = deck.locator(".status-panel");

  await expect(deck).toBeVisible();
  await expect(volume).toHaveAttribute("type", "range");
  await expect(volume).toHaveAttribute("min", "10");
  await expect(volume).toHaveAttribute("max", "100");
  await expect(volume).toHaveValue("70");
  await expect(velocity).toHaveAttribute("type", "range");
  await expect(velocity).toHaveAttribute("min", "1");
  await expect(velocity).toHaveAttribute("max", "127");
  await expect(velocity).toHaveValue("96");

  await volume.focus();
  await volume.press("ArrowUp");
  await expect(volume).toHaveValue("71");
  await expect(deck.locator('output[for="gain"]')).toHaveText("71%");

  await volume.evaluate((control) => {
    control.closest("label")?.dispatchEvent(
      new WheelEvent("wheel", {
        bubbles: true,
        cancelable: true,
        deltaY: 100,
      }),
    );
  });
  await expect(volume).toHaveValue("70");

  await velocity.fill("110");
  await expect(velocity).toHaveValue("110");
  await expect(deck.locator('output[for="velocity"]')).toHaveText("110");

  await wood.selectOption("walnut");
  await expect(wood).toHaveValue("walnut");
  await expect(page.locator("#wood-description")).toHaveText(
    "Dark semi-ring-porous walnut with restrained rays and soft curl.",
  );
  await expect(page.locator("html")).toHaveAttribute(
    "data-wood-species",
    "walnut",
  );

  await expect(status).toHaveAttribute("aria-live", "polite");
  await expect(status).toHaveAttribute("data-error", "false");
  await expect(status).toHaveText(ENGINE_READY);
});

test("performance deck exposes engine failures as live errors", async ({
  page,
}) => {
  await installFailedEngine(page);
  await page.goto("/#/play");

  const status = page
    .getByRole("region", { name: "Performance controls" })
    .locator(".status-panel");

  await expect(status).toHaveAttribute("aria-live", "polite");
  await expect(status).toHaveAttribute("data-error", "true");
  await expect(status).toHaveText("visual engine failure");
});

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
