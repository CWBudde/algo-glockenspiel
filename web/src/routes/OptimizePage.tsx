/**
 * The Optimize tab.
 *
 * This is the shell only. It exists in this PR so that the router, the layout
 * and the styling are settled before two parallel branches fill it in, and so
 * that they touch disjoint files: the form and the API client are one PR, the
 * progress stream, the cost chart and the audition another. Each slot below
 * names the file that will replace it.
 */
export function OptimizePage() {
  return (
    <section className="optimize-panel" aria-labelledby="optimize-heading">
      <header className="optimize-header">
        <h2 id="optimize-heading">Optimize</h2>
        <p className="optimize-lead">
          Fit the instrument model against a reference recording, watch the cost
          fall, then audition and download the result.
        </p>
      </header>

      {/* slot: availability notice -- features/optimize/useApiAvailable.ts */}
      {/* slot: the fit form -- features/optimize/FitForm.tsx */}
      {/* slot: the cost chart -- features/optimize/CostChart.tsx */}
      {/* slot: audition and download -- features/optimize/Audition.tsx */}

      <p className="optimize-placeholder" aria-live="polite">
        Fitting from the browser is not wired up yet. Until it is, run a fit
        from the command line:
      </p>

      <pre className="optimize-command">
        <code>
          glockenspiel fit --reference recording.wav --output preset.json
        </code>
      </pre>
    </section>
  );
}
