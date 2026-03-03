# plugin/vst3

This package is the repository's first VST3 integration seam.

It does not contain any SDK bindings yet. Instead, it defines:

- a stable plugin-facing parameter list
- stable parameter IDs and keys
- conversion helpers between plugin state and `internal/model.BarParams`

The intent is to keep the host integration layer thin when `vst3go` is added:

- parameter metadata should already exist
- controller/processor state mapping should already be testable
- DSP code stays in existing internal packages

The repository now also includes a first `vst3go`-backed Linux/`cgo` spike:

- [plugin.go](/mnt/projekte/Code/algo-glockenspiel/plugin/vst3/plugin.go)
- [processor_vst3go_linux.go](/mnt/projekte/Code/algo-glockenspiel/plugin/vst3/processor_vst3go_linux.go)
- [main_linux.go](/mnt/projekte/Code/algo-glockenspiel/cmd/glockenspiel-vst3/main_linux.go)

That spike currently:

- registers a VST3 plugin via `vst3go`
- exposes the first parameter set to the host
- advertises an instrument/generator bus layout
- outputs silence while the wrapper and parameter plumbing are validated

It does not yet render the actual glockenspiel synth or handle MIDI input.

At the moment the scaffold is guarded behind the `vst3go` build tag:

```bash
go test -tags=vst3go ./plugin/vst3
go build -tags=vst3go ./cmd/glockenspiel-vst3
```

That extra tag is necessary because `vst3go v0.1.1` currently fails to compile as
published: the Go module does not include the `include/vst3/vst3_c_api.h` header
tree expected by its `cgo` sources.
