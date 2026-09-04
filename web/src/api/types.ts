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
 *
 * The field limits, the default request and the name lists (metrics,
 * optimizers, CMA-ES covariances, mayfly dialects, presets and selections,
 * and the model bounds vocabulary) are not declared here by hand: they are
 * generated from internal/fitschema, the one Go table internal/server and
 * internal/browserfit both validate a request against, and re-exported below
 * under the same names they always had. See ./fitSchema.generated.ts.
 */
import {
  BOUNDS_KEYS as FIT_SCHEMA_BOUNDS_KEYS,
  CMAES_COVARIANCES,
  DEFAULT_FIT_REQUEST as FIT_SCHEMA_DEFAULT_FIT_REQUEST,
  DEFAULT_PARAM_BOUNDS as FIT_SCHEMA_DEFAULT_PARAM_BOUNDS,
  FIT_LIMITS,
  LOG_ENCODED_BOUNDS_KEYS as FIT_SCHEMA_LOG_ENCODED_BOUNDS_KEYS,
  MAYFLY_DEFAULT_REPORT_EVERY,
  MAYFLY_PRESETS,
  MAYFLY_SELECTIONS,
  MAYFLY_VARIANTS,
  METRIC_NAMES,
  MODEL_BOUNDS_LIMITS as FIT_SCHEMA_MODEL_BOUNDS_LIMITS,
  OPTIMIZER_NAMES,
  type CmaesCovariance,
  type MayflyPreset,
  type MayflySelection,
  type MayflyVariant,
  type MetricName,
  type OptimizerName,
} from "./fitSchema.generated";

// Re-exported under the same names imported above, so every import site in
// web/src keeps reading them from "./types" unchanged.
export {
  CMAES_COVARIANCES,
  FIT_LIMITS,
  MAYFLY_DEFAULT_REPORT_EVERY,
  MAYFLY_PRESETS,
  MAYFLY_SELECTIONS,
  MAYFLY_VARIANTS,
  METRIC_NAMES,
  OPTIMIZER_NAMES,
};
export type {
  CmaesCovariance,
  MayflyPreset,
  MayflySelection,
  MayflyVariant,
  MetricName,
  OptimizerName,
};

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
 * "queued" and "running" is terminal.
 *
 * "queued" is a job the server has accepted and not begun: it runs one fit at
 * a time, and a start request that arrives while one is going is lined up
 * rather than refused. A client that starts a single fit never sees it, since
 * a job with nothing ahead of it is running by the time its start request is
 * answered.
 */
export type FitState =
  | "queued"
  | "running"
  | "succeeded"
  | "failed"
  | "canceled";

/** Whether a state is one a job never leaves. */
export function isTerminal(state: FitState): boolean {
  return state !== "running" && state !== "queued";
}

/**
 * The wire form of a job's state. Both `GET api/fit` and every SSE `data:`
 * frame are one whole snapshot -- never a delta -- so a client that reconnects
 * mid-run reads the same shape it was streaming.
 */
/** `optimizer.PinnedDimension`: one result dimension sitting on a bound. */
export interface PinnedDimension {
  name: string;
  value: number;
  bound: "min" | "max";
  limit: number;
}

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

  /**
   * `optimizer.Metrics`: the breakdown of the best point so far, one raw
   * term per thing the composite objective measures, whatever metric the run
   * scores by. A term the reference was too short to measure is null. Absent
   * until the first report.
   */
  metrics?: FitMetrics;

  /**
   * How many of the starting modes came from the reference's partials; zero
   * when the starting preset's own modes were kept (`modes: -1`).
   */
  seededModes: number;

  /**
   * The dimensions of the result that sit on a bound of the search box,
   * once there is a result. A pinned dimension is one the search wanted to
   * push past the box; `value` and `limit` are in the preset's own units.
   */
  pinned?: PinnedDimension[];

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
   * resolved its configuration. This is the whole point of reporting it: a
   * preset chooses a dialect without naming it, and without an echo there is
   * no way to learn which one it chose.
   */
  mayflyVariant?: string;

  /**
   * The resolved seed, as a decimal **string**. It is an int64 and a JS
   * `Number` stops representing every integer past 2^53, so it is rendered
   * verbatim and never passed through `Number()`.
   */
  mayflySeed?: string;

  /**
   * The zero-based index of the search in progress: CMA-ES stops a converged
   * run and starts another from a wider sigma, and this counts them. Absent
   * for every other backend.
   */
  restart?: number;

  /**
   * The mayfly backend's round index. It is the same counter of the Go
   * `optimizer.Progress` under the other backend's meaning -- a mayfly round
   * is a fresh population, not a restart of a converged search -- so it is a
   * field of its own and exactly one of the two is ever present.
   */
  epoch?: number;

  /** Evaluations over elapsed wall time. Zero before the clock has moved. */
  evaluationsPerSecond: number;

  /**
   * How much of the tightest binding budget the run has spent: the larger of
   * iterations over the iteration cap and elapsed over the time budget, and 0
   * when neither is set.
   *
   * It is **not** an ETA. A run stops at the first budget that binds, so this
   * is a lower bound on how far along it is: a search that converges, or one
   * whose backend stops itself, ends well below 1.
   */
  budgetFraction: number;

  /**
   * The backend's own verdict: the run stopped on a convergence criterion
   * rather than on its budget. False while it is still going, and false for
   * one that was cancelled.
   */
  converged: boolean;

  /** Every setting the fit ran under, defaults and resolved values included. */
  request: FitRequestEcho;

  /**
   * How the run's metric weighs the terms `metrics` reports. Absent for the
   * single-term legacy metrics, which have no profile.
   *
   * The weights and the norms are sent rather than carried here, so a
   * per-term display and the score it sits beside cannot disagree: a norm
   * that changes in `optimizer.DefaultNorms` changes both at once.
   */
  profile?: FitProfile;
}

/**
 * `fitRequestEcho` in internal/server/job.go: the whole request as it was
 * resolved, which is the provenance a results view reads.
 *
 * Both seeds and the resolved `seed` are decimal **strings** for the reason
 * `mayflySeed` is on the request: they are int64 and a JS `Number` stops
 * representing every integer past 2^53.
 *
 * A job rebuilt from a run directory after a server restart fills this in
 * from the run's own config.json, which records everything here except the
 * mayfly form fields -- the stagnation rule, the selection strategy and the
 * crossover knobs were folded into the tuning document before the run wrote
 * anything, so a restored job echoes them at 0 and "".
 */
export interface FitRequestEcho {
  note: number;
  velocity: number;
  modes: number;
  optimizer: OptimizerName;
  metric: MetricName;
  maxIterations: number;
  timeBudgetMs: number;
  reportEvery: number;
  align: boolean;
  normalizeGain: boolean;
  downmix: string;
  windowMs: number;

  mayflyVariant?: MayflyVariant;
  mayflyPreset?: MayflyPreset;
  mayflyPopulation?: number;

  /**
   * Always present, whichever backend ran: it is a formatted int64 and never
   * the empty string, so it is not omitted the way the rest of the backend
   * block is. For a CMA-ES run it is simply the mayfly seed the request
   * carried and nothing read.
   */
  mayflySeed: string;
  mayflyEpochs?: number;
  mayflyRestarts?: number;
  mayflyStagnation?: number;
  mayflySelection?: MayflySelection;
  mayflyTargetCost?: number;
  mayflyNc?: number;
  mayflyNcRatio?: number;

  cmaesCovariance?: CmaesCovariance;
  cmaesLambda?: number;
  cmaesSigma?: number;
  /** Always present, for the reason `mayflySeed` is. */
  cmaesSeed: string;
  cmaesRestarts?: number;

  /** Whether a mayfly tuning document was uploaded. The document itself is
   * not echoed: the run directory's config.json holds it. */
  mayflyTuning: boolean;

  /**
   * What the backend resolved for itself: the seed it actually drew, which is
   * what makes a run repeatable, and the worker count it sized to the
   * machine. `workers` is 0 until the backend has resolved itself, which
   * happens one report before the first progress line.
   */
  seed: string;
  workers: number;

  /**
   * The search box the client uploaded, absent when the run used the default
   * one. The default is not echoed in its place because it is not a constant:
   * it is drawn from the reference's own measured fundamental.
   */
  bounds?: FitBoundsEcho;
}

/** An echoed search box: each dimension a `[min, max]` pair. */
export interface FitBoundsEcho {
  inputMix: BoundsRange;
  filterFreq: BoundsRange;
  amplitude: BoundsRange;
  frequency: BoundsRange;
  decayMs: BoundsRange;
  harmonicGain: BoundsRange;
}

/** The active metric's profile: every term it scores by, in reporting order. */
export interface FitProfile {
  name: string;
  terms: FitProfileTerm[];
}

/**
 * One weighted term. `norm` is the value at which the term scores one half,
 * so a raw metric can be drawn against the scale the score judged it on.
 */
export interface FitProfileTerm {
  term: keyof FitMetrics;
  weight: number;
  norm: number;
  unit?: string;
}

/* ------------------------------------------------------------------ */
/* The comparison                                                      */
/* ------------------------------------------------------------------ */

/**
 * `GET api/fit/jobs/{id}/compare?columns=&frames=`: both sides of the A/B in
 * one document, the reference the objective actually scored and a render of
 * the preset the fit produced.
 *
 * It is served pre-reduced because the picture must be the objective's own:
 * the spectrogram comes from `optimizer.ComputeSpectrogram`, the same
 * transform and the same noise-aware floor the score was computed with, so a
 * partial the eye finds is one the score counted.
 *
 * `columns` and `frames` are the resolutions that were asked for, after the
 * server's caps (4096 columns, 256 frames). What each side was actually built
 * at is on the side itself: a signal with fewer samples, or fewer analysis
 * frames, keeps what it has rather than being stretched.
 *
 * `seconds` and `floorDb` are on the comparison and not on a side, because
 * both sides share them by construction. A reference longer than the render
 * cap is **cut** to it, so the two are one time axis; and both spectrograms
 * are painted against the *reference's* noise-aware floor, which is what the
 * objective scores both signals against. Draw them that way: a render given a
 * floor of its own would show detail the score counted as nothing.
 */
export interface FitCompare {
  sampleRate: number;
  seconds: number;
  columns: number;
  frames: number;

  /** Absent when the reference is shorter than one analysis frame, in which
   * case neither side carries a spectrogram. */
  floorDb?: number;

  reference: FitCompareSide;
  render: FitCompareSide;
}

/** One signal of a comparison. */
export interface FitCompareSide {
  samples: number;
  waveform: FitWaveform;

  /**
   * Absent when the reference is shorter than one analysis frame, which is
   * exactly when the objective measures no spectral term either. The two
   * sides are the same length, so they are absent together.
   */
  spectrogram?: FitSpectrogram;
}

/**
 * The envelope a waveform is drawn from: the lowest and the highest sample in
 * each column. Both, because a column of a decayed strike is symmetric about
 * zero and one magnitude per column would draw a shape the signal has not
 * got. Each array is `columns` long.
 */
export interface FitWaveform {
  columns: number;
  min: number[];
  max: number[];
}

/**
 * A spectrogram reduced to a drawable size. `db` is `frames` rows of `bins`
 * values, each the *loudest* bin of the frames and bins it stands for -- the
 * maximum and not the mean, because a partial occupies one bin whose
 * neighbours hold nothing and averaging would fade out what the picture is of.
 *
 * Every value is already held to the comparison's shared `floorDb`; `peakDb`
 * is the loudest value in this matrix, so a display scales between the two
 * without a pass over the data. It is per side because the two peaks are a
 * real difference between the signals, which the floor is not. Row `r` covers
 * `r * maxHz / bins` upwards, and `maxHz` is the Nyquist rate.
 */
export interface FitSpectrogram {
  frames: number;
  bins: number;
  frameSize: number;
  hop: number;
  peakDb: number;
  maxHz: number;
  db: number[][];
}

/**
 * The SSE event names on `api/fit/events`. There is no `id:` and no `retry:`,
 * so there is no Last-Event-ID replay: close the source on "done" and on
 * "shutdown" or the browser reconnects and re-receives the terminal snapshot
 * forever.
 */
export type FitEventName = "progress" | "done" | "shutdown";

/** One term of `optimizer.Metrics`, as the snapshot carries it. */
export interface FitMetrics {
  partial_cents: number | null;
  partial_level_db: number | null;
  partial_decay_octaves: number | null;
  partial_missing: number | null;
  partial_extra: number | null;
  spectral_fine_db: number | null;
  spectral_coarse_db: number | null;
  envelope_db: number | null;
  decay_slope_dbps: number | null;
  waveform: number | null;
  gain_db: number | null;
  waveform_gain_db: number | null;
  lag: number;
  overlap: number;
  reference_partials: number;
  model_partials: number;
  matched: number;
}

/* ------------------------------------------------------------------ */
/* The request                                                         */
/* ------------------------------------------------------------------ */

// MetricName, OptimizerName, CmaesCovariance, MayflyVariant, MayflyPreset,
// MayflySelection and the const array each is derived from are re-exported
// from ./fitSchema.generated near the top of this file.

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

  /**
   * How the starting modes are chosen, as `--modes` is on the command line:
   * 0 seeds one mode per partial the reference's analysis lists, N seeds the
   * strongest N, -1 keeps the starting preset's own modes. Absent means 0.
   */
  modes?: number;
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

  /**
   * The CMA-ES settings, sent only when that backend is chosen. Each one has a
   * default the backend fills in, so an absent field is not a missing setting:
   * `cmaesLambda: 0` takes Hansen's population, `cmaesSeed: 0` asks the backend
   * to pick a seed, and `cmaesRestarts: 0` restarts until the budget is spent.
   */
  cmaesCovariance?: CmaesCovariance;
  cmaesLambda?: number;
  cmaesSigma?: number;
  cmaesSeed?: number;
  cmaesRestarts?: number;
}

/**
 * `defaultFitRequest()` in internal/server/fit.go, which in turn carries the
 * `fit` command's defaults, so a preset fitted from the browser and one fitted
 * from the terminal are the same fit.
 *
 * The values themselves are ./fitSchema.generated's; this assignment exists
 * so the export carries the FitRequestFields type its every reader expects,
 * which the generated file cannot itself declare without importing this one
 * and creating a cycle.
 */
export const DEFAULT_FIT_REQUEST: FitRequestFields =
  FIT_SCHEMA_DEFAULT_FIT_REQUEST;

/**
 * The default cadence for a backend, so the form can follow the optimizer the
 * way the server does.
 */
export function defaultReportEvery(optimizer: OptimizerName): number {
  return optimizer === "mayfly"
    ? MAYFLY_DEFAULT_REPORT_EVERY
    : DEFAULT_FIT_REQUEST.reportEvery;
}

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
  amplitude?: BoundsRange;
  /** A mode's frequency in hertz, as the preset writes it. */
  frequency?: BoundsRange;
  decay_ms?: BoundsRange;
  harmonic_gain?: BoundsRange;
}

/** The keys a bounds document may carry, in the order the docs list them. */
export type BoundsKey = keyof BoundsDocument;

/**
 * `optimizer.BoundsKeys`, from ./fitSchema.generated. Cast to `BoundsKey[]`
 * for the reason DEFAULT_FIT_REQUEST is: the generated file cannot import
 * BoundsDocument back from here.
 */
export const BOUNDS_KEYS = FIT_SCHEMA_BOUNDS_KEYS as readonly BoundsKey[];

/**
 * The dimensions the codec log-encodes. Their bounds must stay strictly above
 * zero: log(0) is not a number the unit-cube encoding can take, and the server
 * answers a non-positive one with a 400.
 */
export const LOG_ENCODED_BOUNDS_KEYS =
  FIT_SCHEMA_LOG_ENCODED_BOUNDS_KEYS as readonly BoundsKey[];

/**
 * The range `model.ValidateBarParams` holds each dimension to, transcribed from
 * the constants in model/params.go. `DecodeParamBounds` in
 * internal/optimizer/boundsfile.go rejects a supplied range that leaves this
 * box, and for a good reason: every candidate drawn from it would fail
 * validation and score +Inf, so the fit would burn its whole budget to produce
 * nothing. Mirroring the check here reports it before the reference is
 * uploaded rather than after.
 */
export const MODEL_BOUNDS_LIMITS: Partial<Record<BoundsKey, BoundsRange>> =
  FIT_SCHEMA_MODEL_BOUNDS_LIMITS as Partial<Record<BoundsKey, BoundsRange>>;

/**
 * `optimizer.DefaultParamBounds`, shown as the placeholder for each field.
 *
 * Two of these are narrowed per fit before the search starts, without a
 * document: `frequency` to half the reference's fundamental up to 0.45 of
 * its sample rate, and `decay_ms` to what a preset at the starting preset's
 * note may carry. A document replaces the whole box, so a `frequency` key
 * in one is the box that runs.
 */
export const DEFAULT_PARAM_BOUNDS: Required<BoundsDocument> =
  FIT_SCHEMA_DEFAULT_PARAM_BOUNDS as Required<BoundsDocument>;

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
