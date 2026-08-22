import { expect, test, type Page } from "@playwright/test";

import type { FitSnapshot } from "../src/api/types";
import { computePlayfieldLayout } from "../src/lib/layout";

const ENGINE_READY = "WASM loaded. Strike a bar to start audio.";
const MOBILE_PLAYFIELD = computePlayfieldLayout();

function fitSnapshot(overrides: Partial<FitSnapshot> = {}): FitSnapshot {
  return {
    jobId: "job-visual",
    state: "running",
    iteration: 1,
    optimizerIterations: 10,
    evaluations: 24,
    currentCost: 0.75,
    bestCost: 0.5,
    elapsedMs: 1250,
    sampleRate: 48_000,
    referenceSeconds: 1.5,
    note: 69,
    velocity: 100,
    optimizer: "simple",
    metric: "rms",
    startedAt: "2026-08-22T12:00:00Z",
    hasPreset: false,
    ...overrides,
  };
}

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

/** Makes Optimize render the static-host guidance instead of the fit form. */
async function installUnavailableFitApi(page: Page): Promise<void> {
  await page.route("**/api/version", async (route) => {
    await route.fulfill({
      status: 404,
      contentType: "application/json",
      body: JSON.stringify({ error: "not found" }),
    });
  });
}

async function installFitScenario(
  page: Page,
  status: FitSnapshot,
  event: { name: "progress" | "done"; snapshot: FitSnapshot },
  eventGate?: Promise<void>,
): Promise<void> {
  await page.route("**/api/version", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ version: "visual-test" }),
    });
  });

  await page.route("**/api/fit", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify(status),
    });
  });

  await page.route("**/api/fit/events", async (route) => {
    if (eventGate !== undefined) {
      await eventGate;
    }

    await route.fulfill({
      contentType: "text/event-stream",
      headers: { "cache-control": "no-store" },
      body: `event: ${event.name}\ndata: ${JSON.stringify(event.snapshot)}\n\n`,
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

test("mobile playfield shares one aligned, reachable pitch viewport", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await installStableEngine(page);
  await page.goto("/#/play");
  await expect(page.getByText(ENGINE_READY, { exact: true })).toBeVisible();

  const viewport = page.locator(".playfield-viewport");
  const rack = page.getByRole("region", { name: "Playable glockenspiel" });
  const keyboard = page.getByRole("region", { name: "Piano alignment" });
  const bars = rack.getByRole("button");

  await expect(viewport).toHaveCSS("overflow-x", "auto");
  await expect(viewport).toHaveCSS("touch-action", "pan-x");
  await expect(bars.first()).toHaveCSS("touch-action", "pan-x");
  await expect(keyboard.getByRole("button").first()).toHaveCSS(
    "touch-action",
    "pan-x",
  );

  const horizontalScrollerCount = await page
    .locator(".instrument-card *")
    .evaluateAll((elements): number => {
      let count = 0;

      for (const element of elements) {
        if (!(element instanceof HTMLElement)) {
          continue;
        }
        const overflow = getComputedStyle(element).overflowX;
        if (
          element.scrollWidth > element.clientWidth + 1 &&
          (overflow === "auto" || overflow === "scroll")
        ) {
          count += 1;
        }
      }

      return count;
    });
  expect(horizontalScrollerCount).toBe(1);

  await expect
    .poll(() => viewport.evaluate((element) => element.scrollLeft))
    .toBe(MOBILE_PLAYFIELD.initialScrollLeft);

  const rackC4 = rack.locator('.bar[data-note="60"]');
  const pianoC4 = keyboard.locator('.piano-key[data-note="60"]');
  const [rackC4Box, pianoC4Box] = await Promise.all([
    rackC4.boundingBox(),
    pianoC4.boundingBox(),
  ]);
  expect(rackC4Box).not.toBeNull();
  expect(pianoC4Box).not.toBeNull();
  const viewportBox = await viewport.boundingBox();
  expect(viewportBox).not.toBeNull();
  expect(rackC4Box!.x).toBeGreaterThanOrEqual(viewportBox!.x);
  expect(rackC4Box!.x + rackC4Box!.width).toBeLessThanOrEqual(
    viewportBox!.x + viewportBox!.width,
  );
  expect(
    Math.abs(
      rackC4Box!.x +
        rackC4Box!.width / 2 -
        (pianoC4Box!.x + pianoC4Box!.width / 2),
    ),
  ).toBeLessThanOrEqual(1);

  const barMetrics = await bars.evaluateAll((elements) =>
    elements.map((element) => {
      const button = element.getBoundingClientRect();
      const label = element.querySelector(".bar-note");
      if (!(label instanceof HTMLElement)) {
        throw new Error("bar has no visible note label");
      }
      const labelBox = label.getBoundingClientRect();
      const style = getComputedStyle(label);

      return {
        width: button.width,
        text: label.textContent?.trim() ?? "",
        readable:
          style.visibility === "visible" &&
          style.display !== "none" &&
          Number.parseFloat(style.fontSize) >= 12,
        labelBox: {
          left: labelBox.left,
          right: labelBox.right,
          top: labelBox.top,
          bottom: labelBox.bottom,
        },
      };
    }),
  );
  expect(barMetrics).toHaveLength(25);
  expect(barMetrics.every(({ width }) => width >= 44)).toBe(true);
  expect(
    barMetrics.every(({ text, readable }) => text !== "" && readable),
  ).toBe(true);

  for (const [index, bar] of barMetrics.entries()) {
    for (const other of barMetrics.slice(index + 1)) {
      const horizontalOverlap =
        Math.min(bar.labelBox.right, other.labelBox.right) -
        Math.max(bar.labelBox.left, other.labelBox.left);
      const verticalOverlap =
        Math.min(bar.labelBox.bottom, other.labelBox.bottom) -
        Math.max(bar.labelBox.top, other.labelBox.top);
      expect(horizontalOverlap > 0 && verticalOverlap > 0).toBe(false);
    }
  }

  const keyboardPanel = page.locator(".keyboard-panel");
  expect(
    await keyboardPanel.evaluate(
      (element) => element.scrollWidth === element.clientWidth,
    ),
  ).toBe(true);

  await viewport.evaluate((element) => {
    element.scrollLeft = 0;
  });
  await page
    .getByRole("region", { name: "Performance controls" })
    .getByRole("combobox", { name: "Wood" })
    .selectOption("maple");
  await expect
    .poll(() => viewport.evaluate((element) => element.scrollLeft))
    .toBe(0);
  expect(
    await viewport.evaluate((element, selector) => {
      const key = document.querySelector(selector);
      if (!(key instanceof HTMLElement)) {
        return false;
      }
      const viewportBox = element.getBoundingClientRect();
      const keyBox = key.getBoundingClientRect();

      return (
        keyBox.left >= viewportBox.left && keyBox.right <= viewportBox.right
      );
    }, '.piano-key[data-note="36"]'),
  ).toBe(true);
  await viewport.evaluate((element) => {
    element.scrollLeft = element.scrollWidth;
  });
  expect(
    await viewport.evaluate((element, selector) => {
      const key = document.querySelector(selector);
      if (!(key instanceof HTMLElement)) {
        return false;
      }
      const viewportBox = element.getBoundingClientRect();
      const keyBox = key.getBoundingClientRect();

      return (
        keyBox.left >= viewportBox.left && keyBox.right <= viewportBox.right
      );
    }, '.piano-key[data-note="96"]'),
  ).toBe(true);

  expect(
    await page
      .locator("body")
      .evaluate((element) => element.scrollWidth === element.clientWidth),
  ).toBe(true);
  expect(
    await page
      .getByRole("region", { name: "Performance controls" })
      .evaluate(
        (element) =>
          !element.contains(document.querySelector(".playfield-viewport")),
      ),
  ).toBe(true);
});

test("rack exposes every bar with material and accessible-name hooks", async ({
  page,
}) => {
  await installStableEngine(page);
  await page.goto("/#/play");

  const rack = page.getByRole("region", { name: "Playable glockenspiel" });
  const bars = rack.getByRole("button");
  const naturals = rack.locator(".bar.natural");
  const accidentals = rack.locator(".bar.accidental");

  await expect(bars).toHaveCount(25);
  await expect(naturals).toHaveCount(15);
  await expect(accidentals).toHaveCount(10);
  await expect(naturals.first()).toHaveCSS(
    "background-image",
    /^url\("data:image\/svg\+xml/,
  );
  await expect(accidentals.first()).toHaveCSS(
    "background-image",
    /^url\("data:image\/svg\+xml/,
  );

  const names = await bars.evaluateAll((elements) =>
    elements.map((element) => element.getAttribute("aria-label")),
  );
  expect(names.every((name) => name !== null && name.length > 0)).toBe(true);
  expect(new Set(names).size).toBe(25);
});

test("keyboard keeps its full named range and active-state hook", async ({
  page,
}) => {
  await installStableEngine(page);
  await page.goto("/#/play");
  await expect(page.getByText(ENGINE_READY, { exact: true })).toBeVisible();

  const keyboard = page.getByRole("region", { name: "Piano alignment" });
  const keys = keyboard.getByRole("button");
  const whites = keyboard.locator(".piano-key.white");
  const blacks = keyboard.locator(".piano-key.black");
  const first = keyboard.locator('.piano-key[data-note="36"]');
  const last = keyboard.locator('.piano-key[data-note="96"]');

  await expect(keys).toHaveCount(61);
  await expect(whites).toHaveCount(36);
  await expect(blacks).toHaveCount(25);
  await expect(first).toHaveAccessibleName("C2");
  await expect(last).toHaveAccessibleName("C7");

  const names = await keys.evaluateAll((elements) =>
    elements.map((element) => element.getAttribute("aria-label")),
  );
  const notes = await keys.evaluateAll((elements) =>
    elements.map((element) => Number((element as HTMLElement).dataset.note)),
  );
  expect(names.every((name) => name !== null && name.length > 0)).toBe(true);
  expect(new Set(names).size).toBe(61);
  expect(new Set(notes).size).toBe(61);
  expect(Math.min(...notes)).toBe(36);
  expect(Math.max(...notes)).toBe(96);

  const pianoC4 = keyboard.locator('.piano-key[data-note="60"]');
  const restingBackground = await pianoC4.evaluate(
    (element) => getComputedStyle(element).backgroundImage,
  );
  await pianoC4.evaluate((element) => element.classList.add("is-active"));
  await expect(pianoC4).toHaveClass(/\bis-active\b/);
  const activeBackground = await pianoC4.evaluate(
    (element) => getComputedStyle(element).backgroundImage,
  );
  expect(activeBackground).not.toBe(restingBackground);
});

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

test("Optimize exposes ordered setup and compact service states", async ({
  page,
}) => {
  await installStableEngine(page);

  let releaseProbe = (): void => undefined;
  const probeGate = new Promise<void>((resolve) => {
    releaseProbe = resolve;
  });

  await page.route("**/api/version", async (route) => {
    await probeGate;
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

  await page.goto("/#/optimize");

  const service = page.getByRole("status", { name: "Checking fit service" });
  await expect(service).toHaveAttribute("data-state", "probing");

  releaseProbe();
  await expect(
    page.getByRole("status", {
      name: "Fit service connected · visual-test",
    }),
  ).toHaveAttribute("data-state", "available");

  const form = page.locator(".fit-form");
  const mainSections = form.locator(":scope > fieldset.fit-group");
  await expect(mainSections).toHaveCount(3);
  await expect(mainSections.nth(0)).toContainText("1Reference");
  await expect(mainSections.nth(1)).toContainText("2Note");
  await expect(mainSections.nth(2)).toContainText("3Fit setup");

  const advanced = form.locator("details.fit-advanced");
  await expect(advanced).toHaveCount(1);
  await expect(advanced).toHaveJSProperty("open", false);

  await expect(form.locator(".fit-job-state")).toHaveText("Ready to start");
  const results = page.getByRole("region", { name: "Results" });
  await expect(results).toContainText(
    "Start a fit to see live progress, the cost curve, and audition controls.",
  );
  await expect(results.locator("canvas")).toHaveCount(0);
  await expect(results.getByRole("heading", { name: "Status" })).toHaveCount(0);
  await expect(results.getByRole("heading", { name: "Audition" })).toHaveCount(
    0,
  );

  const workspace = page.locator(".optimize-workspace");
  await expect(workspace).toHaveCSS(
    "grid-template-columns",
    /^\d+(?:\.\d+)?px \d+(?:\.\d+)?px$/,
  );
  await page.setViewportSize({ width: 900, height: 800 });
  await expect(workspace).toHaveCSS(
    "grid-template-columns",
    /^\d+(?:\.\d+)?px$/,
  );

  const unavailablePage = await page.context().newPage();
  await installStableEngine(unavailablePage);
  await installUnavailableFitApi(unavailablePage);
  await unavailablePage.goto("/#/optimize");
  await expect(
    unavailablePage.getByRole("status", {
      name: "Fit service unavailable",
    }),
  ).toHaveAttribute("data-state", "unavailable");
  await expect(
    unavailablePage.getByText("glockenspiel serve", { exact: true }),
  ).toBeVisible();
  await unavailablePage.close();
});

test("Optimize reopens Advanced when one of its fields is invalid", async ({
  page,
}) => {
  await installStableEngine(page);
  await installStableFitApi(page);
  await page.goto("/#/optimize");
  await expect(
    page.getByRole("status", {
      name: "Fit service connected · visual-test",
    }),
  ).toBeVisible();

  const form = page.locator(".fit-form");
  const advanced = form.locator("details.fit-advanced");
  const summary = form.getByText("Advanced settings", { exact: true });

  await summary.click();
  const optimizer = form.getByLabel("Optimizer", { exact: true });
  await optimizer.selectOption("mayfly");
  await form.getByLabel("Population").fill("37");
  await form.getByLabel("Seed").fill("42");
  await optimizer.selectOption("simple");
  await optimizer.selectOption("mayfly");
  await expect(form.getByLabel("Population")).toHaveValue("37");
  await expect(form.getByLabel("Seed")).toHaveValue("42");
  await form.getByLabel("Report every").fill("100001");
  await form.getByLabel("Reference recording (WAV)").setInputFiles({
    name: "reference.wav",
    mimeType: "audio/wav",
    buffer: Buffer.from("not decoded before client validation"),
  });
  await summary.click();
  await expect(advanced).toHaveJSProperty("open", false);

  await form.getByRole("button", { name: "Start fit" }).click();

  await expect(advanced).toHaveAttribute("open", "");
  await expect(
    form.getByText("The report interval must be in [0, 100000].", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    form.getByText("Some fields need fixing before the fit can start.", {
      exact: true,
    }),
  ).toBeVisible();
});

test("Optimize reconnects to a running fit and draws its SSE update", async ({
  page,
}) => {
  const running = fitSnapshot();
  const progress = fitSnapshot({
    iteration: 2,
    optimizerIterations: 20,
    evaluations: 44,
    currentCost: 0.4,
    bestCost: 0.25,
    elapsedMs: 2400,
    hasPreset: true,
  });
  let releaseEvent = (): void => undefined;
  const eventGate = new Promise<void>((resolve) => {
    releaseEvent = resolve;
  });

  await installStableEngine(page);
  await installFitScenario(
    page,
    running,
    {
      name: "progress",
      snapshot: progress,
    },
    eventGate,
  );
  await page.goto("/#/optimize");

  const form = page.locator(".fit-form");
  await expect(form.locator(".fit-job-state")).toHaveText(
    "Fit job-visual running",
  );
  await expect(form.getByRole("button", { name: "Start fit" })).toBeDisabled();
  await expect(form.getByRole("button", { name: "Cancel fit" })).toBeEnabled();

  const results = page.getByRole("region", { name: "Results" });
  await expect(
    results.getByText("Waiting for first cost report…"),
  ).toBeVisible();
  await expect(results.locator("canvas")).toHaveCount(0);

  releaseEvent();
  await expect(results.getByRole("region", { name: "Status" })).toContainText(
    "20",
  );
  await expect(
    results.getByRole("img", { name: /Cost curve over 1 samples\./ }),
  ).toBeVisible();
  await expect(results.getByText("Waiting for first cost report…")).toHaveCount(
    0,
  );
});

test("Optimize keeps a canceled fit with a preset usable in Results", async ({
  page,
}) => {
  const canceled = fitSnapshot({
    state: "canceled",
    iteration: 4,
    optimizerIterations: 40,
    evaluations: 88,
    currentCost: 0.2,
    bestCost: 0.1,
    elapsedMs: 4800,
    stopReason: "canceled",
    finishedAt: "2026-08-22T12:00:05Z",
    hasPreset: true,
  });

  await installStableEngine(page);
  await installFitScenario(page, canceled, {
    name: "done",
    snapshot: canceled,
  });
  await page.goto("/#/optimize");

  const form = page.locator(".fit-form");
  await expect(form.locator(".fit-job-state")).toHaveText(
    "Fit job-visual canceled",
  );
  await expect(form.getByRole("button", { name: "Start fit" })).toBeEnabled();
  await expect(form.getByRole("button", { name: "Cancel fit" })).toBeDisabled();

  const results = page.getByRole("region", { name: "Results" });
  await expect(
    results.getByRole("img", { name: /Cost curve over 1 samples\./ }),
  ).toBeVisible();
  await expect(
    results.getByRole("button", { name: "Render and play" }),
  ).toBeEnabled();
  await expect(
    results.getByRole("link", { name: "Download preset JSON" }),
  ).toHaveAttribute("href", "api/fit/preset");
});

test("Optimize recovers the active fit after a start conflict", async ({
  page,
}) => {
  const running = fitSnapshot({ jobId: "job-elsewhere" });
  let statusReads = 0;

  await installStableEngine(page);
  await page.route("**/api/version", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ version: "visual-test" }),
    });
  });
  await page.route("**/api/fit/start", async (route) => {
    await route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({ error: "a fit is already running" }),
    });
  });
  await page.route("**/api/fit", async (route) => {
    statusReads += 1;

    if (statusReads === 1) {
      await route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({ error: "no fit has been started" }),
      });
      return;
    }

    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify(running),
    });
  });
  await page.route("**/api/fit/events", async (route) => {
    await route.fulfill({
      contentType: "text/event-stream",
      body: `event: progress\ndata: ${JSON.stringify(running)}\n\n`,
    });
  });

  await page.goto("/#/optimize");
  await expect.poll(() => statusReads).toBe(1);

  const form = page.locator(".fit-form");
  await form.getByLabel("Reference recording (WAV)").setInputFiles({
    name: "reference.wav",
    mimeType: "audio/wav",
    buffer: Buffer.from("reference fixture"),
  });
  await form.getByRole("button", { name: "Start fit" }).click();

  await expect(
    form.getByText(
      "a fit is already running. Cancel it first, or wait for it to finish.",
      { exact: true },
    ),
  ).toBeVisible();
  await expect(form.locator(".fit-job-state")).toHaveText(
    "Fit job-elsewhere running",
  );
  await expect(form.getByRole("button", { name: "Cancel fit" })).toBeEnabled();
  await expect.poll(() => statusReads).toBe(2);
});

test("Optimize at 1440x1000", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await installStableEngine(page);
  await installStableFitApi(page);
  await page.goto("/#/optimize");

  await expect(
    page.getByRole("status", {
      name: "Fit service connected · visual-test",
    }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Start fit" })).toBeVisible();
  await waitForStablePaint(page);
  await expect(page).toHaveScreenshot("optimize-1440x1000.png");
});
