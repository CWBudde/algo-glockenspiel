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

Only `GET` and `HEAD` are accepted; anything else gets a `405` with an `Allow`
header. There are no directory listings: the server holds a map of known files,
so a request for a directory is simply a `404`.

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

## Scope

This is the Phase 4.1 skeleton. Fitting over HTTP — the job manager, the JSON
endpoints and the SSE progress stream — is Phase 4.2 and is not here yet.
