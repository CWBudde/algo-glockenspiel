/**
 * Parses a duration the way fitschema.ParseDuration does on the Go side: a
 * Go duration string, or a bare number read as seconds. The result is
 * returned unchanged for the wire and in seconds for a caller's own range
 * check.
 *
 * This used to be one function per front end that read it -- FitForm.tsx's
 * own `parseDuration` and a second copy the browser worker path never got
 * around to needing -- which is one function too many for a two-line rule.
 * It lives beside api/fit.ts rather than in a features/ directory because
 * both fit front ends may need it, not just the form.
 */
export function parseDuration(
  raw: string,
): { seconds: number } | { error: string } {
  const trimmed = raw.trim();

  if (trimmed === "") {
    return { error: "The time budget is required." };
  }

  // strconv.ParseFloat's decimal grammar, exponent included, so "1e3" is read
  // as 1000 seconds here exactly as the server reads it. Number() alone is more
  // permissive than ParseFloat -- it also takes "0x10" and "Infinity" -- so the
  // pattern, not Number(), decides what counts as a bare number.
  const bareSeconds = /^[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?$/;
  const bare = Number(trimmed);

  if (bareSeconds.test(trimmed) && Number.isFinite(bare)) {
    return { seconds: bare };
  }

  // time.ParseDuration's grammar, restricted to the units a person types here.
  const pattern = /^[+-]?(\d+(\.\d*)?(ns|us|µs|ms|s|m|h))+$/;

  if (!pattern.test(trimmed)) {
    return {
      error: "The time budget must be a duration such as 30s, 2m or 1h.",
    };
  }

  const unitSeconds: Record<string, number> = {
    ns: 1e-9,
    us: 1e-6,
    µs: 1e-6,
    ms: 1e-3,
    s: 1,
    m: 60,
    h: 3600,
  };

  let total = 0;

  for (const part of trimmed.matchAll(/(\d+(?:\.\d*)?)(ns|us|µs|ms|s|m|h)/g)) {
    total += Number(part[1]) * unitSeconds[part[2]];
  }

  return { seconds: trimmed.startsWith("-") ? -total : total };
}
