import { useEffect, useRef, useState } from "react";

import {
  DEFAULT_FIT_REQUEST,
  isTerminal,
  type FitRequestFields,
  type FitSnapshot,
} from "../../api/types";
import type { FitWorkerCommand, FitWorkerEvent } from "./fitProtocol";
import { recordPoint, type CostPoint, type FitEvents } from "./useFitEvents";

export interface WasmFitClient {
  start(form: FormData): Promise<FitSnapshot>;
  cancel(jobId: string): Promise<FitSnapshot>;
  preset(): Promise<Blob>;
  render(note: number, velocity: number, duration: number): Promise<Blob>;
}

export interface WasmFitWorker {
  client: WasmFitClient | null;
  status: string;
  error: boolean;
  events: FitEvents;
}

const EMPTY_EVENTS: FitEvents = {
  snapshot: null,
  points: [],
  revision: 0,
  streaming: false,
  streamError: null,
};

function formNumber(form: FormData, name: string, fallback: number): number {
  const value = form.get(name);

  return typeof value === "string" ? Number(value) : fallback;
}

function formString(form: FormData, name: string, fallback: string): string {
  const value = form.get(name);

  return typeof value === "string" ? value : fallback;
}

function formBoolean(form: FormData, name: string, fallback: boolean): boolean {
  return formString(form, name, String(fallback)) === "true";
}

function requestFromForm(form: FormData): FitRequestFields {
  return {
    note: formNumber(form, "note", DEFAULT_FIT_REQUEST.note),
    velocity: formNumber(form, "velocity", DEFAULT_FIT_REQUEST.velocity),
    optimizer: formString(
      form,
      "optimizer",
      DEFAULT_FIT_REQUEST.optimizer,
    ) as FitRequestFields["optimizer"],
    metric: formString(
      form,
      "metric",
      DEFAULT_FIT_REQUEST.metric,
    ) as FitRequestFields["metric"],
    maxIterations: formNumber(
      form,
      "maxIterations",
      DEFAULT_FIT_REQUEST.maxIterations,
    ),
    timeBudget: formString(form, "timeBudget", DEFAULT_FIT_REQUEST.timeBudget),
    reportEvery: formNumber(
      form,
      "reportEvery",
      DEFAULT_FIT_REQUEST.reportEvery,
    ),
    align: formBoolean(form, "align", DEFAULT_FIT_REQUEST.align),
    normalizeGain: formBoolean(
      form,
      "normalizeGain",
      DEFAULT_FIT_REQUEST.normalizeGain,
    ),
    mayflyVariant: formString(
      form,
      "mayflyVariant",
      DEFAULT_FIT_REQUEST.mayflyVariant,
    ) as FitRequestFields["mayflyVariant"],
    mayflyPopulation: formNumber(
      form,
      "mayflyPopulation",
      DEFAULT_FIT_REQUEST.mayflyPopulation,
    ),
    mayflySeed: formString(form, "mayflySeed", DEFAULT_FIT_REQUEST.mayflySeed),
  };
}

async function partBytes(
  form: FormData,
  name: string,
  required = false,
): Promise<ArrayBuffer> {
  const value = form.get(name);
  if (value instanceof Blob) {
    return value.arrayBuffer();
  }

  if (required) {
    throw new Error(`the ${name} file is missing`);
  }

  return new ArrayBuffer(0);
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/**
 * Owns the dedicated optimizer worker used by static deployments.
 *
 * The worker starts lazily only after the server probe has failed. It is not
 * terminated when the user switches tabs: a fit is a page-level job and must
 * keep running while the Play tab is open, just as a server fit does.
 */
export function useWasmFitWorker(enabled: boolean): WasmFitWorker {
  const [client, setClient] = useState<WasmFitClient | null>(null);
  const [status, setStatus] = useState("Waiting for the fit service probe...");
  const [error, setError] = useState(false);
  const [events, setEvents] = useState<FitEvents>(EMPTY_EVENTS);

  const startedRef = useRef(false);
  const pointsRef = useRef<CostPoint[]>([]);

  useEffect(() => {
    if (!enabled || startedRef.current) {
      return;
    }

    startedRef.current = true;
    setStatus("Loading browser optimizer...");

    const worker = new Worker(new URL("./fit.worker.ts", import.meta.url), {
      type: "module",
    });

    let pendingStart: {
      resolve: (snapshot: FitSnapshot) => void;
      reject: (error: Error) => void;
    } | null = null;
    let pendingCancel: {
      resolve: (snapshot: FitSnapshot) => void;
      reject: (error: Error) => void;
    } | null = null;
    let requestID = 0;
    const artifacts = new Map<
      number,
      { resolve: (blob: Blob) => void; reject: (error: Error) => void }
    >();

    const send = (command: FitWorkerCommand, transfer: Transferable[] = []) => {
      worker.postMessage(command, transfer);
    };

    const requestArtifact = (
      command:
        | { type: "preset" }
        | {
            type: "render";
            note: number;
            velocity: number;
            duration: number;
          },
    ): Promise<Blob> =>
      new Promise((resolve, reject) => {
        requestID += 1;
        artifacts.set(requestID, { resolve, reject });
        send({ ...command, requestId: requestID });
      });

    const fitClient: WasmFitClient = {
      async start(form) {
        const [reference, preset, bounds] = await Promise.all([
          partBytes(form, "reference", true),
          partBytes(form, "preset"),
          partBytes(form, "bounds"),
        ]);

        return new Promise<FitSnapshot>((resolve, reject) => {
          pendingStart = { resolve, reject };
          send(
            {
              type: "start",
              request: requestFromForm(form),
              reference,
              preset,
              bounds,
            },
            [reference, preset, bounds],
          );
        });
      },
      cancel(jobId) {
        return new Promise<FitSnapshot>((resolve, reject) => {
          pendingCancel = { resolve, reject };
          send({ type: "cancel", jobId });
        });
      },
      preset() {
        return requestArtifact({ type: "preset" });
      },
      render(note, velocity, duration) {
        return requestArtifact({ type: "render", note, velocity, duration });
      },
    };

    const failPending = (failure: Error) => {
      pendingStart?.reject(failure);
      pendingStart = null;
      pendingCancel?.reject(failure);
      pendingCancel = null;

      for (const pending of artifacts.values()) {
        pending.reject(failure);
      }
      artifacts.clear();
    };

    worker.onmessage = (event: MessageEvent<FitWorkerEvent>) => {
      const message = event.data;

      switch (message.type) {
        case "loaded":
          setClient(fitClient);
          setStatus("Browser optimizer ready");
          setError(false);
          break;
        case "snapshot": {
          const snapshot = message.snapshot;

          setEvents((previous) => {
            if (previous.snapshot?.jobId !== snapshot.jobId) {
              pointsRef.current = [];
            }

            recordPoint(pointsRef.current, snapshot);

            return {
              snapshot,
              points: pointsRef.current,
              revision: previous.revision + 1,
              streaming: !isTerminal(snapshot.state),
              streamError: null,
            };
          });

          pendingStart?.resolve(snapshot);
          pendingStart = null;

          if (pendingCancel !== null && isTerminal(snapshot.state)) {
            pendingCancel.resolve(snapshot);
            pendingCancel = null;
          }
          break;
        }
        case "startError":
          pendingStart?.reject(new Error(message.message));
          pendingStart = null;
          break;
        case "cancelError":
          pendingCancel?.reject(new Error(message.message));
          pendingCancel = null;
          break;
        case "artifact": {
          const pending = artifacts.get(message.requestId);
          if (pending === undefined) {
            break;
          }

          artifacts.delete(message.requestId);
          if (message.error !== undefined) {
            pending.reject(new Error(message.error));
          } else if (message.data === undefined) {
            pending.reject(new Error("the WASM optimizer returned no data"));
          } else {
            pending.resolve(
              new Blob([message.data], { type: message.mediaType }),
            );
          }
          break;
        }
        case "error": {
          const failure = new Error(message.message);
          failPending(failure);
          setStatus(message.message);
          setError(true);
          setEvents((previous) => ({
            ...previous,
            streaming: false,
            streamError: message.message,
          }));
          break;
        }
      }
    };

    worker.onerror = (event) => {
      const failure = new Error(
        event.message || "the browser optimizer worker failed",
      );
      console.error("WASM fit worker failed", event);
      failPending(failure);
      setStatus(messageOf(failure));
      setError(true);
    };

    send({ type: "load", baseURL: document.baseURI });
  }, [enabled]);

  return { client, status, error, events };
}
