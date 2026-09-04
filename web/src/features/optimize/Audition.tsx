import { useEffect, useRef, useState } from "react";

import {
  fitJobAudioUrl,
  fitJobPresetUrl,
  fitJobReferenceUrl,
  getFitJobPreset,
} from "../../api/fit";
import type { FitSnapshot } from "../../api/types";

/** The server's render cap, `maxRenderSeconds` in internal/server/fit.go. */
const maxRenderSeconds = 60;

export interface AuditionProps {
  snapshot: FitSnapshot | null;
  artifacts?: FitArtifacts | undefined;
  /**
   * Makes the fitted preset choosable in the Play tab and returns the name it
   * was listed under. Absent leaves the button out, which is what a harness
   * that mounts this component on its own gets.
   */
  onUseInPlay?:
    | ((document: string, jobId: string | null) => string)
    | undefined;
}

export interface FitArtifacts {
  preset(): Promise<Blob>;
  render(note: number, velocity: number, duration: number): Promise<Blob>;
}

/**
 * The default render length the server would pick: min(reference, the cap).
 *
 * Rounded up to a hundredth of a second, because the reference's length is a
 * sample count divided by a rate and reads as 0.5573696145124717 in a number
 * field. Rounding up rather than to nearest keeps a very short reference from
 * defaulting to zero, which the server refuses.
 */
function defaultDuration(referenceSeconds: number): number {
  if (!Number.isFinite(referenceSeconds) || referenceSeconds <= 0) {
    return maxRenderSeconds;
  }

  const rounded = Math.ceil(referenceSeconds * 100) / 100;

  return Math.min(rounded, maxRenderSeconds);
}

/**
 * Play the fitted preset, and download it.
 *
 * The gate is `hasPreset`, not `state === "succeeded"`. The Go comment on that
 * field says why: a run cancelled after its first report still leaves the best
 * parameters found so far, and those are exactly what someone who cancelled a
 * long mayfly run wants to hear. Gating on the state instead would hide a
 * perfectly good preset behind a "canceled" label.
 */
export function Audition({
  snapshot,
  artifacts,
  onUseInPlay,
}: AuditionProps) {
  const hasPreset = snapshot?.hasPreset ?? false;

  const jobId = snapshot?.jobId ?? null;
  const referenceSeconds = snapshot?.referenceSeconds ?? 0;

  // Both pieces of state are stamped with the job they belong to and read only
  // when the stamp still matches. A new job therefore falls back to the new
  // reference's default length and to no rendered audio without an effect
  // resetting anything -- a reset in an effect would be a cascading render, and
  // a render of a stale player would briefly offer the previous fit's audio.
  //
  // The typed length is kept as the text the field holds, not as a number: an
  // emptied or half-typed field has no number in it, and feeding the resulting
  // NaN back into a controlled `value` renders "NaN" into the box.
  const [chosen, setChosen] = useState<{
    jobId: string | null;
    text: string;
  } | null>(null);
  const [rendered, setRendered] = useState<{
    jobId: string | null;
    url: string;
    owned: boolean;
  } | null>(null);
  const [artifactError, setArtifactError] = useState<{
    jobId: string | null;
    message: string;
  } | null>(null);
  const [rendering, setRendering] = useState(false);
  // Stamped with the job for the same reason the error is: a new fit's results
  // must not carry the previous one's confirmation.
  const [added, setAdded] = useState<{
    jobId: string | null;
    label: string;
  } | null>(null);

  // Which of the two sources the player is on, stamped with the job for the
  // same reason as the rest: a new fit starts back on its own render rather
  // than inheriting the previous fit's choice of "reference". Absent (or
  // stale) reads as "fit", which is what a fresh render should play.
  const [abSource, setAbSource] = useState<{
    jobId: string | null;
    kind: "fit" | "reference";
  } | null>(null);
  // Captured just before a source switch, and consumed once the newly
  // sourced element reports its metadata, so the A/B toggle moves the
  // listening point across rather than restarting it. A src swap on one
  // <audio> element was chosen over two synchronized elements: only one
  // decoder is ever running, and there is no second clock to keep in step
  // with the first while the user is mid-comparison.
  const resumeRef = useRef<{ time: number; playing: boolean } | null>(null);

  const durationText =
    chosen !== null && chosen.jobId === jobId
      ? chosen.text
      : String(defaultDuration(referenceSeconds));

  // An empty field is Number("") === 0, which the bounds check below rejects
  // just as the server would.
  const duration = durationText.trim() === "" ? 0 : Number(durationText);

  const audioUrl =
    rendered !== null && rendered.jobId === jobId ? rendered.url : null;

  // The reference has no counterpart in the browser worker: `artifacts` is
  // the WASM path's contract and it has no per-job endpoints at all, only the
  // encoded bytes it already holds in memory. The A/B toggle is therefore
  // absent there rather than pointed at a URL that does not exist.
  const referenceUrl =
    artifacts === undefined && jobId !== null
      ? fitJobReferenceUrl(jobId)
      : null;

  const sourceKind =
    abSource !== null && abSource.jobId === jobId ? abSource.kind : "fit";
  const activeAudioUrl =
    sourceKind === "reference" ? referenceUrl : audioUrl;

  const playerRef = useRef<HTMLAudioElement | null>(null);

  useEffect(() => {
    const player = playerRef.current;

    if (player === null || audioUrl === null) {
      return;
    }

    // The button says "render and play", so the render is started rather than
    // merely offered. A browser that refuses the autoplay leaves the controls
    // sitting there ready, which is all the fallback this needs -- hence the
    // rejection is swallowed rather than shown.
    void player.play().catch(() => undefined);
  }, [audioUrl]);

  useEffect(
    () => () => {
      if (rendered?.owned === true) {
        URL.revokeObjectURL(rendered.url);
      }
    },
    [rendered],
  );

  if (snapshot === null || !hasPreset) {
    return (
      <section className="optimize-audition" aria-labelledby="audition-heading">
        <h3 id="audition-heading">Audition</h3>
        <p className="optimize-note">
          There is nothing to play yet. A preset becomes available as soon as
          the fit has reported once, even if it is later cancelled.
        </p>
      </section>
    );
  }

  const play = async () => {
    setArtifactError(null);

    if (artifacts !== undefined) {
      setRendering(true);

      try {
        const audio = await artifacts.render(
          snapshot.note,
          snapshot.velocity,
          duration,
        );
        setRendered({
          jobId,
          url: URL.createObjectURL(audio),
          owned: true,
        });
      } catch (cause) {
        setArtifactError({
          jobId,
          message:
            cause instanceof Error
              ? cause.message
              : "The fitted note could not be rendered.",
        });
      } finally {
        setRendering(false);
      }

      return;
    }

    // A render started while the A/B toggle sits on "reference" is still the
    // fit's own sound, not the recording's, so the toggle is brought back to
    // "fit" rather than leaving the click silently do nothing to what is
    // heard.
    setAbSource({ jobId: snapshot.jobId, kind: "fit" });

    const base = fitJobAudioUrl(snapshot.jobId, duration);
    // The response is `no-store`, but a browser that reuses an <audio>
    // element's src without refetching would still play the previous fit, so
    // the URL is busted per request too.
    const bust = `t=${String(Date.now())}`;

    setRendered({
      jobId,
      url: `${base}${base.includes("?") ? "&" : "?"}${bust}`,
      owned: false,
    });
  };

  /**
   * fittedDocument reads the preset back as text, from whichever backend
   * produced it.
   *
   * Both paths already exist: the browser worker hands over the encoded bytes
   * the download uses, and the service answers the job's own preset endpoint
   * with the document as JSON. Neither is a new endpoint, and the preset is
   * deliberately not kept in page state -- it is fetched when it is wanted,
   * so a long fit does not carry a document nobody asked for through every
   * snapshot.
   *
   * The service's answer is re-encoded rather than fetched as text, because
   * getFitJobPreset is the typed reader this front end already has for it.
   * What the engine needs is a preset document, and a decoded-then-encoded
   * one is the same document: every field survives, and Go validates it
   * either way.
   */
  const fittedDocument = async (): Promise<string> => {
    if (artifacts === undefined) {
      return JSON.stringify(await getFitJobPreset(snapshot.jobId));
    }

    return (await artifacts.preset()).text();
  };

  const sendToPlayTab = async () => {
    if (onUseInPlay === undefined) {
      return;
    }

    setArtifactError(null);

    try {
      const label = onUseInPlay(await fittedDocument(), jobId);
      setAdded({ jobId, label });
    } catch (cause) {
      setAdded(null);
      setArtifactError({
        jobId,
        message:
          cause instanceof Error
            ? cause.message
            : "The fitted preset could not be added to the Play tab.",
      });
    }
  };

  const downloadPreset = async () => {
    if (artifacts === undefined) {
      return;
    }

    setArtifactError(null);

    try {
      const data = await artifacts.preset();
      const url = URL.createObjectURL(data);
      const link = document.createElement("a");
      link.href = url;
      link.download = `${jobId ?? "glockenspiel-fit"}.json`;
      link.click();
      window.setTimeout(() => {
        URL.revokeObjectURL(url);
      }, 0);
    } catch (cause) {
      setArtifactError({
        jobId,
        message:
          cause instanceof Error
            ? cause.message
            : "The fitted preset could not be downloaded.",
      });
    }
  };

  /**
   * Switches the player between the fitted render and the reference,
   * carrying the listening point across.
   *
   * `currentTime` is captured before the state update rather than after,
   * because the state update swaps the element's `src` and a browser resets
   * `currentTime` to 0 as soon as it does; there is nothing left to read by
   * the time this component re-renders. `onLoadedMetadata` on the element
   * below applies what was captured here once the new source has loaded far
   * enough to accept a seek.
   *
   * A capture already waiting to be applied is never overwritten. Switching
   * again before the previous source finished loading would otherwise read a
   * `currentTime` the browser has already reset to zero, and the listening
   * point would be lost by the very act of switching quickly.
   */
  const selectSource = (kind: "fit" | "reference") => {
    if (kind === sourceKind) {
      return;
    }

    const player = playerRef.current;

    if (player !== null && resumeRef.current === null) {
      resumeRef.current = { time: player.currentTime, playing: !player.paused };
    }

    setAbSource({ jobId, kind });
  };

  const durationInvalid = !(
    Number.isFinite(duration) &&
    duration > 0 &&
    duration <= maxRenderSeconds
  );

  return (
    <section className="optimize-audition" aria-labelledby="audition-heading">
      <h3 id="audition-heading">Audition</h3>

      <p className="optimize-note">
        Rendered at note {snapshot.note}, velocity {snapshot.velocity}, the same
        pair the fit was run against.
      </p>

      <div className="audition-controls">
        <label className="audition-field" htmlFor="audition-duration">
          Duration (s)
          <input
            id="audition-duration"
            type="number"
            min={0.1}
            max={maxRenderSeconds}
            step={0.1}
            value={durationText}
            aria-describedby="audition-duration-hint"
            onChange={(event) => {
              setChosen({ jobId, text: event.target.value });
            }}
          />
        </label>

        <button
          type="button"
          onClick={() => void play()}
          disabled={durationInvalid || rendering}
        >
          {rendering ? "Rendering…" : "Render and play"}
        </button>

        {artifacts === undefined ? (
          /* The server supplies Content-Disposition with the job's file name. */
          <a
            className="audition-download"
            href={fitJobPresetUrl(snapshot.jobId)}
          >
            Download preset JSON
          </a>
        ) : (
          <button
            className="audition-download"
            type="button"
            onClick={() => void downloadPreset()}
          >
            Download preset JSON
          </button>
        )}

        {onUseInPlay !== undefined && (
          <button type="button" onClick={() => void sendToPlayTab()}>
            Use in Play tab
          </button>
        )}
      </div>

      {added?.jobId === jobId && (
        /*
          Polite rather than assertive, and a status rather than an alert: the
          sound has been added, not switched to, so nothing the user is doing
          has changed underneath them.
        */
        <p className="optimize-note" role="status">
          Added to the Play tab as “{added.label}”. It stays until the page is
          reloaded.
        </p>
      )}

      <p id="audition-duration-hint" className="optimize-note">
        Longer than 0 and at most {maxRenderSeconds} seconds; the server refuses
        anything else.
      </p>

      {artifactError?.jobId === jobId && (
        <p className="fit-field-error" role="alert">
          {artifactError.message}
        </p>
      )}

      {referenceUrl !== null && audioUrl !== null && (
        <fieldset className="audition-ab">
          <legend>Playing</legend>

          <div className="audition-ab-option">
            <input
              type="radio"
              id="audition-ab-fit"
              name="audition-ab"
              checked={sourceKind === "fit"}
              onChange={() => {
                selectSource("fit");
              }}
            />
            <label htmlFor="audition-ab-fit">Fitted render</label>
          </div>

          <div className="audition-ab-option">
            <input
              type="radio"
              id="audition-ab-reference"
              name="audition-ab"
              checked={sourceKind === "reference"}
              onChange={() => {
                selectSource("reference");
              }}
            />
            <label htmlFor="audition-ab-reference">Reference</label>
          </div>
        </fieldset>
      )}

      {activeAudioUrl !== null && (
        // No caption track: a rendered instrument note, or the recording it
        // was fitted against, carries no speech.
        <audio
          ref={playerRef}
          className="audition-player"
          controls
          src={activeAudioUrl}
          onLoadedMetadata={() => {
            const pending = resumeRef.current;
            const player = playerRef.current;

            if (pending === null || player === null) {
              return;
            }

            resumeRef.current = null;
            player.currentTime = pending.time;

            if (pending.playing) {
              void player.play().catch(() => undefined);
            }
          }}
        >
          {sourceKind === "reference"
            ? "Your browser cannot play the reference recording."
            : "Your browser cannot play the rendered preset."}
        </audio>
      )}
    </section>
  );
}
