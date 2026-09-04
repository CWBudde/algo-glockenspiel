/**
 * Pure helpers behind `RunList`.
 *
 * They are split out of the component so the ordering, the state labels and
 * the elapsed/cost formatting can be unit-tested with vitest: web/package.json
 * carries vitest but not @testing-library/react, and adding a rendering
 * harness for one component is a bigger call than this task should make on
 * its own. Rendering itself stays Playwright's job.
 */

import type { FitJobListEntry, FitState } from "../../api/types";

/**
 * Newest first, which is how a run history reads: the fit someone just
 * started belongs at the top, not wherever its id happens to sort.
 *
 * Ties are broken by job id, descending. Two jobs sharing a `startedAt` only
 * happens at whole-second precision loss on some platforms, but without a
 * tiebreaker such a pair would swap order on every poll that re-sorts the
 * list, which reads as the list shuffling itself for no reason.
 */
export function sortRunsNewestFirst(
  jobs: readonly FitJobListEntry[],
): FitJobListEntry[] {
  return [...jobs].sort((a, b) => {
    const byStart = Date.parse(b.startedAt) - Date.parse(a.startedAt);

    if (byStart !== 0) {
      return byStart;
    }

    return b.jobId.localeCompare(a.jobId);
  });
}

/** The word a run's state reads as in the list. */
export function runStateLabel(state: FitState): string {
  switch (state) {
    case "queued":
      return "Queued";
    case "running":
      return "Running";
    case "succeeded":
      return "Succeeded";
    case "failed":
      return "Failed";
    case "canceled":
      return "Canceled";
    default:
      return state;
  }
}

/**
 * Whether the list is worth polling: true while any row is still queued or
 * running. A finished history never changes on its own, so a list of only
 * terminal rows has nothing to refresh for.
 */
export function hasActiveRun(jobs: readonly FitJobListEntry[]): boolean {
  return jobs.some((job) => job.state === "queued" || job.state === "running");
}

/**
 * How long a run has been going, in milliseconds.
 *
 * A finished run's span is fixed, `finishedAt` minus `startedAt`. A run still
 * queued or running has no `finishedAt` yet, so its span is measured against
 * `now`, which the caller supplies rather than reading the clock here, so the
 * function stays pure and testable with a fixed instant.
 */
export function runElapsedMs(job: FitJobListEntry, now: number): number {
  const started = Date.parse(job.startedAt);
  const ended = job.finishedAt === undefined ? now : Date.parse(job.finishedAt);

  return Math.max(0, ended - started);
}

/**
 * Renders an elapsed span the way `FitStatus` renders one job's elapsed time,
 * so a run read here and a run watched live are described the same way.
 */
export function formatRunElapsed(elapsedMs: number): string {
  const seconds = elapsedMs / 1000;

  if (seconds < 60) {
    return `${seconds.toFixed(1)} s`;
  }

  const minutes = Math.floor(seconds / 60);

  return `${String(minutes)} m ${(seconds - minutes * 60).toFixed(1)} s`;
}

/**
 * The best cost column, formatted the way `FitStatus` formats a fit's cost:
 * six significant digits, or a dash for a run that never reported one (a
 * queued job, or one cancelled before its first report).
 */
export function formatRunCost(job: FitJobListEntry): string {
  if (!Number.isFinite(job.bestCost)) {
    return "-";
  }

  return job.bestCost.toPrecision(6);
}
