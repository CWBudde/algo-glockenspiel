/**
 * Pure helpers behind `CostChart`'s compare picker.
 *
 * The picker overlays other runs' cost curves onto the live one, each read
 * from `getFitJobTrace`, which answers `internal/fitrun/trace.go`'s
 * `trace.jsonl` verbatim: one JSON object per line, not itself valid JSON.
 * `parseTraceToPoints` is the only place that file's shape is read on the web
 * side.
 */

import type { FitJobListEntry } from "../../api/types";
import { isTerminal } from "../../api/types";
import { sortRunsNewestFirst } from "./runList";
import type { CostPoint } from "./useFitEvents";

/**
 * How many other runs the picker may overlay at once.
 *
 * Three, chosen for legibility rather than for any limit on the server side:
 * the live run already draws two lines (best and current), and each overlay
 * adds one more to a chart that has no way to separate crossing lines beyond
 * colour and a dashed stroke. A fourth curve is the point at which telling
 * two of them apart stops being a glance and starts being a squint.
 */
export const MAX_COMPARE_RUNS = 3;

/**
 * The runs the picker may offer: every finished job but the one whose curve
 * is already live.
 *
 * A queued or running job is left out because its trace is still being
 * written and its curve is better read live, through the job list's own row,
 * than as a picked-once snapshot that stops updating the moment it is
 * fetched.
 */
export function compareEligibleJobs(
  jobs: readonly FitJobListEntry[],
  excludeJobId: string | null,
): FitJobListEntry[] {
  return sortRunsNewestFirst(
    jobs.filter((job) => isTerminal(job.state) && job.jobId !== excludeJobId),
  );
}

/**
 * Adds or removes one job from the selection, refusing to grow past `cap`.
 *
 * Removing is never refused -- a selection over the cap can only be reached
 * by lowering the cap itself, which nothing here does, but a picker that
 * cannot be un-picked past some limit would be a trap rather than a cap.
 */
export function toggleCompareSelection(
  selected: readonly string[],
  jobId: string,
  cap: number = MAX_COMPARE_RUNS,
): string[] {
  if (selected.includes(jobId)) {
    return selected.filter((id) => id !== jobId);
  }

  if (selected.length >= cap) {
    return [...selected];
  }

  return [...selected, jobId];
}

/**
 * Parses one job's `trace.jsonl` into the same `CostPoint` shape the live SSE
 * stream builds, so the overlay and the live curve are drawn by the same
 * chart code.
 *
 * A line this cannot make sense of -- one write caught mid-flush by a server
 * that was killed, a stray blank line -- is skipped rather than aborting the
 * whole trace: a partial trace from a run that is still going, or that ended
 * abnormally, is exactly what a caller asking for a finished job's trace
 * should still get as much of as survived.
 */
export function parseTraceToPoints(trace: string): CostPoint[] {
  const points: CostPoint[] = [];

  for (const line of trace.split("\n")) {
    const trimmed = line.trim();

    if (trimmed === "") {
      continue;
    }

    let parsed: unknown;

    try {
      parsed = JSON.parse(trimmed);
    } catch {
      continue;
    }

    if (typeof parsed !== "object" || parsed === null) {
      continue;
    }

    const record = parsed as Record<string, unknown>;
    const iteration = record.optimizer_iterations;
    const best = record.best;
    const current = record.current;

    if (
      typeof iteration !== "number" ||
      typeof best !== "number" ||
      typeof current !== "number"
    ) {
      continue;
    }

    points.push({ iteration, best, current });
  }

  return points;
}

/** The picker's label for one eligible run: short, and enough to tell it apart. */
export function compareRunLabel(job: FitJobListEntry): string {
  const shortId = job.jobId.length > 8 ? job.jobId.slice(0, 8) : job.jobId;

  return `${shortId} · ${job.optimizer}/${job.metric} · note ${String(job.note)}`;
}
