import type { FitRequestFields, FitSnapshot } from "../../api/types";

export type FitWorkerCommand =
  | { type: "load"; baseURL: string }
  | {
      type: "start";
      request: FitRequestFields;
      reference: ArrayBuffer;
      preset: ArrayBuffer;
      bounds: ArrayBuffer;
    }
  | { type: "cancel"; jobId: string }
  | { type: "preset"; requestId: number }
  | {
      type: "render";
      requestId: number;
      note: number;
      velocity: number;
      duration: number;
    };

export type FitWorkerEvent =
  | { type: "loaded" }
  | { type: "snapshot"; snapshot: FitSnapshot }
  | { type: "startError"; message: string }
  | { type: "cancelError"; message: string }
  | {
      type: "artifact";
      requestId: number;
      mediaType: string;
      data?: ArrayBuffer;
      error?: string;
    }
  | { type: "error"; message: string };
