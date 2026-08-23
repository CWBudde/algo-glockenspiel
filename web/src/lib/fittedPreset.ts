import type { SoundPreset } from "../api/presets.generated";

/**
 * A preset the Optimize tab produced, described the way the Play tab's picker
 * describes a built-in one.
 *
 * It reuses SoundPreset deliberately: a fitted sound is chosen by the same
 * control, applied by the same setPreset, and rebuilt into the same engine, so
 * a second shape would only be a second thing to keep in step.
 */
export interface FittedPreset extends SoundPreset {
  /** The document itself, which the engine is given once at registration. */
  readonly document: string;
}

/** The note a preset is authored at when its document does not say. */
const FALLBACK_NOTE = 69;

/**
 * readFittedPreset turns a fitted preset document into a pickable sound.
 *
 * The parse here is shallow on purpose. preset.Decode in Go is the authority on
 * whether a document is a preset -- it validates every parameter and upgrades
 * the schema -- and it runs on the other side of the bridge, where its error
 * comes back as a string. This one only answers the two questions the picker
 * has to answer before the engine is involved at all: is this JSON, and what
 * should the entry be called.
 *
 * `sequence` numbers the registrations in a session. It is what keeps two fits
 * apart when the job id does not: the server reuses its single slot, so a
 * reload or a second run can hand back a job id that has been seen before.
 */
export function readFittedPreset(
  document: string,
  jobId: string | null,
  sequence: number,
): FittedPreset {
  let parsed: unknown;

  try {
    parsed = JSON.parse(document);
  } catch {
    throw new Error("The fitted preset is not valid JSON.");
  }

  if (parsed === null || typeof parsed !== "object") {
    throw new Error("The fitted preset is not a preset document.");
  }

  const fields = parsed as { name?: unknown; note?: unknown };

  if (!("parameters" in parsed)) {
    throw new Error("The fitted preset has no parameters.");
  }

  const name =
    typeof fields.name === "string" && fields.name.trim() !== ""
      ? fields.name.trim()
      : "Fitted preset";

  const note =
    typeof fields.note === "number" &&
    Number.isFinite(fields.note) &&
    fields.note >= 0 &&
    fields.note <= 127
      ? fields.note
      : FALLBACK_NOTE;

  // The id never collides with a built-in one, which the module refuses
  // outright: an id that shadowed an embedded preset would leave the picker
  // offering a built-in sound that plays something else.
  return {
    id: `fitted-${String(sequence)}`,
    label: `${name} · ${jobId ?? `fit ${String(sequence)}`}`,
    note,
    document,
  };
}
