//go:build amd64

#include "textflag.h"

// Packed float32 phase-rotation kernel for SSE2. Same block pair as the AVX2
// kernel -- two AoSoA blocks, 16 rotors -- but XMM is four lanes wide, so the
// pair is four half-blocks (A low, A high, B low, B high) advanced together.
// The bank rounds its block count up to an even number precisely so that works
// out with no tail path: a block pair is always exactly four XMM registers.
//
// Per rotor and sample the model is
//
//	t  = im*cos + re*sin
//	re = re*cos - im*sin
//	im = amp*x + t
//
// and, as in the AVX2 kernel, the excitation is folded into the accumulator
// seed rather than added to t afterwards:
//
//	im' = amp*x + re*sin + im*cos
//	t   = im' - amp*x
//
// which leaves both re' and im' two dependent operations from their inputs and
// takes t, which nothing else depends on, off the critical path. amp*x for the
// next sample is computed one iteration ahead, which is why the kernel reads
// input[samples] and why the caller passes a padded scratch buffer.
//
// Why the arithmetic is associated the way it is
//
// SSE2 has no FMA, so the two chained VFMADD231PS of the AVX2 kernel become
// four separate roundings here however they are written. That leaves a genuine
// choice, and the two candidates are not equally valuable:
//
//   - associate as the AVX2 kernel does -- seed with amp*x and accumulate the
//     two products into it -- which is what MULPS+ADDPS naturally expresses;
//   - associate as kernel_generic.go does, evaluating (amp*x + re*sin) + im*cos
//     strictly left to right.
//
// They are the same instruction count, the same dependency chain and the same
// register pressure. But the second one is bit-identical to the portable
// kernel, so this kernel takes it deliberately: it costs nothing and it
// upgrades the portable kernel from an approximate oracle into an exact one.
//
// That bit-identity is not a property of amd64, and it is worth being precise
// about why, because the obvious reason is wrong. gc contracts a*b+c into an
// FMA wherever the target has one, which on amd64 means GOAMD64=v3 and up, not
// never. kernel_generic.go therefore carries explicit float32 rounding barriers
// so that it really does round MULSS, ADDSS, MULSS, ADDSS in this order at
// every GOAMD64 level and on every architecture. Without them the claim below
// would hold at the amd64 baseline and nowhere else. See "The portable kernel
// is one program" in docs/oscillator-bank.md, TestPortableKernelDoesNotFuse,
// TestSSE2IsBitIdenticalToPortable and the fuzz harness.
//
// Every operand order below is load-bearing for that claim. ADDPS returns its
// destination operand when both are NaN, and SUBPS is not commutative at all,
// so the accumulator is always the destination and the product is always the
// source, matching the order the Go expression evaluates in.
//
// Register discipline
//
//	X0..X3   re for the four half-blocks: A low, A high, B low, B high
//	X4..X7   im, same order
//	X8..X11  amp*x for the next sample, same order; each one is dead the moment
//	         its half-block has consumed it, so it is reused in place to hold
//	         that half-block's t until the lane fold
//	X12      broadcast excitation, then a scratch product
//	X13,X14  cos and sin, reloaded per half-block per sample
//	X15      the accumulator seed being turned into im'
//
// Sixteen XMM registers cannot hold re, im, cos, sin and amp for four
// half-blocks at once -- that is twenty registers -- so cos, sin and amp are
// reloaded from memory every sample. The loop is latency-bound at roughly two
// dependent vector adds per sample per half-block, and twelve L1 loads fit
// inside that at two loads per cycle, so the reloads are free in practice.
// MOVUPS rather than a memory operand on MULPS: Go's allocator does not
// guarantee 16-byte alignment for a []float32, and MULPS with a memory operand
// requires it.
//
// acc is [samples][4]float32. The first block pair writes it and every later
// pair adds into it, so a bank wider than 16 rotors is just more passes and the
// caller never has to zero the buffer. The lane fold is
// (A_low + B_low) + (A_high + B_high), which is the pairwise tree
// reduceLanesGeneric and the AVX2 kernel both use; rule two of the numeric
// contract makes that order binding, not a detail.

// ROTORSTEP advances one half-block by one sample. RE and IM are updated in
// place; AMPX holds amp*x on entry and this half-block's t on exit. OFF is the
// half-block's byte offset inside the block pair.
//
// The continuations carry inline // comments, which is supported and not an
// accident: the preprocessor strips the comment before it evaluates the
// backslash, and the standard library leans on the same thing in md5block_s390x.s,
// p256_asm_ppc64le.s and x/crypto's chacha20 and poly1305. If you doubt it, put
// a bogus mnemonic on the second-to-last line of this macro: the assembler
// reports it at all eight ROTORSTEP call sites, which it could only do if the
// whole body is inside the macro. Recorded here so the question is not reopened.
#define ROTORSTEP(RE, IM, AMPX, OFF) \
	MOVUPS OFF(CX), X13  \ // cos
	MOVUPS OFF(DX), X14  \ // sin
	MOVAPS RE, X12       \
	MULPS  X14, X12      \ // re*sin
	MOVAPS AMPX, X15     \
	ADDPS  X12, X15      \ // amp*x + re*sin
	MOVAPS IM, X12       \
	MULPS  X13, X12      \ // im*cos
	ADDPS  X12, X15      \ // im' = (amp*x + re*sin) + im*cos
	MULPS  X13, RE       \ // re*cos
	MULPS  X14, IM       \ // im*sin
	SUBPS  IM, RE        \ // re' = re*cos - im*sin
	MOVAPS X15, IM       \ // im  = im'
	SUBPS  AMPX, X15     \ // t   = im' - amp*x
	MOVAPS X15, AMPX       // park t where amp*x was

// LOOKAHEAD computes amp*x for the next sample from the broadcast excitation
// left in X12. amp is the destination so the multiply reads amp*x in the order
// the portable kernel writes it.
#define LOOKAHEAD(DST, OFF) \
	MOVUPS OFF(R8), DST \
	MULPS  X12, DST

// func oscBankBlocksSSE2(re, im, cosCoeff, sinCoeff, amp *float32, blocks int, input *float32, samples int, acc *float32)
TEXT ·oscBankBlocksSSE2(SB), NOSPLIT, $0-72
	MOVQ re+0(FP), AX
	MOVQ im+8(FP), BX
	MOVQ cosCoeff+16(FP), CX
	MOVQ sinCoeff+24(FP), DX
	MOVQ amp+32(FP), R8
	MOVQ blocks+40(FP), R9
	MOVQ input+48(FP), R10
	MOVQ samples+56(FP), R11
	MOVQ acc+64(FP), R12

	TESTQ R11, R11
	JLE   done

	SHRQ  $1, R9 // block pairs
	TESTQ R9, R9
	JLE   done

	// First pair: same arithmetic, but it writes acc instead of adding to it.
	MOVUPS (AX), X0
	MOVUPS 16(AX), X1
	MOVUPS 32(AX), X2
	MOVUPS 48(AX), X3
	MOVUPS (BX), X4
	MOVUPS 16(BX), X5
	MOVUPS 32(BX), X6
	MOVUPS 48(BX), X7

	MOVQ R10, SI
	MOVQ R12, DI
	MOVQ R11, R13

	MOVSS  (SI), X12
	SHUFPS $0, X12, X12
	LOOKAHEAD(X8, 0)
	LOOKAHEAD(X9, 16)
	LOOKAHEAD(X10, 32)
	LOOKAHEAD(X11, 48)

firstloop:
	ROTORSTEP(X0, X4, X8, 0)
	ROTORSTEP(X2, X6, X10, 32)
	ROTORSTEP(X1, X5, X9, 16)
	ROTORSTEP(X3, X7, X11, 48)

	ADDPS X10, X8 // A_low  + B_low
	ADDPS X11, X9 // A_high + B_high
	ADDPS X9, X8  // the pairwise tree reduceLanesGeneric fixes

	MOVUPS X8, (DI)

	MOVSS  4(SI), X12
	SHUFPS $0, X12, X12
	LOOKAHEAD(X8, 0)
	LOOKAHEAD(X9, 16)
	LOOKAHEAD(X10, 32)
	LOOKAHEAD(X11, 48)

	ADDQ $4, SI
	ADDQ $16, DI
	DECQ R13
	JNZ  firstloop

	MOVUPS X0, (AX)
	MOVUPS X1, 16(AX)
	MOVUPS X2, 32(AX)
	MOVUPS X3, 48(AX)
	MOVUPS X4, (BX)
	MOVUPS X5, 16(BX)
	MOVUPS X6, 32(BX)
	MOVUPS X7, 48(BX)

	ADDQ $64, AX
	ADDQ $64, BX
	ADDQ $64, CX
	ADDQ $64, DX
	ADDQ $64, R8
	DECQ R9
	JZ   done

pairloop:
	MOVUPS (AX), X0
	MOVUPS 16(AX), X1
	MOVUPS 32(AX), X2
	MOVUPS 48(AX), X3
	MOVUPS (BX), X4
	MOVUPS 16(BX), X5
	MOVUPS 32(BX), X6
	MOVUPS 48(BX), X7

	MOVQ R10, SI
	MOVQ R12, DI
	MOVQ R11, R13

	MOVSS  (SI), X12
	SHUFPS $0, X12, X12
	LOOKAHEAD(X8, 0)
	LOOKAHEAD(X9, 16)
	LOOKAHEAD(X10, 32)
	LOOKAHEAD(X11, 48)

sampleloop:
	ROTORSTEP(X0, X4, X8, 0)
	ROTORSTEP(X2, X6, X10, 32)
	ROTORSTEP(X1, X5, X9, 16)
	ROTORSTEP(X3, X7, X11, 48)

	ADDPS X10, X8
	ADDPS X11, X9
	ADDPS X9, X8

	MOVUPS (DI), X13
	ADDPS  X8, X13 // acc + folded, in that order
	MOVUPS X13, (DI)

	MOVSS  4(SI), X12
	SHUFPS $0, X12, X12
	LOOKAHEAD(X8, 0)
	LOOKAHEAD(X9, 16)
	LOOKAHEAD(X10, 32)
	LOOKAHEAD(X11, 48)

	ADDQ $4, SI
	ADDQ $16, DI
	DECQ R13
	JNZ  sampleloop

	MOVUPS X0, (AX)
	MOVUPS X1, 16(AX)
	MOVUPS X2, 32(AX)
	MOVUPS X3, 48(AX)
	MOVUPS X4, (BX)
	MOVUPS X5, 16(BX)
	MOVUPS X6, 32(BX)
	MOVUPS X7, 48(BX)

	ADDQ $64, AX
	ADDQ $64, BX
	ADDQ $64, CX
	ADDQ $64, DX
	ADDQ $64, R8
	DECQ R9
	JNZ  pairloop

done:
	RET

// func oscVoiceRotorsSSE2(re, im, cosCoeff, sinCoeff, amp *float32, rotors int, input *float32, samples int, acc *float32)
//
// The voice-major counterpart of oscBankBlocksSSE2, and the same four-way split
// of the same 64 bytes: a rotor is eight voices, XMM is four lanes, so a rotor
// pair is four XMM registers -- rotor A voices 0-3, A voices 4-7, B voices 0-3,
// B voices 4-7. Every offset, every register and the whole ROTORSTEP macro are
// unchanged; what a lane means is what changed.
//
// Two consequences.
//
// The excitation is two vector loads instead of one broadcast: voices 0-3 of the
// frame drive the two low half-rotors, voices 4-7 drive the two high ones. X12
// still carries it into LOOKAHEAD, so this needs no extra register -- it needs
// the loads in the right order, which is why the lookahead group below is
// grouped by half rather than by rotor.
//
// And there is no horizontal fold. acc is [samples][8]float32 and lane l is
// voice l's output, so the pass adds A to B within each half and stores both
// halves: 32 bytes per sample, no reduction pass, nothing to get rule two wrong
// in. Rule four of the numeric contract is what this order is held to, and the
// association inside ROTORSTEP still makes this kernel bit-identical to
// processVoiceRotorsGeneric.
TEXT ·oscVoiceRotorsSSE2(SB), NOSPLIT, $0-72
	MOVQ re+0(FP), AX
	MOVQ im+8(FP), BX
	MOVQ cosCoeff+16(FP), CX
	MOVQ sinCoeff+24(FP), DX
	MOVQ amp+32(FP), R8
	MOVQ rotors+40(FP), R9
	MOVQ input+48(FP), R10
	MOVQ samples+56(FP), R11
	MOVQ acc+64(FP), R12

	TESTQ R11, R11
	JLE   voicedone

	SHRQ  $1, R9 // rotor pairs
	TESTQ R9, R9
	JLE   voicedone

	// First pair: same arithmetic, but it writes acc instead of adding to it.
	MOVUPS (AX), X0
	MOVUPS 16(AX), X1
	MOVUPS 32(AX), X2
	MOVUPS 48(AX), X3
	MOVUPS (BX), X4
	MOVUPS 16(BX), X5
	MOVUPS 32(BX), X6
	MOVUPS 48(BX), X7

	MOVQ R10, SI
	MOVQ R12, DI
	MOVQ R11, R13

	MOVUPS (SI), X12 // excitation, voices 0-3
	LOOKAHEAD(X8, 0)
	LOOKAHEAD(X10, 32)
	MOVUPS 16(SI), X12 // excitation, voices 4-7
	LOOKAHEAD(X9, 16)
	LOOKAHEAD(X11, 48)

voicefirstloop:
	ROTORSTEP(X0, X4, X8, 0)
	ROTORSTEP(X2, X6, X10, 32)
	ROTORSTEP(X1, X5, X9, 16)
	ROTORSTEP(X3, X7, X11, 48)

	ADDPS X10, X8 // rotor A + rotor B, voices 0-3
	ADDPS X11, X9 // rotor A + rotor B, voices 4-7

	MOVUPS X8, (DI)
	MOVUPS X9, 16(DI)

	MOVUPS 32(SI), X12
	LOOKAHEAD(X8, 0)
	LOOKAHEAD(X10, 32)
	MOVUPS 48(SI), X12
	LOOKAHEAD(X9, 16)
	LOOKAHEAD(X11, 48)

	ADDQ $32, SI
	ADDQ $32, DI
	DECQ R13
	JNZ  voicefirstloop

	MOVUPS X0, (AX)
	MOVUPS X1, 16(AX)
	MOVUPS X2, 32(AX)
	MOVUPS X3, 48(AX)
	MOVUPS X4, (BX)
	MOVUPS X5, 16(BX)
	MOVUPS X6, 32(BX)
	MOVUPS X7, 48(BX)

	ADDQ $64, AX
	ADDQ $64, BX
	ADDQ $64, CX
	ADDQ $64, DX
	ADDQ $64, R8
	DECQ R9
	JZ   voicedone

voicepairloop:
	MOVUPS (AX), X0
	MOVUPS 16(AX), X1
	MOVUPS 32(AX), X2
	MOVUPS 48(AX), X3
	MOVUPS (BX), X4
	MOVUPS 16(BX), X5
	MOVUPS 32(BX), X6
	MOVUPS 48(BX), X7

	MOVQ R10, SI
	MOVQ R12, DI
	MOVQ R11, R13

	MOVUPS (SI), X12
	LOOKAHEAD(X8, 0)
	LOOKAHEAD(X10, 32)
	MOVUPS 16(SI), X12
	LOOKAHEAD(X9, 16)
	LOOKAHEAD(X11, 48)

voicesampleloop:
	ROTORSTEP(X0, X4, X8, 0)
	ROTORSTEP(X2, X6, X10, 32)
	ROTORSTEP(X1, X5, X9, 16)
	ROTORSTEP(X3, X7, X11, 48)

	ADDPS X10, X8
	ADDPS X11, X9

	MOVUPS (DI), X13
	ADDPS  X8, X13 // acc + folded, in that order
	MOVUPS X13, (DI)
	MOVUPS 16(DI), X13
	ADDPS  X9, X13
	MOVUPS X13, 16(DI)

	MOVUPS 32(SI), X12
	LOOKAHEAD(X8, 0)
	LOOKAHEAD(X10, 32)
	MOVUPS 48(SI), X12
	LOOKAHEAD(X9, 16)
	LOOKAHEAD(X11, 48)

	ADDQ $32, SI
	ADDQ $32, DI
	DECQ R13
	JNZ  voicesampleloop

	MOVUPS X0, (AX)
	MOVUPS X1, 16(AX)
	MOVUPS X2, 32(AX)
	MOVUPS X3, 48(AX)
	MOVUPS X4, (BX)
	MOVUPS X5, 16(BX)
	MOVUPS X6, 32(BX)
	MOVUPS X7, 48(BX)

	ADDQ $64, AX
	ADDQ $64, BX
	ADDQ $64, CX
	ADDQ $64, DX
	ADDQ $64, R8
	DECQ R9
	JNZ  voicepairloop

voicedone:
	RET
