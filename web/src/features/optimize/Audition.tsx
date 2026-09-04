import { useEffect, useRef, useState } from "react";

import { getFitPreset } from "../../api/fit";
import type { FitSnapshot } from "../../api/types";

/** The server's render cap, `maxRenderSeconds` in internal/server/fit.go. */
const maxRenderSeconds = 60;

export interface AuditionProps {
  snapshot: FitSnapshot | null;
  /**
   * The job the server (or the browser worker) currently considers active.
   * The audio and preset reads below are hardcoded to the current-job
   * endpoints -- `api/fit/audio`, `getFitPreset()` -- which always answer
   * about this job, never about `snapshot.jobId` if that names something
   * else. See `auditionAppliesToActiveJob`.
   */
  activeJobId: string | null;
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
 * Whether the audition and download controls may act on the displayed
 * snapshot.
 *
 * `api/fit/audio` and `getFitPreset()` (`api/fit/preset`) both always answer
 * about whichever job the server currently considers active; there is no
 * per-job URL wired in here yet. A snapshot picked from the run list can name
 * a different, historical job, and offering the controls for it would play
 * or download the active run's audio under the picked run's label -- wrong
 * and silent about being wrong. `jobId === null` is refused too: it means
 * nothing is displayed, which is never the active job either.
 */
export function auditionAppliesToActiveJob(
  jobId: string | null,
  activeJobId: string | null,
): boolean {
  return jobId !== null && jobId === activeJobId;
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
  activeJobId,
  artifacts,
  onUseInPlay,
}: AuditionProps) {
  const hasPreset = snapshot?.hasPreset ?? false;

  const jobId = snapshot?.jobId ?? null;
  const referenceSeconds = snapshot?.referenceSeconds ?? 0;
  const activeJobApplies = auditionAppliesToActiveJob(jobId, activeJobId);

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

  const durationText =
    chosen !== null && chosen.jobId === jobId
      ? chosen.text
      : String(defaultDuration(referenceSeconds));

  // An empty field is Number("") === 0, which the bounds check below rejects
  // just as the server would.
  const duration = durationText.trim() === "" ? 0 : Number(durationText);

  const audioUrl =
    rendered !== null && rendered.jobId === jobId ? rendered.url : null;

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

  if (!activeJobApplies) {
    // The audio and preset reads below only ever answer about the active
    // job, so they are withheld rather than pointed at the wrong run's
    // sound: a picked historical run shows its own numbers and cost curve
    // above, but audition and download stay tied to whatever is running now.
    return (
      <section className="optimize-audition" aria-labelledby="audition-heading">
        <h3 id="audition-heading">Audition</h3>
        <p className="optimize-note">
          Audition and download follow the active run, not the one selected
          above. Select the active run in the list, or start a new fit, to
          hear or download it.
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

    const query = new URLSearchParams({
      note: String(snapshot.note),
      velocity: String(snapshot.velocity),
      duration: String(duration),
      // The render depends on the job, and the job is not in the URL. The
      // response is `no-store`, but a browser that reuses an <audio> element's
      // src without refetching would still play the previous fit, so the URL
      // is busted per request.
      t: String(Date.now()),
    });

    setRendered({
      jobId,
      url: `api/fit/audio?${query.toString()}`,
      owned: false,
    });
  };

  /**
   * fittedDocument reads the preset back as text, from whichever backend
   * produced it.
   *
   * Both paths already exist: the browser worker hands over the encoded bytes
   * the download uses, and the service answers api/fit/preset with the document
   * as JSON. Neither is a new endpoint, and the preset is deliberately not kept
   * in page state -- it is fetched when it is wanted, so a long fit does not
   * carry a document nobody asked for through every snapshot.
   *
   * The service's answer is re-encoded rather than fetched as text, because
   * getFitPreset is the typed reader this front end already has for it. What
   * the engine needs is a preset document, and a decoded-then-encoded one is
   * the same document: every field survives, and Go validates it either way.
   */
  const fittedDocument = async (): Promise<string> => {
    if (artifacts === undefined) {
      return JSON.stringify(await getFitPreset());
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
          <a className="audition-download" href="api/fit/preset">
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

      {audioUrl !== null && (
        // No caption track: a rendered instrument note carries no speech.
        <audio
          ref={playerRef}
          className="audition-player"
          controls
          src={audioUrl}
        >
          Your browser cannot play the rendered preset.
        </audio>
      )}
    </section>
  );
}
