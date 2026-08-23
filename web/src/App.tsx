import { useEffect, useState } from "react";

import { DEFAULT_SOUND_PRESET_ID } from "./api/presets.generated";
import { useAudioEngine } from "./audio/useAudioEngine";
import { useEngineWorker } from "./audio/useEngineWorker";
import { Topbar } from "./components/Topbar";
import { OptimizePage } from "./routes/OptimizePage";
import { PlayPage } from "./routes/PlayPage";
import { useTheme } from "./lib/theme";
import { parseRoute, type Route } from "./routes/routes";
import { useApiAvailable } from "./features/optimize/useApiAvailable";
import { useWasmFitWorker } from "./features/optimize/useWasmFitWorker";

/** The dial reads 10..100; the engine takes a linear gain of 0.1..1.0. */
function gainFromPercent(percent: number): number {
  return Math.min(1, Math.max(0.1, percent / 100));
}

/**
 * Where the Reverb dial starts.
 *
 * Not zero. The engine's own default is dry, so every Go test and every offline
 * render is unchanged by the feature existing, and the room is something the
 * web app opts into. But a control that ships at the bottom of its range is a
 * feature nobody finds, and a glockenspiel is the kind of bright, fast-decaying
 * source a room flatters rather than blurs. A fifth of the way up is audible
 * without being the point.
 */
const DEFAULT_REVERB_PERCENT = 20;

export function App() {
  const [route, setRoute] = useState<Route>(() =>
    parseRoute(window.location.hash),
  );
  const [gainPercent, setGainPercent] = useState(70);
  const [presetId, setPresetId] = useState(DEFAULT_SOUND_PRESET_ID);
  const [reverbPercent, setReverbPercent] = useState(DEFAULT_REVERB_PERCENT);
  const theme = useTheme();

  useEffect(() => {
    const onHashChange = () => {
      setRoute(parseRoute(window.location.hash));
    };

    window.addEventListener("hashchange", onHashChange);

    return () => {
      window.removeEventListener("hashchange", onHashChange);
    };
  }, []);

  // The engine lives here rather than in PlayPage: switching to Optimize
  // unmounts the play surface, and neither the Go runtime nor a ringing note
  // should die because the user looked at another tab.
  const engine = useEngineWorker();
  const audio = useAudioEngine(engine.client, gainFromPercent(gainPercent));

  // The chosen sound is pushed rather than passed to start(), for the same
  // reason the engine lives here: it has to survive a tab switch, and it is
  // choosable before the engine exists. The worker holds it until init runs.
  useEffect(() => {
    engine.client?.setPreset(presetId);
  }, [engine.client, presetId]);

  // The room is pushed the same way and for the same reason, but without the
  // preset's caveats: it costs no rebuild, so it can be sent on every step of a
  // drag, and the module holds it across a preset swap so it does not have to
  // be replayed here.
  useEffect(() => {
    engine.client?.setReverb(reverbPercent / 100);
  }, [engine.client, reverbPercent]);

  const fitApi = useApiAvailable(route === "optimize");
  const wasmFit = useWasmFitWorker(
    route === "optimize" && fitApi.availability === "unavailable",
  );

  return (
    <main className="studio-shell">
      <Topbar
        route={route}
        theme={theme.preference}
        onThemeChange={theme.setPreference}
      />

      {route === "play" ? (
        <PlayPage
          engine={engine}
          audio={audio}
          gain={gainPercent}
          onGainChange={setGainPercent}
          presetId={presetId}
          onPresetChange={setPresetId}
          reverb={reverbPercent}
          onReverbChange={setReverbPercent}
        />
      ) : (
        <OptimizePage api={fitApi} wasm={wasmFit} />
      )}
    </main>
  );
}
