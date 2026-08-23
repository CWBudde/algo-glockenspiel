import { loadWasm } from "../../audio/loadWasm";
import { hasFitExports, type GlockenspielFitWasm } from "../../audio/wasmTypes";
import type { FitSnapshot } from "../../api/types";
import type { FitWorkerCommand, FitWorkerEvent } from "./fitProtocol";

interface WorkerScope {
  postMessage(message: FitWorkerEvent, transfer?: Transferable[]): void;
  onmessage: ((event: MessageEvent<FitWorkerCommand>) => void) | null;
}

const scope = self as unknown as WorkerScope;
const encoder = new TextEncoder();

let api: GlockenspielFitWasm | null = null;
let loading: Promise<void> | null = null;

function post(event: FitWorkerEvent, transfer: Transferable[] = []): void {
  scope.postMessage(event, transfer);
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

async function load(baseURL: string): Promise<void> {
  const loaded = await loadWasm(
    baseURL,
    "fit",
    hasFitExports,
    (runtimeError: unknown) => {
      post({
        type: "error",
        message: `WASM optimizer stopped: ${messageOf(runtimeError)}`,
      });
    },
  );

  api = loaded.api;
  post({ type: "loaded" });
}

function start(command: Extract<FitWorkerCommand, { type: "start" }>): void {
  if (api === null) {
    post({ type: "startError", message: "the WASM optimizer is not loaded" });

    return;
  }

  const error = api.fitStart(
    JSON.stringify(command.request),
    new Uint8Array(command.reference),
    new Uint8Array(command.preset),
    new Uint8Array(command.bounds),
    (snapshotJSON) => {
      try {
        const snapshot = JSON.parse(snapshotJSON) as FitSnapshot;
        post({ type: "snapshot", snapshot });
      } catch {
        post({
          type: "error",
          message: "the WASM optimizer sent an invalid progress snapshot",
        });
      }
    },
  );

  if (typeof error === "string" && error.length > 0) {
    post({ type: "startError", message: error });
  }
}

function cancel(jobId: string): void {
  if (api === null) {
    post({ type: "cancelError", message: "the WASM optimizer is not loaded" });

    return;
  }

  const error = api.fitCancel(jobId);
  if (typeof error === "string" && error.length > 0) {
    post({ type: "cancelError", message: error });
  }
}

function preset(requestId: number): void {
  if (api === null) {
    post({
      type: "artifact",
      requestId,
      mediaType: "application/json",
      error: "the WASM optimizer is not loaded",
    });

    return;
  }

  const result = api.fitPreset();
  if (typeof result.error === "string") {
    post({
      type: "artifact",
      requestId,
      mediaType: "application/json",
      error: result.error,
    });

    return;
  }

  if (typeof result.data !== "string") {
    post({
      type: "artifact",
      requestId,
      mediaType: "application/json",
      error: "the WASM optimizer returned no preset",
    });

    return;
  }

  const bytes = encoder.encode(result.data);
  const data = bytes.buffer;
  post({ type: "artifact", requestId, mediaType: "application/json", data }, [
    data,
  ]);
}

function render(command: Extract<FitWorkerCommand, { type: "render" }>): void {
  if (api === null) {
    post({
      type: "artifact",
      requestId: command.requestId,
      mediaType: "audio/wav",
      error: "the WASM optimizer is not loaded",
    });

    return;
  }

  const result = api.fitRender(
    command.note,
    command.velocity,
    command.duration,
  );
  if (typeof result.error === "string") {
    post({
      type: "artifact",
      requestId: command.requestId,
      mediaType: "audio/wav",
      error: result.error,
    });

    return;
  }

  if (!(result.data instanceof Uint8Array)) {
    post({
      type: "artifact",
      requestId: command.requestId,
      mediaType: "audio/wav",
      error: "the WASM optimizer returned no audio",
    });

    return;
  }

  const data = result.data.buffer as ArrayBuffer;
  post(
    {
      type: "artifact",
      requestId: command.requestId,
      mediaType: "audio/wav",
      data,
    },
    [data],
  );
}

scope.onmessage = (event: MessageEvent<FitWorkerCommand>) => {
  const command = event.data;

  switch (command.type) {
    case "load":
      loading ??= load(command.baseURL).catch((error: unknown) => {
        post({ type: "error", message: messageOf(error) });
      });
      break;
    case "start":
      start(command);
      break;
    case "cancel":
      cancel(command.jobId);
      break;
    case "preset":
      preset(command.requestId);
      break;
    case "render":
      render(command);
      break;
  }
};
