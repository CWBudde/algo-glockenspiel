import { useId, useMemo, useRef, useState } from "react";

import { parseDuration } from "../../api/duration";
import {
  cancelFit as cancelServerFit,
  FitApiError,
  getFitStatus as getServerFitStatus,
  startFit as startServerFit,
} from "../../api/fit";
import {
  MAYFLY_CONVERGENCE_KEYS,
  MAYFLY_SCHEDULE_KEYS,
  mayflyTuningFieldsFor,
  type MayflyTuningField,
} from "../../api/mayflyTuning.generated";
import {
  DEFAULT_SOUND_PRESET_ID,
  SOUND_PRESETS,
} from "../../api/presets.generated";
import {
  BOUNDS_KEYS,
  CMAES_COVARIANCES,
  DEFAULT_FIT_REQUEST,
  DEFAULT_PARAM_BOUNDS,
  defaultReportEvery,
  FIT_LIMITS,
  LOG_ENCODED_BOUNDS_KEYS,
  MAYFLY_PRESETS,
  MAYFLY_SELECTIONS,
  MAYFLY_VARIANTS,
  METRIC_NAMES,
  MODEL_BOUNDS_LIMITS,
  OPTIMIZER_NAMES,
  type BoundsDocument,
  type BoundsKey,
  type BoundsRange,
  type CmaesCovariance,
  type FitSnapshot,
  type MayflyConvergenceDocument,
  type MayflyScheduleDocument,
  type MayflyTuningDocument,
  type MayflyTuningValue,
  type MayflyVariant,
  type MetricName,
  type OptimizerName,
} from "../../api/types";

/**
 * The fit form.
 *
 * Every scalar is held to the range internal/server/params.go holds it to, and
 * the reference to the 16 MiB `defaultMaxReferenceBytes`, so a value the server
 * would refuse never round-trips: a 400 that arrives after a 16 MiB upload has
 * been sent is a slow way to learn that `note` was 200.
 */

export interface FitFormProps {
  /** The job the page is currently watching, if any. */
  snapshot: FitSnapshot | null;
  /**
   * Called with the snapshot every start and cancel answers with. The start
   * call also passes the `maxIterations` it sent, because the server does not
   * echo the request back and the progress panel reads "n of m" against it.
   */
  onSnapshot: (snapshot: FitSnapshot, maxIterations?: number) => void;
  /** Uses the HTTP service by default; static deployments provide WASM actions. */
  actions?: FitActions | undefined;
}

export interface FitActions {
  start(form: FormData): Promise<FitSnapshot>;
  cancel(jobId?: string): Promise<FitSnapshot>;
  status?: () => Promise<FitSnapshot>;
}

const SERVER_ACTIONS: FitActions = {
  start: startServerFit,
  cancel: cancelServerFit,
  status: getServerFitStatus,
};

/**
 * What buildForm answers with: the finished body plus the `maxIterations` it
 * sent, or the per-field errors that stopped it.
 *
 * Named rather than written inline because prettier 3.8 and 3.9 format a
 * multi-line union differently and each rewrites the other's output; on one
 * line every 3.x agrees. See the note in the CI format job.
 */
type BuiltBody = { form: FormData; maxIterations: number };
type BuiltForm = BuiltBody | { errors: FieldErrors };

/** The scalar fields, held as strings so a half-typed number is not clobbered. */
interface ScalarFields {
  note: string;
  velocity: string;
  maxIterations: string;
  reportEvery: string;
  timeBudget: string;
  mayflyPopulation: string;
  mayflySeed: string;
  mayflyPreset: string;
  mayflySelection: string;
  mayflyEpochs: string;
  mayflyRestarts: string;
  mayflyStagnation: string;
  mayflyTargetCost: string;
  mayflyNc: string;
  mayflyNcRatio: string;
  cmaesLambda: string;
  cmaesSigma: string;
  cmaesSeed: string;
  cmaesRestarts: string;
}

/**
 * The tuning knobs as typed, keyed by `MayflyTuningField.key`.
 *
 * An absent or empty entry is an omitted knob, and an omitted knob is the
 * whole point of the document's design: it keeps whatever the dialect or the
 * preset already chose. Values for knobs the current dialect does not own stay
 * in state while another dialect is selected but are never built, because the
 * builder is handed only the fields on screen.
 */
type TuningValues = Partial<Record<string, string>>;

/** One `[min, max]` row of the bounds editor, as typed. */
type BoundsRow = { min: string; max: string };

type BoundsRows = Record<BoundsKey, BoundsRow>;

type FieldErrors = Partial<Record<string, string>>;

const INITIAL_SCALARS: ScalarFields = {
  note: String(DEFAULT_FIT_REQUEST.note),
  velocity: String(DEFAULT_FIT_REQUEST.velocity),
  maxIterations: String(DEFAULT_FIT_REQUEST.maxIterations),
  reportEvery: String(DEFAULT_FIT_REQUEST.reportEvery),
  timeBudget: DEFAULT_FIT_REQUEST.timeBudget,
  mayflyPopulation: String(DEFAULT_FIT_REQUEST.mayflyPopulation),
  mayflySeed: DEFAULT_FIT_REQUEST.mayflySeed,
  mayflyPreset: DEFAULT_FIT_REQUEST.mayflyPreset,
  mayflySelection: DEFAULT_FIT_REQUEST.mayflySelection,
  mayflyEpochs: String(DEFAULT_FIT_REQUEST.mayflyEpochs),
  mayflyRestarts: String(DEFAULT_FIT_REQUEST.mayflyRestarts),
  mayflyStagnation: String(DEFAULT_FIT_REQUEST.mayflyStagnation),
  // The three that have no default: absent is not zero for any of them.
  mayflyTargetCost: "",
  mayflyNc: "",
  mayflyNcRatio: "",
  cmaesLambda: String(DEFAULT_FIT_REQUEST.cmaesLambda),
  cmaesSigma: String(DEFAULT_FIT_REQUEST.cmaesSigma),
  cmaesSeed: String(DEFAULT_FIT_REQUEST.cmaesSeed),
  cmaesRestarts: String(DEFAULT_FIT_REQUEST.cmaesRestarts),
};

const EMPTY_BOUNDS_ROWS: BoundsRows = Object.fromEntries(
  BOUNDS_KEYS.map((key) => [key, { min: "", max: "" }]),
) as BoundsRows;

/** Human labels for the bounds keys, which are wire names. */
const BOUNDS_LABELS: Record<BoundsKey, string> = {
  input_mix: "Input mix",
  filter_freq: "Filter frequency (Hz)",
  amplitude: "Mode amplitude",
  frequency: "Mode frequency (Hz)",
  decay_ms: "Decay (ms)",
  harmonic_gain: "Harmonic gain",
};

const METRIC_LABELS: Record<MetricName, string> = {
  balanced: "balanced — partials, spectrum, envelope and waveform together",
  placement: "placement — partial-heavy, for a global search",
  polish: "polish — waveform-heavy, for a local refinement",
  rms: "rms — root mean square of the time-domain difference",
  log: "log — logarithmic amplitude difference",
  spectral: "spectral — magnitude spectrum difference",
};

/** Parses a whole number held to an inclusive range, as formInt does. */
function parseInt10(
  label: string,
  raw: string,
  low: number,
  high: number,
): { value: number } | { error: string } {
  const trimmed = raw.trim();

  if (trimmed === "") {
    return { error: `${label} is required.` };
  }

  if (!/^[+-]?\d+$/.test(trimmed)) {
    return { error: `${label} must be a whole number.` };
  }

  const value = Number(trimmed);

  if (value < low || value > high) {
    return { error: `${label} must be in [${low}, ${high}].` };
  }

  return { value };
}

/** Parses an unbounded 64-bit seed, as formInt64 does. */
function parseSeed(raw: string): { value: string } | { error: string } {
  const trimmed = raw.trim();

  if (trimmed === "") {
    return { error: "The mayfly seed is required." };
  }

  if (!/^[+-]?\d+$/.test(trimmed)) {
    return { error: "The mayfly seed must be a whole number." };
  }

  // Range-checked as a BigInt rather than a Number: past 2^53 a Number no
  // longer represents every integer, so a seed the server would accept could
  // silently be sent as a different one.
  const value = BigInt(trimmed);

  if (value < -(2n ** 63n) || value > 2n ** 63n - 1n) {
    return { error: "The mayfly seed does not fit in a 64-bit integer." };
  }

  return { value: value.toString() };
}

/** Validates one bounds row, returning nothing for an untouched one. */
function parseBoundsRow(
  key: BoundsKey,
  row: BoundsRow,
): { range?: BoundsRange } | { error: string } {
  const min = row.min.trim();
  const max = row.max.trim();

  if (min === "" && max === "") {
    return {};
  }

  if (min === "" || max === "") {
    return { error: "Both ends of the range are required." };
  }

  const low = Number(min);
  const high = Number(max);

  if (!Number.isFinite(low) || !Number.isFinite(high)) {
    return { error: "Both ends must be finite numbers." };
  }

  if (low >= high) {
    return { error: `The minimum ${low} must be below the maximum ${high}.` };
  }

  // The codec log-encodes these dimensions into the unit cube, so a bound at or
  // below zero has no encoding at all; the server answers one with a 400.
  if (LOG_ENCODED_BOUNDS_KEYS.includes(key) && low <= 0) {
    return {
      error: "This dimension is log-encoded, so both ends must be above zero.",
    };
  }

  // A box outside the model's own domain is one every candidate fails
  // validation in, so the fit would spend its whole budget scoring +Inf.
  // DecodeParamBounds refuses it; refusing it here saves the upload first.
  const limit = MODEL_BOUNDS_LIMITS[key];

  if (limit !== undefined && (low < limit[0] || high > limit[1])) {
    return {
      error: `The model accepts only [${limit[0]}, ${limit[1]}] for this dimension.`,
    };
  }

  return { range: [low, high] };
}

/**
 * Parses an optional whole number: an empty field is an omitted knob rather
 * than a zero, exactly as formIntPtr treats one.
 *
 * The three scalars this parses -- the cost target, the offspring count and
 * the offspring ratio -- all have a meaningful zero, so "unset" cannot be
 * spelled as one and the absent value has to survive all the way to the wire.
 */
export function parseOptionalInt(
  label: string,
  raw: string,
  low: number,
  high: number,
): { value?: number } | { error: string } {
  const trimmed = raw.trim();

  if (trimmed === "") {
    return {};
  }

  return parseInt10(label, trimmed, low, high);
}

/** The same, for a value that need not be whole. */
export function parseOptionalNumber(
  label: string,
  raw: string,
  low: number,
  high: number,
): { value?: number } | { error: string } {
  const trimmed = raw.trim();

  if (trimmed === "") {
    return {};
  }

  // The pattern, not Number(), decides what a number is: Number() also takes
  // "0x10" and "Infinity", neither of which Go's ParseFloat reads.
  if (!/^[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?$/.test(trimmed)) {
    return { error: `${label} must be a number.` };
  }

  const value = Number(trimmed);

  if (!Number.isFinite(value)) {
    return { error: `${label} must be a finite number.` };
  }

  if (value < low || value > high) {
    return { error: `${label} must be in [${low}, ${high}].` };
  }

  return { value };
}

/** The error key one tuning knob reports under. */
export function tuningErrorKey(key: string): string {
  return `tuning-${key}`;
}

/**
 * Knobs the form already offers as a field of their own, and which the knob
 * editor therefore leaves out.
 *
 * These are the settings algo-piano's optimizer audit found to matter most --
 * round length above all, then early stopping -- so they are promoted out of
 * the long alphabetical list and given plain names near the top. Rendering them
 * in both places would put two identically labelled inputs on one form, and no
 * amount of documented precedence makes that readable.
 *
 * The CLI keeps both spellings, because there the shorthand saves writing a
 * file at all. A form field is a form field either way, so there is nothing to
 * save here.
 */
const PROMOTED_TUNING_KEYS = new Set([
  "epochs",
  "restarts",
  "stagnation_iterations",
  "target_cost",
  "nc",
  "nc_ratio",
  "selection",
]);

/** Holds a knob to the range the generated table carries for it. */
function tuningRangeError(
  field: MayflyTuningField,
  value: number,
): string | null {
  if (field.min !== undefined) {
    const tooLow =
      field.minExclusive === true ? value <= field.min : value < field.min;

    if (tooLow) {
      return field.minExclusive === true
        ? `${field.label} must be above ${field.min}.`
        : `${field.label} must be at least ${field.min}.`;
    }
  }

  if (field.max !== undefined) {
    const tooHigh =
      field.maxExclusive === true ? value >= field.max : value > field.max;

    if (tooHigh) {
      return field.maxExclusive === true
        ? `${field.label} must be below ${field.max}.`
        : `${field.label} must be at most ${field.max}.`;
    }
  }

  return null;
}

/** What one knob contributes: a value, nothing at all, or a reason it cannot. */
type ParsedKnob =
  | { value: MayflyTuningValue }
  | { omitted: true }
  | { error: string };

/** Reads one knob according to its kind. An empty field is omitted, never zero. */
function parseTuningField(field: MayflyTuningField, raw: string): ParsedKnob {
  const trimmed = raw.trim();

  if (trimmed === "") {
    return { omitted: true };
  }

  if (field.kind === "bool") {
    // A checkbox has no empty state, so the value is held as a string: an
    // untouched knob is "", and only a touched one is "true" or "false".
    return { value: trimmed === "true" };
  }

  if (field.kind === "enum") {
    if (field.options !== undefined && !field.options.includes(trimmed)) {
      return {
        error: `${field.label} must be one of ${field.options.join(", ")}.`,
      };
    }

    return { value: trimmed };
  }

  const parsed =
    field.kind === "int"
      ? parseInt10(field.label, trimmed, -Infinity, Infinity)
      : parseOptionalNumber(field.label, trimmed, -Infinity, Infinity);

  if ("error" in parsed) {
    return { error: parsed.error };
  }

  if (parsed.value === undefined) {
    return { omitted: true };
  }

  const rangeError = tuningRangeError(field, parsed.value);

  return rangeError === null ? { value: parsed.value } : { error: rangeError };
}

/** What buildMayflyTuningDocument answers with. */
type BuiltTuning =
  | { document: MayflyTuningDocument; count: number }
  | { errors: FieldErrors };

/**
 * Builds the `mayflyTuning` document from the knob editor.
 *
 * It takes the fields to read rather than reading the whole generated table,
 * so the caller decides which dialect's knobs are in play and a knob the form
 * is not showing can never reach the wire.
 *
 * `count` is what decides whether the document is sent at all. An untouched
 * editor yields zero and no part: an empty document would be harmless, but a
 * part that is always present makes "did the user tune anything" unanswerable
 * from the request alone.
 */
export function buildMayflyTuningDocument(
  fields: readonly MayflyTuningField[],
  values: TuningValues,
): BuiltTuning {
  const found: FieldErrors = {};
  const document: MayflyTuningDocument = {};
  const convergence: MayflyConvergenceDocument = {};
  const schedule: MayflyScheduleDocument = {};

  let count = 0;

  for (const field of fields) {
    const parsed = parseTuningField(field, values[field.key] ?? "");

    if ("error" in parsed) {
      found[tuningErrorKey(field.key)] = parsed.error;

      continue;
    }

    if ("omitted" in parsed) {
      continue;
    }

    count += 1;

    // The generated table lists every knob flat, because that is how the CLI
    // help and the docs read; the document nests two blocks of them. The block
    // membership is generated alongside the table from MayflyConvergence and
    // MayflySchedule, so a key moved between blocks in Go cannot leave this
    // building the wrong shape.
    if (MAYFLY_CONVERGENCE_KEYS.includes(field.key)) {
      (convergence as Record<string, MayflyTuningValue>)[field.key] =
        parsed.value;
    } else if (MAYFLY_SCHEDULE_KEYS.includes(field.key)) {
      (schedule as Record<string, MayflyTuningValue>)[field.key] = parsed.value;
    } else {
      document[field.key] = parsed.value;
    }
  }

  if (Object.keys(found).length > 0) {
    return { errors: found };
  }

  // An empty block is left out rather than written as {}: the decoder reads a
  // present block as "these keys were chosen", and an empty one would say
  // nothing while still looking deliberate.
  if (Object.keys(convergence).length > 0) {
    document.convergence = convergence;
  }

  if (Object.keys(schedule).length > 0) {
    document.schedule = schedule;
  }

  return { document, count };
}

/**
 * The starting-preset choice that is not a built-in sound.
 *
 * It is a sentinel in the same select rather than a second control because it
 * is the same decision: what the fit starts from. No preset can be called this
 * -- an id is a filename stem -- so the two cannot collide.
 */
const PRESET_FROM_FILE = "__file__";

/** The `preset` part a choice sends, or null when there is nothing to send. */
function presetPart(
  choice: string,
  file: File | null,
): { blob: Blob; name: string } | null {
  if (choice === PRESET_FROM_FILE) {
    return file === null ? null : { blob: file, name: file.name };
  }

  const builtin = SOUND_PRESETS.find((entry) => entry.id === choice);

  if (builtin === undefined) {
    return null;
  }

  return {
    blob: new Blob([builtin.document], { type: "application/json" }),
    name: `${builtin.id}.json`,
  };
}

export function FitForm({ snapshot, onSnapshot, actions }: FitFormProps) {
  const ids = useId();
  const fieldId = (name: string) => `${ids}-${name}`;

  const referenceRef = useRef<HTMLInputElement>(null);
  const presetRef = useRef<HTMLInputElement>(null);

  // What the fit starts from: a built-in sound's id, or PRESET_FROM_FILE with
  // the file beside it. The file input behind them is never the source of
  // truth, because it cannot be cleared back to a built-in by a click on a
  // menu entry -- and because the dialog can be dismissed, which has to leave
  // the choice exactly as it was.
  const [presetChoice, setPresetChoice] = useState<string>(
    DEFAULT_SOUND_PRESET_ID,
  );
  const [presetFile, setPresetFile] = useState<File | null>(null);

  const [scalars, setScalars] = useState<ScalarFields>(INITIAL_SCALARS);
  const [metric, setMetric] = useState<MetricName>(DEFAULT_FIT_REQUEST.metric);
  const [optimizer, setOptimizer] = useState<OptimizerName>(
    DEFAULT_FIT_REQUEST.optimizer,
  );
  const [mayflyVariant, setMayflyVariant] = useState<MayflyVariant>(
    DEFAULT_FIT_REQUEST.mayflyVariant,
  );
  const [cmaesCovariance, setCmaesCovariance] = useState<CmaesCovariance>(
    DEFAULT_FIT_REQUEST.cmaesCovariance ?? "separable",
  );
  const [align, setAlign] = useState(DEFAULT_FIT_REQUEST.align);
  const [normalizeGain, setNormalizeGain] = useState(
    DEFAULT_FIT_REQUEST.normalizeGain,
  );

  const [tuningValues, setTuningValues] = useState<TuningValues>({});

  const [useBounds, setUseBounds] = useState(false);
  const [boundsRows, setBoundsRows] = useState<BoundsRows>(EMPTY_BOUNDS_ROWS);
  const [advancedOpen, setAdvancedOpen] = useState(false);

  const [errors, setErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const running = snapshot?.state === "running";
  const fitActions = actions ?? SERVER_ACTIONS;
  const mayfly = optimizer === "mayfly";
  const cmaes = optimizer === "cmaes";
  const jobState =
    snapshot === null
      ? "Ready to start"
      : snapshot.state === "running"
        ? `Fit ${snapshot.jobId} running`
        : `Fit ${snapshot.jobId} ${snapshot.state}`;

  const openPresetDialog = () => {
    const input = presetRef.current;

    if (input === null) {
      return;
    }

    // Cleared first so that picking the same file again is still a change: the
    // input compares against what it already holds, and "Choose another file"
    // resolving to nothing would look like the dialog had been dismissed.
    input.value = "";
    input.click();
  };

  const setScalar = (name: keyof ScalarFields, value: string) => {
    setScalars((previous) => ({ ...previous, [name]: value }));
  };

  const setTuning = (key: string, value: string) => {
    setTuningValues((previous) => ({ ...previous, [key]: value }));
  };

  const setBound = (key: BoundsKey, end: keyof BoundsRow, value: string) => {
    setBoundsRows((previous) => ({
      ...previous,
      [key]: { ...previous[key], [end]: value },
    }));
  };

  /**
   * The dialect the knob editor is for.
   *
   * A preset picks a dialect of its own and the engine refuses a preset and an
   * explicit variant together, so a chosen preset takes the variant select off
   * the screen -- removed rather than disabled, for the reason the whole mayfly
   * block is -- and leaves the editor with the shared knobs only. The empty
   * string is how "no dialect is known yet" is spelled: it matches the shared
   * knobs, whose own variant field is empty, and no dialect's.
   */
  const mayflyPreset = scalars.mayflyPreset.trim();
  const tuningVariant = mayflyPreset === "" ? mayflyVariant : "";
  const tuningFields = useMemo(
    () =>
      mayflyTuningFieldsFor(tuningVariant).filter(
        (field) => !PROMOTED_TUNING_KEYS.has(field.key),
      ),
    [tuningVariant],
  );

  /** The maximum reference size, spelled the way the server's message does. */
  const referenceLimitLabel = useMemo(
    () => `${FIT_LIMITS.maxReferenceBytes / (1 << 20)} MiB`,
    [],
  );

  /**
   * Validates everything and builds the multipart body, or returns the field
   * errors. Nothing is uploaded until this succeeds.
   */
  function buildForm(): BuiltForm {
    const found: FieldErrors = {};

    const reference = referenceRef.current?.files?.[0] ?? null;

    if (reference === null) {
      found.reference = "A reference WAV must be chosen.";
    } else if (reference.size > FIT_LIMITS.maxReferenceBytes) {
      found.reference = `The reference is ${reference.size} bytes, above the ${referenceLimitLabel} limit.`;
    } else if (reference.size === 0) {
      found.reference = "The reference is empty.";
    }

    const note = parseInt10(
      "The note",
      scalars.note,
      FIT_LIMITS.note.min,
      FIT_LIMITS.note.max,
    );
    const velocity = parseInt10(
      "The velocity",
      scalars.velocity,
      FIT_LIMITS.velocity.min,
      FIT_LIMITS.velocity.max,
    );
    const maxIterations = parseInt10(
      "The iteration limit",
      scalars.maxIterations,
      FIT_LIMITS.maxIterations.min,
      FIT_LIMITS.maxIterations.max,
    );
    const reportEvery = parseInt10(
      "The report interval",
      scalars.reportEvery,
      FIT_LIMITS.reportEvery.min,
      FIT_LIMITS.reportEvery.max,
    );
    const budget = parseDuration(scalars.timeBudget);

    if ("error" in note) {
      found.note = note.error;
    }

    if ("error" in velocity) {
      found.velocity = velocity.error;
    }

    if ("error" in maxIterations) {
      found.maxIterations = maxIterations.error;
    }

    if ("error" in reportEvery) {
      found.reportEvery = reportEvery.error;
    }

    if ("error" in budget) {
      found.timeBudget = budget.error;
    } else if (
      budget.seconds <= FIT_LIMITS.timeBudgetSeconds.min ||
      budget.seconds > FIT_LIMITS.timeBudgetSeconds.max
    ) {
      found.timeBudget =
        "The time budget must be above zero and at most 1h (3600s).";
    }

    let population: number | null = null;
    let seed: string | null = null;
    let epochs: number | null = null;
    let restarts: number | null = null;
    let stagnation: number | null = null;
    let targetCost: number | undefined;
    let nc: number | undefined;
    let ncRatio: number | undefined;
    let tuning: MayflyTuningDocument | null = null;
    let tuningCount = 0;

    if (mayfly) {
      const parsedPopulation = parseInt10(
        "The mayfly population",
        scalars.mayflyPopulation,
        FIT_LIMITS.mayflyPopulation.min,
        FIT_LIMITS.mayflyPopulation.max,
      );
      const parsedSeed = parseSeed(scalars.mayflySeed);

      if ("error" in parsedPopulation) {
        found.mayflyPopulation = parsedPopulation.error;
      } else {
        population = parsedPopulation.value;
      }

      if ("error" in parsedSeed) {
        found.mayflySeed = parsedSeed.error;
      } else {
        seed = parsedSeed.value;
      }

      const parsedEpochs = parseInt10(
        "The epoch count",
        scalars.mayflyEpochs,
        FIT_LIMITS.mayflyEpochs.min,
        FIT_LIMITS.mayflyEpochs.max,
      );
      const parsedRestarts = parseInt10(
        "The restart count",
        scalars.mayflyRestarts,
        FIT_LIMITS.mayflyRestarts.min,
        FIT_LIMITS.mayflyRestarts.max,
      );
      const parsedStagnation = parseInt10(
        "The stagnation limit",
        scalars.mayflyStagnation,
        FIT_LIMITS.mayflyStagnation.min,
        FIT_LIMITS.mayflyStagnation.max,
      );

      if ("error" in parsedEpochs) {
        found.mayflyEpochs = parsedEpochs.error;
      } else {
        epochs = parsedEpochs.value;
      }

      if ("error" in parsedRestarts) {
        found.mayflyRestarts = parsedRestarts.error;
      } else {
        restarts = parsedRestarts.value;
      }

      if ("error" in parsedStagnation) {
        found.mayflyStagnation = parsedStagnation.error;
      } else {
        stagnation = parsedStagnation.value;
      }

      // The three whose zero is a value of its own, so an empty field has to
      // stay absent rather than become one.
      const parsedTargetCost = parseOptionalNumber(
        "The target cost",
        scalars.mayflyTargetCost,
        FIT_LIMITS.mayflyTargetCost.min,
        FIT_LIMITS.mayflyTargetCost.max,
      );
      const parsedNC = parseOptionalInt(
        "The offspring count",
        scalars.mayflyNc,
        FIT_LIMITS.mayflyNc.min,
        FIT_LIMITS.mayflyNc.max,
      );
      const parsedNCRatio = parseOptionalNumber(
        "The offspring ratio",
        scalars.mayflyNcRatio,
        FIT_LIMITS.mayflyNcRatio.min,
        FIT_LIMITS.mayflyNcRatio.max,
      );

      if ("error" in parsedTargetCost) {
        found.mayflyTargetCost = parsedTargetCost.error;
      } else {
        targetCost = parsedTargetCost.value;
      }

      if ("error" in parsedNC) {
        found.mayflyNc = parsedNC.error;
      } else {
        nc = parsedNC.value;
      }

      if ("error" in parsedNCRatio) {
        found.mayflyNcRatio = parsedNCRatio.error;
      } else {
        ncRatio = parsedNCRatio.value;
      }

      const builtTuning = buildMayflyTuningDocument(tuningFields, tuningValues);

      if ("errors" in builtTuning) {
        Object.assign(found, builtTuning.errors);
      } else {
        tuning = builtTuning.document;
        tuningCount = builtTuning.count;
      }
    }

    let lambda: number | null = null;
    let sigma: number | null = null;
    let cmaesSeed: number | null = null;
    let cmaesRestarts: number | null = null;

    if (cmaes) {
      const parsedLambda = parseInt10(
        "The population",
        scalars.cmaesLambda,
        FIT_LIMITS.cmaesLambda.min,
        FIT_LIMITS.cmaesLambda.max,
      );
      const parsedSigma = parseOptionalNumber(
        "The step size",
        scalars.cmaesSigma,
        FIT_LIMITS.cmaesSigma.min,
        FIT_LIMITS.cmaesSigma.max,
      );
      const parsedCmaesSeed = parseInt10(
        "The seed",
        scalars.cmaesSeed,
        FIT_LIMITS.cmaesSeed.min,
        FIT_LIMITS.cmaesSeed.max,
      );
      const parsedCmaesRestarts = parseInt10(
        "The restart limit",
        scalars.cmaesRestarts,
        FIT_LIMITS.cmaesRestarts.min,
        FIT_LIMITS.cmaesRestarts.max,
      );

      if ("error" in parsedLambda) {
        found.cmaesLambda = parsedLambda.error;
      } else if (parsedLambda.value === 1) {
        // Zero is "take Hansen's default", so one is the only population the
        // range admits that the backend then refuses.
        found.cmaesLambda = "The population must be 0 or at least 2.";
      } else {
        lambda = parsedLambda.value;
      }

      if ("error" in parsedSigma) {
        found.cmaesSigma = parsedSigma.error;
      } else if (parsedSigma.value !== undefined) {
        // Zero is left to travel: the backend reads it as "take the default",
        // the same way it reads a zero population and a zero seed.
        sigma = parsedSigma.value;
      }

      if ("error" in parsedCmaesSeed) {
        found.cmaesSeed = parsedCmaesSeed.error;
      } else {
        cmaesSeed = parsedCmaesSeed.value;
      }

      if ("error" in parsedCmaesRestarts) {
        found.cmaesRestarts = parsedCmaesRestarts.error;
      } else {
        cmaesRestarts = parsedCmaesRestarts.value;
      }
    }

    const bounds: BoundsDocument = {};
    let boundsCount = 0;

    if (useBounds) {
      for (const key of BOUNDS_KEYS) {
        const parsed = parseBoundsRow(key, boundsRows[key]);

        if ("error" in parsed) {
          found[`bounds-${key}`] = parsed.error;

          continue;
        }

        if (parsed.range !== undefined) {
          bounds[key] = parsed.range;
          boundsCount += 1;
        }
      }

      if (boundsCount === 0 && Object.keys(found).length === 0) {
        found.bounds =
          "Narrowing the bounds is on, but no dimension has been given a range.";
      }
    }

    if (Object.keys(found).length > 0) {
      const advancedError = Object.keys(found).some(
        (name) =>
          name === "reportEvery" ||
          name === "mayflyPopulation" ||
          name === "mayflySeed" ||
          name === "mayflyEpochs" ||
          name === "mayflyRestarts" ||
          name === "mayflyStagnation" ||
          name === "mayflyTargetCost" ||
          name === "mayflyNc" ||
          name === "mayflyNcRatio" ||
          name === "cmaesLambda" ||
          name === "cmaesSigma" ||
          name === "cmaesSeed" ||
          name === "cmaesRestarts" ||
          name === "bounds" ||
          name.startsWith("bounds-") ||
          // Every knob of the tuning editor reports under this prefix, so the
          // list does not have to be restated each time the generated table
          // grows a knob.
          name.startsWith("tuning-"),
      );

      if (advancedError) {
        // An error hidden inside a closed disclosure is effectively no error
        // at all: the summary says the form needs fixing, but the field that
        // explains how is absent. Opening only on an advanced error keeps the
        // ordinary setup compact without making validation cryptic.
        setAdvancedOpen(true);
      }

      return { errors: found };
    }

    const form = new FormData();

    // reference is non-null here: an absent one is a field error above.
    form.append("reference", reference as File);

    // A built-in is sent as its own document rather than as its name: the
    // server reads `preset` with FormFile and the WASM module takes preset
    // bytes, so neither end can resolve an id. The default is sent like any
    // other, which keeps one path through here rather than a special case that
    // relies on the two ends agreeing about what "omitted" falls back to.
    const preset = presetPart(presetChoice, presetFile);

    if (preset !== null) {
      form.append("preset", preset.blob, preset.name);
    }

    if (useBounds && boundsCount > 0) {
      // A JSON Blob rather than a scalar field: the server reads `bounds` with
      // FormFile, exactly as it reads the optional starting preset.
      form.append(
        "bounds",
        new Blob([JSON.stringify(bounds)], { type: "application/json" }),
        "bounds.json",
      );
    }

    form.append("note", String((note as { value: number }).value));
    form.append("velocity", String((velocity as { value: number }).value));
    form.append("metric", metric);
    form.append("optimizer", optimizer);
    form.append(
      "maxIterations",
      String((maxIterations as { value: number }).value),
    );
    form.append(
      "reportEvery",
      String((reportEvery as { value: number }).value),
    );
    // A duration string, never milliseconds: timeBudgetMs exists only inside Go.
    form.append("timeBudget", scalars.timeBudget.trim());
    form.append("align", String(align));
    form.append("normalizeGain", String(normalizeGain));

    if (mayfly) {
      // The variant and the preset are exclusive: MayflyOptimizer refuses a
      // run that names both, and parseFitRequest reads an empty variant field
      // as "the preset chooses". Sending the field empty rather than dropping
      // it keeps the WASM front end, which falls back to the default variant
      // for an absent field, from reintroducing the conflict.
      form.append("mayflyVariant", mayflyPreset === "" ? mayflyVariant : "");
      form.append("mayflyPreset", mayflyPreset);
      form.append("mayflyPopulation", String(population));
      form.append("mayflySeed", String(seed));
      form.append("mayflyEpochs", String(epochs));
      form.append("mayflyRestarts", String(restarts));
      form.append("mayflyStagnation", String(stagnation));
      form.append("mayflySelection", scalars.mayflySelection);

      // Absent rather than zero: the server reads these three with the pointer
      // twins of its form helpers, so an omitted field leaves the knob
      // unwritten while a written zero is a value.
      if (targetCost !== undefined) {
        form.append("mayflyTargetCost", String(targetCost));
      }

      if (nc !== undefined) {
        form.append("mayflyNc", String(nc));
      }

      if (ncRatio !== undefined) {
        form.append("mayflyNcRatio", String(ncRatio));
      }

      if (tuning !== null && tuningCount > 0) {
        // A JSON Blob, exactly as `bounds` is: readMayflyTuningPart reads
        // `mayflyTuning` with FormFile, not FormValue.
        form.append(
          "mayflyTuning",
          new Blob([JSON.stringify(tuning)], { type: "application/json" }),
          "mayflyTuning.json",
        );
      }
    }

    if (cmaes) {
      form.append("cmaesCovariance", cmaesCovariance);
      form.append("cmaesLambda", String(lambda));

      // An empty field is left out of the request entirely, which is how the
      // service is told to keep its own default.
      if (sigma !== null) {
        form.append("cmaesSigma", String(sigma));
      }

      form.append("cmaesSeed", String(cmaesSeed));
      form.append("cmaesRestarts", String(cmaesRestarts));
    }

    return { form, maxIterations: (maxIterations as { value: number }).value };
  }

  async function onSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();

    setFormError(null);
    setNotice(null);

    const built = buildForm();

    if ("errors" in built) {
      setErrors(built.errors);
      setFormError("Some fields need fixing before the fit can start.");

      return;
    }

    setErrors({});
    setBusy(true);

    try {
      const started = await fitActions.start(built.form);

      onSnapshot(started, built.maxIterations);
      setNotice(`Fit ${started.jobId} started.`);
    } catch (cause) {
      if (cause instanceof FitApiError) {
        // The 409 is the single-fit-slot conflict and deserves its own words:
        // "a fit is already running" is actionable, "request failed" is not.
        setFormError(
          cause.isConflict
            ? `${cause.message}. Cancel it first, or wait for it to finish.`
            : cause.message,
        );

        if (cause.isConflict) {
          await watchTheRunningFit();
        }
      } else {
        setFormError(
          cause instanceof Error
            ? cause.message
            : "The fit could not be started.",
        );
      }
    } finally {
      setBusy(false);
    }
  }

  /**
   * Makes the fit that holds the slot the job this page is watching.
   *
   * A 409 says another tab or a CLI client owns the single slot, so the job
   * behind the conflict is not the one on screen -- the page may be showing an
   * older, finished run, or nothing at all. Without this the Cancel button the
   * error message points at stays disabled and the advice cannot be followed
   * without a reload. No `maxIterations` is passed with the snapshot: this
   * page did not send the request and does not know the limit.
   */
  async function watchTheRunningFit() {
    if (fitActions.status === undefined) {
      return;
    }

    try {
      onSnapshot(await fitActions.status());
    } catch {
      // The conflict itself is already on screen and is the actionable half.
      // A follow-up read that fails as well adds nothing the user can act on,
      // and replacing the conflict message with its error would take the
      // useful half away.
    }
  }

  async function onCancel() {
    setFormError(null);
    setNotice(null);
    setBusy(true);

    try {
      // The job id is passed so that cancelling while the watched run ends and
      // another begins cannot silently kill the newcomer; the server answers a
      // mismatch with a 409 rather than stopping the wrong fit.
      const stopped = await fitActions.cancel(snapshot?.jobId);

      onSnapshot(stopped);
      setNotice(`Fit ${stopped.jobId} is ${stopped.state}.`);
    } catch (cause) {
      setFormError(
        cause instanceof FitApiError
          ? cause.message
          : cause instanceof Error
            ? cause.message
            : "The fit could not be cancelled.",
      );
    } finally {
      setBusy(false);
    }
  }

  /** Renders the message under a field and wires it up for a screen reader. */
  function describedBy(name: string): string | undefined {
    return errors[name] === undefined ? undefined : `${fieldId(name)}-error`;
  }

  /** One row of the tuning editor, rendered from the generated knob table. */
  function tuningRow(field: MayflyTuningField) {
    const name = tuningErrorKey(field.key);
    const id = fieldId(name);
    const value = tuningValues[field.key] ?? "";
    const help = `${id}-help`;
    const describedBySelf =
      errors[name] === undefined ? help : `${help} ${id}-error`;

    return (
      <div className="fit-field" key={field.key}>
        <label htmlFor={id}>{field.label}</label>

        {field.kind === "enum" ? (
          <select
            aria-describedby={describedBySelf}
            id={id}
            onChange={(event) => {
              setTuning(field.key, event.target.value);
            }}
            value={value}
          >
            {/*
              The empty option is the omitted knob, and it is first so that it
              is what an untouched row shows. Choosing it again removes the key
              from the document rather than writing a "default" value, which
              the document has no way to spell.
            */}
            <option value="">(keep the dialect's own)</option>
            {(field.options ?? []).map((option) => (
              <option key={option} value={option}>
                {option}
              </option>
            ))}
          </select>
        ) : field.kind === "bool" ? (
          <input
            aria-describedby={describedBySelf}
            checked={value === "true"}
            id={id}
            onChange={(event) => {
              // An untouched checkbox is "", not false: a checkbox has no
              // third state, so the string is what keeps "never set" apart
              // from "deliberately off". Unticking a ticked one writes false.
              setTuning(field.key, String(event.target.checked));
            }}
            type="checkbox"
          />
        ) : (
          <input
            aria-describedby={describedBySelf}
            aria-invalid={errors[name] !== undefined}
            id={id}
            inputMode={field.kind === "int" ? "numeric" : "decimal"}
            max={field.max}
            min={field.min}
            onChange={(event) => {
              setTuning(field.key, event.target.value);
            }}
            placeholder="default"
            step={field.kind === "int" ? 1 : "any"}
            type="number"
            value={value}
          />
        )}

        <p className="fit-hint" id={help}>
          {field.help}
        </p>
        {fieldError(name)}
      </div>
    );
  }

  function fieldError(name: string) {
    const message = errors[name];

    if (message === undefined) {
      return null;
    }

    return (
      <p className="fit-field-error" id={`${fieldId(name)}-error`}>
        {message}
      </p>
    );
  }

  return (
    <form
      className="fit-form"
      noValidate
      onSubmit={(event) => void onSubmit(event)}
    >
      <fieldset className="fit-group">
        <legend>
          <span className="fit-step-number">1</span>
          <span>Reference</span>
        </legend>

        <div className="fit-field">
          <label htmlFor={fieldId("reference")}>
            Reference recording (WAV)
          </label>
          <input
            accept=".wav,audio/wav,audio/x-wav"
            aria-describedby={describedBy("reference")}
            aria-invalid={errors.reference !== undefined}
            id={fieldId("reference")}
            name="reference"
            ref={referenceRef}
            required
            type="file"
          />
          <p className="fit-hint">
            Uses the first channel. Maximum {referenceLimitLabel}.
          </p>
          {fieldError("reference")}
        </div>

        <div className="fit-field">
          <label htmlFor={fieldId("preset")}>Starting preset</label>
          <select
            id={fieldId("preset")}
            onChange={(event) => {
              if (event.target.value !== PRESET_FROM_FILE) {
                setPresetChoice(event.target.value);

                return;
              }

              // The choice is not moved here. A dialog can be dismissed, and
              // the select is controlled, so leaving the state alone is what
              // puts the previous entry back on screen when it is; the choice
              // moves to the file in the change handler below, which only runs
              // when a file was actually picked.
              openPresetDialog();
            }}
            value={presetChoice}
          >
            {SOUND_PRESETS.map((entry) => (
              <option key={entry.id} value={entry.id}>
                {entry.label}
              </option>
            ))}
            <option value={PRESET_FROM_FILE}>
              {presetFile === null
                ? "Load from file…"
                : `File: ${presetFile.name}`}
            </option>
          </select>

          {/*
           * The real input, and nothing but the dialog behind it: what has been
           * chosen is shown by the select above, which can also say "a built-in
           * sound", something a file input has no way to display. `hidden`
           * rather than the visually-hidden treatment the chart's summary gets,
           * because this one is not for reading either -- it is a control with
           * no role left to play once the select speaks for it, and a hidden
           * input still opens its dialog when it is clicked.
           */}
          <input
            accept=".json,application/json"
            hidden
            onChange={(event) => {
              const chosen = event.target.files?.[0] ?? null;

              // A dismissed dialog either fires nothing at all or fires with an
              // empty list, and both mean the same thing here: the choice was
              // never moved off what it was, so putting the previous entry back
              // is a matter of leaving it alone.
              if (chosen === null) {
                return;
              }

              setPresetFile(chosen);
              setPresetChoice(PRESET_FROM_FILE);
            }}
            ref={presetRef}
            type="file"
          />

          <p className="fit-hint">
            {presetChoice === PRESET_FROM_FILE ? (
              <>
                Fitting starts from this document.{" "}
                <button
                  className="fit-inline-button"
                  onClick={openPresetDialog}
                  type="button"
                >
                  Choose another file
                </button>
              </>
            ) : (
              "Fitting starts from the chosen built-in sound, or from a preset JSON document of your own."
            )}
          </p>
        </div>
      </fieldset>

      <fieldset className="fit-group">
        <legend>
          <span className="fit-step-number">2</span>
          <span>Note</span>
        </legend>

        <div className="fit-row">
          <div className="fit-field">
            <label htmlFor={fieldId("note")}>MIDI note</label>
            <input
              aria-describedby={describedBy("note")}
              aria-invalid={errors.note !== undefined}
              id={fieldId("note")}
              inputMode="numeric"
              max={FIT_LIMITS.note.max}
              min={FIT_LIMITS.note.min}
              onChange={(event) => {
                setScalar("note", event.target.value);
              }}
              type="number"
              value={scalars.note}
            />
            {fieldError("note")}
          </div>

          <div className="fit-field">
            <label htmlFor={fieldId("velocity")}>Velocity</label>
            <input
              aria-describedby={describedBy("velocity")}
              aria-invalid={errors.velocity !== undefined}
              id={fieldId("velocity")}
              inputMode="numeric"
              max={FIT_LIMITS.velocity.max}
              min={FIT_LIMITS.velocity.min}
              onChange={(event) => {
                setScalar("velocity", event.target.value);
              }}
              type="number"
              value={scalars.velocity}
            />
            {fieldError("velocity")}
          </div>
        </div>
      </fieldset>

      <fieldset className="fit-group">
        <legend>
          <span className="fit-step-number">3</span>
          <span>Fit setup</span>
        </legend>

        <div className="fit-row">
          <div className="fit-field">
            <label htmlFor={fieldId("metric")}>Metric</label>
            <select
              id={fieldId("metric")}
              onChange={(event) => {
                setMetric(event.target.value as MetricName);
              }}
              value={metric}
            >
              {METRIC_NAMES.map((name) => (
                <option key={name} value={name}>
                  {METRIC_LABELS[name]}
                </option>
              ))}
            </select>
          </div>

          <div className="fit-field">
            <label htmlFor={fieldId("optimizer")}>Optimizer</label>
            <select
              id={fieldId("optimizer")}
              onChange={(event) => {
                const chosen = event.target.value as OptimizerName;

                // The cadence follows the backend, because its unit changes
                // with it: an iteration is one evaluation for the simple
                // optimizer and a whole generation -- some fifty renders --
                // for mayfly. Only a field still holding the previous
                // backend's default is moved, so a cadence the user typed is
                // never overwritten underneath them.
                setScalars((previous) =>
                  previous.reportEvery === String(defaultReportEvery(optimizer))
                    ? {
                        ...previous,
                        reportEvery: String(defaultReportEvery(chosen)),
                      }
                    : previous,
                );

                setOptimizer(chosen);
              }}
              value={optimizer}
            >
              {OPTIMIZER_NAMES.map((name) => (
                <option key={name} value={name}>
                  {name}
                </option>
              ))}
            </select>
          </div>
        </div>

        <div className="fit-row">
          <div className="fit-field">
            <label htmlFor={fieldId("maxIterations")}>Iteration limit</label>
            <input
              aria-describedby={describedBy("maxIterations")}
              aria-invalid={errors.maxIterations !== undefined}
              id={fieldId("maxIterations")}
              inputMode="numeric"
              max={FIT_LIMITS.maxIterations.max}
              min={FIT_LIMITS.maxIterations.min}
              onChange={(event) => {
                setScalar("maxIterations", event.target.value);
              }}
              type="number"
              value={scalars.maxIterations}
            />
            {fieldError("maxIterations")}
          </div>

          <div className="fit-field">
            <label htmlFor={fieldId("timeBudget")}>Time budget</label>
            <input
              aria-describedby={describedBy("timeBudget")}
              aria-invalid={errors.timeBudget !== undefined}
              id={fieldId("timeBudget")}
              onChange={(event) => {
                setScalar("timeBudget", event.target.value);
              }}
              type="text"
              value={scalars.timeBudget}
            />
            <p className="fit-hint">
              Examples: <code>30s</code>, <code>2m</code> or <code>1h</code>.
              Maximum 1 hour.
            </p>
            {fieldError("timeBudget")}
          </div>
        </div>

        <div className="fit-checks">
          <div className="fit-check">
            <input
              checked={align}
              id={fieldId("align")}
              onChange={(event) => {
                setAlign(event.target.checked);
              }}
              type="checkbox"
            />
            <label htmlFor={fieldId("align")}>
              Align the render to the reference onset
            </label>
          </div>

          <div className="fit-check">
            <input
              checked={normalizeGain}
              id={fieldId("normalizeGain")}
              onChange={(event) => {
                setNormalizeGain(event.target.checked);
              }}
              type="checkbox"
            />
            <label htmlFor={fieldId("normalizeGain")}>Normalize gain</label>
          </div>
        </div>

        <details
          className="fit-advanced"
          onToggle={(event) => {
            setAdvancedOpen(event.currentTarget.open);
          }}
          open={advancedOpen}
        >
          <summary>Advanced settings</summary>

          <div className="fit-advanced-body">
            <div className="fit-field">
              <label htmlFor={fieldId("reportEvery")}>Report every</label>
              <input
                aria-describedby={describedBy("reportEvery")}
                aria-invalid={errors.reportEvery !== undefined}
                id={fieldId("reportEvery")}
                inputMode="numeric"
                max={FIT_LIMITS.reportEvery.max}
                min={FIT_LIMITS.reportEvery.min}
                onChange={(event) => {
                  setScalar("reportEvery", event.target.value);
                }}
                type="number"
                value={scalars.reportEvery}
              />
              <p className="fit-hint">
                {mayfly
                  ? "Mayfly generations between progress reports. One generation is the whole swarm, roughly fifty renders."
                  : "Optimizer iterations between progress reports. One iteration is roughly one render."}
              </p>
              {fieldError("reportEvery")}
            </div>

            {/*
              The mayfly fields are removed rather than disabled when the
              simple optimizer is chosen: the server does not read them at all
              in that case, and a greyed-out control that does nothing is a
              control that lies.
            */}
            {mayfly ? (
              <section
                aria-labelledby={fieldId("mayfly-heading")}
                className="fit-advanced-section"
              >
                <h3 id={fieldId("mayfly-heading")}>Mayfly optimizer</h3>

                <div className="fit-row">
                  <div className="fit-field">
                    <label htmlFor={fieldId("mayflyPreset")}>Preset</label>
                    <select
                      id={fieldId("mayflyPreset")}
                      onChange={(event) => {
                        setScalar("mayflyPreset", event.target.value);
                      }}
                      value={scalars.mayflyPreset}
                    >
                      <option value="">(none — choose a variant)</option>
                      {MAYFLY_PRESETS.map((name) => (
                        <option key={name} value={name}>
                          {name}
                        </option>
                      ))}
                    </select>
                    <p className="fit-hint">
                      A preset picks a dialect and a whole configuration for a
                      kind of landscape.
                    </p>
                  </div>

                  {/*
                    The variant select is removed while a preset is chosen for
                    the reason the whole mayfly block is removed for the simple
                    optimizer: the engine refuses a run that names a preset and
                    a dialect at once, so a control that appears to choose one
                    would be a control that lies.
                  */}
                  {mayflyPreset === "" ? (
                    <div className="fit-field">
                      <label htmlFor={fieldId("mayflyVariant")}>
                        Mayfly variant
                      </label>
                      <select
                        id={fieldId("mayflyVariant")}
                        onChange={(event) => {
                          setMayflyVariant(event.target.value as MayflyVariant);
                        }}
                        value={mayflyVariant}
                      >
                        {MAYFLY_VARIANTS.map((name) => (
                          <option key={name} value={name}>
                            {name}
                          </option>
                        ))}
                      </select>
                    </div>
                  ) : null}

                  <div className="fit-field">
                    <label htmlFor={fieldId("mayflyPopulation")}>
                      Population
                    </label>
                    <input
                      aria-describedby={describedBy("mayflyPopulation")}
                      aria-invalid={errors.mayflyPopulation !== undefined}
                      id={fieldId("mayflyPopulation")}
                      inputMode="numeric"
                      max={FIT_LIMITS.mayflyPopulation.max}
                      min={FIT_LIMITS.mayflyPopulation.min}
                      onChange={(event) => {
                        setScalar("mayflyPopulation", event.target.value);
                      }}
                      type="number"
                      value={scalars.mayflyPopulation}
                    />
                    {fieldError("mayflyPopulation")}
                  </div>

                  <div className="fit-field">
                    <label htmlFor={fieldId("mayflySeed")}>Seed</label>
                    <input
                      aria-describedby={describedBy("mayflySeed")}
                      aria-invalid={errors.mayflySeed !== undefined}
                      id={fieldId("mayflySeed")}
                      inputMode="numeric"
                      onChange={(event) => {
                        setScalar("mayflySeed", event.target.value);
                      }}
                      // A text field, not a number one: the seed is an exact
                      // int64 decimal string, and a number input is free to
                      // hand back a normalised value -- "1e+19" for a seed near
                      // the top of the range -- which BigInt() then refuses.
                      type="text"
                      value={scalars.mayflySeed}
                    />
                    {fieldError("mayflySeed")}
                  </div>
                </div>

                <div className="fit-row">
                  <div className="fit-field">
                    <label htmlFor={fieldId("mayflyEpochs")}>Epochs</label>
                    <input
                      aria-describedby={describedBy("mayflyEpochs")}
                      aria-invalid={errors.mayflyEpochs !== undefined}
                      id={fieldId("mayflyEpochs")}
                      inputMode="numeric"
                      max={FIT_LIMITS.mayflyEpochs.max}
                      min={FIT_LIMITS.mayflyEpochs.min}
                      onChange={(event) => {
                        setScalar("mayflyEpochs", event.target.value);
                      }}
                      type="number"
                      value={scalars.mayflyEpochs}
                    />
                    <p className="fit-hint">Optimizer epochs per fit.</p>
                    {fieldError("mayflyEpochs")}
                  </div>

                  <div className="fit-field">
                    <label htmlFor={fieldId("mayflyRestarts")}>Restarts</label>
                    <input
                      aria-describedby={describedBy("mayflyRestarts")}
                      aria-invalid={errors.mayflyRestarts !== undefined}
                      id={fieldId("mayflyRestarts")}
                      inputMode="numeric"
                      max={FIT_LIMITS.mayflyRestarts.max}
                      min={FIT_LIMITS.mayflyRestarts.min}
                      onChange={(event) => {
                        setScalar("mayflyRestarts", event.target.value);
                      }}
                      type="number"
                      value={scalars.mayflyRestarts}
                    />
                    <p className="fit-hint">
                      Restarts from a fresh population.
                    </p>
                    {fieldError("mayflyRestarts")}
                  </div>

                  <div className="fit-field">
                    <label htmlFor={fieldId("mayflyStagnation")}>
                      Stagnation limit
                    </label>
                    <input
                      aria-describedby={describedBy("mayflyStagnation")}
                      aria-invalid={errors.mayflyStagnation !== undefined}
                      id={fieldId("mayflyStagnation")}
                      inputMode="numeric"
                      max={FIT_LIMITS.mayflyStagnation.max}
                      min={FIT_LIMITS.mayflyStagnation.min}
                      onChange={(event) => {
                        setScalar("mayflyStagnation", event.target.value);
                      }}
                      type="number"
                      value={scalars.mayflyStagnation}
                    />
                    <p className="fit-hint">
                      Iterations without improvement before stopping. Zero
                      disables it.
                    </p>
                    {fieldError("mayflyStagnation")}
                  </div>
                </div>

                <div className="fit-row">
                  <div className="fit-field">
                    <label htmlFor={fieldId("mayflyTargetCost")}>
                      Target cost
                    </label>
                    <input
                      aria-describedby={describedBy("mayflyTargetCost")}
                      aria-invalid={errors.mayflyTargetCost !== undefined}
                      id={fieldId("mayflyTargetCost")}
                      inputMode="decimal"
                      onChange={(event) => {
                        setScalar("mayflyTargetCost", event.target.value);
                      }}
                      // Empty, not zero, when it is not wanted: zero is a
                      // usable target, so only an absent field can mean "off".
                      placeholder="off"
                      step="any"
                      type="number"
                      value={scalars.mayflyTargetCost}
                    />
                    <p className="fit-hint">
                      Stop once the best cost reaches this. Leave empty for no
                      target.
                    </p>
                    {fieldError("mayflyTargetCost")}
                  </div>

                  <div className="fit-field">
                    <label htmlFor={fieldId("mayflyNc")}>Offspring count</label>
                    <input
                      aria-describedby={describedBy("mayflyNc")}
                      aria-invalid={errors.mayflyNc !== undefined}
                      id={fieldId("mayflyNc")}
                      inputMode="numeric"
                      max={FIT_LIMITS.mayflyNc.max}
                      min={FIT_LIMITS.mayflyNc.min}
                      onChange={(event) => {
                        setScalar("mayflyNc", event.target.value);
                      }}
                      placeholder="default"
                      type="number"
                      value={scalars.mayflyNc}
                    />
                    <p className="fit-hint">
                      <code>-1</code> derives it from the ratio, <code>0</code>{" "}
                      disables crossover. Empty keeps the dialect's own.
                    </p>
                    {fieldError("mayflyNc")}
                  </div>

                  <div className="fit-field">
                    <label htmlFor={fieldId("mayflyNcRatio")}>
                      Offspring ratio
                    </label>
                    <input
                      aria-describedby={describedBy("mayflyNcRatio")}
                      aria-invalid={errors.mayflyNcRatio !== undefined}
                      id={fieldId("mayflyNcRatio")}
                      inputMode="decimal"
                      max={FIT_LIMITS.mayflyNcRatio.max}
                      min={FIT_LIMITS.mayflyNcRatio.min}
                      onChange={(event) => {
                        setScalar("mayflyNcRatio", event.target.value);
                      }}
                      placeholder="default"
                      step="any"
                      type="number"
                      value={scalars.mayflyNcRatio}
                    />
                    <p className="fit-hint">
                      Offspring as a fraction of the population, used only when
                      the count is <code>-1</code>.
                    </p>
                    {fieldError("mayflyNcRatio")}
                  </div>

                  <div className="fit-field">
                    <label htmlFor={fieldId("mayflySelection")}>
                      Parent selection
                    </label>
                    <select
                      id={fieldId("mayflySelection")}
                      onChange={(event) => {
                        setScalar("mayflySelection", event.target.value);
                      }}
                      value={scalars.mayflySelection}
                    >
                      <option value="">(keep the dialect's own)</option>
                      {MAYFLY_SELECTIONS.map((name) => (
                        <option key={name} value={name}>
                          {name}
                        </option>
                      ))}
                    </select>
                  </div>
                </div>

                <section
                  aria-labelledby={fieldId("tuning-heading")}
                  className="fit-advanced-section"
                >
                  <h4 id={fieldId("tuning-heading")}>Tuning</h4>

                  <p className="fit-hint">
                    Every knob is optional: an empty field is left out of the
                    document, and a knob that is left out keeps whatever the
                    dialect or the preset already chose. An untouched editor
                    sends no document at all.
                  </p>

                  <p className="fit-hint">
                    The settings above are not repeated here. Male and female
                    population are, because they override Population and may
                    differ from each other; a knob set here wins.
                  </p>

                  {/*
                    The knobs come from the generated table rather than a list
                    written here, so a knob that moves upstream moves in the
                    form as well: `just gen-mayfly-tuning` is the only step.
                  */}
                  {tuningVariant === "" ? (
                    <p className="fit-hint">
                      Only the shared knobs are shown: the preset chooses the
                      dialect, and a dialect&apos;s own knobs cannot be chosen
                      before the dialect is.
                    </p>
                  ) : null}

                  <div className="fit-row">
                    {tuningFields.map((field) => tuningRow(field))}
                  </div>
                </section>
              </section>
            ) : null}

            {/* Removed rather than disabled when another backend is chosen,
                for the reason the mayfly block above is. */}
            {cmaes ? (
              <section
                aria-labelledby={fieldId("cmaes-heading")}
                className="fit-advanced-section"
              >
                <h3 id={fieldId("cmaes-heading")}>CMA-ES optimizer</h3>

                <div className="fit-row">
                  <div className="fit-field">
                    <label htmlFor={fieldId("cmaesCovariance")}>
                      Covariance
                    </label>
                    <select
                      id={fieldId("cmaesCovariance")}
                      onChange={(event) => {
                        setCmaesCovariance(
                          event.target.value as CmaesCovariance,
                        );
                      }}
                      value={cmaesCovariance}
                    >
                      {CMAES_COVARIANCES.map((name) => (
                        <option key={name} value={name}>
                          {name}
                        </option>
                      ))}
                    </select>
                    <p className="fit-hint">
                      Separable learns the diagonal only; block learns a dense
                      matrix per mode.
                    </p>
                  </div>

                  <div className="fit-field">
                    <label htmlFor={fieldId("cmaesLambda")}>Population</label>
                    <input
                      aria-describedby={describedBy("cmaesLambda")}
                      aria-invalid={errors.cmaesLambda !== undefined}
                      id={fieldId("cmaesLambda")}
                      inputMode="numeric"
                      max={FIT_LIMITS.cmaesLambda.max}
                      min={FIT_LIMITS.cmaesLambda.min}
                      onChange={(event) => {
                        setScalar("cmaesLambda", event.target.value);
                      }}
                      type="number"
                      value={scalars.cmaesLambda}
                    />
                    <p className="fit-hint">
                      0 takes Hansen&apos;s default, twelve at the dimensions
                      this model encodes.
                    </p>
                    {fieldError("cmaesLambda")}
                  </div>

                  <div className="fit-field">
                    <label htmlFor={fieldId("cmaesSigma")}>Step size</label>
                    <input
                      aria-describedby={describedBy("cmaesSigma")}
                      aria-invalid={errors.cmaesSigma !== undefined}
                      id={fieldId("cmaesSigma")}
                      inputMode="decimal"
                      max={FIT_LIMITS.cmaesSigma.max}
                      min={FIT_LIMITS.cmaesSigma.min}
                      onChange={(event) => {
                        setScalar("cmaesSigma", event.target.value);
                      }}
                      step="0.05"
                      type="number"
                      value={scalars.cmaesSigma}
                    />
                    <p className="fit-hint">
                      A fraction of the search box; 0 takes the default of 0.3,
                      which covers a third of it.
                    </p>
                    {fieldError("cmaesSigma")}
                  </div>
                </div>

                <div className="fit-row">
                  <div className="fit-field">
                    <label htmlFor={fieldId("cmaesSeed")}>Seed</label>
                    <input
                      aria-describedby={describedBy("cmaesSeed")}
                      aria-invalid={errors.cmaesSeed !== undefined}
                      id={fieldId("cmaesSeed")}
                      inputMode="numeric"
                      onChange={(event) => {
                        setScalar("cmaesSeed", event.target.value);
                      }}
                      type="number"
                      value={scalars.cmaesSeed}
                    />
                    <p className="fit-hint">0 picks a seed and reports it.</p>
                    {fieldError("cmaesSeed")}
                  </div>

                  <div className="fit-field">
                    <label htmlFor={fieldId("cmaesRestarts")}>Restarts</label>
                    <input
                      aria-describedby={describedBy("cmaesRestarts")}
                      aria-invalid={errors.cmaesRestarts !== undefined}
                      id={fieldId("cmaesRestarts")}
                      inputMode="numeric"
                      max={FIT_LIMITS.cmaesRestarts.max}
                      min={FIT_LIMITS.cmaesRestarts.min}
                      onChange={(event) => {
                        setScalar("cmaesRestarts", event.target.value);
                      }}
                      type="number"
                      value={scalars.cmaesRestarts}
                    />
                    <p className="fit-hint">
                      0 restarts means restart until the budget is spent.
                    </p>
                    {fieldError("cmaesRestarts")}
                  </div>
                </div>
              </section>
            ) : null}

            <section
              aria-labelledby={fieldId("bounds-heading")}
              className="fit-advanced-section"
            >
              <h3 id={fieldId("bounds-heading")}>Bounds</h3>

              <div className="fit-check">
                <input
                  checked={useBounds}
                  id={fieldId("useBounds")}
                  onChange={(event) => {
                    setUseBounds(event.target.checked);
                  }}
                  type="checkbox"
                />
                <label htmlFor={fieldId("useBounds")}>
                  Narrow the search bounds
                </label>
              </div>

              <p className="fit-hint">
                Set both ends for a dimension to narrow it. Empty dimensions
                keep their defaults.
              </p>

              {fieldError("bounds")}

              {useBounds
                ? BOUNDS_KEYS.map((key) => (
                    <div className="fit-row fit-bounds-row" key={key}>
                      <span
                        className="fit-bounds-label"
                        id={fieldId(`bounds-${key}`)}
                      >
                        {BOUNDS_LABELS[key]}
                      </span>

                      <div className="fit-field">
                        <label htmlFor={fieldId(`bounds-${key}-min`)}>
                          Minimum
                        </label>
                        <input
                          aria-describedby={describedBy(`bounds-${key}`)}
                          aria-invalid={errors[`bounds-${key}`] !== undefined}
                          // The visible label reads "Minimum" seven times over;
                          // the accessible name names its dimension.
                          aria-label={`${BOUNDS_LABELS[key]} minimum`}
                          id={fieldId(`bounds-${key}-min`)}
                          onChange={(event) => {
                            setBound(key, "min", event.target.value);
                          }}
                          placeholder={String(DEFAULT_PARAM_BOUNDS[key][0])}
                          type="number"
                          value={boundsRows[key].min}
                        />
                      </div>

                      <div className="fit-field">
                        <label htmlFor={fieldId(`bounds-${key}-max`)}>
                          Maximum
                        </label>
                        <input
                          aria-describedby={describedBy(`bounds-${key}`)}
                          aria-invalid={errors[`bounds-${key}`] !== undefined}
                          aria-label={`${BOUNDS_LABELS[key]} maximum`}
                          id={fieldId(`bounds-${key}-max`)}
                          onChange={(event) => {
                            setBound(key, "max", event.target.value);
                          }}
                          placeholder={String(DEFAULT_PARAM_BOUNDS[key][1])}
                          type="number"
                          value={boundsRows[key].max}
                        />
                      </div>

                      {fieldError(`bounds-${key}`)}
                    </div>
                  ))
                : null}
            </section>
          </div>
        </details>
      </fieldset>

      <div className="fit-job-control">
        <p aria-live="polite" className="fit-job-state">
          {jobState}
        </p>

        <div className="fit-actions">
          <button
            className="fit-button"
            disabled={busy || running}
            type="submit"
          >
            {busy && !running ? "Starting…" : "Start fit"}
          </button>

          <button
            className="fit-button fit-button-secondary"
            disabled={busy || !running}
            onClick={() => void onCancel()}
            type="button"
          >
            Cancel fit
          </button>
        </div>
      </div>

      {/*
        One live region for both halves. The form's own errors and the server's
        are announced the same way, because from where the reader sits they are
        the same event: the fit did not start, and here is why.
      */}
      <div aria-live="polite" className="fit-messages" role="status">
        {formError === null ? null : (
          <p className="fit-form-error">{formError}</p>
        )}
        {notice === null ? null : <p className="fit-notice">{notice}</p>}
      </div>
    </form>
  );
}
