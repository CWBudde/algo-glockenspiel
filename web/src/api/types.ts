/**
 * The wire types of `glockenspiel serve`'s fit API.
 *
 * These are transcribed field for field from the Go structs that produce them:
 * `fitSnapshot` and `fitState` in internal/server/job.go, `fitRequest` and
 * `defaultFitRequest` in internal/server/fit.go, `preset.Preset` in
 * internal/preset/preset.go and `model.BarParams` in model/params.go.
 *
 * internal/server/fit_test.go re-declares `fitSnapshot` locally on purpose, so
 * that "a field rename in the server is a failing test rather than a silently
 * renamed wire field". This file is the same guard on the browser side: it is
 * the single description of the contract for every module under src/, and it
 * lists every field the server sends, including the ones no component reads
 * yet.
 */

/** The error body every non-2xx JSON answer carries. */
export interface ApiError {
  error: string;
}

/** GET api/version. */
export interface VersionResponse {
  version: string;
}

/* ------------------------------------------------------------------ */
/* The job                                                             */
/* ------------------------------------------------------------------ */

/**
 * Where a job is. `fitState` in internal/server/job.go; every value but
 * "running" is terminal.
 */
export type FitState = "running" | "succeeded" | "failed" | "canceled";

/** Whether a state is one a job never leaves. */
export function isTerminal(state: FitState): boolean {
  return state !== "running";
}

/**
 * The wire form of a job's state. Both `GET api/fit` and every SSE `data:`
 * frame are one whole snapshot -- never a delta -- so a client that reconnects
 * mid-run reads the same shape it was streaming.
 */
export interface FitSnapshot {
  jobId: string;
  state: FitState;

  /**
   * `iteration` counts progress *reports*; `optimizerIterations` is the
   * backend's own count and is the one comparable with the request's
   * `maxIterations`. They are two fields for a reason.
   */
  iteration: number;
  optimizerIterations: number;
  evaluations: number;
  currentCost: number;
  bestCost: number;
  elapsedMs: number;

  /**
   * An opaque backend string, present once the run stopped: gonum statuses
   * such as "FunctionConvergence" and mayfly's "time_budget" both appear.
   * Omitted while it is empty.
   */
  stopReason?: string;

  /** The failure message, present only for state "failed". */
  error?: string;

  sampleRate: number;
  referenceSeconds: number;
  note: number;
  velocity: number;
  optimizer: OptimizerName;
  metric: MetricName;

  /** RFC 3339 timestamps, as Go's time.Time marshals them. */
  startedAt: string;
  finishedAt?: string;

  /**
   * Whether `api/fit/preset` and `api/fit/audio` will answer. This is *not*
   * `state === "succeeded"`: a run cancelled after its first report still
   * leaves the best parameters found so far.
   */
  hasPreset: boolean;

  /**
   * What the mayfly backend settled on, present only once a mayfly run has
   * resolved its configuration. This is the whole point of reporting it: with
   * `variant: "auto"` the user asked the optimizer to choose the dialect, and
   * without an echo there is no way to learn which one it chose.
   */
  mayflyVariant?: string;

  /**
   * The resolved seed, as a decimal **string**. It is an int64 and a JS
   * `Number` stops representing every integer past 2^53, so it is rendered
   * verbatim and never passed through `Number()`.
   */
  mayflySeed?: string;

  /** Why an automatically chosen dialect was chosen. Empty when none was. */
  mayflyRecommendation?: string;
}

/**
 * The SSE event names on `api/fit/events`. There is no `id:` and no `retry:`,
 * so there is no Last-Event-ID replay: close the source on "done" and on
 * "shutdown" or the browser reconnects and re-receives the terminal snapshot
 * forever.
 */
export type FitEventName = "progress" | "done" | "shutdown";

/* ------------------------------------------------------------------ */
/* The request                                                         */
/* ------------------------------------------------------------------ */

/** `optimizer.ParseMetric`'s vocabulary. */
export type MetricName = "rms" | "log" | "spectral";

/** `selectOptimizer`'s vocabulary. */
export type OptimizerName = "simple" | "mayfly";

/**
 * `MayflyOptimizer.Validate`'s vocabulary.
 *
 * "auto" is a request rather than a dialect: it asks the optimizer to measure
 * the landscape and pick one. Which one it picked is only knowable afterwards,
 * from `FitSnapshot.mayflyVariant`.
 */
export const MAYFLY_VARIANTS = [
  "auto",
  "ma",
  "desma",
  "olce",
  "eobbma",
  "gsasma",
  "mpma",
  "aoblmoa",
] as const;

/**
 * Derived from the list rather than spelled out a second time. A union this
 * long also formats differently under prettier 3.8 and 3.9 -- the CI check
 * installs whatever 3.x is current -- and deriving it sidesteps that for good.
 */
export type MayflyVariant = (typeof MAYFLY_VARIANTS)[number];

/**
 * `mayfly.ConfigPreset`'s vocabulary, from config_loader.go upstream.
 *
 * A preset already chooses a dialect, so the engine refuses a preset and an
 * explicit variant together; the form sends one or the other, never both.
 */
export const MAYFLY_PRESETS = [
  "unimodal",
  "multimodal",
  "highly_multimodal",
  "deceptive",
  "narrow_valley",
  "high_dimensional",
  "fast_convergence",
  "stable_convergence",
  "multi_objective",
] as const;

/** Derived from the list, for the reason MayflyVariant is. */
export type MayflyPreset = (typeof MAYFLY_PRESETS)[number];

/** The `selection` knob's vocabulary: how crossover pairs its parents. */
export const MAYFLY_SELECTIONS = ["rank", "tournament"] as const;

/** Derived from the list, for the reason MayflyVariant is. */
export type MayflySelection = (typeof MAYFLY_SELECTIONS)[number];

export const METRIC_NAMES: readonly MetricName[] = [
  "rms",
  "log",
  "spectral",
] as const;

export const OPTIMIZER_NAMES: readonly OptimizerName[] = [
  "simple",
  "mayfly",
] as const;

/**
 * The scalar fields of `POST api/fit/start`, as they are spelled in the
 * multipart form.
 *
 * `timeBudget` is a duration **string** on the wire -- "30s", "2m", or a bare
 * number read as seconds, exactly as `fit --time-budget` reads it. The
 * `timeBudgetMs` of Go's `fitRequest` is an internal representation and is
 * never sent.
 *
 * `mayflySeed` is a decimal **string** for a related reason: the server reads
 * it with `strconv.ParseInt` into an int64, and a JS `Number` stops
 * representing every integer past 2^53, so a seed carried as a `number` could
 * reach the server as a different one than the user typed.
 */
export interface FitRequestFields {
  note: number;
  velocity: number;
  optimizer: OptimizerName;
  metric: MetricName;
  maxIterations: number;
  timeBudget: string;
  reportEvery: number;
  align: boolean;
  normalizeGain: boolean;
  mayflyVariant: MayflyVariant;
  mayflyPopulation: number;
  mayflySeed: string;

  /**
   * A `mayfly.ConfigPreset` name, or "" for none. It is exclusive with
   * `mayflyVariant`: a preset already picked a dialect, and the engine refuses
   * both at once, so the form sends `mayflyVariant: ""` alongside a preset.
   */
  mayflyPreset: string;

  /** The wrapper's own run schedule; all three fold into `schedule`. */
  mayflyEpochs: number;
  mayflyRestarts: number;

  /** Iterations without improvement before the run stops. Zero disables it. */
  mayflyStagnation: number;

  /** A `selection` name, or "" to keep what the dialect chose. */
  mayflySelection: string;

  /**
   * The three knobs whose zero is a real value, so "unset" cannot be spelled
   * as one. They are absent rather than zero when the user typed nothing:
   * `fitRequest` holds them as Go pointers for exactly this reason -- zero is
   * a usable cost target, `mayflyNc` reserves -1 for "derive it" and 0 for "no
   * crossover at all", and a zero ratio is a ratio.
   */
  mayflyTargetCost?: number;
  mayflyNc?: number;
  mayflyNcRatio?: number;

  /**
   * The tuning document, carried inline for the browser optimizer only.
   *
   * Over HTTP the same document is a multipart **file part** named
   * `mayflyTuning`, exactly as `bounds` is, and never a scalar field; see
   * `readMayflyTuningPart` in internal/server/fit.go. The WASM entry point has
   * a fixed five-argument contract with nowhere to put a second file, so
   * `browserfit.Request` takes the document in the request JSON instead.
   */
  mayflyTuning?: MayflyTuningDocument;
}

/**
 * `defaultFitRequest()` in internal/server/fit.go, which in turn carries the
 * `fit` command's defaults, so a preset fitted from the browser and one fitted
 * from the terminal are the same fit.
 */
export const DEFAULT_FIT_REQUEST: FitRequestFields = {
  note: 69,
  velocity: 100,
  optimizer: "simple",
  metric: "rms",
  maxIterations: 100,
  timeBudget: "30s",
  reportEvery: 10,
  align: true,
  normalizeGain: false,
  mayflyVariant: "desma",
  mayflyPopulation: 10,
  mayflySeed: "1",
  mayflyPreset: "",
  mayflyEpochs: 1,
  mayflyRestarts: 0,
  mayflyStagnation: 0,
  mayflySelection: "",
};

/**
 * The server's own limits, from internal/server/fit.go and
 * internal/server/params.go. They are mirrored here so a value that cannot be
 * accepted is refused before it is uploaded, not after.
 */
export const FIT_LIMITS = {
  /** `defaultMaxReferenceBytes`: 16 MiB. */
  maxReferenceBytes: 16 << 20,
  note: { min: 0, max: 127 },
  velocity: { min: 0, max: 127 },
  /** `maxFitIterations`. */
  maxIterations: { min: 1, max: 100_000 },
  reportEvery: { min: 0, max: 100_000 },
  mayflyPopulation: { min: 2, max: 4096 },
  /** `parseFitRequest`'s own bounds on the wrapper schedule. */
  mayflyEpochs: { min: 1, max: 1000 },
  mayflyRestarts: { min: 0, max: 1000 },
  /** Bounded by `maxFitIterations`, as the iteration limit is. */
  mayflyStagnation: { min: 0, max: 100_000 },
  /** `maxFitTargetCost`; a cost is whatever the metric produces. */
  mayflyTargetCost: { min: -1e12, max: 1e12 },
  /**
   * mayfly.NCAuto is -1, so -1 is the floor rather than zero, and the ceiling
   * is the population cap: offspring are produced by pairing the population.
   */
  mayflyNc: { min: -1, max: 4096 },
  mayflyNcRatio: { min: 0, max: 4096 },
  /** `maxFitTimeBudget`, in seconds; the lower bound is exclusive. */
  timeBudgetSeconds: { min: 0, max: 3600 },
  /** `maxRenderSeconds`; the lower bound is exclusive. */
  renderSeconds: { min: 0, max: 60 },
} as const;

/* ------------------------------------------------------------------ */
/* Bounds                                                              */
/* ------------------------------------------------------------------ */

/** One `[min, max]` pair in a bounds document. Both ends must be finite. */
export type BoundsRange = [min: number, max: number];

/**
 * The optional `bounds` multipart field: the same JSON document
 * `fit --bounds` reads, parsed by the same code.
 *
 * Every key is optional and an omitted key keeps the corresponding default
 * bound, so a document can narrow one dimension without restating the rest.
 * Unknown keys are rejected with a 400 rather than ignored -- a misspelled
 * dimension that was silently dropped would run the fit against the default
 * box while the client believed it had narrowed one.
 */
export interface BoundsDocument {
  input_mix?: BoundsRange;
  filter_freq?: BoundsRange;
  base_frequency?: BoundsRange;
  amplitude?: BoundsRange;
  frequency_mult?: BoundsRange;
  decay_ms?: BoundsRange;
  harmonic_gain?: BoundsRange;
}

/** The keys a bounds document may carry, in the order the docs list them. */
export type BoundsKey = keyof BoundsDocument;

export const BOUNDS_KEYS: readonly BoundsKey[] = [
  "input_mix",
  "filter_freq",
  "base_frequency",
  "amplitude",
  "frequency_mult",
  "decay_ms",
  "harmonic_gain",
] as const;

/**
 * The dimensions the codec log-encodes. Their bounds must stay strictly above
 * zero: log(0) is not a number the unit-cube encoding can take, and the server
 * answers a non-positive one with a 400.
 */
export const LOG_ENCODED_BOUNDS_KEYS: readonly BoundsKey[] = [
  "filter_freq",
  "base_frequency",
  "frequency_mult",
  "decay_ms",
] as const;

/**
 * The range `model.ValidateBarParams` holds each dimension to, transcribed from
 * the constants in model/params.go. `DecodeParamBounds` in
 * internal/optimizer/boundsfile.go rejects a supplied range that leaves this
 * box, and for a good reason: every candidate drawn from it would fail
 * validation and score +Inf, so the fit would burn its whole budget to produce
 * nothing. Mirroring the check here reports it before the reference is
 * uploaded rather than after.
 *
 * `frequency_mult` is deliberately absent, exactly as it is there: it is a
 * multiplier, and what the model bounds is the mode frequency it produces
 * together with `base_frequency`, not the multiplier itself.
 */
export const MODEL_BOUNDS_LIMITS: Partial<Record<BoundsKey, BoundsRange>> = {
  input_mix: [0, 2],
  filter_freq: [20, 20000],
  base_frequency: [0.01, 50000],
  amplitude: [-2, 2],
  decay_ms: [0.1, 5000],
  harmonic_gain: [0, 2],
};

/** `optimizer.DefaultParamBounds`, shown as the placeholder for each field. */
export const DEFAULT_PARAM_BOUNDS: Required<BoundsDocument> = {
  input_mix: [0, 2],
  filter_freq: [20, 20000],
  base_frequency: [0.01, 50000],
  amplitude: [-2, 2],
  frequency_mult: [0.5, 10],
  decay_ms: [0.1, 500],
  harmonic_gain: [0, 2],
};

/* ------------------------------------------------------------------ */
/* Mayfly tuning                                                       */
/* ------------------------------------------------------------------ */

/**
 * `optimizer.MayflyConvergence`: when a run may stop early.
 *
 * `target_cost` is nested and optional in Go for the reason it is optional
 * here -- zero is a usable target, so "no target" has to be the absent key.
 */
export interface MayflyConvergenceDocument {
  target_cost?: number;
  min_improvement?: number;
  stagnation_iterations?: number;
  min_iterations?: number;
}

/** `optimizer.MayflySchedule`: the wrapper's own run schedule. */
export interface MayflyScheduleDocument {
  epochs?: number;
  restarts?: number;
  classify_evals?: number;
}

/** What a flat tuning knob may hold, by `MayflyTuningField.kind`. */
export type MayflyTuningValue = number | boolean | string;

/**
 * The optional `mayflyTuning` document: `optimizer.MayflyTuning`, which is a
 * curated subset of mayfly's own config keys.
 *
 * Every key is optional and an omitted key keeps whatever the variant factory
 * or the preset already chose, so an empty document is a no-op and a document
 * naming one knob changes one knob. The flat keys are indexed rather than
 * spelled out: `MAYFLY_TUNING_FIELDS` in ./mayflyTuning.generated.ts is
 * generated from the Go table and is the single list of them.
 */
export interface MayflyTuningDocument {
  convergence?: MayflyConvergenceDocument;
  schedule?: MayflyScheduleDocument;
  [key: string]:
    | MayflyTuningValue
    | MayflyConvergenceDocument
    | MayflyScheduleDocument
    | undefined;
}

/* ------------------------------------------------------------------ */
/* The preset                                                          */
/* ------------------------------------------------------------------ */

/** Where the Chebyshev waveshaper sits in the chain. */
export type ChebyshevStage = "excitation" | "output";

/** `model.ChebyshevParams`. */
export interface ChebyshevParams {
  enabled: boolean;
  /** Omitted in a v1 document, where it means "excitation". */
  stage?: ChebyshevStage;
  harmonic_gains: number[];
}

/**
 * `model.ModeParams`: one resonant mode. `harmonics` are optional
 * integer-multiple partials on top of this mode's oscillator and are omitted
 * by every v1 document.
 */
export interface ModeParams {
  amplitude: number;
  frequency: number;
  decay_ms: number;
  harmonics?: number[];
}

/** `model.BarParams`: the top-level model parameters for one bar. */
export interface BarParams {
  input_mix: number;
  filter_frequency: number;
  base_frequency: number;
  modes: ModeParams[];
  chebyshev: ChebyshevParams;
}

/**
 * `preset.Preset`, the body of `GET api/fit/preset` and the document the
 * optional `preset` multipart field carries.
 *
 * `version` is "1.0" or "2.0"; v2 adds the variable-length mode array,
 * per-mode harmonics and the explicit Chebyshev stage.
 */
export interface Preset {
  version: string;
  name: string;
  note: number;
  parameters: BarParams;
}
