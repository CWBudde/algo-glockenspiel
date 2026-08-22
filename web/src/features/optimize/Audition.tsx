import { useEffect, useRef, useState } from "react";

import type { FitSnapshot } from "../../api/types";

/** The server's render cap, `maxRenderSeconds` in internal/server/fit.go. */
const maxRenderSeconds = 60;

export interface AuditionProps {
  snapshot: FitSnapshot | null;
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
export function Audition({ snapshot }: AuditionProps) {
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

  const play = () => {
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

    setRendered({ jobId, url: `api/fit/audio?${query.toString()}` });
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

        <button type="button" onClick={play} disabled={durationInvalid}>
          Render and play
        </button>

        {/*
          The server sends Content-Disposition: attachment with the job's own
          file name, so an ordinary link is the whole of the download.
        */}
        <a className="audition-download" href="api/fit/preset">
          Download preset JSON
        </a>
      </div>

      <p id="audition-duration-hint" className="optimize-note">
        Longer than 0 and at most {maxRenderSeconds} seconds; the server refuses
        anything else.
      </p>

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
