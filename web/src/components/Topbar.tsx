import brandMark from "../../assets/glockenmark.svg";
import type { Route } from "../routes/routes";
import { ROUTES } from "../routes/routes";

export interface TopbarProps {
  route: Route;
}

/**
 * The masthead and the tab bar.
 *
 * The tabs are ordinary links to hash fragments, not buttons with a click
 * handler: that keeps the browser's back button, middle click and "copy link
 * address" working, and it is why no server route had to change to gain a
 * second page. See src/App.tsx for the router that reads them back.
 *
 * The hamburger that used to sit here opened nothing, and the title claimed to
 * be a VST3 that this page is not; both are gone.
 */
export function Topbar({ route }: TopbarProps) {
  return (
    <header className="studio-topbar">
      <div className="brand">
        <img src={brandMark} alt="" className="brand-mark" />
        <div>
          <p className="brand-kicker">Physical Model Instrument</p>
          <h1>Algo Glockenspiel</h1>
        </div>
      </div>

      <nav className="tab-bar" aria-label="Sections">
        {ROUTES.map((entry) => (
          <a
            key={entry.id}
            className="tab"
            href={`#/${entry.id}`}
            aria-current={route === entry.id ? "page" : undefined}
          >
            {entry.label}
          </a>
        ))}
      </nav>
    </header>
  );
}
