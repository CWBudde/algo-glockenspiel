package pack

import (
	"encoding/json"
	"fmt"
	"html"
	"strconv"
	"strings"
)

// themeStorageKey is where the web app remembers the theme its visitor chose.
//
// It is web/src/lib/theme.ts's THEME_STORAGE_KEY, spelled out here for the
// reason web/index.html spells it out: the page reads it before anything else
// loads. The three copies are pinned to each other by
// web/src/lib/theme.test.ts. A browser keeps storage per origin, so the
// choice carries over only when this page is served from the app's origin;
// from any other port the page follows the system preference, which is what
// the app's "auto" does too.
const themeStorageKey = "algo-glockenspiel:theme"

// StatusPage renders the progress as one self-contained HTML page.
//
// No stylesheet, no build step and one script: the page is served by a
// command whose job is to fit bars rather than to ship a front end, and it is
// the thing you leave open on a second screen for an hour. What it borrows
// from the web app in web/ is the look -- the palette, the type, and the
// theme contract, so a dark visitor there is a dark visitor here -- and the
// habit of updating in place: the script asks for status.json every few
// seconds and rewrites the table, so a page left open does not flash white
// every refresh and does not lose its scroll position.
//
// The status is embedded in the page as JSON and the first paint is drawn
// from that by the same code that draws every later one. Rendering the table
// in Go as well would be a second renderer of the same nine columns, and two
// renderers of one table drift; the test that pins this page checks that the
// embedded JSON and status.json are the same bytes instead.
//
// It is deliberately read-only and carries no controls. Something that could
// stop a fit would need to be as careful about who is asking as
// internal/server is; this is a page to leave open on a phone, and stays
// worth no more scrutiny than a log tail.
func StatusPage(status *Status) (string, error) {
	// Marshalled before anything is assembled, so an encoding failure is an
	// error the handler can report rather than a half-written page. The
	// encoder escapes <, > and & inside strings, which is what keeps a run
	// directory named "</script>" from ending the data element early.
	data, err := json.Marshal(status)
	if err != nil {
		return "", err
	}

	replacer := strings.NewReplacer(
		"{{title}}", html.EscapeString(pageTitle(status)),
		"{{pack}}", html.EscapeString(status.Pack),
		"{{kind}}", status.Kind,
		"{{headline}}", html.EscapeString(headline(status)),
		"{{refresh}}", strconv.Itoa(StatusRefreshSeconds),
		"{{storage-key}}", themeStorageKey,
		"{{dark-tokens}}", statusDarkTokens,
		"{{data}}", string(data),
	)

	return replacer.Replace(statusPageTemplate), nil
}

func pageTitle(status *Status) string {
	if status.Kind == KindJoint {
		return status.Pack + " -- joint fit"
	}

	return fmt.Sprintf("%s -- %d/%d", status.Pack, status.Finished, len(status.Notes))
}

// statusDarkTokens is the dark palette, written once and placed twice: under
// the media query narrowed to "not explicitly light", and under the explicit
// choice. The two selectors have to carry the same declarations, and one
// constant is how they cannot fail to.
//
// The values are the web app's, from web/src/styles/index.css. What flips is
// the lighting -- the canvas, the card, the ink -- and the state colours are
// the app's dark variants of the same, so a running fit is the same green on
// both pages.
const statusDarkTokens = `
    --color-scheme: dark;
    --canvas: #16110d;
    --parchment: #241c15;
    --parchment-light: #2c2219;
    --charcoal: #f3e7d6;
    --charcoal-muted: #bfab94;
    --surface-card: #1e1811;
    --surface-border: rgba(236, 208, 168, 0.22);
    --surface-border-strong: #7a5734;
    --state-ready: #74b892;
    --state-idle: #cf9c56;
    --error: #ff9d86;
    --focus: #9db8ff;`

// statusPageTemplate is the page with its placeholders. The CSS and the
// script are plain text here rather than a fmt format, so a percent sign in
// either is a percent sign.
const statusPageTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{title}}</title>
<link rel="icon" href="data:,">
<script>
  // The theme, before the first paint: the same read web/index.html does, so
  // a visitor who chose dark in the app is not shown a bright page first.
  try {
    var theme = localStorage.getItem("{{storage-key}}");
    if (theme === "light" || theme === "dark") {
      document.documentElement.setAttribute("data-theme", theme);
    }
  } catch (error) {
    // Blocked site data means the system preference, which is what "auto"
    // would have given them anyway.
  }
</script>
<style>
  :root {
    --color-scheme: light;
    --canvas: #ded7ca;
    --parchment: #efe4d1;
    --parchment-light: #fffaf1;
    --charcoal: #3d2415;
    --charcoal-muted: #6b5442;
    --surface-card: #f3eee6;
    --surface-border: rgba(85, 52, 24, 0.35);
    --surface-border-strong: #89572e;
    --state-ready: #477359;
    --state-idle: #a67b43;
    --error: #8f2213;
    --focus: #1c3f8f;
    --font-display: "Avenir Next Condensed", "Franklin Gothic Demi Cond", "Trebuchet MS", sans-serif;
    --font-body: "Avenir Next", "Segoe UI", sans-serif;
    --tracking-label: 0.06em;
    --tracking-wide: 0.08em;
  }
  @media (prefers-color-scheme: dark) {
    :root:not([data-theme="light"]) {{{dark-tokens}}
    }
  }
  :root[data-theme="dark"] {{{dark-tokens}}
  }
  html { color-scheme: var(--color-scheme); }
  body {
    font: 14px/1.5 var(--font-body);
    color: var(--charcoal);
    background: var(--canvas);
    max-width: 60rem;
    margin: 2rem auto;
    padding: 0 1rem;
  }
  header { margin-bottom: 1rem; }
  .eyebrow {
    margin: 0;
    font-size: 0.76rem;
    letter-spacing: var(--tracking-wide);
    text-transform: uppercase;
    color: var(--charcoal-muted);
  }
  h1 {
    margin: 0 0 0.35rem;
    font-family: var(--font-display);
    font-weight: 600;
    font-size: clamp(1.4rem, 2.4vw, 2rem);
    letter-spacing: 0.05em;
    text-transform: uppercase;
    line-height: 1.1;
  }
  h2 {
    margin: 0 0 0.4rem;
    font-family: var(--font-display);
    font-weight: 600;
    font-size: 1.05rem;
    letter-spacing: var(--tracking-label);
    text-transform: uppercase;
  }
  p { margin: 0.2rem 0; }
  .summary { font-size: 1.05rem; }
  .pace, .footer { color: var(--charcoal-muted); }
  .footer { margin-top: 1rem; }
  .footer.failed { color: var(--error); }
  .attention {
    margin: 1rem 0;
    padding: 0.6rem 1rem;
    border: 1px solid var(--error);
    border-left-width: 4px;
    border-radius: 6px;
    background: var(--parchment);
  }
  .attention ul { margin: 0; padding-left: 1.2rem; }
  .fits {
    width: 100%;
    margin-top: 1rem;
    border: 1px solid var(--surface-border-strong);
    border-radius: 8px;
    border-spacing: 0;
    background: var(--surface-card);
    font-variant-numeric: tabular-nums;
    overflow: hidden;
  }
  .fits th, .fits td {
    padding: 0.3rem 0.6rem;
    border-bottom: 1px solid var(--surface-border);
    text-align: left;
    vertical-align: top;
  }
  .fits tbody tr:last-child td { border-bottom: 0; }
  .fits thead th {
    color: var(--charcoal-muted);
    font-size: 0.76rem;
    letter-spacing: var(--tracking-label);
    text-transform: uppercase;
    background: var(--parchment);
  }
  .fits td.num { text-align: right; }
  .fits td.remark { color: var(--charcoal-muted); font-size: 0.9em; }
  .state { font-weight: 600; }
  .state-running { color: var(--state-ready); }
  .state-pending { color: var(--charcoal-muted); font-weight: 400; }
  .state-done { color: var(--charcoal); }
  .state-canceled, .state-stale { color: var(--error); }
  .bar {
    display: block;
    height: 0.3rem;
    margin-top: 0.2rem;
    border-radius: 2px;
    background: var(--surface-border);
  }
  .bar > i {
    display: block;
    height: 100%;
    border-radius: 2px;
    background: var(--state-ready);
  }
  a { color: var(--focus); }
  @media (max-width: 640px) {
    body { margin: 1rem auto; }
    .table-scroll { overflow-x: auto; }
    .fits { min-width: 36rem; }
  }
</style>
</head>
<body>
<main id="status" data-kind="{{kind}}">
  <header>
    <p class="eyebrow" id="eyebrow"></p>
    <h1>{{pack}}</h1>
    <p class="summary" id="summary">{{headline}}</p>
    <p class="pace" id="pace"></p>
  </header>
  <section class="attention" id="attention" hidden>
    <h2>Attention</h2>
    <ul></ul>
  </section>
  <div class="table-scroll">
    <table class="fits">
      <thead id="head"></thead>
      <tbody id="rows"></tbody>
    </table>
  </div>
  <p class="footer" id="footer"></p>
</main>
<noscript>
  <p>{{headline}}. This page updates itself with JavaScript; the same numbers
  are at <a href="status.json">status.json</a>.</p>
</noscript>
<script type="application/json" id="status-data">{{data}}</script>
<script>
(function () {
  "use strict";

  var REFRESH_MS = {{refresh}} * 1000;
  var KIND_LABEL = { pack: "pack run", joint: "joint fit", ablation: "ablation of joint fits" };

  var status = JSON.parse(document.getElementById("status-data").textContent);
  var main = document.getElementById("status");
  var eyebrow = document.getElementById("eyebrow");
  var summary = document.getElementById("summary");
  var paceLine = document.getElementById("pace");
  var attention = document.getElementById("attention");
  var attentionList = attention.querySelector("ul");
  var head = document.getElementById("head");
  var rows = document.getElementById("rows");
  var footer = document.getElementById("footer");

  var inFlight = false;
  var lastRead = null;

  function pad(value) { return (value < 10 ? "0" : "") + value; }
  function clock(date) { return pad(date.getHours()) + ":" + pad(date.getMinutes()) + ":" + pad(date.getSeconds()); }
  function shortClock(date) { return pad(date.getHours()) + ":" + pad(date.getMinutes()); }

  function duration(ms) {
    var seconds = Math.round(ms / 1000);
    if (seconds < 60) { return seconds + "s"; }
    var minutes = Math.floor(seconds / 60);
    seconds -= minutes * 60;
    if (minutes < 60) { return minutes + "m" + pad(seconds) + "s"; }
    var hours = Math.floor(minutes / 60);
    minutes -= hours * 60;
    return hours + "h" + pad(minutes) + "m";
  }

  function score(value) { return value == null ? "n/a" : value.toFixed(6); }

  function keytrack(note) {
    if (note.keytrack != null) { return note.keytrack.toFixed(4); }
    return note.arm === "fixed" ? "—" : "";
  }

  function evaluations(note) {
    if (!(note.budget > 0)) { return String(note.evaluations); }
    return note.evaluations + "/" + note.budget + " (" + Math.round(100 * note.evaluations / note.budget) + "%)";
  }

  function silence(note, current) {
    return Date.parse(current.read) - Date.parse(note.last_write);
  }

  function remark(note, current) {
    if (note.stale) { return "no write for " + duration(silence(note, current)); }
    if (note.state === "canceled") { return "will be repeated by the next run"; }
    if (note.state === "running") { return "current " + score(note.current); }
    return "";
  }

  function el(tag, text, className) {
    var node = document.createElement(tag);
    if (text != null) { node.textContent = text; }
    if (className) { node.className = className; }
    return node;
  }

  function stateCell(note) {
    var cell = el("td", note.state, "state state-" + note.state);
    if (note.stale) {
      cell.className += " state-stale";
      cell.textContent = note.state + " (stale)";
    }
    if (note.state === "running" && note.budget > 0) {
      var bar = el("span", null, "bar");
      var fill = el("i");
      fill.style.width = Math.min(100, 100 * note.evaluations / note.budget).toFixed(1) + "%";
      bar.appendChild(fill);
      cell.appendChild(bar);
    }
    return cell;
  }

  function noteName(note) { return note.name || String(note.note); }

  function titleOf(current) {
    if (current.kind === "joint") { return current.pack + " -- joint fit"; }
    return current.pack + " -- " + current.finished + "/" + current.notes.length;
  }

  function summaryOf(current) {
    var total = current.notes.length;
    if (current.kind === "joint") {
      var only = current.notes[0];
      return only.state === "running" ? "one joint fit, " + evaluations(only) : "one joint fit, " + only.state;
    }
    var running = current.notes.filter(function (note) { return note.state === "running" && note.budget > 0; });
    var line = current.finished + "/" + total + " done";
    if (running.length === 1) {
      line += ", " + Math.round(100 * running[0].evaluations / running[0].budget) + "% through the current fit";
    } else if (running.length > 1) {
      line += ", " + running.length + " running";
    }
    if (current.canceled) { line += ", " + current.canceled + " cancelled"; }
    if (current.stale) { line += ", " + current.stale + " stale"; }
    return line;
  }

  function paceOf(current) {
    if (!(current.pace_ms > 0)) { return "no fit finished yet"; }
    if (current.running === 0 && current.pending === 0) {
      return "done, " + duration(current.elapsed_ms) + " spent";
    }
    var eta = new Date(Date.parse(current.read) + current.remaining_ms);
    return duration(current.pace_ms) + " per fit (median), " + duration(current.elapsed_ms) +
      " spent, about " + duration(current.remaining_ms) + " left, ETA " + shortClock(eta);
  }

  function fillHead(columns) {
    var row = el("tr");
    columns.forEach(function (column) { row.appendChild(el("th", column)); });
    head.replaceChildren(row);
  }

  function ablationRow(note, current) {
    var row = el("tr");
    var pending = note.state === "pending";
    row.appendChild(el("td", note.name));
    row.appendChild(el("td", note.arm || ""));
    row.appendChild(stateCell(note));
    row.appendChild(el("td", pending ? "" : score(note.best), "num"));
    row.appendChild(el("td", pending ? "" : keytrack(note), "num"));
    row.appendChild(el("td", pending ? "" : evaluations(note), "num"));
    row.appendChild(el("td", pending ? "" : duration(note.elapsed_ms), "num"));
    row.appendChild(el("td", remark(note, current), "remark"));
    return row;
  }

  function noteRow(note, current) {
    var row = el("tr");
    row.appendChild(el("td", noteName(note)));
    row.appendChild(stateCell(note));
    row.appendChild(el("td", evaluations(note), "num"));
    row.appendChild(el("td", score(note.best), "num"));
    row.appendChild(el("td", score(note.current), "num"));
    row.appendChild(el("td", duration(note.elapsed_ms), "num"));
    row.appendChild(el("td", remark(note, current), "remark"));
    return row;
  }

  function render(current) {
    var ablation = current.kind === "ablation";

    main.setAttribute("data-kind", current.kind);
    eyebrow.textContent = KIND_LABEL[current.kind] || current.kind;
    summary.textContent = summaryOf(current);
    paceLine.textContent = paceOf(current);

    var problems = current.notes.filter(function (note) { return note.stale || note.state === "canceled"; });
    attentionList.replaceChildren();
    problems.forEach(function (note) {
      attentionList.appendChild(el("li", noteName(note) + ": " + remark(note, current)));
    });
    attention.hidden = problems.length === 0;

    fillHead(ablation
      ? ["fit", "arm", "state", "score", "keytrack", "evaluations", "elapsed", ""]
      : ["note", "state", "evaluations", "best", "current", "elapsed", ""]);

    var body = document.createDocumentFragment();
    current.notes.forEach(function (note) {
      body.appendChild(ablation ? ablationRow(note, current) : noteRow(note, current));
    });
    rows.replaceChildren(body);

    document.title = titleOf(current);
    footer.textContent = "read " + clock(new Date(Date.parse(current.read))) +
      ", refreshing every " + (REFRESH_MS / 1000) + "s";
    footer.className = "footer";
  }

  // One request at a time, none while the tab is hidden, and a timeout so a
  // connection that neither answers nor fails cannot stop every later poll.
  function tick() {
    if (document.hidden || inFlight) { return; }
    inFlight = true;

    var controller = typeof AbortController === "function" ? new AbortController() : null;
    var timer = controller ? setTimeout(function () { controller.abort(); }, 2 * REFRESH_MS) : null;
    var options = { cache: "no-store" };
    if (controller) { options.signal = controller.signal; }

    fetch("status.json", options)
      .then(function (response) {
        if (!response.ok) { throw new Error("HTTP " + response.status); }
        return response.json();
      })
      .then(function (next) {
        status = next;
        lastRead = new Date();
        render(status);
      })
      .catch(function () {
        // The table keeps the numbers it had; only the footer says they are old.
        footer.textContent = "last read " + clock(lastRead || new Date(Date.parse(status.read))) +
          ", retrying every " + (REFRESH_MS / 1000) + "s";
        footer.className = "footer failed";
      })
      .then(function () {
        if (timer) { clearTimeout(timer); }
        inFlight = false;
      });
  }

  render(status);
  setInterval(tick, REFRESH_MS);
  document.addEventListener("visibilitychange", function () {
    if (!document.hidden) { tick(); }
  });
})();
</script>
</body>
</html>
`
