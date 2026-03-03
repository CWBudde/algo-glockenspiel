# Algo Glockenspiel Web Demo

Browser demo for the default glockenspiel preset using Go WebAssembly plus a small JavaScript mixer.

## Build

```bash
./scripts/build-wasm.sh
```

Or:

```bash
mkdir -p web/dist
GOOS=js GOARCH=wasm go build -o web/dist/glockenspiel.wasm ./cmd/glockenspiel-wasm
cp "$(go env GOROOT)/misc/wasm/wasm_exec.js" web/
```

## Serve

```bash
python3 -m http.server -d web 8080
```

Then open `http://localhost:8080`.

## Usage

- Click or tap bars to strike notes
- Use the printed keyboard bindings for quick play
- Adjust `Velocity` for attack strength
- Adjust `Level` for overall output gain
