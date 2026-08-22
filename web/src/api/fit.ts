/**
 * The typed client for `glockenspiel serve`'s fit API.
 *
 * Every URL here is **relative** -- "api/fit", never "/api/fit". The bundle is
 * built with `base: "./"` for the same reason: it has to work both at the
 * server root (http://localhost:8080/) and under the project sub-path GitHub
 * Pages hands out. An absolute path would resolve to the Pages domain root and
 * 404 there.
 */

import type { ApiError, FitSnapshot, Preset, VersionResponse } from "./types";

/** The base every request is resolved against. */
const API_BASE = "api/";

/**
 * A failed request, carrying the status so a caller can tell a 409 "a fit is
 * already running" from a generic failure and say something useful about it.
 */
export class FitApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "FitApiError";
    this.status = status;
  }

  /** Whether this is the single-fit-slot conflict. */
  get isConflict(): boolean {
    return this.status === 409;
  }

  /** Whether the server has no job to report on yet. */
  get isNotFound(): boolean {
    return this.status === 404;
  }
}

/**
 * Turns a non-2xx response into a FitApiError carrying the server's own
 * message.
 *
 * Most failures are `{"error": "..."}`, because writeJSONError sends every one
 * of them in that shape. Two are not, and both are reachable from this client:
 * an unknown path under /api/fit/ is answered by http.NotFound with plain text,
 * and allowReadMethods answers a wrong method -- /api/version's 405 among them
 * -- with http.Error. Reading the body as JSON regardless would throw a parse
 * error over the real status, which is the one thing the caller needs, so the
 * body is read as text first and only then parsed.
 */
async function toError(response: Response): Promise<FitApiError> {
  let body = "";

  try {
    body = await response.text();
  } catch {
    // A body that cannot be read is not more informative than the status.
    return new FitApiError(response.status, `HTTP ${response.status}`);
  }

  const trimmed = body.trim();

  if (trimmed.startsWith("{")) {
    try {
      const parsed = JSON.parse(trimmed) as Partial<ApiError>;

      if (typeof parsed.error === "string" && parsed.error !== "") {
        return new FitApiError(response.status, parsed.error);
      }
    } catch {
      // Fall through to the text below.
    }
  }

  return new FitApiError(
    response.status,
    trimmed === "" ? `HTTP ${response.status}` : trimmed,
  );
}

/** Performs a request and decodes a JSON body, or throws a FitApiError. */
async function requestJSON<T>(path: string, init?: RequestInit): Promise<T> {
  let response: Response;

  try {
    response = await fetch(API_BASE + path, init);
  } catch (cause) {
    // fetch rejects only for a transport failure; there is no status to report.
    throw new FitApiError(
      0,
      cause instanceof Error
        ? `the server could not be reached: ${cause.message}`
        : "the server could not be reached",
    );
  }

  if (!response.ok) {
    throw await toError(response);
  }

  return (await response.json()) as T;
}

/**
 * Starts a fit.
 *
 * The form must carry a `reference` file; `preset` and `bounds` are optional
 * files and every scalar is optional, falling back to defaultFitRequest(). A
 * 409 means the single fit slot is taken.
 */
export function startFit(form: FormData): Promise<FitSnapshot> {
  // No Content-Type header: the browser has to set the multipart boundary.
  return requestJSON<FitSnapshot>("fit/start", {
    method: "POST",
    body: form,
  });
}

/**
 * Cancels the running fit and resolves once it has actually stopped.
 *
 * A 200 means the slot is genuinely free -- the handler blocks on the job's
 * done channel -- so a new fit can be started immediately with no polling. A
 * 202 means the wait was cut short by the request's own context or by
 * shutdown; the cancellation still took effect.
 *
 * Passing the job id makes cancel safe against the race it would otherwise
 * have: cancelling while the watched run ends and another begins must not
 * silently kill the newcomer. The server answers a mismatch with a 409.
 */
export function cancelFit(jobId?: string): Promise<FitSnapshot> {
  const query =
    jobId === undefined || jobId === ""
      ? ""
      : `?job=${encodeURIComponent(jobId)}`;

  return requestJSON<FitSnapshot>(`fit/cancel${query}`, { method: "POST" });
}

/** Reads the most recent job. A 404 means nothing has been started yet. */
export function getFitStatus(): Promise<FitSnapshot> {
  return requestJSON<FitSnapshot>("fit");
}

/**
 * Reads the best preset the current job has produced. A 409 means the job has
 * not produced one yet; `FitSnapshot.hasPreset` says in advance.
 */
export function getFitPreset(): Promise<Preset> {
  return requestJSON<Preset>("fit/preset");
}

/**
 * The URL of an audition render of the fitted preset.
 *
 * Every parameter is optional and falls back to the job's own: the fit's note
 * and velocity, and min(referenceSeconds, 60) for the duration. The response
 * is `no-store`, but the URL alone does not identify the fit, so a caller that
 * re-renders after a new fit should add its own cache-busting parameter.
 */
export function fitAudioUrl(
  note?: number,
  velocity?: number,
  duration?: number,
): string {
  const query = new URLSearchParams();

  if (note !== undefined) {
    query.set("note", String(note));
  }

  if (velocity !== undefined) {
    query.set("velocity", String(velocity));
  }

  if (duration !== undefined) {
    query.set("duration", String(duration));
  }

  const suffix = query.size === 0 ? "" : `?${query.toString()}`;

  return `${API_BASE}fit/audio${suffix}`;
}

/** The URL the fitted preset downloads from, Content-Disposition and all. */
export function fitPresetUrl(): string {
  return `${API_BASE}fit/preset`;
}

/** The URL of the SSE progress stream. */
export function fitEventsUrl(): string {
  return `${API_BASE}fit/events`;
}

/**
 * Reads the running binary's version. This is the availability probe: on
 * GitHub Pages there is no /api/ catch-all, so the request falls through to
 * the static 404 and this rejects.
 */
export function getVersion(): Promise<VersionResponse> {
  return requestJSON<VersionResponse>("version");
}
