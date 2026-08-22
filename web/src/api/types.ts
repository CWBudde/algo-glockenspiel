/**
 * The wire shapes of the fit API.
 *
 * PLACEHOLDER. This file carries only the two types the progress stream needs,
 * transcribed field for field from `fitSnapshot` and the `fitState` constants
 * in `internal/server/job.go`. The Optimize-form PR adds the fuller version --
 * `Preset`, `BarParams`, `ModeParams`, `ChebyshevParams`, `ApiError` -- and this
 * file is deleted in favour of it when the two branches meet.
 *
 * Transcribing rather than generating is deliberate, and it is what
 * `internal/server/fit_test.go` already does on the Go side: a field renamed in
 * the server becomes a type error here rather than a silently undefined value
 * at runtime.
 */

/** The states a job can be in. Everything but "running" is terminal. */
export type FitState = "running" | "succeeded" | "failed" | "canceled";

/**
 * One whole status object. Every SSE event and every status response is one of
 * these; the stream never sends a delta.
 */
export interface FitSnapshot {
  jobId: string;
  state: FitState;

  /**
   * How many progress reports have been made. This is NOT the optimizer's
   * iteration count -- `optimizerIterations` is, and it is the one comparable
   * with the request's `maxIterations`.
   */
  iteration: number;
  optimizerIterations: number;
  evaluations: number;
  currentCost: number;
  bestCost: number;
  elapsedMs: number;

  /**
   * Why the run stopped. An opaque string: gonum statuses such as
   * "FunctionConvergence" and mayfly's own "time_budget" both appear here, so
   * it is displayed rather than matched against.
   */
  stopReason?: string;
  error?: string;

  sampleRate: number;
  referenceSeconds: number;
  note: number;
  velocity: number;
  optimizer: string;
  metric: string;

  startedAt: string;
  finishedAt?: string;

  /**
   * Whether /api/fit/preset and /api/fit/audio will answer. This is not the
   * same as state === "succeeded": a run cancelled after its first report still
   * leaves the best parameters found so far.
   */
  hasPreset: boolean;
}
