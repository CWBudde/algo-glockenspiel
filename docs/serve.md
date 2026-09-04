# Serving the web app

`glockenspiel serve` hosts the browser front end on a local port:

```bash
just build-web          # build the app and the module into web/dist
go run ./cmd/glockenspiel serve
```

Then open <http://localhost:8080>.

## Flags

| Flag         | Default                              | Meaning                                                     |
| ------------ | ------------------------------------ | ----------------------------------------------------------- |
| `--addr`     | `:8080`                              | Listen address, for example `:9000` or `127.0.0.1:8080`.    |
| `--dist`     | `web/dist`                           | Directory holding the built app and the WebAssembly module. |
| `--work-dir` | `<user cache dir>/glockenspiel/fits` | Directory every served fit writes its run directory under.  |

The work directory defaults to the user's cache directory — `~/.cache/glockenspiel/fits`
on Linux — rather than to the working directory, because `serve` is run from
wherever the user happens to be and a run directory appearing under whatever
that was would be a surprise. Point it somewhere else with `--work-dir` when the
runs are meant to be collected, for example `--work-dir out/serve`.

## Routes

| Route                        | Served from                                                             |
| ---------------------------- | ----------------------------------------------------------------------- |
| `/`                          | `index.html` in `--dist`.                                               |
| `/assets/…`, `/wasm_exec.js` | `--dist` on disk.                                                       |
| `/glockenspiel.wasm`         | `--dist` on disk.                                                       |
| `/api/version`               | JSON `{"version": "…"}`, the same string `glockenspiel version` prints. |
| `/api/fit…`                  | The fit API, below.                                                     |

The static routes and `/api/version` accept only `GET` and `HEAD`; anything else
gets a `405` with an `Allow` header. There are no directory listings, and there
is no fallback to `index.html` for an unknown path: a silent fallback would turn
a mistyped asset path into a page that loads and then misbehaves. The front end
routes on the URL fragment (`#/play`, `#/optimize`) precisely so that a second
tab needs no route here.

Every response carries `Cache-Control: no-cache` and an `ETag` derived from the
file's content, which keeps a reload down to a `304` while nothing has changed —
and, unlike a modification time, still delivers a module that was rebuilt inside
the same second. The bundle's own assets already have content-hashed names, so
they revalidate for free.

## What is embedded and what is not

Almost nothing. The binary carries a single `placeholder.html` and reads
everything else from `--dist`.

The app is a Vite bundle with content-hashed file names, and the WebAssembly
module is a compiler output. Both live in `web/dist`, which is gitignored and
only exists after `just build-web`, while `go:embed` reads the working tree
rather than git — so embedding either would produce a binary whose contents
depend on whether someone happened to run a build step first. It also keeps
`go build ./...` working on a machine with no Node installed, which every CI job
depends on.

The server therefore says so when a half is not there:

- at startup, on stderr, once per missing half:

  ```
  warning: The web app has not been built: web/dist/index.html was not found.
  It is a build artifact and is not part of a checkout. Build it with `just build-web` and reload this page.
  ```

- per request. A missing `index.html` answers `/` with the embedded placeholder
  page and a `503`; a missing `glockenspiel.wasm` answers with the same text as
  above and a `503`, so the browser's network tab and console name the fix
  rather than showing an anonymous `404`.

Only those two files are reported that way, and only when they are _missing_.
Any other absent file is an ordinary `404`. A file that exists but cannot be
read — wrong permissions, an I/O error, a symlink leading out of `--dist` — is a
`500` with the real cause in the server log, because rebuilding would not fix
any of those.

The check runs per request, so a build finished while the server is running is
picked up on the next reload; no restart is needed.

## The fit API

`glockenspiel fit`, reachable over HTTP. The server runs exactly **one** fit at a
time: fitting is CPU-bound and evaluates candidates on every core, so a second
concurrent run would not do twice the work, it would do the same work half as
fast in each. A second start request is therefore not refused — it is queued,
and it begins when the fit ahead of it ends.

| Route             | Method | Meaning                                               |
| ----------------- | ------ | ----------------------------------------------------- |
| `/api/fit/start`  | `POST` | Upload a reference and start a fit. `202` on success. |
| `/api/fit/cancel` | `POST` | Stop a fit and wait for it to stop.                   |
| `/api/fit`        | `GET`  | The most recent job's state.                          |
| `/api/fit/events` | `GET`  | Server-Sent Events carrying the same state, live.     |
| `/api/fit/preset` | `GET`  | The fitted preset, as preset JSON.                    |
| `/api/fit/audio`  | `GET`  | The fitted preset rendered as a 16-bit mono WAV.      |
| `/api/fit/jobs`   | `GET`  | The job history, newest first.                        |

The unnumbered routes all mean "the most recent job", which is the one a client
watching a fit is watching. The per-job routes below mean a named one.

The write routes accept `POST` and nothing else. That gate is a separate one
from the read gate on purpose: loosening the existing gate to admit `POST` would
have opened the static tree, the wasm and the version endpoint to writes at the
same time.

### The queue

A start request is answered `202` with a snapshot whose state is `running` when
nothing is ahead of it and `queued` when something is. A queued job has a job id
and a run directory from the moment it is accepted, so a client can watch it,
name it, and cancel it before it has done any work.

`POST /api/fit/cancel?job=<id>` cancels the job it names, whether that job is
running or still waiting. A waiting one is dropped and recorded as `canceled`
with the stop reason `canceled_while_queued`; it never runs. Cancelling a job
that has already finished, or one that does not exist, is a `409` — the client
is asking to stop something that is not going to stop. Without `?job=`, cancel
means the most recent job.

Shutting the server down empties the queue and cancels whatever is running, so
no search outlives the process that started it.

### One job

| Route                          | Method | Meaning                                         |
| ------------------------------ | ------ | ----------------------------------------------- |
| `/api/fit/jobs/{id}`           | `GET`  | That job's state, in the shape `/api/fit` uses. |
| `/api/fit/jobs/{id}/preset`    | `GET`  | Its fitted preset, as preset JSON.              |
| `/api/fit/jobs/{id}/audio`     | `GET`  | Its preset rendered as a WAV.                   |
| `/api/fit/jobs/{id}/trace`     | `GET`  | Its `trace.jsonl`, as `application/x-ndjson`.   |
| `/api/fit/jobs/{id}/reference` | `GET`  | The signal the objective scored, as a WAV.      |
| `/api/fit/jobs/{id}/compare`   | `GET`  | Both signals as one comparison payload.         |

An `{id}` that names no job is a `404`. An `{id}` that is not a job id at all —
one carrying a path separator or a `..` — is a `400`, refused before anything is
looked up. Nothing a client sends ever reaches the filesystem either way: a
job's directory is the one the server recorded when it made it, never one built
from the URL.

The audio route renders the preset on demand, exactly as `/api/fit/audio` does,
so `?note=`, `?velocity=` and `?duration=` mean the same thing for a fit from
last week as for the one running now. The run directory's `render.wav` is
`fitrun`'s own artifact at one fixed note and length, and it is not what this
route serves.

The reference route serves the run directory's `reference.wav`, which is the
cut, downmixed, peak-normalised mono the objective actually scored, and not the
upload. An A/B against the render has to be against that one, or part of the
difference a listener hears is the loader's rather than the fit's.

The compare route answers with both signals described the same way:

```json
{
  "sampleRate": 44100,
  "seconds": 1.5,
  "columns": 1024,
  "frames": 128,
  "floorDb": -74.2,
  "reference": { "samples": 66150, "waveform": {}, "spectrogram": {} },
  "render": { "samples": 66150, "waveform": {}, "spectrogram": {} }
}
```

The span and the floor are stated once because both sides share them. A
reference longer than the render cap is cut to it rather than drawn whole beside
a clamped render, so the same column is the same moment on both sides; and both
spectrograms are painted against the reference's noise-aware floor, which is
what the objective scores both signals against. A render given a floor of its
own would show detail the score counted as nothing.

`?columns=` sets the waveform envelope's width and `?frames=` the
spectrogram's, capped at 4096 and 256, so the payload's size follows what was
asked for rather than how long the reference is. A waveform column is the
lowest and the highest sample of its span; a spectrogram column is the loudest
value of each of 256 frequency rows, because a partial occupies one bin whose
neighbours hold nothing and a mean would fade out exactly what the picture is
of. A signal shorter than one analysis frame carries no spectrogram at all,
which is the same signal the objective measures no spectral term for.

The transform is `optimizer.ComputeSpectrogram`, the composite objective's own,
noise-aware floor included. That is the point of serving a picture instead of
two WAV files: a partial the eye finds in the picture is one the score counted.

### What a snapshot says

Every snapshot — the start reply, `/api/fit`, `/api/fit/jobs/{id}` and each SSE
frame — carries the whole state of one job, and that includes its provenance:

- `request` is every setting the fit ran under, the defaults the client never
  sent included, plus the seed the backend actually drew and the worker count it
  sized to the machine. Both seeds and the resolved `seed` are decimal strings,
  because they are int64 and a JavaScript `Number` loses the low bits of one
  past 2^53. An uploaded search box is echoed under `bounds`; the default box is
  not, because it is drawn from the reference's own measured fundamental rather
  than being a constant.
- `profile` is the active metric's weights and norms, one entry per term the
  score counts. It is sent so that a per-term display and the score beside it
  cannot disagree: both are scaled by the same numbers. A single-term legacy
  metric has no profile and the field is absent.
- `metrics` is the raw breakdown of the best point so far, `gain_db` — the level
  gain solved and applied before the spectral and envelope terms — included. It
  arrives with the first report and is renewed at every report that improves,
  which is every report that carries a breakdown in `trace.jsonl`.
- `evaluationsPerSecond` is throughput. `budgetFraction` is how much of the
  tightest binding budget the run has spent, the larger of iterations over the
  iteration cap and elapsed over the time budget. It is not an ETA: a run stops
  at the first budget that binds, so a search that converges ends well below 1.
- `converged` is the backend's own verdict, not a state: the run stopped on a
  convergence criterion rather than on its budget.
- `restart` counts CMA-ES restarts and `epoch` counts mayfly rounds. They are
  the same counter inside the optimizer read under two meanings, so exactly one
  of them is ever present and a reader never has to know which backend ran.

### History and restarts

`/api/fit/jobs` lists every job newest first: its id, state, start and finish,
the best cost it reached, the score it recorded, and the note, velocity,
optimizer and metric it was asked for.

The list survives a restart. On startup the work directory is read back, and
every directory in it holding a `config.json` becomes a job again. `fitrun`
writes `config.json` before the search and `result.json` after it, so a
directory holding the first and not the second is a fit that died with the
process running it: it comes back as `failed` with the stop reason
`interrupted`, never as `running`. A run whose `result.json` records a cancelled
stop reason comes back as `canceled`, and everything else as `succeeded`. It is
the same rule `glockenspiel-campaign status` reads a campaign's directories by,
because these are the same directories.

Every run directory stays on disk for good; only the list a running process
holds is bounded, at the **200 newest**. A job past that cap is still on disk,
is still read by the campaign tooling, and comes back if the server is restarted
over the same work directory — it is simply not in `/api/fit/jobs` until then.

### Where a fit is run

The server does not fit anything itself. A start request is validated, written
into a fresh run directory, and handed to `internal/fitrun` — the same engine
`glockenspiel fitrun` and the training campaign use. A fit started from the
browser therefore leaves exactly the directory a campaign job leaves, and the
campaign's collect step can read it without knowing which front end produced it.

The directory and the job id are one string, `fit-<UTC yyyymmddThhmmss>-<counter>`,
so a client holding a job id can find the run it names. It sorts
chronologically and it is safe as a path segment; nothing a client sends is ever
part of it, which stays the cheapest possible answer to path traversal.

| File              | What it holds                                                          |
| ----------------- | ---------------------------------------------------------------------- |
| `upload.wav`      | The uploaded recording, byte for byte.                                 |
| `reference.wav`   | The signal the objective scored: cut, downmixed, peak-normalised mono. |
| `analysis.json`   | The partials measured from that same cut.                              |
| `config.json`     | The whole design of the run, and what the backend resolved for itself. |
| `trace.jsonl`     | One line per progress report.                                          |
| `checkpoint.json` | The state a resume would read.                                         |
| `preset.json`     | The fitted preset, with its provenance block.                          |
| `render.wav`      | That preset rendered over the reference's length.                      |
| `result.json`     | The summary the campaign scores from.                                  |
| `log.txt`         | The lines `fitrun` would have printed to a terminal.                   |

`upload.wav` and `reference.wav` are both there on purpose: the first is what
was sent, the second is what was scored. `/api/fit/audio` serves neither — it
renders the fitted preset on demand, so `?note=`, `?velocity=` and `?duration=`
can differ from the fit's own.

### Starting a fit

`POST /api/fit/start` takes a `multipart/form-data` body. The only required part
is `reference`, a WAV file. Everything else mirrors a flag of the `fit` command
and keeps that command's default when it is absent, with one deliberate
exception: `optimizer` stays `simple` here and in the web form, while the CLI
defaults to `cmaes`. A request that says nothing about the backend gets the
cheap, predictable run a browser session expects; phase 8.4 moved only the CLI
default.

| Field                                             | Default            | Notes                                                                                                                                                                                                                                                                                                                                                                                    |
| ------------------------------------------------- | ------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `reference` (file)                                | required           | Read as `fit` reads it: one channel, cut to the first strike, peak-normalised.                                                                                                                                                                                                                                                                                                           |
| `preset` (file)                                   | built-in           | An optional starting preset, as JSON.                                                                                                                                                                                                                                                                                                                                                    |
| `bounds` (file)                                   | default box        | Optional search bounds, as JSON. See below.                                                                                                                                                                                                                                                                                                                                              |
| `note`, `velocity`                                | 69, 100            | MIDI, `0`–`127`.                                                                                                                                                                                                                                                                                                                                                                         |
| `optimizer`                                       | `simple`           | `simple`, `mayfly` or `cmaes`.                                                                                                                                                                                                                                                                                                                                                           |
| `metric`                                          | `balanced`         | A composite profile, `balanced`, `placement` or `polish`, or a legacy term, `rms`, `log` or `spectral`.                                                                                                                                                                                                                                                                                  |
| `downmix`, `window`                               | `first`, none      | As `--downmix` and `--window`: which channel of a multi-channel file, and a fixed cut length after the onset instead of the strike's own end. The window is a duration, at most an hour.                                                                                                                                                                                                 |
| `modes`                                           | `0`                | As `--modes`: `0` seeds one starting mode per partial the reference's analysis lists, `N` the strongest `N`, `-1` keeps the starting preset's own modes. A seeded fit writes a v2 preset.                                                                                                                                                                                                |
| `maxIterations`                                   | `100`              | At most 100000.                                                                                                                                                                                                                                                                                                                                                                          |
| `timeBudget`                                      | `30s`              | A Go duration, or a bare number of seconds. At most `1h`.                                                                                                                                                                                                                                                                                                                                |
| `reportEvery`                                     | `10`, `1` (mayfly) | How often progress is reported, and therefore streamed. Counted in the backend's own iterations, which is why the default follows it: a mayfly iteration is a whole generation, roughly fifty renders, against about one for a simple iteration, so ten would mean the first report lands after the default time budget has already run out. A value the request names is used as it is. |
| `align`, `normalizeGain`                          | on, off            | As `--align` and `--normalize-gain`.                                                                                                                                                                                                                                                                                                                                                     |
| `mayflyVariant`, `mayflyPopulation`, `mayflySeed` | `desma`, `10`, `1` | Only read for `--optimizer mayfly`.                                                                                                                                                                                                                                                                                                                                                      |
| `mayflyPreset`                                    | none               | A Mayfly preset name. Cannot be combined with `mayflyVariant`.                                                                                                                                                                                                                                                                                                                           |
| `mayflyEpochs`, `mayflyRestarts`                  | `1`, `0`           | Warm and cold rounds. Each in `[1,1000]` and `[0,1000]`.                                                                                                                                                                                                                                                                                                                                 |
| `mayflyStagnation`                                | `0`                | Stop a round after N iterations without progress. Must be narrower than a round.                                                                                                                                                                                                                                                                                                         |
| `mayflyTargetCost`                                | none               | Stop once the best cost reaches it. Absent and `0` differ.                                                                                                                                                                                                                                                                                                                               |
| `mayflyNc`, `mayflyNcRatio`                       | none               | Crossover offspring count and ratio. `mayflyNc` accepts `-1` for automatic and `0` for none.                                                                                                                                                                                                                                                                                             |
| `mayflySelection`                                 | mayfly's default   | `rank` or `tournament`.                                                                                                                                                                                                                                                                                                                                                                  |
| `mayflyTuning` (file)                             | none               | The Mayfly tuning document, as JSON. See below.                                                                                                                                                                                                                                                                                                                                          |
| `cmaesCovariance`                                 | `separable`        | Only read for `optimizer=cmaes`. `separable` learns the diagonal, `block` a dense matrix per mode. Anything else is a `400`.                                                                                                                                                                                                                                                             |
| `cmaesLambda`, `cmaesSigma`                       | `0`, `0.3`         | Population per generation and initial step size. A zero population takes Hansen's `4 + floor(3 ln n)`; the step size is a fraction of the normalized box, at most `1`, and a zero takes the backend's default of `0.3`.                                                                                                                                                                  |
| `cmaesSeed`, `cmaesRestarts`                      | `0`, `0`           | A zero seed asks the backend to pick one. A zero restart count restarts until the budget is spent; at most 1000.                                                                                                                                                                                                                                                                         |

### Mayfly tuning

`mayflyTuning` is a file part, like `bounds`, and holds the same document
`fit --mayfly-tuning` reads, parsed by the same code. Every key is optional and
an omitted key keeps whatever the dialect already chose; unknown keys and
trailing content are rejected. The key list is in
[mayfly-tuning.md](mayfly-tuning.md), and [optimizer.md](optimizer.md) explains
how the document fits into a run.

A malformed document is a `400` on the start request. It never claims the single
fit slot, so the next well-formed request still starts rather than getting a
`409`.

Every snapshot after the first report carries `metrics`, the breakdown of the best point so
far: each term of the composite objective as a raw number, the gain applied, the alignment lag,
and how many of the reference's partials the candidate's modes matched, whatever metric the run
scores by. A term the reference was too short to measure is `null`. Every snapshot carries
`seededModes`, how many of the starting modes came from the reference's partials, and a
terminal one carries `pinned`: the dimensions of the result that sit on a bound of the search
box, each with its name in the preset's own field names, its value and the bound it sits on.

A CMA-ES snapshot carries `restart`, the zero-based index of the search in
progress. Only that backend restarts, so the field is absent for every other one
and for the first run of a CMA-ES fit.

Progress snapshots carry `mayflyVariant` and `mayflySeed`. Without them a client
that named a preset, which selects a dialect of its own, could never learn which
dialect actually ran. `mayflySeed` is a **string**, because a JavaScript
`Number` loses integers past 2^53.

### Bounds

`bounds` is the same document `fit --bounds` reads, parsed by the same code.
Every key is optional and holds one `[min, max]` pair; an omitted key keeps the
corresponding default bound, so a document can narrow a single dimension without
restating the rest. Unknown keys are rejected rather than ignored — a misspelled
dimension that was silently dropped would run the fit against the default box
while the client believed it had narrowed one, and so is anything following the
object: a second document appended to the first would be dropped just as
quietly.

```json
{
  "input_mix": [0.0, 2.0],
  "filter_freq": [500.0, 8000.0],
  "amplitude": [-1.0, 1.0],
  "frequency": [400.0, 12000.0],
  "decay_ms": [50.0, 400.0],
  "harmonic_gain": [0.0, 1.0]
}
```

`frequency` is a mode's frequency in hertz as the preset writes it. Without the
part the service narrows it to half the reference's fundamental up to 0.45 of
the sample rate, and `decay_ms` to what a preset at the starting preset's note
may carry; a document replaces the whole box. `base_frequency` and
`frequency_mult`, the keys from before Phase 8.3, are refused with a reason.

Supplied bounds are a **hard constraint**, exactly as on the command line. The
default box is widened where the starting preset falls outside it; a box the
client asked for is not, or the fitted preset could violate the very limits that
were requested. The starting point is clamped into the box instead. Malformed
JSON, trailing content after the object, an unknown key, an inverted or empty
range, a range a log-encoded dimension cannot take (`filter_freq`, `frequency`
and `decay_ms` must stay above zero) and a
range that leaves the model's own domain (`input_mix` beyond `[0, 2]`, say) are
each a `400` before a job slot is claimed.

Like the reference and the starting preset, the part is read from bytes in
memory and its filename is never touched.

```bash
curl -s -X POST http://localhost:8080/api/fit/start \
  -F reference=@a4.wav -F bounds=@bounds.json -F timeBudget=1m
```

There is no `sampleRate` field. The CLI has `--sample-rate` and errors when it
disagrees with the file; over an upload that check could only ever be the client
restating what it just sent, so the rate comes from the reference itself and is
reported back in every status.

There is no work directory and no checkpointing either. Both are file-shaped and
the API is deliberately not: **nothing a client sends is used to build a
filesystem path**, which is the cheapest possible answer to path traversal on
what is the server's first write surface. The uploaded reference stays in
memory, the uploaded part's filename is never read, and the fitted preset is
held in memory for the read endpoints rather than written anywhere.

```bash
curl -s -X POST http://localhost:8080/api/fit/start \
  -F reference=@a4.wav \
  -F optimizer=mayfly -F mayflyPopulation=20 -F maxIterations=200 -F timeBudget=2m
```

Uploads are bounded by `http.MaxBytesReader` at **16 MiB**, which is about three
minutes of 16-bit mono at 44.1 kHz. That is an order of magnitude more than a
fit ever wants — the objective renders and scores the whole reference once per
candidate evaluation, so a three-minute reference makes a hundred-iteration run
take hours — and small enough that the decoded form stays bounded. An oversized
body is a `413`.

The reference's declared sample rate is bounded too, at **4 kHz to 192 kHz**.
A WAV header states its rate as an unsigned 32-bit number, and that rate becomes
the job's: it multiplies every later allocation, so an upload claiming a
multi-gigahertz rate would ask the audition render for a hundred billion
samples. A rate outside the range is a `400`, checked before a job slot is
claimed.

### Watching it

`GET /api/fit/events` is a Server-Sent Events stream. Each event carries a whole
status object rather than a delta, so a client that reconnects is immediately
correct rather than correct once it has replayed a history:

```
event: progress
data: {"jobId":"fit-1","state":"running","iteration":3,"bestCost":0.0412,…}

event: done
data: {"jobId":"fit-1","state":"succeeded","stopReason":"function_converge",…}
```

The stream opens with the current state, so attaching mid-run — or after the run
already ended — draws something immediately. Progress is fed from the optimizer's
existing `Progress` callback, the same one the CLI hangs checkpointing off, so
nothing inside the optimizer knows that HTTP exists. Missing a `progress` event
loses a point on a curve and nothing else; the terminal `done` event is never
missed, because a subscriber watches the job's completion directly.

`HEAD` on the stream answers with its headers and returns instead of entering
the loop: Go suppresses the body for `HEAD` but not the handler, so a probe that
streamed would hang until the fit ended.

A stream ends when the job ends, when the client goes away, or when the server
shuts down — in the last case with an `event: shutdown` first, so the browser
console names a reason instead of showing an anonymous dropped connection.

### Cancelling

`POST /api/fit/cancel` cancels through the `context.Context` the optimizer
already takes, and then **waits** for the run to actually stop before it answers.
That is what makes cancel-then-start work without polling: if it returned as soon
as the context was cancelled, a client that immediately started a new fit would
race the old goroutine's last evaluation and get a `409` it could do nothing
about but retry. Pass `?job=fit-1` to refuse the cancel if the current job is no
longer the one you meant to stop.

A cancelled run keeps the best parameters it found. Losing them would make
"cancel" mean "throw away the last ten minutes", which is the opposite of what
someone watching a cost curve flatten out wants — so `/api/fit/preset` answers
for a cancelled job too.

### Reading the result

`GET /api/fit/preset` returns the fitted preset in the same schema `fit --output`
writes, so it loads with `glockenspiel synth --preset`. `GET /api/fit/audio`
renders it: `?note=`, `?velocity=` and `?duration=` default to the job's own note
and velocity and to the reference's length, which is what makes the render and
the reference directly comparable. Duration is capped at 60 seconds — the
default included, so a reference longer than the cap renders its first 60
seconds rather than all of it — because the
whole file is built in memory before a byte is sent — which is also what keeps a
mid-encode failure a `500` rather than a truncated download the browser reports
as successful.

Both answer `404` before any fit has been started and `409` while the first one
is still running and has produced nothing yet.

### Shutdown

An SSE response never ends on its own, and `http.Server.Shutdown` waits for
active connections, so a stream left open would spend the entire shutdown
timeout on every Ctrl-C. `Run` therefore closes the streams — and cancels the
running fit — _before_ it calls `Shutdown`. A fit does not outlive the server
that started it.

## The browser side

The Optimize tab of the app this server hosts is a client of everything above:
it starts a fit from a form, watches `/api/fit/events`, draws the cost curve,
and auditions and downloads the result. See
[web-app.md](web-app.md#the-optimize-loop).

Nothing else is: the tab is the only consumer, and `curl` against the same port
remains a first-class way to drive a fit.
