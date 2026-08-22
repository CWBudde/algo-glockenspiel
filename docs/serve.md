# Serving the web app

`glockenspiel serve` hosts the browser front end on a local port:

```bash
just build-web          # produce web/dist/glockenspiel.wasm
go run ./cmd/glockenspiel serve
```

Then open <http://localhost:8080>.

## Flags

| Flag     | Default    | Meaning                                                  |
| -------- | ---------- | -------------------------------------------------------- |
| `--addr` | `:8080`    | Listen address, for example `:9000` or `127.0.0.1:8080`. |
| `--dist` | `web/dist` | Directory holding the WebAssembly build artifacts.       |

## Routes

| Route                                  | Served from                                                             |
| -------------------------------------- | ----------------------------------------------------------------------- |
| `/`                                    | The embedded `index.html`.                                              |
| `/main.js`, `/styles.css`, `/assets/…` | The embedded static tree.                                               |
| `/dist/glockenspiel.wasm`              | `--dist` on disk.                                                       |
| `/api/version`                         | JSON `{"version": "…"}`, the same string `glockenspiel version` prints. |
| `/api/fit…`                            | The fit API, below.                                                     |

The static routes and `/api/version` accept only `GET` and `HEAD`; anything else
gets a `405` with an `Allow` header. There are no directory listings: the server
holds a map of known files, so a request for a directory is simply a `404`.

Nothing is content-addressed yet, so every response carries `Cache-Control:
no-cache`. Both the embedded files and the wasm additionally carry an `ETag`
derived from their content, which keeps a reload down to a `304` while nothing
has changed — and, unlike a modification time, still delivers a module that was
rebuilt inside the same second. Fingerprinted asset names are Phase 5.3.

## What is embedded and what is not

The hand-written part of the app — `index.html`, the scripts, the stylesheet
and `web/assets/` — is compiled into the binary by `web/embed.go`. The
WebAssembly module is not. `web/dist` is gitignored and only exists after
`just build-web`, while `go:embed` reads the working tree rather than git, so
embedding it would produce a binary whose contents depend on whether someone
happened to run a build step first — silently, with a page that loads and then
cannot make a sound.

The server therefore reads the module from disk and says so when it is not
there:

- at startup, on stderr:

  ```
  warning: The WebAssembly module is missing: web/dist/glockenspiel.wasm was not found.
  It is a build artifact and is not part of a checkout. Build it with `just build-web` (or ./scripts/build-wasm.sh) and reload this page.
  ```

- per request, as a `503` on `/dist/glockenspiel.wasm` carrying the same text,
  so the browser's network tab and console name the fix rather than showing an
  anonymous `404`.

Only a _missing_ module is reported that way. A module that exists but cannot be
read — wrong permissions, an I/O error, a symlink leading out of `--dist` — is a
`500` with the real cause in the server log, because rebuilding would not fix
any of those.

The check runs per request, so a build finished while the server is running is
picked up on the next reload; no restart is needed.

## The fit API

`glockenspiel fit`, reachable over HTTP. The server owns exactly **one** fit at a
time: fitting is CPU-bound and evaluates candidates on every core, so a second
concurrent run would not do twice the work, it would do the same work half as
fast in each — and it would make "the fitted preset" ambiguous for every read
endpoint.

| Route             | Method | Meaning                                               |
| ----------------- | ------ | ----------------------------------------------------- |
| `/api/fit/start`  | `POST` | Upload a reference and start a fit. `202` on success. |
| `/api/fit/cancel` | `POST` | Stop the running fit and wait for it to stop.         |
| `/api/fit`        | `GET`  | The current job's state.                              |
| `/api/fit/events` | `GET`  | Server-Sent Events carrying the same state, live.     |
| `/api/fit/preset` | `GET`  | The fitted preset, as preset JSON.                    |
| `/api/fit/audio`  | `GET`  | The fitted preset rendered as a 16-bit mono WAV.      |

The write routes accept `POST` and nothing else. That gate is a separate one
from the read gate on purpose: loosening the existing gate to admit `POST` would
have opened the static tree, the wasm and the version endpoint to writes at the
same time.

### Starting a fit

`POST /api/fit/start` takes a `multipart/form-data` body. The only required part
is `reference`, a WAV file. Everything else mirrors a flag of the `fit` command
and keeps that command's default when it is absent:

| Field                                             | Default            | Notes                                                     |
| ------------------------------------------------- | ------------------ | --------------------------------------------------------- |
| `reference` (file)                                | required           | Mono or the first channel of a multi-channel file.        |
| `preset` (file)                                   | built-in           | An optional starting preset, as JSON.                     |
| `note`, `velocity`                                | 69, 100            | MIDI, `0`–`127`.                                          |
| `optimizer`                                       | `simple`           | `simple` or `mayfly`.                                     |
| `metric`                                          | `rms`              | `rms`, `log` or `spectral`.                               |
| `maxIterations`                                   | `100`              | At most 100000.                                           |
| `timeBudget`                                      | `30s`              | A Go duration, or a bare number of seconds. At most `1h`. |
| `reportEvery`                                     | `10`               | How often progress is reported, and therefore streamed.   |
| `align`, `normalizeGain`                          | on, off            | As `--align` and `--normalize-gain`.                      |
| `mayflyVariant`, `mayflyPopulation`, `mayflySeed` | `desma`, `10`, `1` | Only read for `--optimizer mayfly`.                       |

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

## Scope

The browser side is Phase 4.3: nothing under `web/` calls any of this yet, so
fitting from a page still means fitting with `curl` against the same port the
page is served from.
