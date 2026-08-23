import brandMark from "../../assets/glockenmark.svg";
import type { ThemePreference } from "../lib/theme";
import type { Route } from "../routes/routes";
import { ROUTES } from "../routes/routes";
import { ThemeSwitch } from "./ThemeSwitch";

export interface TopbarProps {
  route: Route;
  theme: ThemePreference;
  onThemeChange: (preference: ThemePreference) => void;
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
export function Topbar({ route, theme, onThemeChange }: TopbarProps) {
  return (
    <header className="studio-topbar">
      <div className="brand">
        <img src={brandMark} alt="" className="brand-mark" />
        <div>
          <p className="brand-kicker">Algorithmic Instrument</p>
          <h1>Algo Glockenspiel</h1>
        </div>
      </div>

      <div className="topbar-controls">
        <ThemeSwitch preference={theme} onPreferenceChange={onThemeChange} />

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
      </div>
    </header>
  );
}
