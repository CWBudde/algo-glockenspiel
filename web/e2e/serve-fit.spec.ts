import path from "node:path";

import { expect, test } from "@playwright/test";

import { goFitServerIsReal, noGoServerReason } from "./goServer";
import { installStableEngine } from "./stableEngine";

/**
 * Drives a real fit through `glockenspiel serve`, started by the second
 * `webServer` entry in playwright.config.ts. Every other spec in this
 * directory exercises the app's layout against a mocked or WASM-only fit
 * API; this one is the end-to-end proof that the HTTP service Task 1 through
 * 8 built actually runs a fit and shows its result, and it is deliberately a
 * behaviour test rather than a screenshot: real optimizer runs are not
 * pixel-stable across machines the way a mocked snapshot is.
 */

const referencePath = path.resolve(
  import.meta.dirname,
  "../../testdata/reference/glockenspiel_a4.wav",
);

/** One line of trace.jsonl, as far as this test reads it. */
interface TraceLine {
  best: number;
}

test.describe("a real fit through the Go server", () => {
  test("the cost curve falls, and the comparison shows both signals", async ({
    page,
    request,
  }, testInfo) => {
    testInfo.setTimeout(120_000);

    // scripts/playwright-go-server.sh falls back to a bare listener when the
    // Go toolchain is unavailable or the build fails, so this suite is not
    // aborted for a developer without Go installed. That listener answers
    // every path with the same small document, where the real server answers
    // the job history with a `jobs` array. A real list is therefore the
    // proof, and not -- as it once was -- a 404 from an idle GET /api/fit:
    // the server now adopts run directories it finds in its work directory,
    // so a server reused from an earlier run of this suite may well have an
    // active job before this spec starts one, and reading that as "the
    // fallback is up" would skip the one spec that runs a real fit.
    test.skip(!(await goFitServerIsReal(request)), noGoServerReason);

    await installStableEngine(page);
    await page.goto("/#/optimize");

    await expect(
      page.getByRole("status", { name: /Fit service connected/ }),
    ).toBeVisible({ timeout: 15_000 });

    const form = page.locator(".fit-form");

    // A short reference and a small budget: enough iterations that a local
    // search reliably improves on its starting point, short enough that the
    // whole run through a real render finishes inside the test's patience.
    await form
      .getByLabel("Reference recording (WAV)")
      .setInputFiles(referencePath);
    await form.getByLabel("Iteration limit").fill("20");

    // "Report every" lives inside Advanced settings, collapsed by default.
    await form.getByText("Advanced settings", { exact: true }).click();
    await form.getByLabel("Report every").fill("1");

    await form.getByRole("button", { name: "Start fit" }).click();

    const jobState = form.locator(".fit-job-state");
    await expect(jobState).toHaveText(/^Fit \S+ running$/, {
      timeout: 15_000,
    });

    const stateText = await jobState.textContent();
    const jobId = stateText?.match(/^Fit (\S+) running$/)?.[1];
    expect(jobId, `job id parsed from "${stateText}"`).toBeTruthy();

    await expect(jobState).toHaveText(/^Fit \S+ succeeded$/, {
      timeout: 90_000,
    });

    // The cost curve falls: read the run's own trace rather than the chart,
    // which is drawn from the same numbers and would make this a test of
    // itself. `best` is a running minimum by construction (trace.go), so
    // this only fails if the search made no improvement at all.
    const trace = await request.get(`/api/fit/jobs/${jobId}/trace`);
    expect(trace.ok()).toBe(true);

    const lines = (await trace.text())
      .split("\n")
      .filter((line) => line.trim().length > 0)
      .map((line) => JSON.parse(line) as TraceLine);

    expect(lines.length).toBeGreaterThan(1);
    expect(lines.at(-1)?.best).toBeLessThan(lines[0].best);

    // The comparison view shows both signals, by their accessible labels
    // rather than by pixels: this is a behaviour test, not a screenshot.
    const comparison = page.getByRole("region", { name: "Comparison" });
    await expect(
      comparison.getByRole("img", { name: /^Reference waveform/ }),
    ).toBeVisible({ timeout: 15_000 });
    await expect(
      comparison.getByRole("img", { name: /^Fitted render waveform/ }),
    ).toBeVisible();
  });
});
