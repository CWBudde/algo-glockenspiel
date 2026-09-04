import type { APIRequestContext } from "@playwright/test";

/**
 * Whether /api is the real `glockenspiel serve` rather than the stand-in.
 *
 * scripts/playwright-go-server.sh falls back to a bare listener when the Go
 * toolchain is unavailable or the build fails, so a developer without Go
 * still gets the rest of this suite. That listener answers every path with
 * the same small document; the real server answers the job history with a
 * `jobs` array. Reading the history is therefore the probe, and it is one
 * function because both specs that drive the real server have to agree on
 * what "the real server" means.
 *
 * It deliberately does not ask whether a fit is running. The server adopts
 * run directories it finds in its work directory, so an idle server is no
 * longer the same thing as a server with no active job.
 */
export async function goFitServerIsReal(
  request: APIRequestContext,
): Promise<boolean> {
  const listed = await request.get("/api/fit/jobs");

  if (!listed.ok()) {
    return false;
  }

  const body: unknown = await listed.json();

  return (
    typeof body === "object" &&
    body !== null &&
    Array.isArray((body as { jobs?: unknown }).jobs)
  );
}

/** What to say when it is not. */
export const noGoServerReason =
  "the Go fit server is not available (see scripts/playwright-go-server.sh)";
