import { useCallback, useEffect, useRef, useState } from "react";

import type { AudioEngine } from "../audio/useAudioEngine";
import type { EngineWorker } from "../audio/useEngineWorker";
import { ControlRail } from "../components/ControlRail";
import { Keyboard } from "../components/Keyboard";
import { PresetStrip } from "../components/PresetStrip";
import { Rack } from "../components/Rack";
import { computeKeyMap } from "../lib/layout";
import { useNoteActivation } from "../lib/useNoteActivation";
import { applyWoodTexture } from "../lib/wood";

const KEY_MAP = computeKeyMap();

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

/**
 * withDropouts appends the underrun count, and only when there is one.
 *
 * The number is the phase's own acceptance criterion -- "no dropouts under
 * load" -- made observable rather than asserted: a render quantum that found
 * the queue empty is counted in the consumer and reported here, so a transport
 * that cannot keep up says so instead of merely sounding wrong.
 */
function withDropouts(status: string, underruns: number): string {
  if (underruns === 0) {
    return status;
  }

  return `${status} - ${underruns} dropout${underruns === 1 ? "" : "s"}`;
}

export interface PlayPageProps {
  engine: EngineWorker;
  audio: AudioEngine;
  /** Master output as a percentage; the engine takes 0.1..1.0. */
  gain: number;
  onGainChange: (gain: number) => void;
}

export function PlayPage({ engine, audio, gain, onGainChange }: PlayPageProps) {
  const [velocity, setVelocity] = useState(96);
  const [species, setSpecies] = useState("beech");
  const { activeNotes, activate } = useNoteActivation();

  useEffect(() => {
    applyWoodTexture(document.documentElement, species);
  }, [species]);

  const { client } = engine;
  const { start, isReady } = audio;

  const strike = useCallback(
    (note: number) => {
      if (!client) {
        return;
      }

      const play = () => {
        activate(note);
        client.noteOn(note, clamp(velocity, 1, 127));
      };

      // The graph only exists after a user gesture, and the first strike is
      // that gesture. Once it is running the note is struck synchronously, so
      // the audible latency of the first note is not paid by every one after.
      if (!isReady()) {
        void start().then(play, () => {
          // useAudioEngine has already put the reason in the status panel.
        });

        return;
      }

      play();
    },
    [client, activate, start, isReady, velocity],
  );

  // The document-level key listener is installed once; it reaches the current
  // strike through a ref so that a velocity change does not tear the listener
  // down and build it again.
  const strikeRef = useRef(strike);

  useEffect(() => {
    strikeRef.current = strike;
  }, [strike]);

  // The computer keyboard. `repeat` is filtered so that holding a key does not
  // machine-gun the note, and the pressed set guards the case where the browser
  // reports a repeat as a fresh keydown.
  useEffect(() => {
    const pressed = new Set<string>();

    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.repeat) {
        return;
      }

      const key = event.key.toUpperCase();
      const note = KEY_MAP.get(key);
      if (note === undefined || pressed.has(key)) {
        return;
      }

      pressed.add(key);
      strikeRef.current(note);
    };

    const onKeyUp = (event: globalThis.KeyboardEvent) => {
      pressed.delete(event.key.toUpperCase());
    };

    document.addEventListener("keydown", onKeyDown);
    document.addEventListener("keyup", onKeyUp);

    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.removeEventListener("keyup", onKeyUp);
    };
  }, []);

  // The audio engine has the more recent news once it has anything to say:
  // "Ready at 44100 Hz" supersedes "WASM loaded. Strike a bar to start audio."
  const status = audio.status
    ? withDropouts(audio.status, audio.underruns)
    : engine.status;
  const statusIsError = audio.status ? audio.error : engine.error;

  return (
    <>
      <PresetStrip species={species} onSpeciesChange={setSpecies} />

      <section className="instrument-card">
        <div className="instrument-main">
          <ControlRail
            gain={gain}
            onGainChange={onGainChange}
            velocity={velocity}
            onVelocityChange={setVelocity}
            status={status}
            statusIsError={statusIsError}
          />

          <Rack onStrike={strike} activeNotes={activeNotes} />
        </div>

        <Keyboard onStrike={strike} activeNotes={activeNotes} />
      </section>
    </>
  );
}
