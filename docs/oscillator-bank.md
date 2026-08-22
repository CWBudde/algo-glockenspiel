# The Oscillator Bank

`internal/oscbank` is the synthesis core: a bank of `N` decaying quadrature
oscillators, each carrying up to `M` integer-multiple harmonic partials. Both
counts are ordinary runtime values. The four-mode bar model it replaced encoded
its count in a `const NumModes = 4`, five `.s` files and a hand-unrolled scalar
loop.

## The recursion

Every rotor advances by one complex multiply per sample:

```
t  = im*cos + re*sin
re = re*cos - im*sin
im = amp*x + t
```

`cos` and `sin` are the phase-rotation coefficients already scaled by the
per-sample decay factor, so amplitude decay and phase advance cost nothing
extra. `t` is the rotor's contribution to the output. There is no `sin()` in the
inner loop and no state to renormalize, because the decay factor stays below 1 —
a sustained rotor at a decay factor of exactly 1 would need magnitude
renormalization, which this bank deliberately does not do.

## Harmonics

Harmonics are rotors, not waveshaping. Oscillator `n` with harmonic gains
`g[0..M-1]` expands into `M` rotors: rotor `k` runs at `(k+1) * frequency`,
shares the oscillator's decay, and carries amplitude `A * g[k]`. Partials are
therefore computed _on top of_ the oscillators rather than baked into the
excitation ahead of them.

The Chebyshev waveshaper survives as an optional stage on either side of the
bank (`chebyshev.stage`: `excitation` or `output`). `excitation` is the v1
placement and stays the default, which is what keeps v1 presets sounding the
same.

## Layout

State lives in five `[]float32` arrays — `re`, `im`, `cosCoeff`, `sinCoeff`,
`amp` — in AoSoA blocks of eight rotors, rounded up to an **even** number of
blocks. Two blocks is what the AVX2 kernel consumes per pass; the even count
also lets a future 16-lane AVX-512 kernel take two blocks at a time, with no
separate tail path anywhere. The 4-lane kernels take the same block pair rather
than half a block — see "The NEON kernel" and "The SSE2 kernel" for why the fold
order rather than the register width decides that.

Unused lanes carry zero coefficients and zero amplitude. They stay at zero
forever and contribute nothing, so no masking is needed.

## Accumulation

The old kernel reduced eight lanes to one scalar _per sample_, paying a
`VEXTRACTF128` plus `VHADDPS` plus two conversions inside the sample loop. The
bank does not. Each block pair accumulates into a per-frame partial buffer and
the horizontal sum happens once per chunk:

- the kernel folds a block's two 128-bit halves as it stores, which is free
  because the loop is latency-bound and the shuffle port is idle;
- `reduceLanes` collapses the remaining four partials per frame, eight frames at
  a time, at roughly two uops per sample.

Chunks are 256 samples, so the partial buffer is 4 KiB and stays in L1.

## The AVX2 kernel

`oscbank_avx2_amd64.s` keeps two blocks — sixteen rotors — in registers and
advances them together. One block alone cannot fill the vector ports: the
recursion is latency-bound, not throughput-bound.

The naive ordering puts three dependent operations between one sample's `im` and
the next: a multiply and an add to reach `t`, then another add for `amp*x + t`.
That is twelve cycles per sample. The kernel folds the excitation into the
accumulator seed instead,

```
im' = amp*x + re*sin + im*cos      two chained FMAs
t   = im' - amp*x
```

which leaves both `re'` and `im'` eight cycles from their inputs and takes `t`,
which nothing else depends on, off the critical path. `amp*x` for the next
sample is computed one iteration ahead, which is why the kernel reads one sample
past the end of its input and why the bank hands it a padded scratch buffer.

The portable kernel in `kernel_generic.go` associates the arithmetic the same
way, and is written so that it cannot fuse its multiply-adds on any target — see
"The portable kernel is one program" below, because the compiler will fuse them
given half a chance. The next section says exactly how far apart that leaves the
two.

## The NEON kernel

`oscbank_arm64.s` is the same program on 128-bit registers. A NEON vector holds
four float32, so one AoSoA block is two registers and the kernel still consumes
a block pair — sixteen rotors, four vectors — per pass. Taking a whole pair
rather than a half block at a time is not an efficiency choice: it is what keeps
the lane fold in `(A.lo + B.lo) + (A.hi + B.hi)` order, and rule two of the
contract has no tolerance.

`FMLA` and `FMLS` do the work `VFMADD231PS` and `VFNMADD231PS` do on amd64, in
the same places and in the same order, so the two kernels are bit-identical.
`golden_test.go` pins that with a vector of expected float32 words: it is the
only way to check rule one across architectures, since no process can run both
kernels. Measured against the reference on the same generated case, NEON on
arm64 and AVX2 on amd64 diverge in the same samples by the same amount, to the
last digit — which is the same claim arrived at from the other side.

There is no runtime gate. Advanced SIMD is mandatory in ARMv8-A, so there is no
arm64 machine that can run the binary and not run the kernel; `cpufeat` reports
`HasASIMD` unconditionally and a kernel must not consult it, because the OS
capability word it would otherwise be derived from comes back empty under
emulation.

The reduction is where the two architectures differ in shape rather than in
arithmetic. `FADDP` adds adjacent pairs of the concatenation of its two source
vectors, so two instructions halve four frames and a third finishes them —
`(a0+a1) + (a2+a3)` per frame, with no permute needed to repair the order,
because `FADDP` keeps the frames in place where `VHADDPS` interleaves them.

One practical note for anyone editing the file: Go's arm64 assembler has no
mnemonic for floating-point vector multiply, add, subtract or pairwise-add. Only
`VFMLA` and `VFMLS` exist. The kernel encodes the other four itself through
`WORD` macros, whose encodings `go tool objdump` decodes back to the expected
instruction names.

### The rounding barrier this kernel is the reason for

`advanceRotor` binds every product to its own `float32`, and the line most
easily mistaken for decoration is the last one, `newIm - ampx`. It is not
decoration, and this kernel is how that was found out.

Before the barriers existed, gc on arm64 compiled the reference's rotor step to
`FMADDS` twice for the accumulator seed and `FMULS`/`FMSUBS` for the real part —
the same arithmetic the kernel does, in the same places. But it also contracted
`t = next - ampx` into a single `FMSUBS next, amp, x`, because `ampx` is a
product and a subtract following a product fuses exactly as readily as an add
does. So the reference recovered `t` with one rounding where a packed kernel
needs two, having already materialised `amp*x` in a register and being unable to
un-round it.

That one instruction was the entire difference between this kernel and the
reference on arm64: put a rounding back into `amp*x` and the two agreed to the
bit. Which sounds harmless, and is exactly the problem. It made the reference a
poor oracle — nearly bit-identical to a fused backend on one architecture and
six roundings away from it on another, so "how far apart are these two" had a
different answer depending on where you asked. The same contraction on amd64 at
`GOAMD64=v3` is what broke the SSE2 kernel's bit-identity assertion outright.

The barriers close it. `TestPortableKernelDoesNotFuse` asserts the arithmetic
from inside, and `TestPortableKernelMatchesTheGoldenVector` pins the number
every target has to arrive at. **Anyone tempted to remove those `float32`
conversions as noise should read this paragraph first**: the last one in
`advanceRotor` looks the most redundant and is the one that actually moved.

## The numeric contract

Four rules, and every future backend is judged by them. The first three are
about the rotor-major bank and are unchanged by anything below; the fourth
covers the voice-major path and is stated at the end of this section, because it
is about a layout the rest of this section has not described yet.

**Packed kernels are the reference, and they agree with each other to the bit.**
A backend that has FMA fuses; two backends that both have FMA must produce
identical float32 words for identical inputs, on every sample, with no
tolerance. Bit-identity is what makes a render reproducible across machines, and
it is cheap to hold to as long as everyone associates the arithmetic the same
way — which is the second rule.

**The accumulation order is part of the contract, not an implementation
detail.** `reduceLanesGeneric` sums the pairwise tree `(lane[0] + lane[1]) +
(lane[2] + lane[3])`, and the fold-on-store inside the packed kernels must
reproduce exactly that order. Floating-point addition is not associative, so a
backend that folds `((l0 + l2) + l1) + l3` is a different program, not a faster
one. `reduceLanesAVX2` pays a `VPERMPS` to undo `VHADDPS`'s per-half
interleaving for precisely this reason.

**The portable kernel is a correctness reference held to a bound, not to the
bit.** It is not allowed to be a different algorithm — it associates and
accumulates identically — but it must be allowed to round differently, because
the whole point of an FMA is that `a*b + c` rounds once instead of twice, and no
portable Go expression can ask for that. What follows derives how far apart that
one substitution can push the two.

### What one FMA costs

Write `u = 2^-24 ≈ 5.96e-8` for the float32 unit roundoff. Per rotor and sample
the two kernels compute the same three quantities, with different numbers of
roundings:

| Quantity                       | Packed                        | Portable               |
| ------------------------------ | ----------------------------- | ---------------------- |
| `ampx = amp*x`                 | 1 (`VMULPS`)                  | 1                      |
| `im' = ampx + re*sin + im*cos` | 2 (two chained `VFMADD231PS`) | 4 (mul, add, mul, add) |
| `re' = re*cos - im*sin`        | 2 (`VMULPS`, `VFNMADD231PS`)  | 3 (mul, mul, sub)      |
| `t = im' - ampx`               | 1                             | 1                      |

`ampx` and `t` are the same operation on both sides and cancel out of the
difference. The two that differ contribute at most the sum of their own error
budgets, so per step the two kernels can disagree about the new state by

```
|Δim'| <= 6u * (|amp*x| + |re*sin| + |im*cos|)
|Δre'| <= 5u * (|re*cos| + |im*sin|)
```

Both bracketed sums are bounded the same way. The coefficient pair `(cos, sin)`
is a rotation scaled by the decay factor, so `cos² + sin² = d²`, and
Cauchy-Schwarz gives `|re*sin| + |im*cos| <= d*ρ` with `ρ = sqrt(re² + im²)` the
rotor's state magnitude. Take the larger constant and call the per-step
injection

```
δ(n) = 6u * (|amp*x(n)| + d*ρ(n))
```

### Why it does not compound

This is the part worth stating plainly. The state update is
`s(n+1) = d*R(φ)*s(n) + e(n)`, with `R` a rotation and `e` the excitation. An
error `E` already present in the state therefore evolves as
`E(n+1) = d*R(φ)*E(n) + δ(n)`. A rotation preserves magnitude exactly, so

```
||E(n+1)|| <= d*||E(n)|| + ||δ(n)||
```

and because the decay factor is _strictly_ below 1, that recursion is
contractive. Old error is forgotten at the same rate as old signal. Summing the
geometric series over a chunk of `N` samples gives the worst case, in which
every rounding error happens to point the same way:

```
||E(N)|| <= δ_max * (1 - d^N) / (1 - d)
```

Rounding errors do not point the same way — their signs are uncorrelated across
samples and across rotors — so the realistic composition is in quadrature:

```
||E(N)|| <= δ_max * g(N, d),    g(N, d) = sqrt((1 - d^2N) / (1 - d²))
```

`g` is the whole story. For a fast rotor it is small and saturates almost
immediately: a 1 ms half-life at 48 kHz has `d = 0.9857` and `g -> 5.9`, so the
two kernels can never drift more than about `6 * 5.9 * u ≈ 2.1e-6` apart
relative to the signal that drives them, no matter how long the render. A 100 ms
half-life has `d = 1 - 1.44e-4` and `g -> 59`, giving `2.1e-5`. Length only
matters until `N ≈ 1/(1-d)`; past that the bound is flat, because the bank is
forgetting error as fast as it makes it.

At `d = 1` the same formula reads `g = sqrt(N)`, which grows without bound. That
is the formal version of the rule the bank already enforces informally: a
sustained rotor has no numeric contract at all, only a drift rate. Decay below 1
is not just what keeps the recursion from needing renormalization — it is what
makes the backends comparable in the first place.

### The tolerance a test may use

Scaling the bound to a bank means combining the per-rotor injection scales. They
combine in quadrature, not in a sum — the same argument that let error compose as
a square root over samples applies over lanes, because one rotor's rounding tells
you nothing about its neighbour's:

```
E(n) = sqrt( Σ_r (|amp_r * x(n)| + d_r * ρ_r(n))² )
```

The distinction is worth a sentence, because on a wide bank it is the whole
difference. An ell-1 sum is the adversarial version, larger by up to `sqrt(R)`
for `R` similarly scaled rotors — sixteen times too generous on a 256-rotor bank
to catch anything. `errorEnvelope` in `contract_test.go` implements the
quadrature form above, and a backend implemented against the ell-1 version would
be built to a bound the harness does not enforce.

`E` is a no-cancellation envelope: it is deliberately not the peak of the
rendered output. A bank whose rotors happen to cancel produces a small output
and exactly the same absolute error, so normalizing to the realized peak turns a
correct backend into a failing one. `contract_test.go` derives `E` from the state
the render starts from, in one extra scalar pass, by running the magnitude
recursion `ρ(n+1) = d*ρ(n) + |amp*x(n)|` — which is contractive for the same
reason the error is.

The reduction adds a second term that is easy to forget. Rule two makes every
backend fold the lanes in the same order with the same number of adds, so the
reduction cannot make two backends _disagree_ — but each of those adds re-rounds
operands that already differ, and can round them a further ULP apart. There are
three adds to fold a block pair's four lanes, three more in `reduceLanes`, and
one per additional block pair accumulating into `acc`. That term is flat in `N`,
which is why it is invisible in a long render and dominant on the first sample,
where `g` is still 1:

```
tol = u * max_n E(n) * (6 * g(N, d_max) + 6 + pairs - 1)
```

Measured against the AVX2 kernel, the worst realized ratio is about 0.02 over the
backend-differential grid and about 0.35 over several million fuzz executions —
halving both constants makes the harness fail within a second. That is the
intended calibration. The bound is not a rubber stamp: a new backend that lands
inside it is conforming, and one that exceeds it has an actual bug rather than
bad luck.

One test asks a larger question than this and needs a larger tolerance.
`TestBankMatchesScalarReference` compares the bank against a float64 reference
that derives its own coefficients, so rounding `cos` and `sin` to float32
perturbs the recursion itself rather than just its arithmetic: the decay factor
becomes `d(1 + δ)` and after `n` steps the state is off by `(1 + δ)^n`. That bias
is systematic — it points the same way on every step — so it composes in the
ell-1 form rather than in quadrature, and `referenceTolerance` adds it on top.
That term is not part of the backend contract, and no backend should be judged
by it.

### Denormals

`Bank.ProcessBlock` opens a `FlushDenormals` scope for the length of the block:
MXCSR FTZ+DAZ on amd64, FPCR FZ on arm64, with the caller's mode put back on the
way out so a host's own policy survives being called. `RealtimeEngine.ProcessBlock`
opens one per callback as well, so the bank's own scopes usually find the bits
already set and cost a register read each. On that path a rotor ringing down past
about `1e-38` reaches exactly zero instead of grinding through the subnormal
range, where the hardware traps into microcode and the recursion slows by one to
two orders of magnitude.

That does not retire the analysis below, which is what an earlier version of this
section predicted it would. Two paths still run unflushed, and one of them is the
harness that validates this entire contract.

`FuzzOscBankMatchesGeneric` and the contract tests drive `processRotorBlocks`
directly rather than going through `Bank`, because the contract has to hold for
coefficients no real oscillator produces — so no scope is ever opened and every
tolerance in this document is measured with denormals live. And `FlushDenormals`
is a documented no-op wherever there is no reachable control register, which for
this project means `GOARCH=wasm`: the web build keeps IEEE denormal behaviour and
a decaying bank there stays as slow as the host makes it.

Running unflushed has a consequence for this contract, and it is the one place
the derivation above is weaker than it looks. Every bound here is relative: it models a rounding
as `fl(x) = x(1 + δ)` with `|δ| <= u`. That model is exact for normal results and
wrong for subnormal ones, where the true statement carries an extra absolute term
of up to `2^-150`. Two backends evaluating the same rotor deep in the subnormal
range can therefore differ by more than `u` times anything, because an FMA rounds
its product once while a separate multiply and add round a subnormal product
first.

The gap is theoretical rather than observed. The fuzz corpus drives rotors into
that range deliberately — one whole amplitude regime seeds state at `1e-30` and
one decay regime reaches subnormals within a few samples — and neither this
harness nor the SSE2 kernel's bit-identity assertion has found a divergence
there. **The tolerances in this document do not depend on flush-to-zero, and are
not relaxed to accommodate its absence.** The unflushed subnormal range is where
the harness is at its strictest, and it stays that way.

### The SSE2 corollary

SSE2 has no FMA. A packed SSE2 kernel therefore sits on the _portable_ side of
the contract, not the packed side: it is not required to be bit-identical to
AVX2, and it must not be tested as if it were. It is held to the same
`6u * g(N, d)` bound as `kernel_generic.go`, for the same reason and by the same
harness. Only its lane fold has to be exact — the accumulation order is rule
two, and rule two has no tolerance.

What SSE2 _is_ required to be bit-identical to is the portable kernel on the
same machine, if and only if it makes the same rounding choices. It turns out to
make them, and deliberately: see "The SSE2 kernel" below. A packed kernel with
no FMA rounds `im'` four times however it is written, so associating exactly as
`kernel_generic.go` does costs it nothing — same instruction count, same
dependency chain, same register pressure — and it upgrades the portable kernel
from an approximate oracle for SSE2 into an exact one.
`TestSSE2IsBitIdenticalToPortable` and the fuzz harness assert it, so the
kernel's association cannot quietly drift away from the reference's.

The claim is unconditional, and it took work to make it so. It rests on the
portable kernel being one program, which for a while it was not.

### The portable kernel is one program

The Go specification permits an implementation to contract `a + b*c` into a
single operation that rounds once instead of twice, and gc takes the permission
whenever the target has FMA. That is arm64 at every optimization level, and
amd64 from `GOAMD64=v3` upwards, where FMA stops being an optional extension and
becomes part of the assumed instruction set. Written the obvious way,
`kernel_generic.go` was therefore not one program but three, and which one it
was depended on a build flag nobody had passed yet.

It is tempting to file that under "the bound covers it", and for the bound it is
true — a rounding either way is a rounding either way. It is not true for
anything sharper. The SSE2 kernel is bit-identical to the unfused reference by
construction, so at `GOAMD64=v3` it stopped matching by one to two ULPs. Worse,
the reference every other backend is measured against was itself moving: the
same test at v1 and at v3 was not the same test.

So `advanceRotor` now binds every product to its own `float32` before it is added
or subtracted. An explicit `float32` conversion is a rounding point the
specification does not let fusion cross, which is the same technique
`model.chebyshevScalar` uses to keep its own seam closed. `t = im' - amp*x`
needs the barrier too, and is the one most easily missed: `amp*x` is a product,
so a subtract following it contracts to an `FMSUB` exactly as readily as an add
would.

There is a trap in doing this, and it cost a 3x regression before it was
noticed. Each conversion costs gc's inliner five points. The obvious version --
one function that indexes the five rotor arrays and does the arithmetic -- came
to 98 against a budget of 80, stopped being inlined, and started paying a real
call with five slice headers on the stack for every lane of every sample. The
arithmetic is therefore in `advanceRotor`, which takes scalars, costs 58, and
inlines; the indexing stayed in the loop. Anyone editing either should re-check
`go build -gcflags=-m`, because the failure mode here is a silent 3x, not a
compile error.

Three consequences worth stating plainly.

The portable kernel is now a genuine oracle. It performs identical arithmetic at
`GOAMD64=v1`, `v3` and `v4` and on arm64, so a test that pins a backend against
it pins the same thing everywhere. `TestPortableKernelDoesNotFuse` asserts the
arithmetic directly and proves its own inputs discriminate; `go build
-gcflags=-S` shows zero `VFMADD` at every amd64 level and zero `FMADD`/`FMSUB`
on arm64. Use the compiler's own listing for that check and not `go tool
objdump`, which does not know the `VFMADD` mnemonics and decodes them as `MOVL`.

The portable kernel is slower on arm64 than it was, because it has given up FMA
there. That is the right trade: it is the roughly 7x-slower reference, NEON is
ungated on arm64, and nothing ships the portable path on a machine that has a
packed one.

And the fused/unfused split in the table above is now a property of the backend
rather than of the toolchain. Every packed kernel that has FMA fuses; the
reference never does; the six-rounding gap between them is the same number on
every target. Note that this cuts against an older instinct: it is now correct
to require the portable kernel to be bit-identical to itself across
architectures, and any prose or test still saying otherwise is stale.

### The fourth rule: the voice-major accumulation order

`VoiceBank` renders up to `LaneWidth` voices at once by making the lane index
the voice index. "The voice-major bank" below describes the layout; this is the
rule its kernels are held to.

**Summing over rotors is the whole reduction, and its order is contractual.**
The kernels advance rotors in pairs. Within a pair the two rotors' output terms
are added together first; pairs then accumulate into the output in ascending
order, the first pair writing it and every later pair adding into it. Written
out, four rotors give `(t0 + t1) + (t2 + t3)` per voice and per sample, and a
backend that accumulates `t0 + t1 + t2 + t3` left to right is a different
program, not a faster one. This is rule two's argument applied to the axis that
survived — floating-point addition is still not associative — but it is a
different sum over different operands, so it is a separate rule rather than a
restatement.

**Rule two does not apply to this path at all.** There is no lane fold to order.
`reduceLanes` has no voice-major counterpart, and adding one would be a bug: it
would sum eight unrelated voices into one signal. A backend author looking for
the horizontal fold should find this paragraph instead of inventing one.

Rules one and three carry over unchanged and are enforced by the same harness.
`FuzzOscBankMatchesGeneric` drives both layouts from one corpus,
`processVoiceRotorsGeneric` is the oracle, and `goldenVoiceFused` /
`goldenVoicePortable` in `golden_test.go` pin the cross-architecture claim the
way `goldenFused` and `goldenPortable` do for the rotor-major path. The
tolerance is `voiceContractTolerance`, which is the same bound with two honest
substitutions: the error envelope runs over one voice's rotors driven by one
voice's excitation rather than over the whole bank, because that is what a lane
accumulates, and the fold term counts this path's adds — one per rotor pair and
one per later pair — instead of the six a horizontal reduction paid.

## The SSE2 kernel

`oscbank_sse2_amd64.s` takes the same block pair as the AVX2 kernel and splits
it four ways: XMM is four lanes, so sixteen rotors are four half-blocks — block
A low, A high, B low, B high — advanced together. This is the case the even
block count in "Layout" was rounded for, and it needs no tail path.

Its one real design decision is the association, and the SSE2 corollary above is
the answer: it evaluates `(amp*x + re*sin) + im*cos` strictly left to right,
which is what `kernel_generic.go` compiles to on amd64, rather than seeding an
accumulator the way the AVX2 kernel's FMA chain does. Both cost four roundings
and one dependency chain of two adds. Only one of them is bit-identical to the
reference. Every operand order in the kernel is load-bearing for that: `ADDPS`
returns its destination when both operands are NaN and `SUBPS` is not
commutative at all, so the accumulator is always the destination.

Sixteen XMM registers cannot hold `re`, `im`, `cos`, `sin` and `amp` for four
half-blocks — that is twenty — so `cos`, `sin` and `amp` are reloaded from L1
every sample. Twelve loads at two per cycle fit comfortably inside a loop that
is latency-bound on two dependent adds per half-block, so the reloads are free.
The `amp*x` register for each half-block is dead the moment its half-block has
consumed it and is reused in place to carry that half-block's `t` to the fold,
which is what makes the whole pass fit in sixteen registers at all.

`reduceLanes` has no SSE2 path and should not grow one. `VHADDPS` is SSE3, so a
4-lane fold would have to transpose four frames with `UNPCKLPS`/`UNPCKHPS`
first — about four uops per frame, which is what the scalar loop already costs.
The reduction is memory-shaped, not arithmetic-shaped. A third implementation of
a summation order that rule two pins exactly would be one more place to get rule
two wrong, for nothing.

## The voice-major bank

Everything above describes `Bank`, which is rotor-major: one voice's `N*M`
rotors fill the lanes, one scalar excitation is broadcast to all of them, and
`reduceLanes` folds the lanes down to one scalar per sample. `VoiceBank`
(`voicebank.go`) turns that array inside out. The rotor arrays become
`[rotor][voice]`: rotor `r` of every voice sits contiguously in one lane vector,
so one packed step advances the same partial of eight different voices.

Every voice of a bank has the same shape — the same oscillator count and the
same harmonic count, differing only in frequency, decay and amplitude. That is
what a polyphonic engine holds anyway, one preset and many notes, and it is what
makes the layout rectangular. A voice carrying fewer oscillators than the shape
leaves its trailing rotors inert, the same way `Bank`'s padding lanes are inert,
and a lane with no voice at all holds zero coefficients and zero amplitude
forever.

Two things follow, and both are the point.

The excitation stops being a scalar. `input` is `[samples][LaneWidth]`
interleaved, so every voice is driven by its own stream. In the packed kernels
that is a vector load where the rotor-major ones issue a `VBROADCASTSS` or a
`VLD1R`, which costs nothing; the one-sample lookahead that keeps `amp*x` off
the critical path widens with it, so the guard element at the end of the scratch
buffer becomes a guard _frame_.

And the horizontal fold disappears. Summing over rotors already produces one
value per voice, so the accumulator is the output and separating the voices is
the caller's deinterleave. This is worth being precise about, because "the fold
moved" would be the wrong summary: the fold is gone. Rule two of the contract
pins the order in which four accumulator lanes are summed, and on this path
there is nothing to sum — a kernel that folded lanes here would be adding eight
unrelated voices together. What replaces rule two is rule four, above, which
fixes the order of the sum that does happen: the one over rotors.

### What it costs

Idle lanes. The bank advances all `LaneWidth` lanes whether or not they carry a
voice, so with fewer sounding voices than lanes the work is the same as with
eight. A single sustained voice is therefore _slower_ in this layout than in
`Bank`, where that voice's own rotors fill the lanes — which is exactly why the
rotor-major path stays. Offline rendering, the fitting objectives and every
golden vector in this package go through `Bank` and are unaffected by any of
this; nothing in `model` changed.

Counting vector steps rather than time: `V` sounding voices of `R = N*M` rotors
each cost `V * ceil(R / LaneWidth)` vector steps rotor-major and `R` vector
steps voice-major, whatever `V` is, up to `LaneWidth`. For the four-oscillator,
four-harmonic presets this project ships, `R` is 16 and `ceil(R / LaneWidth)` is
2, so the two are level at eight voices and the rotor-major path is ahead below
that. Presets whose rotor count is not a multiple of the lane width tilt it the
other way, because the padding a single voice carries is pure waste there and
disappears here. **These are instruction counts, not measurements**, and the
measurements the realtime engine now produces are in "The realtime render path"
below; they agree with the counting and show what else a voice costs besides its
rotors.

There is no cross-voice arithmetic anywhere on the path, which is what makes the
layout testable in the strongest possible way:
`TestVoiceBankIsBitIdenticalToSingleVoiceRenders` asserts that eight voices
rendered together produce the same float32 words as eight voices rendered one at
a time, on every backend, with no tolerance. A tolerance there would pass while
lanes leaked into each other, which is the one bug this layout can have and the
rotor-major one cannot.

## The realtime render path

`RealtimeEngine.ProcessBlock` (`internal/synth/realtime.go`) is the caller the
voice-major bank was built for, and it is the only one. Offline rendering,
preset validation and the fitting objectives all still go through `model.Bar`
and its own rotor-major `Bank`, unchanged and bit for bit.

### What is per voice and what is voice-major

Only the rotors are shared. A struck note's chain is

```
impulse -> lowpass -> [shaper] -> rotor bank -> [shaper] -> + dry mix
```

and every stage but the middle one stays inside the note's own `model.Bar`. The
excitation lowpass is a biquad with a `float64` delay line, the Chebyshev shaper
is a per-sample polynomial that may sit either in front of the bank or behind
it, and the dry mix reads the filtered excitation the same block produced.
Nothing in any of those is a rotor recursion, so nothing in them gains from
being packed across notes, and the shaper's two possible positions fall out
naturally: shaping the excitation is part of what a voice contributes to the
interleaved input, shaping the output is part of what it does with its own lane
afterwards. `model.Bar.StartBankInput` and `model.Bar.FinishBankOutput` are the
two halves; `ProcessExcitation` is still their single-voice composition, and it
computes exactly what it computed before.

So a block is three passes rather than one loop: start every sounding lane's
chain up to the bank and scatter the result into `[samples][LaneWidth]`, run one
`VoiceBank.ProcessBlock` per bank, then lift each lane back out and finish that
voice's chain, gain and mix in place. The deinterleave is the caller's, as
`VoiceBank` intends, and it lands straight in the slot's own block buffer, so
the post-bank chain finishes in place and no sample is copied twice.

### Lanes, slots and stealing

The engine holds `maxVoices` pooled slots and one bank per `LaneWidth` lanes.
The lane is **not** a property of the slot. A voice's rotor state is a stride
through a bank's arrays and cannot be moved cheaply, while the slot list is
permuted by both voice stealing — which rotates the stolen slot to the back —
and retirement, which swaps a dead slot past the end. Tying the lane to the list
position would therefore mix one note's rotors into another's, silently. The
lane travels with the note instead: a slot takes a lane at note-on and holds it
until the voice retires, a retrigger and a steal both keep the lane the slot is
already sounding on, and `restrikeSlot` clears exactly that lane
(`VoiceBank.ResetVoice`) and rewrites exactly that lane's coefficients
(`VoiceBank.SetVoice`) so the notes around it keep ringing.

A retiring voice hands its lane back and a note-on takes the lowest free one,
which is what keeps the sounding voices packed into the low banks: a block walks
banks up to the highest lane a voice holds, so lanes that drifted upwards would
multiply the rotor work without changing a sample.
`TestSoundingVoicesStayPackedIntoTheLowestLanes` is that invariant, and
`TestLanesStayDistinctAcrossStealingAndRetirement` is the one that no two
sounding voices ever share a lane.

The bank's shape is pinned at engine construction, where every lane of every
bank is configured with the preset's own oscillators. `VoiceBank.SetVoice`
discards all rotor state when the shape moves, which on the audio path would be
every sounding note going silent at once; transposition scales frequencies and
decays and never touches the mode or harmonic counts, so once the shape is
pinned no note-on can move it.

### What the engine's correctness tests pin

Two claims, and they are deliberately of different kinds.

**Per voice, exactly.** The voice-major kernel has no cross-lane arithmetic at
all, so a note must render the same float32 words whether it is alone in a bank
or sharing one with seven others, and whichever lane it lands in.
`TestPolyphonicRenderIsBitIdenticalPerVoice` strikes ten notes with staggered
onsets across two banks and requires each one's mono output to equal, with no
tolerance, what the same note renders alone in lane 0 of its own engine. A
tolerance would pass while lanes leaked; equality is also what catches a lane
mix-up after a steal, and it catches a note-on disturbing its neighbours,
because the earlier voices would stop matching in exactly the block the next
note arrives in.

**Against the old serial path, bounded.** The two layouts sum a voice's rotors
in different orders — `Bank` folds them across a block's eight lanes and
finishes with the pairwise tree of rule two, `VoiceBank` sums ascending rotor
pairs down one lane under rule four — and reassociating a `float32` sum changes
it. Nothing else in the chain moves. So
`TestPolyphonicRenderMatchesTheSerialPath` asserts a bound rather than equality:
one part in 100000 of the block's own peak, which is roughly two hundred times
`float32` epsilon and four orders of magnitude below anything audible, yet far
too tight for an actual lane mix-up to hide under.

The measured deviation for the shipped preset is **zero**. That is a property of
the preset, not of the change: four modes with no harmonics is four rotors, the
block fold then has nothing to fold, and both paths end at `(r0+r1)+(r2+r3)`.
The subtest that gives every mode four harmonics is there so the bound is
measuring something — sixteen rotors do reassociate, and the worst deviation
there is `3.4e-7` relative, comfortably inside the bound.

### What it actually bought

Interleaved runs of `BenchmarkRealtimeEngineVoiceCount`, ten rounds of each
build alternating, medians of `ns/op` for one 128-frame block at 48 kHz.
`modes=4x1` is the shipped preset, four modes and no harmonics; `modes=4x4`
gives every mode four harmonics, which is the sixteen-rotor case.

| voices | 4x1 serial | 4x1 voice-major | 4x4 serial | 4x4 voice-major |
| ------ | ---------- | --------------- | ---------- | --------------- |
| 1      | 1834       | 2273 (+24%)     | 1768       | 4022 (+128%)    |
| 2      | 2966       | 3194 (+8%)      | 2900       | 5323 (+84%)     |
| 4      | 5842       | 5100 (-13%)     | 5226       | 6572 (+26%)     |
| 8      | 9706       | 8585 (-12%)     | 10116      | 10434 (+3%)     |
| 16     | 18926      | 18262 (-4%)     | 19223      | 20020 (+4%)     |

_amd64, 12th Gen Intel Core i7-1255U, `go test -benchtime 300ms -count 10`._

| voices | 4x1 serial | 4x1 voice-major | 4x4 serial | 4x4 voice-major |
| ------ | ---------- | --------------- | ---------- | --------------- |
| 1      | 1619       | 2098 (+30%)     | 1620       | 4126 (+155%)    |
| 2      | 2952       | 3244 (+10%)     | 2978       | 5233 (+76%)     |
| 4      | 5642       | 5472 (-3%)      | 5696       | 7407 (+30%)     |
| 8      | 11050      | 9828 (-11%)     | 11164      | 11763 (+5%)     |
| 16     | 22050      | 19410 (-12%)    | 21936      | 23420 (+7%)     |

_arm64, Apple M5, same protocol. That machine is somebody's laptop and is never
idle: load average 2.0 going in and 2.2 coming out, macOS offers no way to pin a
core, and a cross-check of the larger banks there has moved by 20–25% between
runs. Read the shape, not the digits._

Three things to take from that, and the third is the important one.

The crossover lands where the instruction counting above says it does. `V`
voices of `R` rotors cost `V * ceil(R / LaneWidth)` vector steps rotor-major and
`R` steps voice-major. For `modes=4x4` that is `2V` against `16`, level at eight
voices, and the measured curves cross between four and eight. For `modes=4x1` it
is also `2V` against `4` — `roundUpToEven` rounds one block up to two, so a
four-rotor voice pays for sixteen lanes rotor-major and four voice-major — level
at two voices, and the measured curves cross between two and four.

Below the crossover this layout is slower, and at one voice it is much slower.
That is the idle-lane cost stated at the top of this section, arriving on
schedule. In absolute terms it is 2.3 µs of work per 2.67 ms of audio, so it is
not a problem the engine has; it is a reason the rotor-major path stays for
everything that renders one note at a time.

And the win is small even above the crossover, for two reasons that a CPU
profile of the shipped preset at eight voices makes plain. Before:

|                                                                      | share of the block |
| -------------------------------------------------------------------- | ------------------ |
| excitation lowpass (`biquad`)                                        | 35%                |
| `oscBankBlocksAVX2` + `reduceLanes`                                  | 25%                |
| `Bar.ProcessExcitation` itself (the `float32`/`float64` conversions) | 16%                |
| the engine's mix and retirement loops                                | 10%                |
| the Chebyshev shaper                                                 | 5%                 |

After:

|                                                 | share of the block |
| ----------------------------------------------- | ------------------ |
| excitation lowpass (`biquad`)                   | 31%                |
| `Bar.bankInput` itself (the conversions)        | 14%                |
| the engine's gather, scatter and mix loops      | 26%                |
| `oscVoiceRotorsAVX2`                            | 6%                 |
| `Bar.FinishBankOutput` and the Chebyshev shaper | 11%                |

The rotors did what they were supposed to do: 25% of the block down to 6%, the
fourfold saving the lane counting predicts for a four-rotor voice. But roughly
two thirds of that saving is spent again on the interleave. Gathering eight
lanes into `[samples][LaneWidth]` and lifting them back out is 2048 strided
scalar accesses per block at eight voices, and it is what takes the engine's own
loops from 11% of a block to 26%. A packed 8x8 transpose would cut that; a
scalar one is what is here.

The other reason is the one that bounds the whole exercise: **the rotor bank is
not what a realtime voice mostly costs.** The excitation lowpass is a third of
the block, it is per voice by construction, and it does not pack. That is why
the end-to-end `BenchmarkRealtimeEnginePolyphonicPattern` moves by about 1% on
both hosts rather than by the double-digit figure the voice-count sweep
suggests. Polyphony no longer costs linearly _in rotors_; it still costs
linearly in everything else a voice does.

## Measured performance

512-sample blocks. The amd64 rows are a 12th Gen Intel Core i7-1255U,
`taskset -c 0,1`, `-benchtime 4000x`; the first two of those were read off one
benchmark binary, so they share a thermal state. The two arm64 rows are an Apple
M5 and came out of one run of their own; the paragraph after the table says
which run and under what conditions. Rows from different hosts are not
comparable to each other, only within a host:

| Kernel                                       | Host     | Rotors | ns/block  | ns per rotor-block |
| -------------------------------------------- | -------- | ------ | --------- | ------------------ |
| retired four-mode kernel (float64 AVX2)      | i7-1255U | 4      | 1314–1384 | 329–346            |
| `oscbank` 4 oscillators x 4 harmonics (AVX2) | i7-1255U | 16     | 1128–1154 | 70–72              |
| `oscbank` 4 x 4, SSE2 kernel                 | i7-1255U | 16     | 2850–3400 | 178–212            |
| `oscbank` 4 x 4, portable kernel             | i7-1255U | 16     | ~8000     | ~500               |
| `oscbank` 4 x 4, NEON kernel                 | Apple M5 | 16     | 1319      | 82                 |
| `oscbank` 4 x 4, portable kernel             | Apple M5 | 16     | 7009      | 438                |

The two arm64 rows replace a `TODO` that stood as long as the only arm64 this
repository could reach was qemu-user, which is a translation layer: trustworthy
for correctness and instruction validity, worthless for timing. They were taken
on a native Apple M5 (4 performance and 6 efficiency cores, macOS 26.6.1,
Go 1.26.5 darwin/arm64) through `scripts/bench-remote.sh`, which rsyncs the
working tree to the host over ssh and runs, in one invocation:

```
go test -run '^$' \
  -bench '^(BenchmarkBank4x4|BenchmarkBank4x4Portable|BenchmarkBank16x4|BenchmarkBank64x4|BenchmarkReduceLanes4x4)$' \
  -benchmem -count 10 ./internal/oscbank
```

Both rows are medians of those ten iterations, and both come out of that one
binary, so they share a thermal state. On arm64 `BenchmarkBank4x4` _is_ the NEON
path — Advanced SIMD is mandatory in ARMv8-A, so there is no runtime gate to
turn off and no `…NEON`-suffixed benchmark. `BenchmarkBank4x4Portable` in
`internal/oscbank/kernel_arm64_test.go` drives the reference kernel directly for
the comparison.

The ratio is 5.3x, which is the number worth carrying, and it is wider than the
amd64 SSE2-to-portable ratio of about 3x for the reason this section predicted:
both kernels are four lanes wide, but the NEON kernel issues `FMLA`/`FMLS` while
the portable reference gave up FMA on arm64 when it grew its rounding barriers.
The gap widened because the portable side got slower, not because the packed
side got faster.

Two caveats, both real. macOS offers no CPU pinning — there is no `taskset` —
so which core the benchmark lands on is the scheduler's choice, and a
performance core and an efficiency core are far apart on this part. And the
machine was not idle: it is somebody's logged-in laptop, `uptime` reported load
averages of 4.2 entering the run and 3.1 leaving it, and `ps` showed a Steam
process taking a whole core for part of it. A second run of the same command
some minutes later reproduced 4 x 4 within 1% and the portable kernel within 5%,
but ran the larger banks 20–25% slower, which is what background load on an
unpinned scheduler looks like. Treat the 5.3x as the result and the absolute
nanoseconds as an upper bound.

Two more numbers from the same run, for the parts of the arm64 backend that have
no amd64 counterpart to be read against. `BenchmarkReduceLanes4x4` folds a
256-sample, 4-lane accumulator in a median of 33.5 ns, about 0.13 ns per frame:
the `FADDP` pairwise fold does four frames in three instructions where the
scalar loop does one frame in three adds. Scaling on the M5, in ns per
rotor-block: 82 at 4 x 4 (16 rotors), 68 at 16 x 4 (64 rotors), 64 at 64 x 4
(256 rotors) — the same downward drift as the amd64 scaling table below, from
the same cause, on a host that has nothing to do with it.

The SSE2 row lands where a 4-lane unfused kernel should: about 2.1x the AVX2
kernel and about 3x faster than the portable one. Half the lanes accounts for
most of the gap and the missing FMA for the rest — two multiplies and two adds
per rotor and sample where AVX2 issues two FMAs.

That row was measured on a loaded machine, and the honest way to read it is as a
ratio. The load average during the run sat between 18 and 36 — five agents
working in the same checkout — on a machine with far fewer cores than that. The
same binary in the same run reported 1336–1372 ns/block for AVX2 and
8755–11608 for the portable kernel, both around 15% above their own rows above,
so the absolute SSE2 figure is inflated by roughly the same amount and the
ratios are what survive. Re-measure the row on an idle machine before quoting
its absolute number anywhere.

The first row is history, not something to re-run: `QuadDecayOscillator` and its
five `.s` files were deleted in Phase 2.1 once nothing rendered through them. It
is kept because it is the number this bank had to beat, and it did — four times
the oscillator work for 15% less time.

Scaling on the i7-1255U, from `go test ./internal/oscbank -bench Bank`:

| Configuration | Rotors | Blocks | ns/block | ns per rotor-block |
| ------------- | ------ | ------ | -------- | ------------------ |
| 4 x 1         | 4      | 2      | ~1140    | ~285               |
| 4 x 4         | 16     | 2      | ~1140    | ~71                |
| 8 x 4         | 32     | 4      | ~2200    | ~69                |
| 16 x 4        | 64     | 8      | ~4200    | ~66                |
| 64 x 1        | 64     | 8      | ~4150    | ~65                |
| 64 x 4        | 256    | 32     | ~16200   | ~63                |

Cost tracks the rotor count divided by the lane width, not the oscillator count.
Sixty-four oscillators cost 3.6x four oscillators, not 16x, because four
oscillators leave twelve of sixteen lanes empty; past one block pair the cost per
rotor is flat to within 10% out to 256 rotors, and drifts slightly _down_ as the
fixed per-pass overhead amortizes.

## Preset compatibility

Schema v2 exists alongside v1 rather than replacing it. `Load` holds a `"1.0"`
document to the v1 rules — exactly four modes, no per-mode harmonics, no
explicit shaper stage — so a file that quietly grew v2 fields is reported
instead of rendering differently than its version claims. `preset.Upgrade`
converts, and `Save` preserves the version it was given.

`BarParams.Modes` being a slice made plain struct assignment a bug:
`scaledParamsForNote` was transposing the synthesizer's own preset on every
note. It clones now, and `TestRenderingIsIndependentOfPresetState` guards it.

## Known limits

- Cross-voice lane packing is adopted. `RealtimeEngine.ProcessBlock` renders
  every sounding voice through the voice-major banks, and the rotor cost of a
  block is now a function of how many banks the polyphony spans rather than of
  how many voices it holds. What that did _not_ fix is the rest of the voice:
  the per-voice excitation lowpass is 37% of a block at eight voices and does
  not pack, so the end-to-end polyphonic benchmark moves by about 1%. See "The
  realtime render path" for the numbers. Making the excitation chain
  voice-major too is the obvious next step and is not in Phase 2.
- The interleave is scalar. Gathering the lanes in and lifting them out is
  2048 strided `float32` accesses per block at eight voices, and it costs about
  two thirds of what packing the rotors saves. A packed 8x8 transpose is the
  fix and is not written.
- The voice-major bank is slower than the rotor-major one below its crossover —
  two sounding voices for the shipped preset, eight for a sixteen-rotor one —
  because it advances all `LaneWidth` lanes whether they carry a voice or not.
  The engine accepts that; a monophonic block is 2.3 µs of work against 2.67 ms
  of audio. Nothing renders one note at a time through it: offline rendering
  keeps using `Bank`.
- AVX2, SSE2 and NEON are packed. Everything else runs the portable kernel,
  which is about 7x slower on amd64. The arm64 measurement above put NEON at
  5.3x its portable reference rather than 7x, and the two are not the same
  comparison: the amd64 figure is eight lanes with FMA against a portable kernel
  that still has FMA, the arm64 figure four lanes with FMA against a portable
  kernel that has none. AVX-512 is deferred, because CI cannot prove it correct
  on a runner pool that only sometimes has the instructions.
- Denormals are flushed per block on amd64 and arm64, and not at all on
  `GOARCH=wasm`, which has no control register to reach. The numeric consequence,
  and why the contract is still measured unflushed, is in "Denormals" above.
- The recursion still costs eight cycles per sample per block pair. Stepping two
  samples at a time through the squared rotation matrix would halve that, at the
  cost of a second coefficient set and a sample-count tail.
- The optimizer does not search per-mode harmonic gains. `ParamCodec` sizes
  itself from the template's mode count but carries per-mode harmonics through
  unchanged.
