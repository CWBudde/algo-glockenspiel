/**
 * The two pages, in tab order.
 *
 * Hash routing rather than paths: internal/server's handleStatic answers every
 * unknown path with a hard 404 and says why -- a silent fallback to index.html
 * "would turn a mistyped asset path into a page that loads and then
 * misbehaves". A fragment never reaches the server, so the app gains a second
 * page without giving that property up, and the same bundle still works on
 * GitHub Pages, which has no rewrite rules either.
 */
export const ROUTES = [
  { id: "play", label: "Play" },
  { id: "optimize", label: "Optimize" },
] as const;

export type Route = (typeof ROUTES)[number]["id"];

export const DEFAULT_ROUTE: Route = "play";

/** parseRoute maps a location hash onto a known route, defaulting to Play. */
export function parseRoute(hash: string): Route {
  const name = hash.replace(/^#\/?/, "");

  return ROUTES.some((entry) => entry.id === name)
    ? (name as Route)
    : DEFAULT_ROUTE;
}
