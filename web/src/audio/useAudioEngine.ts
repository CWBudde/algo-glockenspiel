import { useCallback, useEffect, useRef, useState } from "react";

import { BlockQueue } from "./blockQueue";
import processorURL from "./renderProcessor.ts?worker&url";
import {
  PROCESSOR_NAME,
  type ConsumePort,
  type RecycledBuffer,
  type RenderStats,
  type TransportMessage,
} from "./protocol";
import { messageOf, type EngineClient } from "./useEngineWorker";

/** How often the ScriptProcessorNode fallback samples its own queue, in ms. */
const FALLBACK_STATS_INTERVAL_MS = 500;

/**
 * The query parameter that forces the fallback: `?audio=scriptprocessor`.
 *
 * Without it the fallback is unreachable on any browser released since 2018,
 * which would make it code that is shipped and never run. With it, the same
 * page exercises both consumers, so a regression in the shared BlockQueue shows
 * up in whichever one is tested.
 */
function forcedTransport(): string | null {
  return new URLSearchParams(window.location.search).get("audio");
}

export interface AudioEngine {
  /** True once the graph is running and notes will be heard. */
  ready: boolean;
  /** What the status panel should say about the audio, or "" for nothing yet. */
  status: string;
  error: boolean;
  /** Render quanta that found the queue empty since the graph started. */
  underruns: number;
  /** Starts the graph if it is not running. Idempotent and safe to race. */
  start: () => Promise<void>;
  /** The synchronous answer to "can I strike right now", for the strike path. */
  isReady: () => boolean;
}

/**
 * useAudioEngine owns the AudioContext and the node that feeds it, created
 * lazily on the first strike because a browser will not start an AudioContext
 * without a user gesture.
 *
 * It owns no synthesis. The worker renders ahead into a pool of buffers and
 * the consumer here -- an AudioWorkletNode, or a ScriptProcessorNode where
 * there is no worklet -- drains them and sends each empty buffer back. Both
 * consumers are the same BlockQueue behind different callbacks, so the
 * fallback is a different thread to run on rather than a different engine.
 *
 * masterGain is pushed into the worker whenever it changes, including while a
 * note is ringing, so the Volume dial moves the sound that is already playing.
 */
export function useAudioEngine(
  client: EngineClient | null,
  masterGain: number,
): AudioEngine {
  const [ready, setReady] = useState(false);
  const [status, setStatus] = useState("");
  const [error, setError] = useState(false);
  const [underruns, setUnderruns] = useState(0);

  const contextRef = useRef<AudioContext | null>(null);
  const nodeRef = useRef<AudioNode | null>(null);
  const statsTimerRef = useRef<number | null>(null);
  const readyRef = useRef(false);
  const startPromiseRef = useRef<Promise<void> | null>(null);
  const clientRef = useRef<EngineClient | null>(client);
  const masterGainRef = useRef(masterGain);

  useEffect(() => {
    clientRef.current = client;
    masterGainRef.current = masterGain;

    // Push the gain into a running engine, so the dial moves a note that is
    // already ringing rather than only the next one.
    if (readyRef.current && client) {
      client.setMasterGain(masterGain);
    }
  }, [client, masterGain]);

  // teardown disconnects and closes whatever half of the graph exists. It is
  // written to be safe on a graph that was never finished, because the failure
  // path in start needs exactly that.
  const teardown = useCallback(() => {
    if (statsTimerRef.current !== null) {
      window.clearInterval(statsTimerRef.current);
      statsTimerRef.current = null;
    }

    nodeRef.current?.disconnect();
    nodeRef.current = null;
    void contextRef.current?.close();
    contextRef.current = null;
    readyRef.current = false;

    // The worker still holds the other end of a channel whose buffers are gone
    // with the node; without this it would wait for credit that cannot arrive.
    clientRef.current?.stop();
  }, []);

  const start = useCallback(async () => {
    if (readyRef.current) {
      return;
    }

    if (startPromiseRef.current) {
      return startPromiseRef.current;
    }

    const startup = (async () => {
      const engine = clientRef.current;
      if (!engine) {
        throw new Error("the WebAssembly module is not loaded yet");
      }

      const Context = window.AudioContext ?? window.webkitAudioContext;
      if (!Context) {
        throw new Error("this browser has no Web Audio support");
      }

      const context = new Context();
      contextRef.current = context;

      // The channel the rendered blocks travel down. One end goes to the
      // worker, the other to whichever consumer this browser gets, so the
      // audio never passes through the main thread in either case.
      const channel = new MessageChannel();

      const worklet = await buildWorklet(context, channel.port2);
      if (worklet) {
        worklet.port.onmessage = (event: MessageEvent<RenderStats>) => {
          setUnderruns(event.data.underruns);
        };
        nodeRef.current = worklet;
      } else {
        nodeRef.current = buildFallback(context, channel.port2, (queue) => {
          statsTimerRef.current = window.setInterval(() => {
            setUnderruns(queue.underruns);
          }, FALLBACK_STATS_INTERVAL_MS);
        });
      }

      // The engine is only connected once it is rendering. Building the graph
      // first and starting the producer afterwards would let the consumer run
      // against an empty queue for as long as the engine takes to construct --
      // NewRealtimeEngine measures the preset once per playable note, which is
      // hundreds of milliseconds -- and every one of those render quanta is a
      // counted dropout. Connecting last makes the counter mean what it says.
      await engine.start(context.sampleRate, channel.port1);
      nodeRef.current.connect(context.destination);

      await context.resume();

      engine.setMasterGain(masterGainRef.current);

      readyRef.current = true;
      setReady(true);
      setError(false);
      setUnderruns(0);
      setStatus(
        `Ready at ${Math.round(context.sampleRate)} Hz${worklet ? "" : " (fallback transport)"}`,
      );
    })();

    startPromiseRef.current = startup;

    try {
      await startup;
    } catch (startError) {
      // Startup can fail after the AudioContext exists -- a module that
      // refuses the sample rate, a resume() the browser rejects -- and the
      // refs are already pointing at that half-built graph. Without this the
      // next strike would open a second context over the first, which stays
      // open and keeps its share of the (small) per-page context budget.
      teardown();
      setReady(false);

      console.error(startError);
      setStatus(messageOf(startError));
      setError(true);

      throw startError;
    } finally {
      startPromiseRef.current = null;
    }
  }, [teardown]);

  const isReady = useCallback(() => readyRef.current, []);

  useEffect(
    () => () => {
      // The page owns exactly one graph, but a hot reload or an unmount must
      // not leave a node pulling blocks out of an engine nothing is listening
      // to.
      teardown();
    },
    [teardown],
  );

  return { ready, status, error, underruns, start, isReady };
}

/**
 * buildWorklet builds the AudioWorkletNode and hands it the consumer end of
 * the render channel, or returns null where the browser has no worklet and
 * where ?audio=scriptprocessor asks for the other path. The caller connects it
 * to the destination once the producer is running.
 *
 * A rejected addModule is not fatal for the same reason the absence of
 * audioWorklet is not: there is a working consumer either way, and a page that
 * plays through the older node beats a page that does not play.
 */
async function buildWorklet(
  context: AudioContext,
  port: MessagePort,
): Promise<AudioWorkletNode | null> {
  if (
    context.audioWorklet === undefined ||
    forcedTransport() === "scriptprocessor"
  ) {
    return null;
  }

  try {
    await context.audioWorklet.addModule(processorURL);
  } catch (moduleError) {
    console.warn(
      "AudioWorklet module refused; falling back to ScriptProcessorNode",
      moduleError,
    );

    return null;
  }

  const node = new AudioWorkletNode(context, PROCESSOR_NAME, {
    numberOfInputs: 0,
    numberOfOutputs: 1,
    outputChannelCount: [2],
  });

  const handover: ConsumePort = { type: "consume", port };
  node.port.postMessage(handover, [port]);

  return node;
}

/**
 * buildFallback drains the same queue from a ScriptProcessorNode on the main
 * thread, for a browser with no AudioWorklet. Like buildWorklet it leaves the
 * node unconnected; nothing renders until the caller connects it.
 *
 * The producer is untouched -- synthesis is still in the worker -- so what this
 * costs is the copy running on the main thread again, and with it the jank
 * sensitivity the worklet was there to remove. It is a fallback, not a second
 * design.
 */
function buildFallback(
  context: AudioContext,
  port: MessagePort,
  onQueue: (queue: BlockQueue) => void,
): ScriptProcessorNode {
  const queue = new BlockQueue();

  port.onmessage = (event: MessageEvent<TransportMessage>) => {
    if (event.data.type === "pause") {
      queue.unprime();

      return;
    }

    queue.push(event.data.buffer);
  };
  port.start();

  const node = context.createScriptProcessor(512, 0, 2);
  node.onaudioprocess = (event) => {
    const buffer = event.outputBuffer;
    const left = buffer.getChannelData(0);
    const right = buffer.getChannelData(1);

    queue.fill(left, right, left.length, (spent) => {
      const message: RecycledBuffer = { type: "recycle", buffer: spent };
      port.postMessage(message, [spent.buffer]);
    });
  };

  onQueue(queue);

  return node;
}
