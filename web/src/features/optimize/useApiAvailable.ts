import { useEffect, useState } from "react";

import { getVersion } from "../../api/fit";

/** Whether the fit API answered, is still being probed, or is not there. */
export type ApiAvailability = "probing" | "available" | "unavailable";

export interface ApiProbe {
  availability: ApiAvailability;
  /** The version string the binary reports, once it has answered. */
  version: string | null;
}

/**
 * Probes `GET api/version` once on mount.
 *
 * The same bundle is served two ways. Under `glockenspiel serve` the whole fit
 * API is there; on GitHub Pages none of it is, and there is no /api/ catch-all
 * to say so politely -- the request falls through to the static 404. Rather
 * than render a form whose Start button can only ever fail, the tab asks once
 * and then either shows the form or explains that Optimize needs the local
 * CLI.
 *
 * One probe, no retry: the answer cannot change without a reload, because what
 * it really reports is which of the two deployments served the page.
 */
export function useApiAvailable(): ApiProbe {
  const [probe, setProbe] = useState<ApiProbe>({
    availability: "probing",
    version: null,
  });

  useEffect(() => {
    let cancelled = false;

    getVersion()
      .then((response) => {
        if (!cancelled) {
          setProbe({ availability: "available", version: response.version });
        }
      })
      .catch(() => {
        if (!cancelled) {
          setProbe({ availability: "unavailable", version: null });
        }
      });

    return () => {
      cancelled = true;
    };
  }, []);

  return probe;
}
