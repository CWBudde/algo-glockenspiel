import AxeBuilder from "@axe-core/playwright";
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

/** Makes Optimize select its browser-WASM backend instead of the fit API. */
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

/** Returns visible, enabled controls in the order the browser tabs to them. */
/*
 * A radio group is one tab stop, not one per option: only the checked radio is
 * in the tab order, and the arrow keys move within the group. The theme switch
 * is the only radio group on the page, and listing all three of its inputs
 * here would have the traversal expect three stops the browser will not make.
 */
function tabbableControls(page: Page) {
  return page.locator(
    [
      "a[href]:visible",
      "button:not([disabled]):visible",
      'input:not([disabled]):not([type="radio"]):visible',
      'input[type="radio"]:not([disabled]):checked:visible',
      "select:not([disabled]):visible",
      "summary:visible",
    ].join(", "),
  );
}

/**
 * Walks every control, rather than sampling one, and checks that keyboard focus
 * has a visible three-pixel treatment. The dials draw it on their visible face
 * because the real range input is deliberately transparent.
 */
async function expectFullKeyboardTraversal(page: Page): Promise<void> {
  const controls = await tabbableControls(page).all();
  await page.locator("body").click({ position: { x: 1, y: 1 } });

  for (const control of controls) {
    await page.keyboard.press("Tab");
    await expect(control).toBeFocused();

    const focusStyle = await control.evaluate((element) => {
      // Both the dials and the theme switch keep a transparent native control
      // and draw its focus ring on the visible element that follows it.
      const drawsRingOnSibling =
        element.classList.contains("dial-input") ||
        element.matches('input[type="radio"]');
      const focusTarget = drawsRingOnSibling
        ? element.nextElementSibling
        : element;

      if (!(focusTarget instanceof HTMLElement)) {
        throw new Error("focus target is not an HTML element");
      }

      const style = getComputedStyle(focusTarget);
      return {
        color: style.outlineColor,
        style: style.outlineStyle,
        width: Number.parseFloat(style.outlineWidth),
      };
    });

    expect(focusStyle.style).toBe("solid");
    expect(focusStyle.width).toBeGreaterThanOrEqual(3);
    expect(focusStyle.color).not.toBe("rgba(0, 0, 0, 0)");
  }
}

async function expectNoSeriousAxeViolations(page: Page): Promise<void> {
  const results = await new AxeBuilder({ page }).analyze();
  const violations = results.violations.filter(
    ({ impact }) => impact === "serious" || impact === "critical",
  );

  expect(violations).toEqual([]);
}

async function expectNoBodyOverflow(page: Page): Promise<void> {
  const overflow = await page.locator("body").evaluate((body) => {
    const bodyRight = body.getBoundingClientRect().right;

    return [...body.querySelectorAll("*")]
      .filter(
        (element) =>
          !element.closest(".playfield-viewport") &&
          element.getBoundingClientRect().right > bodyRight + 1,
      )
      .map((element) => ({
        className: element.className,
        right: element.getBoundingClientRect().right,
        tag: element.tagName,
      }));
  });

  expect(overflow).toEqual([]);
  expect(
    await page
      .locator("body")
      .evaluate((body) => body.scrollWidth === body.clientWidth),
  ).toBe(true);
}

async function expectTouchTargets(page: Page): Promise<void> {
  const controls = await tabbableControls(page).all();

  for (const control of controls) {
    let target = control;

    const type = await control.getAttribute("type");

    if (type === "checkbox" || type === "radio") {
      const id = await control.getAttribute("id");
      expect(id).not.toBeNull();
      target = page.locator(`label[for="${id}"]`);
    }

    const box = await target.boundingBox();
    expect(box).not.toBeNull();
    // Chromium can quantize a declared 44px edge to 43.95 CSS pixels.
    expect(box!.width).toBeGreaterThanOrEqual(43.9);
    expect(box!.height).toBeGreaterThanOrEqual(43.9);
  }
}

async function exposeAllOptimizeControls(page: Page): Promise<void> {
  await page.getByLabel("Optimizer", { exact: true }).selectOption("mayfly");
  await page.getByText("Advanced settings", { exact: true }).click();
  await page.getByLabel("Narrow the search bounds").check();
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

test("desktop rack caps at 1000px and stays centered", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await installStableEngine(page);
  await page.goto("/#/play");

  const rack = page.locator(".rack");
  const card = page.locator(".instrument-card");
  const [rackBox, cardBox] = await Promise.all([
    rack.boundingBox(),
    card.boundingBox(),
  ]);

  expect(rackBox).not.toBeNull();
  expect(cardBox).not.toBeNull();
  expect(rackBox!.width).toBeCloseTo(1000, 0);
  expect(rackBox!.x + rackBox!.width / 2).toBeCloseTo(
    cardBox!.x + cardBox!.width / 2,
    0,
  );
});

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

  // Both axes stay pannable: the viewport is the only horizontal scroller, so
  // horizontal drags reach it while vertical drags still scroll the page.
  await expect(viewport).toHaveCSS("overflow-x", "auto");
  await expect(viewport).toHaveCSS("touch-action", "pan-x pan-y");
  await expect(bars.first()).toHaveCSS("touch-action", "pan-x pan-y");
  await expect(keyboard.getByRole("button").first()).toHaveCSS(
    "touch-action",
    "pan-x pan-y",
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
  expect(viewportBox!.width).toBeCloseTo(MOBILE_PLAYFIELD.viewportWidth, 1);
  expect(viewportBox!.width / MOBILE_PLAYFIELD.whiteUnitPx).toBeCloseTo(
    MOBILE_PLAYFIELD.viewportWhiteUnits,
    2,
  );

  const initiallyFramedWhites = await keyboard
    .locator(".piano-key.white")
    .evaluateAll(
      (elements, frame) =>
        elements
          .filter((element) => {
            const box = element.getBoundingClientRect();
            return (
              box.left >= frame.left - 0.5 && box.right <= frame.right + 0.5
            );
          })
          .map((element) => element.getAttribute("aria-label")),
      {
        left: viewportBox!.x,
        right: viewportBox!.x + viewportBox!.width,
      },
    );
  expect(initiallyFramedWhites).toEqual([
    "C4",
    "D4",
    "E4",
    "F4",
    "G4",
    "A4",
    "B4",
  ]);

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

  const compactLandmarks = await page.evaluate(() => {
    const boxes = [
      ".studio-topbar",
      ".control-deck",
      ".instrument-stage",
      ".keyboard-panel",
    ].map((selector) =>
      document.querySelector(selector)?.getBoundingClientRect(),
    );
    if (boxes.some((box) => box === undefined)) {
      throw new Error("compact Play landmarks are missing");
    }

    const [topbar, deck, stage, keyboardPanel] = boxes as DOMRect[];
    return {
      deckHeight: deck.height,
      keyboardBottom: keyboardPanel.bottom,
      keyboardTop: keyboardPanel.top,
      rackTop: stage.top,
      topbarHeight: topbar.height,
    };
  });
  expect(compactLandmarks.topbarHeight).toBeLessThanOrEqual(96);
  expect(compactLandmarks.deckHeight).toBeLessThanOrEqual(230);
  expect(compactLandmarks.rackTop).toBeLessThan(350);
  expect(compactLandmarks.keyboardTop).toBeLessThan(700);
  expect(compactLandmarks.keyboardBottom).toBeLessThanOrEqual(760);

  const compactTargets = await page
    .getByRole("region", { name: "Performance controls" })
    .locator(".dial-assembly")
    .evaluateAll((elements) =>
      elements.map((element) => {
        const box = element.getBoundingClientRect();
        return { height: box.height, width: box.width };
      }),
    );
  expect(compactTargets).toHaveLength(3);
  expect(
    compactTargets.every(({ height, width }) => height >= 44 && width >= 44),
  ).toBe(true);

  for (const panelSelector of [".control-deck-status"]) {
    const contained = await page.locator(panelSelector).evaluate((panel) => {
      const panelBox = panel.getBoundingClientRect();
      return [...panel.querySelectorAll("span, p, select")].every((element) => {
        const box = element.getBoundingClientRect();
        return (
          box.left >= panelBox.left - 1 &&
          box.right <= panelBox.right + 1 &&
          box.top >= panelBox.top - 1 &&
          box.bottom <= panelBox.bottom + 1
        );
      });
    });
    expect(contained).toBe(true);
  }

  for (const lane of [
    rack.locator(".note-lane-naturals"),
    rack.locator(".note-lane-sharps"),
  ]) {
    const supportAlignment = await lane.evaluate((element) => {
      const supports = [
        ...element.querySelectorAll<SVGSVGElement>(".row-support"),
      ];
      const bars = [...element.querySelectorAll<HTMLElement>(".bar")];
      if (supports.length !== 2) {
        throw new Error("mobile row support geometry is missing");
      }

      return supports.flatMap((svg) => {
        const polyline = svg.querySelector("polyline");
        const position = svg.dataset.mountPosition;
        if (
          !(polyline instanceof SVGPolylineElement) ||
          (position !== "upper" && position !== "lower")
        ) {
          throw new Error("mobile row support line is missing");
        }
        const svgBox = svg.getBoundingClientRect();
        const viewBox = svg.viewBox.baseVal;
        const points = Array.from(polyline.points);
        return bars.map((bar, index) => {
          const mount = bar.querySelector<HTMLElement>(
            `.bar-mount[data-mount-position="${position}"]`,
          );
          if (mount === null) {
            throw new Error("mobile bar mount is missing");
          }
          const mountBox = mount.getBoundingClientRect();
          const point = points[index];
          return {
            dx: Math.abs(
              mountBox.left +
                mountBox.width / 2 -
                (svgBox.left + (point.x / viewBox.width) * svgBox.width),
            ),
            dy: Math.abs(
              mountBox.top +
                mountBox.height / 2 -
                (svgBox.top + (point.y / viewBox.height) * svgBox.height),
            ),
          };
        });
      });
    });
    expect(supportAlignment.every(({ dx, dy }) => dx <= 1 && dy <= 1)).toBe(
      true,
    );
  }

  const mobileLayers = await rack.evaluate((element) => ({
    accidentals: Number(
      getComputedStyle(element.querySelector(".bar.accidental") as HTMLElement)
        .zIndex,
    ),
    mallet: Number(
      getComputedStyle(element.querySelector(".mallet") as HTMLElement).zIndex,
    ),
    naturals: Number(
      getComputedStyle(element.querySelector(".bar.natural") as HTMLElement)
        .zIndex,
    ),
    supports: Number(
      getComputedStyle(element.querySelector(".row-support") as SVGElement)
        .zIndex,
    ),
  }));
  expect(mobileLayers.naturals).toBeGreaterThan(mobileLayers.supports);
  expect(mobileLayers.accidentals).toBeGreaterThan(mobileLayers.naturals);
  expect(mobileLayers.mallet).toBeGreaterThan(mobileLayers.accidentals);

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

  const mobileMalletOverlap = await rack.evaluate((element) => {
    const mallet = element.querySelector(".mallet")?.getBoundingClientRect();
    if (mallet === undefined) {
      throw new Error("mobile mallet is missing");
    }

    return [...element.querySelectorAll(".bar")].some((bar) => {
      const box = bar.getBoundingClientRect();
      return (
        mallet.left < box.right &&
        mallet.right > box.left &&
        mallet.top < box.bottom &&
        mallet.bottom > box.top
      );
    });
  });
  expect(mobileMalletOverlap).toBe(false);

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
  await expectNoSeriousAxeViolations(page);
  await expectFullKeyboardTraversal(page);
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
  await expect(rack.locator(".bar-mount")).toHaveCount(50);
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

test("rack geometry aligns constant bars, supports, and foreground mallet", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1024, height: 768 });
  await installStableEngine(page);
  await page.goto("/#/play");
  await expect(page.getByText(ENGINE_READY, { exact: true })).toBeVisible();

  const rack = page.getByRole("region", { name: "Playable glockenspiel" });
  const keyboard = page.getByRole("region", { name: "Piano alignment" });
  const naturalLane = rack.locator(".note-lane-naturals");
  const accidentalLane = rack.locator(".note-lane-sharps");
  const naturals = naturalLane.locator(".bar.natural");
  const accidentals = accidentalLane.locator(".bar.accidental");

  const [rackBodyBox, keyboardBox, firstWhiteKeyBox] = await Promise.all([
    rack.locator(".rack").boundingBox(),
    keyboard.boundingBox(),
    keyboard.locator(".piano-key.white").first().boundingBox(),
  ]);
  expect(rackBodyBox).not.toBeNull();
  expect(keyboardBox).not.toBeNull();
  expect(firstWhiteKeyBox).not.toBeNull();
  expect(rackBodyBox!.width).toBeLessThanOrEqual(1000);
  expect(
    Math.abs(
      rackBodyBox!.x +
        rackBodyBox!.width / 2 -
        (keyboardBox!.x + keyboardBox!.width / 2),
    ),
  ).toBeLessThanOrEqual(1);
  expect(
    keyboardBox!.y - (rackBodyBox!.y + rackBodyBox!.height),
  ).toBeLessThanOrEqual(40);
  expect(firstWhiteKeyBox!.y + firstWhiteKeyBox!.height).toBeLessThanOrEqual(
    768,
  );

  for (const bars of [naturals, accidentals]) {
    const metrics = await bars.evaluateAll((elements) =>
      elements.map((element) => {
        const box = element.getBoundingClientRect();
        return { bottom: box.bottom, top: box.top, width: box.width };
      }),
    );
    const widths = metrics.map(({ width }) => width);
    expect(Math.max(...widths) - Math.min(...widths)).toBeLessThanOrEqual(0.25);
    for (let index = 1; index < metrics.length; index += 1) {
      expect(metrics[index].top).toBeGreaterThan(metrics[index - 1].top);
      expect(metrics[index].bottom).toBeLessThan(metrics[index - 1].bottom);
    }
    const first = metrics[0];
    const last = metrics.at(-1)!;
    expect(last.top - first.top).toBeCloseTo(first.bottom - last.bottom, 0);
    const centers = metrics.map(({ top, bottom }) => (top + bottom) / 2);
    expect(Math.max(...centers) - Math.min(...centers)).toBeLessThanOrEqual(
      0.25,
    );
  }

  const [naturalWidth, accidentalWidth] = await Promise.all([
    naturals
      .first()
      .evaluate((element) => element.getBoundingClientRect().width),
    accidentals
      .first()
      .evaluate((element) => element.getBoundingClientRect().width),
  ]);
  expect(accidentalWidth).toBeCloseTo(naturalWidth, 1);

  await expect(rack.locator(".row-support")).toHaveCount(4);
  await expect(rack.locator(".rail")).toHaveCount(0);

  for (const lane of [naturalLane, accidentalLane]) {
    await expect(lane.locator(".row-support")).toHaveCount(2);
    const alignment = await lane.evaluate((element) => {
      const supports = [
        ...element.querySelectorAll<SVGSVGElement>(".row-support"),
      ];
      const bars = [...element.querySelectorAll<HTMLElement>(".bar")];
      if (supports.length !== 2) {
        throw new Error("row support geometry is missing");
      }

      return supports.flatMap((svg) => {
        const polyline = svg.querySelector("polyline");
        const position = svg.dataset.mountPosition;
        if (
          !(polyline instanceof SVGPolylineElement) ||
          (position !== "upper" && position !== "lower")
        ) {
          throw new Error("row support line is missing");
        }
        const svgBox = svg.getBoundingClientRect();
        const viewBox = svg.viewBox.baseVal;
        const points = Array.from(polyline.points);
        return bars.map((bar, index) => {
          const mount = bar.querySelector<HTMLElement>(
            `.bar-mount[data-mount-position="${position}"]`,
          );
          if (mount === null) {
            throw new Error("bar mount is missing");
          }
          const mountBox = mount.getBoundingClientRect();
          const point = points[index];
          return {
            dx: Math.abs(
              mountBox.left +
                mountBox.width / 2 -
                (svgBox.left + (point.x / viewBox.width) * svgBox.width),
            ),
            dy: Math.abs(
              mountBox.top +
                mountBox.height / 2 -
                (svgBox.top + (point.y / viewBox.height) * svgBox.height),
            ),
          };
        });
      });
    });
    expect(alignment.every(({ dx, dy }) => dx <= 1 && dy <= 1)).toBe(true);
  }

  const layerOrder = await rack.evaluate((element) => {
    const naturalLane = element.querySelector(".note-lane-naturals");
    const accidentalLane = element.querySelector(".note-lane-sharps");
    const support = element.querySelector(".row-support");
    const natural = element.querySelector(".bar.natural");
    const accidental = element.querySelector(".bar.accidental");
    const mallet = element.querySelector(".mallet");
    if (
      !(naturalLane instanceof HTMLElement) ||
      !(accidentalLane instanceof HTMLElement) ||
      !(support instanceof SVGElement) ||
      !(natural instanceof HTMLElement) ||
      !(accidental instanceof HTMLElement) ||
      !(mallet instanceof HTMLElement)
    ) {
      throw new Error("rack layers are missing");
    }
    return {
      accidental: Number(getComputedStyle(accidental).zIndex),
      accidentalLane: getComputedStyle(accidentalLane).zIndex,
      mallet: Number(getComputedStyle(mallet).zIndex),
      natural: Number(getComputedStyle(natural).zIndex),
      naturalLane: getComputedStyle(naturalLane).zIndex,
      support: Number(getComputedStyle(support).zIndex),
    };
  });
  expect(layerOrder.accidentalLane).toBe("auto");
  expect(layerOrder.naturalLane).toBe("auto");
  expect(layerOrder.natural).toBeGreaterThan(layerOrder.support);
  expect(layerOrder.accidental).toBeGreaterThan(layerOrder.natural);
  expect(layerOrder.mallet).toBeGreaterThan(layerOrder.accidental);

  const mallet = rack.locator(".mallet");
  await expect(mallet).toHaveCSS("opacity", "1");
  const [malletBox, barBoxes, malletRackBox] = await Promise.all([
    mallet.boundingBox(),
    rack.locator(".bar").evaluateAll((elements) =>
      elements.map((element) => {
        const box = element.getBoundingClientRect();
        return {
          bottom: box.bottom,
          left: box.left,
          right: box.right,
          top: box.top,
        };
      }),
    ),
    rack.locator(".rack").boundingBox(),
  ]);
  expect(malletBox).not.toBeNull();
  expect(malletRackBox).not.toBeNull();
  const originalMalletWidth = Math.min(156, Math.max(136, 1024 * 0.14));
  const originalMalletHeight = originalMalletWidth / 4.2;
  expect(malletBox!.width).toBeCloseTo(originalMalletWidth * 2, 0);
  expect(malletBox!.y).toBeCloseTo(
    malletRackBox!.y + malletRackBox!.height - originalMalletHeight + 1,
    0,
  );
  expect(
    malletRackBox!.x + malletRackBox!.width - (malletBox!.x + malletBox!.width),
  ).toBeCloseTo(malletRackBox!.width * 0.03, 0);
  expect(
    barBoxes.some(
      (bar) =>
        malletBox!.x < bar.right &&
        malletBox!.x + malletBox!.width > bar.left &&
        malletBox!.y < bar.bottom &&
        malletBox!.y + malletBox!.height > bar.top,
    ),
  ).toBe(false);
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
  const reverb = deck.getByRole("slider", { name: "Reverb" });
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
  await expect(reverb).toHaveAttribute("type", "range");
  await expect(reverb).toHaveAttribute("min", "0");
  await expect(reverb).toHaveAttribute("max", "100");
  await expect(reverb).toHaveValue("20");

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

  // The reverb reads as a percentage like the volume does, and it is the one
  // dial whose range reaches zero: a closed control is an exact bypass in the
  // engine, so the bottom of this range has to be reachable.
  await reverb.fill("0");
  await expect(reverb).toHaveValue("0");
  await expect(deck.locator('output[for="reverb"]')).toHaveText("0%");
  await reverb.fill("45");
  await expect(deck.locator('output[for="reverb"]')).toHaveText("45%");

  // The wood species is decor, not a performance control, and stays absent.
  // The sound is a performance control and is present -- the two assertions sit
  // together so the deck's contents are one decision rather than two.
  await expect(deck.getByRole("combobox", { name: "Wood" })).toHaveCount(0);
  await expect(page.locator("html")).toHaveAttribute(
    "data-wood-species",
    "walnut",
  );

  const sound = deck.getByRole("combobox", { name: "Sound" });
  await expect(sound).toBeVisible();
  await expect(sound).toHaveValue("default");
  await expect(sound.locator("option")).toHaveText([
    "Default Glockenspiel",
    "Recorded Bar",
  ]);

  await sound.selectOption("recorded-bar");
  await expect(sound).toHaveValue("recorded-bar");

  await expect(status).toHaveAttribute("aria-live", "polite");
  await expect(status).toHaveAttribute("data-error", "false");
  await expect(status).toHaveText(ENGINE_READY);
});

// The engine outlives a tab switch, and so must the sound it was told to play.
// Optimize unmounts the whole play surface, so a picker that kept its own state
// would come back on the default while the engine went on playing the other
// bar -- a disagreement nothing on screen could explain.
test("the chosen sound survives a trip through Optimize", async ({ page }) => {
  await installStableEngine(page);
  await page.goto("/#/play");

  const sound = page
    .getByRole("region", { name: "Performance controls" })
    .getByRole("combobox", { name: "Sound" });

  await sound.selectOption("recorded-bar");
  await expect(sound).toHaveValue("recorded-bar");

  // Navigated by clicking the tabs rather than by goto, because that is the
  // trip being tested: the router is a hashchange listener, and a fresh goto
  // would reload the document and reset the state whether or not App holds it.
  const tabs = page.getByRole("navigation", { name: "Sections" });
  await tabs.getByRole("link", { name: "Optimize" }).click();
  await expect(sound).toHaveCount(0);

  await tabs.getByRole("link", { name: "Play" }).click();
  await expect(sound).toHaveValue("recorded-bar");
});

test("play surface uses distinct walnut, maple, and beech layers", async ({
  page,
}) => {
  await installStableEngine(page);
  await page.goto("/#/play");

  const materials = await page.evaluate(() => {
    const topbar = document.querySelector(".studio-topbar");
    const rack = document.querySelector(".rack");
    if (!(topbar instanceof HTMLElement) || !(rack instanceof HTMLElement)) {
      throw new Error("wood surfaces are missing");
    }

    return {
      canvas: getComputedStyle(document.body).backgroundImage,
      frame: getComputedStyle(rack).backgroundImage,
      header: getComputedStyle(topbar).backgroundImage,
      rackBack: getComputedStyle(rack, "::before").backgroundImage,
    };
  });

  expect(materials.canvas).toContain("beech");
  expect(materials.frame).toContain("walnut");
  expect(materials.header).toContain("walnut");
  expect(materials.rackBack).toContain("maple");
});

test("performance dials share aged brass without changing their controls", async ({
  page,
}) => {
  await installStableEngine(page);
  await page.goto("/#/play");

  const deck = page.getByRole("region", { name: "Performance controls" });
  const faces = deck.locator(".dial-face");
  const volume = deck.getByRole("slider", { name: "Volume" });
  const velocity = deck.getByRole("slider", { name: "Velocity" });
  const reverb = deck.getByRole("slider", { name: "Reverb" });

  await expect(faces).toHaveCount(3);
  await expect(faces.nth(0)).toHaveClass(/\bdial-face-aged-brass\b/);
  await expect(faces.nth(1)).toHaveClass(/\bdial-face-aged-brass\b/);
  await expect(faces.nth(2)).toHaveClass(/\bdial-face-aged-brass\b/);

  const materials = await faces.evaluateAll((elements) =>
    elements.map((element) => {
      const rect = element.getBoundingClientRect();

      return {
        height: rect.height,
        indicator: getComputedStyle(
          element.querySelector(".dial-indicator") as HTMLElement,
        ).backgroundColor,
        inner: getComputedStyle(element, "::before").backgroundImage,
        outer: getComputedStyle(element).backgroundImage,
        width: rect.width,
      };
    }),
  );

  expect(
    materials.every(({ width, height }) => width === 66 && height === 66),
  ).toBe(true);
  expect(new Set(materials.map(({ outer }) => outer)).size).toBe(1);
  expect(new Set(materials.map(({ inner }) => inner)).size).toBe(1);
  expect(materials[0]?.outer).toContain("repeating-conic-gradient");
  expect(materials[0]?.outer).toContain("conic-gradient");
  expect(materials[0]?.inner).toContain("repeating-linear-gradient");
  expect(materials[0]?.inner).toContain("radial-gradient");
  expect(
    materials.every(({ indicator }) => indicator !== "rgba(0, 0, 0, 0)"),
  ).toBe(true);

  await volume.focus();
  const focusedStyle = await faces.nth(0).evaluate((element) => {
    const style = getComputedStyle(element);

    return {
      style: style.outlineStyle,
      width: Number.parseFloat(style.outlineWidth),
    };
  });
  expect(focusedStyle.style).toBe("solid");
  expect(focusedStyle.width).toBeGreaterThanOrEqual(3);

  const restingTransform = await faces
    .nth(0)
    .locator(".dial-indicator")
    .evaluate((element) => getComputedStyle(element).transform);
  await volume.press("ArrowUp");
  await expect(volume).toHaveValue("71");
  const adjustedTransform = await faces
    .nth(0)
    .locator(".dial-indicator")
    .evaluate((element) => getComputedStyle(element).transform);
  expect(adjustedTransform).not.toBe(restingTransform);

  await faces.nth(1).click({ position: { x: 33, y: 5 } });
  await expect(velocity).not.toHaveValue("96");
  await expect(deck.locator('output[for="velocity"]')).toHaveText(
    await velocity.inputValue(),
  );

  // The third face is driven the same way as the second, so the pointer
  // gesture is asserted on the dial that was added last rather than only on
  // the two that were there when this test was written.
  await faces.nth(2).click({ position: { x: 33, y: 5 } });
  await expect(reverb).not.toHaveValue("20");
  await expect(deck.locator('output[for="reverb"]')).toHaveText(
    `${await reverb.inputValue()}%`,
  );
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

test("Play and Optimize support full keyboard traversal with visible focus", async ({
  page,
}) => {
  await installStableEngine(page);
  await page.goto("/#/play");
  await expect(page.getByText(ENGINE_READY, { exact: true })).toBeVisible();
  await expectFullKeyboardTraversal(page);

  await installStableFitApi(page);
  await page.goto("/#/optimize");
  await expect(
    page.getByRole("status", {
      name: "Fit service connected · visual-test",
    }),
  ).toBeVisible();
  await exposeAllOptimizeControls(page);
  await expectFullKeyboardTraversal(page);
});

test("Play and available Optimize have no serious Axe violations", async ({
  page,
}) => {
  await installStableEngine(page);
  await page.goto("/#/play");
  await expect(page.getByText(ENGINE_READY, { exact: true })).toBeVisible();
  await expectNoSeriousAxeViolations(page);

  await installStableFitApi(page);
  await page.goto("/#/optimize");
  await expect(
    page.getByRole("status", {
      name: "Fit service connected · visual-test",
    }),
  ).toBeVisible();
  await exposeAllOptimizeControls(page);
  await expectNoSeriousAxeViolations(page);
});

test("reduced-motion preference removes nonessential transitions", async ({
  page,
}) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await installStableEngine(page);
  await page.goto("/#/play");

  const durations = await page
    .locator(".bar")
    .first()
    .evaluate((element) => {
      const style = getComputedStyle(element);
      return {
        animation: style.animationDuration,
        transition: style.transitionDuration,
      };
    });
  expect(Number.parseFloat(durations.animation)).toBeLessThanOrEqual(0.001);
  expect(Number.parseFloat(durations.transition)).toBeLessThanOrEqual(0.001);
});

for (const width of [390, 760, 1024, 1440]) {
  test(`Play and Optimize stay inside the body at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 1000 });
    await installStableEngine(page);
    await page.goto("/#/play");
    await expect(page.getByText(ENGINE_READY, { exact: true })).toBeVisible();

    await expectNoBodyOverflow(page);

    if (width <= 760) {
      await expectTouchTargets(page);
    }

    await installStableFitApi(page);
    await page.goto("/#/optimize");
    await expect(
      page.getByRole("status", {
        name: "Fit service connected · visual-test",
      }),
    ).toBeVisible();
    await exposeAllOptimizeControls(page);
    await expectNoBodyOverflow(page);

    if (width <= 760) {
      await expectTouchTargets(page);
    }
  });
}

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
      name: "Browser optimizer ready · WebAssembly",
    }),
  ).toHaveAttribute("data-state", "available");
  await expect(
    unavailablePage.getByText(
      "This static version runs fitting locally in WebAssembly. It is slower than the native service, but uses the same objectives, parameter bounds and optimizer backends.",
      { exact: true },
    ),
  ).toBeVisible();
  await expect(unavailablePage.locator(".fit-form")).toBeVisible();
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
  // Exact, because the tuning editor also offers "Male population" and
  // "Female population" knobs that a substring match would collide with.
  await form.getByLabel("Population", { exact: true }).fill("37");
  await form.getByLabel("Seed", { exact: true }).fill("42");
  await optimizer.selectOption("simple");
  await optimizer.selectOption("mayfly");
  await expect(form.getByLabel("Population", { exact: true })).toHaveValue(
    "37",
  );
  await expect(form.getByLabel("Seed", { exact: true })).toHaveValue("42");
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

/* ------------------------------------------------------------------ */
/* Theme                                                               */
/* ------------------------------------------------------------------ */

async function readRootTheme(page: Page): Promise<string | null> {
  return page.evaluate(() =>
    document.documentElement.getAttribute("data-theme"),
  );
}

async function readColorScheme(page: Page): Promise<string> {
  return page.evaluate(
    () => getComputedStyle(document.documentElement).colorScheme,
  );
}

test("the theme switch writes the choice onto the document and keeps it", async ({
  page,
}) => {
  await installStableEngine(page);
  await page.goto("/#/play");

  const auto = page.getByRole("radio", { name: "Auto" });
  const dark = page.getByRole("radio", { name: "Dark" });

  await expect(auto).toBeChecked();
  expect(await readRootTheme(page)).toBeNull();

  await dark.check();
  expect(await readRootTheme(page)).toBe("dark");
  expect(await readColorScheme(page)).toBe("dark");

  // The choice has to survive a reload, and it has to be applied by the
  // blocking script in index.html rather than by React: the attribute is
  // already on the root when the document has only just been parsed.
  await page.reload();
  expect(await readRootTheme(page)).toBe("dark");
  await expect(page.getByRole("radio", { name: "Dark" })).toBeChecked();

  // Back to auto, which is stored as the absence of a choice.
  await page.getByRole("radio", { name: "Auto" }).check();
  expect(await readRootTheme(page)).toBeNull();
  expect(
    await page.evaluate(() => localStorage.getItem("algo-glockenspiel:theme")),
  ).toBeNull();
});

test("auto follows the system, and an explicit light overrides it", async ({
  page,
}) => {
  await installStableEngine(page);
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto("/#/play");

  await expect(page.getByRole("radio", { name: "Auto" })).toBeChecked();
  expect(await readColorScheme(page)).toBe("dark");

  await page.getByRole("radio", { name: "Light" }).check();
  expect(await readColorScheme(page)).toBe("light");

  // A system flip while an explicit choice stands must not move the page.
  await page.emulateMedia({ colorScheme: "light" });
  expect(await readColorScheme(page)).toBe("light");

  await page.getByRole("radio", { name: "Auto" }).check();
  expect(await readColorScheme(page)).toBe("light");
  await page.emulateMedia({ colorScheme: "dark" });
  expect(await readColorScheme(page)).toBe("dark");
});

/*
 * The palette is what the dark theme changes, so it is the palette that is
 * checked: every surface the theme owns has to actually be darker than the ink
 * on it, and a token that was added to the light block and forgotten in the
 * dark one shows up here as a cream panel that never changed.
 */
test("dark repaints the page surfaces without touching the instrument", async ({
  page,
}) => {
  await installStableEngine(page);
  await page.goto("/#/play");

  const sample = async () =>
    page.evaluate(() => {
      const read = (selector: string, property: string) => {
        const element = document.querySelector(selector);
        if (element === null) {
          throw new Error(`no ${selector} to sample`);
        }
        return getComputedStyle(element).getPropertyValue(property).trim();
      };

      return {
        body: read("body", "background-color"),
        ink: read("body", "color"),
        card: read(".instrument-card", "background-color"),
        deck: read(".control-deck", "background-color"),
        keyboard: read(".keyboard-panel", "background-color"),
        whiteKey: read(".piano-key.white", "background-image"),
      };
    });

  const light = await sample();

  await page.getByRole("radio", { name: "Dark" }).check();
  const dark = await sample();

  for (const surface of ["body", "card", "deck", "keyboard"] as const) {
    expect(dark[surface], `${surface} is unchanged by the dark theme`).not.toBe(
      light[surface],
    );
    expect(luminance(dark[surface])).toBeLessThan(luminance(light[surface]));
    // Ink over surface, both from the same theme, has to stay legible.
    expect(luminance(dark.ink)).toBeGreaterThan(luminance(dark[surface]) + 0.4);
  }

  // The instrument is an object in the room, not part of the lighting.
  expect(dark.whiteKey).toBe(light.whiteKey);
});

/** Rough perceptual lightness of an `rgb()` string, 0..1. */
function luminance(color: string): number {
  const parts = color.match(/[\d.]+/g);

  if (parts === null || parts.length < 3) {
    throw new Error(`not a color: ${color}`);
  }

  const [red, green, blue] = parts.map(Number);

  return (0.2126 * red + 0.7152 * green + 0.0722 * blue) / 255;
}

test("dark Play and dark Optimize have no serious Axe violations", async ({
  page,
}) => {
  await installStableEngine(page);
  await page.goto("/#/play");
  await expect(page.getByText(ENGINE_READY, { exact: true })).toBeVisible();
  await page.getByRole("radio", { name: "Dark" }).check();
  await expectNoSeriousAxeViolations(page);

  await installStableFitApi(page);
  await page.goto("/#/optimize");
  await expect(
    page.getByRole("status", {
      name: "Fit service connected \u00b7 visual-test",
    }),
  ).toBeVisible();
  await exposeAllOptimizeControls(page);
  await expectNoSeriousAxeViolations(page);
});

test("Play at 1440x1000 in dark", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await installStableEngine(page);
  await page.goto("/#/play");

  await expect(page.getByText(ENGINE_READY, { exact: true })).toBeVisible();
  await page.getByRole("radio", { name: "Dark" }).check();
  await waitForStablePaint(page);
  await expect(page).toHaveScreenshot("play-dark-1440x1000.png");
});

test("Optimize at 1440x1000 in dark", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await installStableEngine(page);
  await installStableFitApi(page);
  await page.goto("/#/optimize");

  await expect(
    page.getByRole("status", {
      name: "Fit service connected \u00b7 visual-test",
    }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Start fit" })).toBeVisible();
  await page.getByRole("radio", { name: "Dark" }).check();
  await waitForStablePaint(page);
  await expect(page).toHaveScreenshot("optimize-dark-1440x1000.png");
});
