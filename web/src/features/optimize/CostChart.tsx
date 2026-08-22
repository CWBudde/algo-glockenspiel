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
import { useEffect, useMemo, useRef } from "react";
import { Line } from "react-chartjs-2";

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
  points: CostPoint[];
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
 * A logarithmic axis cannot plot a zero or a negative, and a cost legitimately
 * can be either: a perfect match is 0, and a metric is free to be signed. Such
 * a sample is dropped from the line rather than being clamped to an invented
 * epsilon, which would draw a floor that is not in the data.
 */
function plottable(
  points: CostPoint[],
  pick: (p: CostPoint) => number,
): Point[] {
  const result: Point[] = [];

  for (const point of points) {
    const y = pick(point);

    if (Number.isFinite(y) && y > 0) {
      result.push({ x: point.iteration, y });
    }
  }

  return result;
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
export function CostChart({ points }: CostChartProps) {
  const chartRef = useRef<Chart<"line", Point[]> | null>(null);

  const prefersReducedMotion = useMemo(
    () =>
      typeof window !== "undefined" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches,
    [],
  );

  // The palette lives in :root as custom properties so the chart matches the
  // instrument rather than carrying a second, drifting set of hexes.
  const palette = useMemo(
    () => ({
      best: cssColor("--accent", "#8d562d"),
      current: cssColor("--gold", "#e3bb7a"),
      ink: cssColor("--ink", "#3d2415"),
      muted: cssColor("--muted", "#6b5442"),
    }),
    [],
  );

  const initialData = useMemo<ChartData<"line", Point[]>>(
    () => ({
      datasets: [
        {
          label: "best cost",
          data: [],
          borderColor: palette.best,
          backgroundColor: palette.best,
          borderWidth: 2,
          pointRadius: 0,
          tension: 0,
        },
        {
          label: "current cost",
          data: [],
          borderColor: palette.current,
          backgroundColor: palette.current,
          borderWidth: 1,
          pointRadius: 0,
          tension: 0,
        },
      ],
    }),
    [palette],
  );

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
          grid: { color: "rgba(85, 52, 24, 0.16)" },
        },
        y: {
          type: "logarithmic",
          title: { display: true, text: "cost", color: palette.muted },
          ticks: { color: palette.muted },
          grid: { color: "rgba(85, 52, 24, 0.16)" },
        },
      },

      plugins: {
        legend: { labels: { color: palette.ink } },
        tooltip: { enabled: true },
      },
    }),
    [palette, prefersReducedMotion],
  );

  useEffect(() => {
    const chart = chartRef.current;

    if (chart === null) {
      return;
    }

    chart.data.datasets[0].data = plottable(points, (p) => p.best);
    chart.data.datasets[1].data = plottable(points, (p) => p.current);
    chart.update("none");
  }, [points]);

  const last = points[points.length - 1];
  const summary =
    last === undefined
      ? "No cost samples yet."
      : `Cost curve over ${String(points.length)} samples. ` +
        `At ${String(last.iteration)} optimizer iterations the best cost is ` +
        `${last.best.toPrecision(4)} and the current cost is ${last.current.toPrecision(4)}.`;

  return (
    <div className="cost-chart">
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
