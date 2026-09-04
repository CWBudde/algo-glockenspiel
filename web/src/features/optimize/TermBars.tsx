import type { FitMetrics, FitProfile } from "../../api/types";
import {
  formatTermShare,
  formatTermValue,
  rawTerms,
  termContributions,
  type TermContribution,
} from "./termBars";

export interface TermBarsProps {
  /** `FitSnapshot.metrics` / the WASM snapshot's own `metrics`. */
  metrics?: FitMetrics | undefined;
  /**
   * `FitSnapshot.profile`, absent for a single-term legacy metric and for
   * every WASM snapshot -- the browser worker's contract has no profile
   * block to send. Absent means the bars fall back to raw values with no
   * scaling, per the same rule the module behind this component documents:
   * a term with nothing to scale it by is a number, not an invented bar.
   */
  profile?: FitProfile | undefined;
}

/** One bar: a label, a track with a fill, and the number that fill stands for. */
function ContributionBar({ contribution }: { contribution: TermContribution }) {
  const percent = Math.round(contribution.scaled * 100);

  return (
    <li className="term-bar-row">
      <span className="term-bar-label">{contribution.label}</span>

      <span className="term-bar-track" aria-hidden="true">
        <span
          className="term-bar-fill"
          data-measured={contribution.measured}
          style={{ width: `${String(percent)}%` }}
        />
      </span>

      {/*
        The number every bar's width stands for, as real text: the raw
        physical value in its own unit, and the share it worked out to under
        the active profile. This is the bar's text alternative, not a
        decoration beside it -- a screen reader gets exactly what a sighted
        reader gets from the fill.
      */}
      <span className="term-bar-value">
        {formatTermValue(contribution.value)}
        {contribution.unit !== undefined && contribution.unit !== ""
          ? ` ${contribution.unit}`
          : ""}
        {" · "}
        {formatTermShare(contribution)}
      </span>
    </li>
  );
}

/** One raw term, with no scaling: the profile-less fallback. */
function RawTermRow({
  term,
}: {
  term: { label: string; value: number | null };
}) {
  return (
    <li className="term-bar-row term-bar-row-raw">
      <span className="term-bar-label">{term.label}</span>
      <span className="term-bar-value">{formatTermValue(term.value)}</span>
    </li>
  );
}

/**
 * The composite objective's ten terms, as bars.
 *
 * Every bar's width and every number beside it comes straight from
 * `termContributions`, which does nothing but replicate
 * `optimizer.Metrics.Contributions` over the weight and the norm the server
 * already sent: nothing here re-derives a norm, so the bars cannot disagree
 * with the score they are drawn beside.
 */
export function TermBars({ metrics, profile }: TermBarsProps) {
  if (metrics === undefined) {
    return (
      <section className="term-bars" aria-labelledby="term-bars-heading">
        <h3 id="term-bars-heading">Objective terms</h3>
        <p className="optimize-note">
          There is nothing to show yet. The term breakdown becomes available
          with the fit's first report.
        </p>
      </section>
    );
  }

  if (profile === undefined) {
    const terms = rawTerms(metrics);

    return (
      <section className="term-bars" aria-labelledby="term-bars-heading">
        <h3 id="term-bars-heading">Objective terms</h3>
        <p className="optimize-note">
          Raw term values. This run has no profile weighting to scale them by,
          so no bar or score share is shown.
        </p>
        <ul className="term-bar-list">
          {terms.map((term) => (
            <RawTermRow key={term.term} term={term} />
          ))}
        </ul>
      </section>
    );
  }

  const contributions = termContributions(metrics, profile);

  return (
    <section className="term-bars" aria-labelledby="term-bars-heading">
      <h3 id="term-bars-heading">Objective terms</h3>
      <p className="optimize-note">
        Each term's raw value and its share of the “{profile.name}” score. A
        term the reference was too short to measure carries no bar.
      </p>
      <ul className="term-bar-list">
        {contributions.map((contribution) => (
          <ContributionBar
            key={contribution.term}
            contribution={contribution}
          />
        ))}
      </ul>
    </section>
  );
}
