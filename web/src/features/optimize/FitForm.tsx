import { useId, useMemo, useRef, useState } from "react";

import { cancelFit, FitApiError, startFit } from "../../api/fit";
import {
  BOUNDS_KEYS,
  DEFAULT_FIT_REQUEST,
  DEFAULT_PARAM_BOUNDS,
  FIT_LIMITS,
  LOG_ENCODED_BOUNDS_KEYS,
  MAYFLY_VARIANTS,
  METRIC_NAMES,
  MODEL_BOUNDS_LIMITS,
  OPTIMIZER_NAMES,
  type BoundsDocument,
  type BoundsKey,
  type BoundsRange,
  type FitSnapshot,
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
}

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
}

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
};

const EMPTY_BOUNDS_ROWS: BoundsRows = Object.fromEntries(
  BOUNDS_KEYS.map((key) => [key, { min: "", max: "" }]),
) as BoundsRows;

/** Human labels for the bounds keys, which are wire names. */
const BOUNDS_LABELS: Record<BoundsKey, string> = {
  input_mix: "Input mix",
  filter_freq: "Filter frequency (Hz)",
  base_frequency: "Base frequency (Hz)",
  amplitude: "Mode amplitude",
  frequency_mult: "Frequency multiplier",
  decay_ms: "Decay (ms)",
  harmonic_gain: "Harmonic gain",
};

const METRIC_LABELS: Record<MetricName, string> = {
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

/**
 * Parses a duration the way formDuration does: a Go duration string, or a bare
 * number read as seconds. The result is returned unchanged for the wire and in
 * seconds for the range check.
 */
function parseDuration(raw: string): { seconds: number } | { error: string } {
  const trimmed = raw.trim();

  if (trimmed === "") {
    return { error: "The time budget is required." };
  }

  // strconv.ParseFloat's decimal grammar, exponent included, so "1e3" is read
  // as 1000 seconds here exactly as the server reads it. Number() alone is more
  // permissive than ParseFloat -- it also takes "0x10" and "Infinity" -- so the
  // pattern, not Number(), decides what counts as a bare number.
  const bareSeconds = /^[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?$/;
  const bare = Number(trimmed);

  if (bareSeconds.test(trimmed) && Number.isFinite(bare)) {
    return { seconds: bare };
  }

  // time.ParseDuration's grammar, restricted to the units a person types here.
  const pattern = /^[+-]?(\d+(\.\d*)?(ns|us|µs|ms|s|m|h))+$/;

  if (!pattern.test(trimmed)) {
    return {
      error: "The time budget must be a duration such as 30s, 2m or 1h.",
    };
  }

  const unitSeconds: Record<string, number> = {
    ns: 1e-9,
    us: 1e-6,
    µs: 1e-6,
    ms: 1e-3,
    s: 1,
    m: 60,
    h: 3600,
  };

  let total = 0;

  for (const part of trimmed.matchAll(/(\d+(?:\.\d*)?)(ns|us|µs|ms|s|m|h)/g)) {
    total += Number(part[1]) * unitSeconds[part[2]];
  }

  return { seconds: trimmed.startsWith("-") ? -total : total };
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

export function FitForm({ snapshot, onSnapshot }: FitFormProps) {
  const ids = useId();
  const fieldId = (name: string) => `${ids}-${name}`;

  const referenceRef = useRef<HTMLInputElement>(null);
  const presetRef = useRef<HTMLInputElement>(null);

  const [scalars, setScalars] = useState<ScalarFields>(INITIAL_SCALARS);
  const [metric, setMetric] = useState<MetricName>(DEFAULT_FIT_REQUEST.metric);
  const [optimizer, setOptimizer] = useState<OptimizerName>(
    DEFAULT_FIT_REQUEST.optimizer,
  );
  const [mayflyVariant, setMayflyVariant] = useState<MayflyVariant>(
    DEFAULT_FIT_REQUEST.mayflyVariant,
  );
  const [align, setAlign] = useState(DEFAULT_FIT_REQUEST.align);
  const [normalizeGain, setNormalizeGain] = useState(
    DEFAULT_FIT_REQUEST.normalizeGain,
  );

  const [useBounds, setUseBounds] = useState(false);
  const [boundsRows, setBoundsRows] = useState<BoundsRows>(EMPTY_BOUNDS_ROWS);

  const [errors, setErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const running = snapshot?.state === "running";
  const mayfly = optimizer === "mayfly";

  const setScalar = (name: keyof ScalarFields, value: string) => {
    setScalars((previous) => ({ ...previous, [name]: value }));
  };

  const setBound = (key: BoundsKey, end: keyof BoundsRow, value: string) => {
    setBoundsRows((previous) => ({
      ...previous,
      [key]: { ...previous[key], [end]: value },
    }));
  };

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
      return { errors: found };
    }

    const form = new FormData();

    // reference is non-null here: an absent one is a field error above.
    form.append("reference", reference as File);

    const preset = presetRef.current?.files?.[0] ?? null;

    if (preset !== null) {
      form.append("preset", preset);
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
      form.append("mayflyVariant", mayflyVariant);
      form.append("mayflyPopulation", String(population));
      form.append("mayflySeed", String(seed));
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
      const started = await startFit(built.form);

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
      } else {
        setFormError("The fit could not be started.");
      }
    } finally {
      setBusy(false);
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
      const stopped = await cancelFit(snapshot?.jobId);

      onSnapshot(stopped);
      setNotice(`Fit ${stopped.jobId} is ${stopped.state}.`);
    } catch (cause) {
      setFormError(
        cause instanceof FitApiError
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
        <legend>Reference</legend>

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
            Mono, or the first channel of a multi-channel file. Up to{" "}
            {referenceLimitLabel}.
          </p>
          {fieldError("reference")}
        </div>

        <div className="fit-field">
          <label htmlFor={fieldId("preset")}>Starting preset (optional)</label>
          <input
            accept=".json,application/json"
            id={fieldId("preset")}
            name="preset"
            ref={presetRef}
            type="file"
          />
          <p className="fit-hint">
            The built-in preset is the starting point when none is chosen.
          </p>
        </div>
      </fieldset>

      <fieldset className="fit-group">
        <legend>The note being fitted</legend>

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
        <legend>Objective</legend>

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
            <label htmlFor={fieldId("normalizeGain")}>
              Normalize gain before scoring
            </label>
          </div>
        </div>
      </fieldset>

      <fieldset className="fit-group">
        <legend>Search</legend>

        <div className="fit-row">
          <div className="fit-field">
            <label htmlFor={fieldId("optimizer")}>Optimizer</label>
            <select
              id={fieldId("optimizer")}
              onChange={(event) => {
                setOptimizer(event.target.value as OptimizerName);
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
        </div>

        <div className="fit-row">
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
              A duration such as <code>30s</code>, <code>2m</code> or{" "}
              <code>1h</code>; a bare number is read as seconds. At most one
              hour.
            </p>
            {fieldError("timeBudget")}
          </div>

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
              Optimizer iterations between progress reports.
            </p>
            {fieldError("reportEvery")}
          </div>
        </div>

        {/*
          The mayfly fields are removed rather than disabled when the simple
          optimizer is chosen: the server does not read them at all in that
          case, and a greyed-out control that does nothing is a control that
          lies.
        */}
        {mayfly ? (
          <div className="fit-row">
            <div className="fit-field">
              <label htmlFor={fieldId("mayflyVariant")}>Mayfly variant</label>
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

            <div className="fit-field">
              <label htmlFor={fieldId("mayflyPopulation")}>Population</label>
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
                // A text field, not a number one: the seed is an exact int64
                // decimal string, and a number input is free to hand back a
                // normalised value -- "1e+19" for a seed near the top of the
                // range -- which BigInt() then refuses. inputMode still brings
                // up the numeric keypad.
                type="text"
                value={scalars.mayflySeed}
              />
              {fieldError("mayflySeed")}
            </div>
          </div>
        ) : null}
      </fieldset>

      <fieldset className="fit-group">
        <legend>Bounds</legend>

        <div className="fit-check">
          <input
            checked={useBounds}
            id={fieldId("useBounds")}
            onChange={(event) => {
              setUseBounds(event.target.checked);
            }}
            type="checkbox"
          />
          <label htmlFor={fieldId("useBounds")}>Narrow the search bounds</label>
        </div>

        <p className="fit-hint">
          Every dimension is optional; one left empty keeps its default.
          Supplied bounds are a hard constraint, so the box is not widened to
          contain the starting preset. A server built before the bounds field
          was added to the fit API ignores the document rather than refusing it,
          so on such a server the fit runs against the default box.
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
                  <label htmlFor={fieldId(`bounds-${key}-min`)}>Minimum</label>
                  <input
                    aria-describedby={describedBy(`bounds-${key}`)}
                    aria-invalid={errors[`bounds-${key}`] !== undefined}
                    // The visible label reads "Minimum" seven times over; the
                    // accessible name names the dimension it belongs to.
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
                  <label htmlFor={fieldId(`bounds-${key}-max`)}>Maximum</label>
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
      </fieldset>

      <div className="fit-actions">
        <button className="fit-button" disabled={busy || running} type="submit">
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
