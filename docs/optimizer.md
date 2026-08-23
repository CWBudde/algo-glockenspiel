# Optimizer design

The fitting stack in `internal/optimizer` renders the current model against a reference WAV and
scores each candidate with the RMS, log-RMS, or spectral objective. The CLI workflow and flags
are documented in [user-guide.md](user-guide.md); this note records the less visible contracts
that implementations and checkpoints depend on.

## Parameter space

Both optimizer backends search the same unit cube. `ParamCodec` first encodes model parameters,
using logarithmic coordinates for positive quantities such as frequencies and decay times, and
then `Bounds.Normalize` maps every encoded dimension to `[0,1]`. This keeps step sizes comparable
across dimensions whose physical ranges differ by orders of magnitude.

Candidates are clamped at the boundary. They are not mirrored: mirroring makes the objective a
folded, many-to-one map and gives a local optimizer artificial continuations to chase across a
bound. User-supplied `--bounds` are hard constraints. `ObjectiveConfig.StrictBounds` prevents the
codec from widening them to contain the template preset, and the initial point is clamped into
the requested box.

## Objective evaluation

The objective keeps the reference and candidate in floating point; it does not quantize through
PCM16 before scoring. `ProjectToPCM16Domain` remains only for reporting. The spectral metric is a
multi-frame STFT over the whole signal rather than a comparison of a single frame.

Onset alignment is enabled by default because a small leading offset can reverse the phase of a
high partial and make the correct parameters score worse than incorrect ones. Gain normalization
is optional and off by default: it is useful when the recording level is unknown, but it makes
the model's amplitude parameters unidentifiable.

`ObjectiveFunction.Evaluate` is safe for concurrent calls. Each evaluation borrows independent
mutable render state from a pool while the reference, codec, and alignment plan remain immutable,
so population-based backends can evaluate candidates in parallel without racing.

## Reading the reference

References are loaded through `internal/wavio`, which exists because the obvious way to read a
WAV here is wrong. `go-audio/wav` decodes every sample format as an integer, so a 32-bit IEEE
float file comes back as its own bit patterns divided by 2^31 — a square wave at roughly ±0.5
for any recording at a sane level. A 32-bit float WAV is what a DAW exports by default, so
every fit against one was fitting a square wave, silently and plausibly, and so was every
legacy-reference regression test.

`internal/wavio` is the single loader for the CLI, the server and the tests; there were three
copies before, which is how the defect survived in all of them at once. The float fixture is
documented in `testdata/reference/README.md`.

## Checkpoints and iteration counts

The checkpoint format is version `2.0`. Version `1.0` encoded decay linearly, so loading one under
the current logarithmic encoding would silently resume at different parameters. Old checkpoints
are therefore rejected with an explanatory error rather than reinterpreted.

Two counters have intentionally different meanings:

- `Iteration` counts progress reports and orders `checkpoint_*.json` files.
- `OptimizerIterations` is the backend's own iteration count, in the same unit as `--max-iter`.

Only `OptimizerIterations` may be subtracted from the remaining iteration budget on resume.
Mayfly checkpoints also retain the variant, population, and seed, but neither backend stores its
complete internal population or simplex. Resume continues from the best encoded parameter vector
and the remaining budget; it is a coarse restart, not a byte-for-byte continuation.

Checkpoint writes are atomic and durable: data is written and synced through a temporary file,
renamed into place, and followed by a directory sync where the platform supports it.
