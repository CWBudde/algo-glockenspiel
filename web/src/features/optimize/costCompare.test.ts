import { describe, expect, it } from "vitest";

import type { FitJobListEntry } from "../../api/types";
import {
  MAX_COMPARE_RUNS,
  compareEligibleJobs,
  compareRunLabel,
  parseTraceToPoints,
  toggleCompareSelection,
} from "./costCompare";

function job(overrides?: Partial<FitJobListEntry>): FitJobListEntry {
  return {
    jobId: "job-1",
    state: "succeeded",
    startedAt: "2026-01-01T00:00:00Z",
    finishedAt: "2026-01-01T00:01:00Z",
    bestCost: 0.1,
    note: 60,
    velocity: 100,
    optimizer: "mayfly",
    metric: "balanced",
    ...overrides,
  };
}

describe("compareEligibleJobs", () => {
  it("keeps only terminal jobs and drops the live one", () => {
    const jobs = [
      job({ jobId: "a", state: "succeeded" }),
      job({ jobId: "b", state: "running" }),
      job({ jobId: "c", state: "failed" }),
      job({ jobId: "d", state: "queued" }),
    ];

    expect(compareEligibleJobs(jobs, "a").map((j) => j.jobId)).toEqual(["c"]);
  });

  it("sorts newest first, like the run list", () => {
    const jobs = [
      job({ jobId: "old", startedAt: "2026-01-01T00:00:00Z" }),
      job({ jobId: "new", startedAt: "2026-01-02T00:00:00Z" }),
    ];

    expect(compareEligibleJobs(jobs, null).map((j) => j.jobId)).toEqual([
      "new",
      "old",
    ]);
  });
});

describe("toggleCompareSelection", () => {
  it("adds an unselected job", () => {
    expect(toggleCompareSelection([], "a")).toEqual(["a"]);
  });

  it("removes an already-selected job", () => {
    expect(toggleCompareSelection(["a", "b"], "a")).toEqual(["b"]);
  });

  it("refuses to grow past the cap", () => {
    const atCap = ["a", "b", "c"];

    expect(toggleCompareSelection(atCap, "d", 3)).toEqual(atCap);
  });

  it("still allows removing one while at the cap", () => {
    const atCap = ["a", "b", "c"];

    expect(toggleCompareSelection(atCap, "b", 3)).toEqual(["a", "c"]);
  });

  it("defaults its cap to MAX_COMPARE_RUNS", () => {
    const atCap = Array.from({ length: MAX_COMPARE_RUNS }, (_, i) => String(i));

    expect(toggleCompareSelection(atCap, "extra")).toEqual(atCap);
  });
});

describe("parseTraceToPoints", () => {
  it("reads one point per line, keyed by optimizer_iterations", () => {
    const trace =
      '{"iteration":1,"optimizer_iterations":1,"restart":0,"lambda":0,"evaluations":8,"elapsed_ms":10,"current":0.5,"best":0.5}\n' +
      '{"iteration":2,"optimizer_iterations":2,"restart":0,"lambda":0,"evaluations":16,"elapsed_ms":20,"current":0.4,"best":0.3}\n';

    expect(parseTraceToPoints(trace)).toEqual([
      { iteration: 1, best: 0.5, current: 0.5 },
      { iteration: 2, best: 0.3, current: 0.4 },
    ]);
  });

  it("skips a line whose cost is null, rather than inventing a number", () => {
    const trace =
      '{"iteration":1,"optimizer_iterations":1,"restart":0,"lambda":0,"evaluations":1,"elapsed_ms":1,"current":null,"best":null}\n' +
      '{"iteration":2,"optimizer_iterations":2,"restart":0,"lambda":0,"evaluations":2,"elapsed_ms":2,"current":0.2,"best":0.2}\n';

    expect(parseTraceToPoints(trace)).toEqual([
      { iteration: 2, best: 0.2, current: 0.2 },
    ]);
  });

  it("skips a line that is not valid JSON, and a trailing blank line", () => {
    const trace =
      '{"iteration":1,"optimizer_iterations":1,"restart":0,"lambda":0,"evaluations":1,"elapsed_ms":1,"current":0.2,"best":0.2}\n' +
      "not json\n" +
      "\n";

    expect(parseTraceToPoints(trace)).toEqual([
      { iteration: 1, best: 0.2, current: 0.2 },
    ]);
  });

  it("is empty for an empty trace", () => {
    expect(parseTraceToPoints("")).toEqual([]);
  });
});

describe("compareRunLabel", () => {
  it("shortens a long job id and names the optimizer, metric and note", () => {
    const label = compareRunLabel(
      job({
        jobId: "fit-20260904T050000-0001",
        optimizer: "cmaes",
        metric: "polish",
        note: 72,
      }),
    );

    expect(label).toBe("050000-0001 · cmaes/polish · note 72");
  });

  // Every job started on the same day shares the "fit-<date>T" prefix, so the
  // label has to come from the part that actually varies between runs -- the
  // time and the counter -- or two rows in the picker read as the same run.
  it("tells apart two runs started the same day", () => {
    const first = compareRunLabel(
      job({ jobId: "fit-20260904T050000-0001", optimizer: "cmaes", metric: "polish", note: 72 }),
    );
    const second = compareRunLabel(
      job({ jobId: "fit-20260904T050512-0002", optimizer: "cmaes", metric: "polish", note: 72 }),
    );

    expect(first).not.toBe(second);
  });
});
