import { describe, expect, it } from "vitest";

import type { FitJobListEntry } from "../../api/types";
import {
  formatRunCost,
  formatRunElapsed,
  hasActiveRun,
  runElapsedMs,
  runStateLabel,
  sortRunsNewestFirst,
} from "./runList";

function job(overrides: Partial<FitJobListEntry> = {}): FitJobListEntry {
  return {
    jobId: "fit-1",
    state: "succeeded",
    startedAt: "2026-09-04T10:00:00Z",
    bestCost: 0.123456789,
    note: 60,
    velocity: 100,
    optimizer: "mayfly",
    metric: "balanced",
    ...overrides,
  };
}

describe("sortRunsNewestFirst", () => {
  it("orders the most recently started run first", () => {
    const older = job({ jobId: "fit-1", startedAt: "2026-09-04T10:00:00Z" });
    const newer = job({ jobId: "fit-2", startedAt: "2026-09-04T11:00:00Z" });

    expect(sortRunsNewestFirst([older, newer])).toEqual([newer, older]);
  });

  it("breaks a tied startedAt by job id, descending, rather than leaving the input order", () => {
    const a = job({ jobId: "fit-aaa", startedAt: "2026-09-04T10:00:00Z" });
    const b = job({ jobId: "fit-bbb", startedAt: "2026-09-04T10:00:00Z" });

    expect(sortRunsNewestFirst([a, b])).toEqual([b, a]);
    // Order-independent: starting from the other input order gives the same
    // answer, which is the point of a tiebreaker.
    expect(sortRunsNewestFirst([b, a])).toEqual([b, a]);
  });

  it("does not mutate the array it was given", () => {
    const older = job({ jobId: "fit-1", startedAt: "2026-09-04T10:00:00Z" });
    const newer = job({ jobId: "fit-2", startedAt: "2026-09-04T11:00:00Z" });
    const input = [older, newer];

    sortRunsNewestFirst(input);

    expect(input).toEqual([older, newer]);
  });
});

describe("runStateLabel", () => {
  it("labels a queued job distinctly from a running one", () => {
    expect(runStateLabel("queued")).toBe("Queued");
    expect(runStateLabel("running")).toBe("Running");
  });

  it("labels every terminal state", () => {
    expect(runStateLabel("succeeded")).toBe("Succeeded");
    expect(runStateLabel("failed")).toBe("Failed");
    expect(runStateLabel("canceled")).toBe("Canceled");
  });
});

describe("hasActiveRun", () => {
  it("is true while a job is queued", () => {
    expect(hasActiveRun([job({ state: "queued" })])).toBe(true);
  });

  it("is true while a job is running", () => {
    expect(hasActiveRun([job({ state: "running" })])).toBe(true);
  });

  it("is false once every job is terminal", () => {
    expect(
      hasActiveRun([
        job({ jobId: "fit-1", state: "succeeded" }),
        job({ jobId: "fit-2", state: "failed" }),
        job({ jobId: "fit-3", state: "canceled" }),
      ]),
    ).toBe(false);
  });

  it("is false for an empty list", () => {
    expect(hasActiveRun([])).toBe(false);
  });
});

describe("runElapsedMs", () => {
  it("measures a finished run against its own finishedAt, not now", () => {
    const finished = job({
      startedAt: "2026-09-04T10:00:00Z",
      finishedAt: "2026-09-04T10:00:05Z",
    });

    // `now` is an hour later; a finished run's span must not grow with it.
    const now = Date.parse("2026-09-04T11:00:00Z");

    expect(runElapsedMs(finished, now)).toBe(5000);
  });

  it("measures a run still going against the supplied now", () => {
    const running = job({
      state: "running",
      startedAt: "2026-09-04T10:00:00Z",
    });
    const now = Date.parse("2026-09-04T10:00:07Z");

    expect(runElapsedMs(running, now)).toBe(7000);
  });
});

describe("formatRunElapsed", () => {
  it("renders under a minute in seconds", () => {
    expect(formatRunElapsed(1500)).toBe("1.5 s");
  });

  it("renders a minute or more as minutes and seconds", () => {
    expect(formatRunElapsed(125_400)).toBe("2 m 5.4 s");
  });
});

describe("formatRunCost", () => {
  it("renders a finite cost to six significant digits", () => {
    expect(formatRunCost(job({ bestCost: 0.123456789 }))).toBe("0.123457");
  });

  it("renders a dash for a job with no cost yet", () => {
    expect(formatRunCost(job({ bestCost: Number.NaN }))).toBe("-");
  });
});
