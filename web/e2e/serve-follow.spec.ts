import fs from "node:fs";
import path from "node:path";

import { expect, test } from "@playwright/test";

import { goFitServerIsReal, noGoServerReason } from "./goServer";
import { installStableEngine } from "./stableEngine";

/**
 * Pins Phase 8.8's browser half against the real Go server: a fit nobody
 * asked this server to run -- `glockenspiel fit` in a second terminal, or a
 * campaign job -- is a directory appearing under its `--work-dir`, and the
 * Optimize tab has to show it as a live run that it cannot stop.
 *
 * The run directory is written here by hand, in the order fitrun writes it,
 * for the same reason internal/server/follow_internal_test.go writes one: the
 * state under test -- a config.json with no result.json beside it, and a
 * trace still growing -- is one no finished run leaves behind. Nothing about
 * the API is stubbed. A route mock would prove that the components render a
 * `followed` field the test itself invented, which is exactly the thing worth
 * not believing: the point of the assertion is that a real directory reaches
 * a real browser through the real server's scan.
 *
 * It is named to sort after serve-fit.spec.ts. Adopting a run makes it the
 * server's active job, so `GET /api/fit` stops answering 404, and that 404 is
 * how the other spec recognises it is talking to the real server rather than
 * to the fallback listener.
 */

/**
 * Where the server under test keeps its runs.
 *
 * The same path scripts/playwright-go-server.sh passes to `--work-dir`, and
 * the same environment variable it honours. A spec that guessed wrong would
 * write a directory nobody reads and then wait for a row that cannot appear,
 * so the two resolve it the same way from the same place.
 */
const workDir =
  process.env.GLOCKENSPIEL_E2E_WORK_DIR ??
  path.resolve(import.meta.dirname, "../test-results/fit-work");

/** The config.json a run leaves before it starts, as fitrun writes it. */
function runConfig(started: Date): string {
  return JSON.stringify({
    note: 84,
    velocity: 100,
    sample_rate: 48_000,
    metric: "balanced",
    modes: 8,
    max_iterations: 40,
    report_every: 1,
    seed: 12_345,
    time_budget: "1m0s",
    reference_options: { downmix: "mono", window_ms: 0 },
    engine: { name: "mayfly" },
    reference: { seconds: 1.5 },
    started: started.toISOString(),
  });
}

/**
 * Appends one whole line to trace.jsonl, in the shape internal/fitrun's trace
 * writer writes and flushes: whole lines only, so a reader arriving mid-write
 * never finds a fragment.
 *
 * `terms` is carried because the term bars are read from it: they are the
 * half of this checkbox that a cost curve alone would not prove.
 */
function appendTraceLine(dir: string, iteration: number, best: number): void {
  const line = JSON.stringify({
    iteration,
    optimizer_iterations: iteration,
    restart: 0,
    lambda: 0,
    evaluations: iteration * 20,
    elapsed_ms: iteration * 500,
    current: best + 0.05,
    best,
    terms: {
      partial_cents: 12.5,
      partial_level_db: 3.25,
      spectral_fine_db: 2.5,
      envelope_db: 1.5,
    },
  });

  fs.appendFileSync(path.join(dir, "trace.jsonl"), `${line}\n`);
}

/** The result.json that ends a run, as fitrun writes it. */
function writeRunResult(dir: string, score: number): void {
  fs.writeFileSync(
    path.join(dir, "result.json"),
    JSON.stringify({
      score,
      evaluations: 60,
      iterations: 3,
      stop_reason: "budget",
      elapsed_seconds: 1.5,
      converged: true,
    }),
  );
}

test.describe("a run directory the server did not start", () => {
  test("shows up in the run list as a live, followed, unstoppable fit", async ({
    page,
    request,
  }, testInfo) => {
    testInfo.setTimeout(90_000);

    test.skip(!(await goFitServerIsReal(request)), noGoServerReason);

    // Started now, so this run sorts to the top of a history that may hold
    // rows from an earlier run of this suite against a reused server.
    const started = new Date();
    const jobId = `fit-${started.toISOString().replace(/[-:.]/g, "")}-e2e`;
    const dir = path.join(workDir, jobId);

    fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(path.join(dir, "config.json"), runConfig(started));
    appendTraceLine(dir, 1, 0.9);

    await installStableEngine(page);
    await page.goto("/#/optimize");

    await expect(
      page.getByRole("status", { name: /Fit service connected/ }),
    ).toBeVisible({ timeout: 15_000 });

    // The server rescans once a second and the list polls; the row appears
    // without anything here reloading, which is the arrangement under test.
    const row = page.locator(".run-list-row").first();

    await expect(row).toContainText("Running", { timeout: 20_000 });
    await expect(row).toContainText("followed");
    await expect(row).toContainText("0.900000");

    // The numbers are the trace's: another line, and the row moves with it.
    appendTraceLine(dir, 2, 0.4);
    await expect(row).toContainText("0.400000", { timeout: 20_000 });

    await row.click();

    const status = page.locator(".fit-status");
    await expect(status).toContainText(jobId);
    await expect(status).toContainText("followed run directory");

    // The cost curve is drawn from the tailed trace: two lines, two samples.
    // The summary beside the canvas is the curve's own text alternative, so
    // reading it is reading the chart rather than the data behind it.
    await expect(page.locator(".cost-chart")).toContainText(
      /Cost curve over [2-9]\d* samples/,
      { timeout: 20_000 },
    );

    // And so are the term bars, which have nothing to show until a trace line
    // carries a breakdown.
    const terms = page.locator(".term-bars");
    await expect(terms).not.toContainText("There is nothing to show yet");
    await expect(terms.locator(".term-bar-row").first()).toBeVisible();

    await expect(status).toContainText("cannot stop it");

    // The stop control cannot reach this run, and says so rather than
    // offering a button whose only answer is a 409.
    //
    // The form's buttons act on the job the page itself is tracking, not on
    // whichever row is selected, so the run has to be that job for this to be
    // an assertion about anything: a reload adopts the server's active job,
    // which is this one, because adopting a run directory records it like any
    // other fit.
    await page.reload();

    const cancel = page
      .locator(".fit-form")
      .getByRole("button", { name: "Cancel fit" });

    await expect(page.locator(".fit-job-origin")).toContainText(
      "cannot stop it",
      { timeout: 15_000 },
    );
    await expect(cancel).toBeDisabled();

    // And starting a fit is not blocked by one: a followed run holds no fit
    // slot on this server, so the form is not frozen by somebody else's
    // search.
    await expect(
      page.locator(".fit-form").getByRole("button", { name: "Start fit" }),
    ).toBeEnabled();

    // The server's own answer, for the same reason the button is disabled.
    const refused = await request.post(`/api/fit/cancel?job=${jobId}`);
    expect(refused.status()).toBe(409);

    // It ends when result.json lands, without the browser asking anything.
    writeRunResult(dir, 0.4);
    await expect(row).toContainText("Succeeded", { timeout: 20_000 });
    await expect(row).toContainText("followed");
  });
});
