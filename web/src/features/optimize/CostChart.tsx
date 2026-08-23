import {
  Chart,
  Legend,
  LineController,
  LineElement,
  LinearScale,
  LogarithmicScale,
  PointElement,
  Tooltip,
  type ChartData,
  type ChartOptions,
  type Point,
} from "chart.js";
import { useEffect, useMemo, useRef, useState } from "react";
import { Line } from "react-chartjs-2";

import { useResolvedTheme, type ResolvedTheme } from "../../lib/theme";
import type { CostPoint } from "./useFitEvents";

/*
 * Only what is drawn is registered. `chart.js/auto` pulls in every controller,
 * every scale and every plugin the library has and defeats tree shaking; this
 * chart is one line controller over two linear-ish scales with a tooltip and a
 * legend, and nothing else needs to be in the bundle.
 */
Chart.register(
  LineController,
  LineElement,
  PointElement,
  LinearScale,
  LogarithmicScale,
  Tooltip,
  Legend,
);

export interface CostChartProps {
  /**
   * The whole curve. `useFitEvents` grows this array in place rather than
   * replacing it, so its identity changes only when the watched job does and
   * it is `revision` that says a sample has arrived.
   */
  points: CostPoint[];
  /** How many samples have been recorded into `points`. */
  revision: number;
}

/** Reads one of the palette's custom properties, with a fallback for SSR-less safety. */
function cssColor(name: string, fallback: string): string {
  if (typeof window === "undefined") {
    return fallback;
  }

  const value = window
    .getComputedStyle(document.documentElement)
    .getPropertyValue(name)
    .trim();

  return value === "" ? fallback : value;
}

/**
 * Reads the palette the stylesheet is currently painting with.
 *
 * The theme does not choose any of the values -- they all come from the
 * computed style -- but it is stamped onto the result, because it is what says
 * when the reading is stale: the memo around this call re-runs when the switch
 * moves, and a chart holding a palette from the other theme is a bug that is
 * otherwise invisible in the object.
 */
function readPalette(theme: ResolvedTheme) {
  return {
    theme,
    best: cssColor("--copper-accent", "#8d562d"),
    current: cssColor("--brass", "#e3bb7a"),
    ink: cssColor("--charcoal", "#3d2415"),
    muted: cssColor("--charcoal-muted", "#6b5442"),
    grid: cssColor("--chart-grid", "rgba(85, 52, 24, 0.16)"),
  };
}

/**
 * Folds the samples from `from` onwards into one dataset's own array.
 *
 * A logarithmic axis cannot plot a zero or a negative, and a cost legitimately
 * can be either: a perfect match is 0, and a metric is free to be signed. Such
 * a sample is dropped from the line rather than being clamped to an invented
 * epsilon, which would draw a floor that is not in the data.
 *
 * The tail is trimmed before appending because the newest sample is not always
 * a new one: `appendPoint` overwrites the last point in place when the
 * optimizer's iteration count has not moved, so whatever is already drawn at or
 * beyond the first re-read sample has to go before it is drawn again.
 */
function extend(
  data: Point[],
  points: CostPoint[],
  from: number,
  pick: (p: CostPoint) => number,
): void {
  const boundary = points[from];

  if (boundary !== undefined) {
    while (data.length > 0) {
      // Chart.js allows a null coordinate to mean a gap in the line; nothing
      // here ever writes one, so an x that is not a number is dropped with the
      // rest of the re-read tail rather than being reasoned about.
      const x = data[data.length - 1].x;

      if (x !== null && x < boundary.iteration) {
        break;
      }

      data.pop();
    }
  }

  for (let index = from; index < points.length; index += 1) {
    const point = points[index];
    const y = pick(point);

    if (Number.isFinite(y) && y > 0) {
      data.push({ x: point.iteration, y });
    }
  }
}

/**
 * The views of the curve the reader can choose between.
 *
 * A null span is the whole run. The rest are counted in optimizer iterations
 * rather than in samples, so the window means the same stretch of the fit
 * whatever `reportEvery` the job was started with.
 */
const RANGES: { span: number | null; label: string }[] = [
  { span: null, label: "All" },
  { span: 1000, label: "Last 1000" },
  { span: 100, label: "Last 100" },
  { span: 10, label: "Last 10" },
];

/**
 * The index of the first sample at or after `iteration`.
 *
 * The scan walks back from the end rather than forward from the start, so its
 * cost is the window's rather than the history's: a run reporting every
 * iteration reaches a hundred thousand samples, and a windowed view only ever
 * redraws the tail of them.
 */
function firstIndexFrom(points: CostPoint[], iteration: number): number {
  let index = points.length;

  while (index > 0 && points[index - 1].iteration >= iteration) {
    index -= 1;
  }

  return index;
}

/** The same guard `FitStatus` applies, so a broken reading is never announced. */
function formatCost(cost: number): string {
  if (!Number.isFinite(cost)) {
    return "-";
  }

  return cost.toPrecision(4);
}

/**
 * The cost curve, streamed.
 *
 * A fit at `reportEvery: 1` emits points as fast as the optimizer iterates, so
 * new samples are pushed straight into the chart's own data and drawn with
 * `update("none")` rather than being handed to React as a new dataset object.
 * Re-rendering the dataset on every event would rebuild the scales and the
 * element cache each time for a chart whose shape has not changed.
 */
export function CostChart({ points, revision }: CostChartProps) {
  const chartRef = useRef<Chart<"line", Point[]> | null>(null);

  // How much of the curve is drawn. A long run flattens out early and spends
  // the rest of its iterations moving in a range the full-height axis cannot
  // show, so the tail is worth being able to look at on its own.
  const [span, setSpan] = useState<number | null>(null);

  const prefersReducedMotion = useMemo(
    () =>
      typeof window !== "undefined" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches,
    [],
  );

  // The palette lives in :root as custom properties so the chart matches the
  // instrument rather than carrying a second, drifting set of hexes. A canvas
  // inherits none of them, so the resolved theme is the dependency that makes
  // the line, the ticks and the grid follow the switch.
  const theme = useResolvedTheme();
  const palette = useMemo(() => readPalette(theme), [theme]);

  /*
   * The shape of the chart, once. It deliberately carries no colors and never
   * changes identity: react-chartjs-2 reconciles the `data` prop into the live
   * chart, and a new object here would hand the datasets these empty arrays
   * back and wipe a curve that is mid-fit. The palette is pushed onto the live
   * datasets by the effect below instead.
   */
  const initialData = useMemo<ChartData<"line", Point[]>>(
    () => ({
      datasets: [
        {
          label: "best cost",
          data: [],
          borderWidth: 2,
          pointRadius: 0,
          tension: 0,
        },
        {
          label: "current cost",
          data: [],
          borderWidth: 1,
          pointRadius: 0,
          tension: 0,
        },
      ],
    }),
    [],
  );

  // Dataset colors are chart state, not options, so they are assigned rather
  // than re-declared. This runs on mount and again whenever the theme moves.
  useEffect(() => {
    const chart = chartRef.current;

    if (chart === null) {
      return;
    }

    const [best, current] = chart.data.datasets;

    best.borderColor = palette.best;
    best.backgroundColor = palette.best;
    current.borderColor = palette.current;
    current.backgroundColor = palette.current;

    chart.update("none");
  }, [palette]);

  const options = useMemo<ChartOptions<"line">>(
    () => ({
      responsive: true,
      maintainAspectRatio: false,

      // The data is already {x, y}, so Chart.js is told not to look for a
      // parser: at one point per optimizer iteration the parse is the work.
      parsing: false,
      normalized: true,

      // Streaming updates are drawn with update("none") regardless; this only
      // governs the first paint, and a reduced-motion preference removes even
      // that.
      animation: prefersReducedMotion ? false : { duration: 200 },

      scales: {
        x: {
          type: "linear",
          title: {
            display: true,
            text: "optimizer iterations",
            color: palette.muted,
          },
          ticks: { color: palette.muted },
          grid: { color: palette.grid },
        },
        y: {
          type: "logarithmic",
          title: { display: true, text: "cost", color: palette.muted },
          ticks: { color: palette.muted },
          grid: { color: palette.grid },
        },
      },

      plugins: {
        legend: { labels: { color: palette.ink } },
        tooltip: { enabled: true },
      },
    }),
    [palette, prefersReducedMotion],
  );

  // How many of `points` are already in the chart's own arrays. A fit that
  // reports every iteration may run to 100,000 of them, so each event folds in
  // only what it brought rather than mapping the whole history again.
  const foldedRef = useRef(0);
  const frameRef = useRef<number | null>(null);

  // `revision` is the dependency that moves: `points` is mutated in place, and
  // the last sample is overwritten rather than appended when the optimizer's
  // iteration count has not changed, so neither the array's identity nor its
  // length can be relied on to say that there is something new to draw.
  useEffect(() => {
    const chart = chartRef.current;

    if (chart === null) {
      return;
    }

    const [best, current] = chart.data.datasets;
    const last = points[points.length - 1];
    let from = foldedRef.current;

    if (span !== null) {
      // A window is redrawn from its first sample every time, because the
      // window moves with the newest one: what leaves it at the left has to go
      // as surely as what arrives at the right has to be added. The work is
      // bounded by the window rather than by the run's length, and nothing is
      // carried over, so `foldedRef` is left saying that the chart holds
      // nothing -- a switch back to the whole curve then refolds it.
      from =
        last === undefined
          ? 0
          : firstIndexFrom(points, last.iteration - span + 1);
      best.data = [];
      current.data = [];

      extend(best.data, points, from, (p) => p.best);
      extend(current.data, points, from, (p) => p.current);
      foldedRef.current = 0;
    } else {
      if (from > points.length) {
        // A new job hands us a shorter array: the previous run's line goes.
        from = 0;
      } else if (from > 0) {
        // The last sample may have been overwritten in place rather than
        // appended, so it is re-read rather than trusted.
        from -= 1;
      }

      if (from === 0) {
        best.data = [];
        current.data = [];
      }

      extend(best.data, points, from, (p) => p.best);
      extend(current.data, points, from, (p) => p.current);
      foldedRef.current = points.length;
    }

    // A burst at reportEvery: 1 arrives faster than the screen refreshes, and
    // every redraw walks the whole line. Collapsing the pending redraws into
    // one per frame keeps the drawing cost proportional to time rather than to
    // the number of samples.
    frameRef.current ??= requestAnimationFrame(() => {
      frameRef.current = null;
      chart.update("none");
    });
  }, [points, revision, span]);

  // Only on unmount: cancelling the pending frame between events would keep
  // rescheduling a redraw that never happens while samples keep arriving.
  useEffect(
    () => () => {
      if (frameRef.current !== null) {
        cancelAnimationFrame(frameRef.current);
        frameRef.current = null;
      }
    },
    [],
  );

  const latest = points[points.length - 1];
  const windowNote =
    span === null
      ? ""
      : ` Showing the last ${String(span)} optimizer iterations.`;
  const summary =
    latest === undefined
      ? "No cost samples yet."
      : `Cost curve over ${String(points.length)} samples. ` +
        `At ${String(latest.iteration)} optimizer iterations the best cost is ` +
        `${formatCost(latest.best)} and the current cost is ${formatCost(latest.current)}.` +
        windowNote;

  return (
    <div className="cost-chart">
      {/*
       * Real radio inputs behind their labels, as in `ThemeSwitch`: four
       * mutually exclusive views of one curve is what a radio group is, and
       * the native control brings the arrow-key traversal, the roving tab stop
       * and the announced "2 of 4" with it.
       */}
      <fieldset className="cost-chart-range">
        <legend>Range</legend>

        {RANGES.map((range) => {
          const id = `cost-range-${range.span === null ? "all" : String(range.span)}`;

          return (
            <div key={id} className="cost-chart-range-option">
              <input
                type="radio"
                id={id}
                name="cost-range"
                checked={span === range.span}
                onChange={() => {
                  setSpan(range.span);
                }}
              />
              <label htmlFor={id}>{range.label}</label>
            </div>
          );
        })}
      </fieldset>

      <div className="cost-chart-canvas">
        <Line
          ref={chartRef}
          data={initialData}
          options={options}
          // A canvas is opaque to a screen reader, so the same reading the
          // sighted user gets from the line is spelled out here and repeated
          // in the summary below, which is announced as it changes.
          aria-label={summary}
          role="img"
        />
      </div>

      <p className="visually-hidden" aria-live="polite">
        {summary}
      </p>
    </div>
  );
}
