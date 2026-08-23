import { THEME_PREFERENCES, type ThemePreference } from "../lib/theme";

const LABELS: Record<ThemePreference, string> = {
  auto: "Auto",
  light: "Light",
  dark: "Dark",
};

export interface ThemeSwitchProps {
  preference: ThemePreference;
  onPreferenceChange: (preference: ThemePreference) => void;
}

/**
 * The three-way color theme switch.
 *
 * These are real radio inputs behind their labels rather than buttons with
 * `aria-pressed`: a set of three mutually exclusive choices is what a radio
 * group is, and the native control brings arrow-key traversal, the roving tab
 * stop and the announced "2 of 3" with it, none of which is worth
 * reimplementing. The input itself is transparent and the label is the visible
 * control, so the focus ring is drawn on the label; see the focus block in
 * styles/index.css.
 */
export function ThemeSwitch({
  preference,
  onPreferenceChange,
}: ThemeSwitchProps) {
  return (
    <fieldset className="theme-switch">
      <legend className="visually-hidden">Color theme</legend>

      {THEME_PREFERENCES.map((entry) => (
        <div key={entry} className="theme-option">
          <input
            type="radio"
            id={`theme-${entry}`}
            name="theme"
            value={entry}
            checked={preference === entry}
            onChange={() => {
              onPreferenceChange(entry);
            }}
          />
          <label htmlFor={`theme-${entry}`}>{LABELS[entry]}</label>
        </div>
      ))}
    </fieldset>
  );
}
