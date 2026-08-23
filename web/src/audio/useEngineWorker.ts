import { useEffect, useRef, useState } from "react";

import type { EngineCommand, EngineEvent } from "./protocol";

/**
 * The page's handle on the engine. Every call is a message to the worker that
 * owns the Go module, so nothing here is synchronous with the sound -- noteOn
 * costs one message hop plus whatever is already queued, and that is the price
 * of synthesis not sharing a thread with React.
 */
export interface EngineClient {
  noteOn(note: number, velocity: number): void;
  setMasterGain(gain: number): void;
  /**
   * Chooses which built-in sound the engine plays. Safe to call before the
   * first strike: the worker holds the id until there is an engine to give it
   * to. Notes ringing at the moment of a live change stop, because the engine
   * is rebuilt around the new bar rather than retuned underneath the old one.
   */
  setPreset(presetId: string): void;
  /**
   * Makes a preset document playable under `presetId`, without switching to
   * it. Safe before the first strike: the worker holds the registration until
   * there is a module to give it to, and applies it before the engine is
   * built.
   */
  registerPreset(presetId: string, document: string): void;
  /** Sets how much of the output goes through the engine's room, 0..1. */
  setReverb(mix: number): void;
  /**
   * Prepares the engine for a sample rate and starts it rendering into `port`,
   * which is transferred to the worker. Resolves once the engine reports that
   * it is running, rejects with what went wrong instead.
   */
  start(sampleRate: number, port: MessagePort): Promise<void>;
  /** Stops rendering and drops the channel, so a later start begins cleanly. */
  stop(): void;
}

export interface EngineWorker {
  /** The client once the module has loaded, otherwise null. */
  client: EngineClient | null;
  /** The last thing worth telling the user, loaded or not. */
  status: string;
  error: boolean;
}

/**
 * useEngineWorker starts the worker that owns the Go module, once for the
 * lifetime of the page, and reports how far it got.
 *
 * It deliberately does not terminate the worker on unmount: the Go runtime
 * cannot be restarted cheaply -- it is a 3 MB module and a preset load -- so a
 * teardown would only cost the page its engine with no way to get it back. It
 * is called from App, which outlives every tab switch.
 */
export function useEngineWorker(): EngineWorker {
  const [client, setClient] = useState<EngineClient | null>(null);
  const [status, setStatus] = useState("Loading WebAssembly...");
  const [error, setError] = useState(false);
  const startedRef = useRef(false);

  useEffect(() => {
    // React 19 runs effects twice in development StrictMode. Two workers would
    // mean two Go runtimes, two engines and two producers racing for one
    // output, so the start is guarded rather than cleaned up. For the same
    // reason there is no cancellation flag: the load has to publish its result
    // whatever happens to the effect that started it, and this hook lives in
    // App, which is mounted for the lifetime of the page anyway.
    if (startedRef.current) {
      return;
    }

    startedRef.current = true;

    const worker = new Worker(new URL("./engine.worker.ts", import.meta.url), {
      type: "module",
    });

    let pendingStart: {
      resolve: () => void;
      reject: (error: Error) => void;
    } | null = null;

    /**
     * failStart hands a failure to whoever is waiting on start(), if anyone is.
     *
     * Nothing else can: start() resolves on a message from the worker, so a
     * worker that dies without sending one leaves the promise pending forever
     * -- and useAudioEngine keeps that promise in startPromiseRef and hands it
     * to every later strike, so a single failed start would cost the page its
     * audio until a reload.
     */
    const failStart = (failure: Error): boolean => {
      if (!pendingStart) {
        return false;
      }

      pendingStart.reject(failure);
      pendingStart = null;

      return true;
    };

    const send = (command: EngineCommand, transfer: Transferable[] = []) => {
      worker.postMessage(command, transfer);
    };

    const engineClient: EngineClient = {
      noteOn(note, velocity) {
        send({ type: "noteOn", note, velocity });
      },
      setMasterGain(gain) {
        send({ type: "setMasterGain", gain });
      },
      setPreset(presetId) {
        send({ type: "setPreset", presetId });
      },
      registerPreset(presetId, document) {
        send({ type: "registerPreset", presetId, document });
      },
      setReverb(mix) {
        send({ type: "setReverb", mix });
      },
      start(sampleRate, port) {
        return new Promise<void>((resolve, reject) => {
          pendingStart = { resolve, reject };
          send({ type: "start", sampleRate, port }, [port]);
        });
      },
      stop() {
        send({ type: "stop" });
      },
    };

    worker.onmessage = (event: MessageEvent<EngineEvent>) => {
      const message = event.data;

      switch (message.type) {
        case "loaded":
          setStatus("WASM loaded. Strike a bar to start audio.");
          setError(false);
          setClient(engineClient);
          break;

        case "started":
          pendingStart?.resolve();
          pendingStart = null;
          break;

        case "error": {
          // An error while a start is in flight belongs to that start, which
          // reports it through the audio status; anything else is the module
          // itself and has nowhere else to be seen.
          const failure = new Error(message.message);
          if (failStart(failure)) {
            return;
          }

          console.error("Engine worker", failure);
          setStatus(message.message);
          setError(true);
          break;
        }
      }
    };

    worker.onerror = (event) => {
      // Everything that escapes the worker lands here: a module that fails to
      // load, and anything the message handler throws -- a Go panic out of
      // init, say -- which is a start that will never report either way.
      console.error("Engine worker failed", event);

      const message =
        event.message || "the audio engine worker failed to start";

      failStart(new Error(message));
      setStatus(message);
      setError(true);
    };

    // The worker resolves the module and the manifest against the page's base
    // URL: it is served from the bundle's asset directory, where a relative
    // "glockenspiel.wasm" would be a 404.
    send({ type: "load", baseURL: document.baseURI });
  }, []);

  return { client, status, error };
}

export function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
