//go:build arm64

#include "textflag.h"

// Go's arm64 assembler has no mnemonic for floating-point vector multiply, add,
// subtract or pairwise-add: FMULS/FADDS/FSUBS are the scalar forms, and the only
// vector float arithmetic it knows is VFMLA and VFMLS. The macros below encode
// the A64 instructions this kernel needs as raw WORD directives.
//
// Not all of them have to be encoded by hand, and it is worth saying why they
// all are, because the reason is what decides whether a future addition belongs
// in this list. A WORD directive cannot take `V8.S4`, so anything hand-encoded
// must be handed register numbers -- and Go's assembly preprocessor has no token
// pasting, so a macro given the number 8 cannot build the name V8 from it. A
// rotor step mixing both notations would have to take every register twice, once
// each way. Encoding the supported instructions the same way as the unsupported
// ones is what lets ROTORSTEP take a register number per operand and read like
// the arithmetic it performs.
//
// Every macro is checked by `go tool objdump`, which decodes each one back to
// the instruction named here; the operand order matches the assembler's own
// convention, destination last.
//
// Absent from the assembler entirely:
//
//	FMUL  Vd.4S, Vn.4S, Vm.4S   0110 1110 001m mmmm 1101 11nn nnnd dddd
//	FADD  Vd.4S, Vn.4S, Vm.4S   0100 1110 001m mmmm 1101 01nn nnnd dddd
//	FSUB  Vd.4S, Vn.4S, Vm.4S   0100 1110 101m mmmm 1101 01nn nnnd dddd
//	FADDP Vd.4S, Vn.4S, Vm.4S   0110 1110 001m mmmm 1101 01nn nnnd dddd
//
// Spelled VFMLA, VFMLS and VMOV by the assembler, re-encoded for uniformity:
//
//	FMLA  Vd.4S, Vn.4S, Vm.4S   0100 1110 001m mmmm 1100 11nn nnnd dddd
//	FMLS  Vd.4S, Vn.4S, Vm.4S   0100 1110 101m mmmm 1100 11nn nnnd dddd
//	MOV   Vd.16B, Vn.16B        0100 1110 101n nnnn 0001 11nn nnnd dddd
//
// MOV is the ORR alias, which is why VMOV16B takes one source register and
// writes its number into both operand fields.
//
// Because the operands are numbers and not names, the register map in front of
// oscBankBlocksNEON is load-bearing documentation, not a courtesy.
#define VFMUL4S(m, n, d)  WORD $(0x6E20DC00 + ((m)<<16) + ((n)<<5) + (d))
#define VFADD4S(m, n, d)  WORD $(0x4E20D400 + ((m)<<16) + ((n)<<5) + (d))
#define VFSUB4S(m, n, d)  WORD $(0x4EA0D400 + ((m)<<16) + ((n)<<5) + (d))
#define VFMLA4S(m, n, d)  WORD $(0x4E20CC00 + ((m)<<16) + ((n)<<5) + (d))
#define VFMLS4S(m, n, d)  WORD $(0x4EA0CC00 + ((m)<<16) + ((n)<<5) + (d))
#define VFADDP4S(m, n, d) WORD $(0x6E20D400 + ((m)<<16) + ((n)<<5) + (d))
#define VMOV16B(n, d)     WORD $(0x4EA01C00 + ((n)<<16) + ((n)<<5) + (d))

// ROTORSTEP advances four rotors by one sample.
//
// The association is the contract, not a preference: it is the same sequence
// the AVX2 kernel runs, in the same order, with the same number of roundings.
// im' is seeded with amp*x and reaches its new value through two chained FMLAs,
// so the recursion's critical path is two fused multiply-adds and nothing else.
// t, which nothing downstream of the accumulator depends on, is recovered
// afterwards by subtracting the seed back out.
//
// re is overwritten by re*cos before im is updated, so both halves of the
// rotation still read the sample's incoming state.
#define ROTORSTEP(RE, IM, COS, SIN, AMPX, TMP, T) \
	VMOV16B(AMPX, TMP)    \ // im' = amp*x
	VFMLA4S(SIN, RE, TMP) \ // im' += re*sin
	VFMLA4S(COS, IM, TMP) \ // im' += im*cos
	VFSUB4S(AMPX, TMP, T) \ // t = im' - amp*x
	VFMUL4S(COS, RE, RE)  \ // re' = re*cos
	VFMLS4S(SIN, IM, RE)  \ // re' -= im*sin
	VMOV16B(TMP, IM)        // im = im'

// ADVANCEPAIR advances a whole block pair -- sixteen rotors -- by one sample and
// leaves the four accumulator lanes in V26.
//
// The lane fold reproduces reduceLanesGeneric's tree exactly:
// (A.lo + B.lo) + (A.hi + B.hi), where A.lo is lanes 0-3 of the first block and
// A.hi lanes 4-7. Rule two of the numeric contract has no tolerance, so the two
// halves are summed separately and only then added together.
//
// amp*x for the *next* sample is computed in the middle of the fold, where the
// four independent multiplies fill the slots the dependent adds leave empty.
// That is what makes the excitation free, and it is why the kernel reads
// input[samples] and why the caller must hand it a padded buffer.
#define ADVANCEPAIR() \
	ROTORSTEP(0, 4,  8, 12, 20, 25, 26) \ // block A, lanes 0-3
	ROTORSTEP(1, 5,  9, 13, 21, 25, 27) \ // block A, lanes 4-7
	ROTORSTEP(2, 6, 10, 14, 22, 25, 28) \ // block B, lanes 0-3
	VFADD4S(28, 26, 26)                 \ // A.lo + B.lo
	ROTORSTEP(3, 7, 11, 15, 23, 25, 28) \ // block B, lanes 4-7
	VFADD4S(28, 27, 27)                 \ // A.hi + B.hi
	VLD1R.P 4(R9), [V24.S4]             \ // x for the next sample
	VFMUL4S(24, 16, 20)                 \
	VFMUL4S(24, 17, 21)                 \
	VFMUL4S(24, 18, 22)                 \
	VFMUL4S(24, 19, 23)                 \
	VFADD4S(27, 26, 26)                   // (A.lo+B.lo) + (A.hi+B.hi)

// LOADPAIR reads one block pair's coefficients and state into registers. One
// VLD1 covers 64 bytes -- two blocks of eight float32 -- and splits them across
// four vectors in memory order, so V0 is block A's low half and V3 is block B's
// high half.
#define LOADPAIR() \
	VLD1 (R0), [V0.S4, V1.S4, V2.S4, V3.S4]     \ // re
	VLD1 (R1), [V4.S4, V5.S4, V6.S4, V7.S4]     \ // im
	VLD1 (R2), [V8.S4, V9.S4, V10.S4, V11.S4]   \ // cos
	VLD1 (R3), [V12.S4, V13.S4, V14.S4, V15.S4] \ // sin
	VLD1 (R4), [V16.S4, V17.S4, V18.S4, V19.S4] \ // amp
	MOVD R6, R9                                 \ // input cursor
	MOVD R8, R10                                \ // accumulator cursor
	MOVD R7, R11                                \ // samples remaining
	VLD1R.P 4(R9), [V24.S4]                     \ // x for the first sample
	VFMUL4S(24, 16, 20)                         \
	VFMUL4S(24, 17, 21)                         \
	VFMUL4S(24, 18, 22)                         \
	VFMUL4S(24, 19, 23)

// NEXTPAIR writes the pair's state back and steps every array on to the next
// pair. Only re and im are written: the coefficients never change.
#define NEXTPAIR() \
	VST1 [V0.S4, V1.S4, V2.S4, V3.S4], (R0) \
	VST1 [V4.S4, V5.S4, V6.S4, V7.S4], (R1) \
	ADD $64, R0                             \
	ADD $64, R1                             \
	ADD $64, R2                             \
	ADD $64, R3                             \
	ADD $64, R4

// func oscBankBlocksNEON(re, im, cosCoeff, sinCoeff, amp *float32, blocks int, input *float32, samples int, acc *float32)
//
// Packed float32 phase-rotation kernel, the arm64 counterpart of
// oscbank_avx2_amd64.s. It runs unconditionally: Advanced SIMD is mandatory in
// ARMv8-A, so there is nothing to dispatch on and cpufeat.HasASIMD is
// deliberately not consulted.
//
// Per rotor and sample the model is
//
//	t  = im*cos + re*sin
//	re = re*cos - im*sin
//	im = amp*x + t
//
// evaluated in the folded order described on ROTORSTEP. NEON's vectors are 128
// bits, so one AoSoA block of eight rotors is two registers and the kernel
// consumes a block pair -- sixteen rotors, four vectors -- per pass. That is the
// same working set as the AVX2 kernel, which is the point: the two must fuse
// the same multiply-adds in the same places to stay bit-identical, and a kernel
// that took half a block at a time would fold its lanes in a different order.
//
// The bank rounds its block count up to an even number, so the pair loop has no
// tail. Half-empty pairs carry zero coefficients and zero amplitude and
// contribute nothing.
//
// Register map, held across the whole sample loop:
//
//	V0-V3    re    A.lo A.hi B.lo B.hi
//	V4-V7    im    A.lo A.hi B.lo B.hi
//	V8-V11   cos   A.lo A.hi B.lo B.hi
//	V12-V15  sin   A.lo A.hi B.lo B.hi
//	V16-V19  amp   A.lo A.hi B.lo B.hi
//	V20-V23  amp*x for the sample in flight, computed one iteration ahead
//	V24      the broadcast excitation sample
//	V25      im' under construction
//	V26-V28  the lane fold
//
//	R0-R4    re, im, cos, sin, amp, walked one block pair at a time
//	R5       block pairs remaining
//	R6-R8    input base, sample count, accumulator base
//	R9-R11   input cursor, accumulator cursor, samples remaining
//
// acc is [samples][4]float32. The first pair writes it and every later pair adds
// into it, so a bank wider than sixteen rotors is just more passes and the
// caller never has to zero the buffer.
TEXT ·oscBankBlocksNEON(SB), NOSPLIT, $0-72
	MOVD re+0(FP), R0
	MOVD im+8(FP), R1
	MOVD cosCoeff+16(FP), R2
	MOVD sinCoeff+24(FP), R3
	MOVD amp+32(FP), R4
	MOVD blocks+40(FP), R5
	MOVD input+48(FP), R6
	MOVD samples+56(FP), R7
	MOVD acc+64(FP), R8

	CMP  $0, R7
	BLE  done
	LSR  $1, R5, R5 // block pairs
	CBZ  R5, done

	// First pair: the same arithmetic, but it writes acc instead of adding to
	// it, which is what saves the caller from zeroing the buffer.
	LOADPAIR()

firstloop:
	ADVANCEPAIR()
	VST1.P [V26.S4], 16(R10)

	SUB  $1, R11
	CBNZ R11, firstloop

	NEXTPAIR()

	SUB  $1, R5
	CBZ  R5, done

pairloop:
	LOADPAIR()

sampleloop:
	ADVANCEPAIR()
	VLD1   (R10), [V28.S4]
	VFADD4S(28, 26, 26)
	VST1.P [V26.S4], 16(R10)

	SUB  $1, R11
	CBNZ R11, sampleloop

	NEXTPAIR()

	SUB  $1, R5
	CBNZ R5, pairloop

done:
	RET

// func reduceLanesNEON(acc, output *float32, samples int)
//
// Collapses [samples][4]float32 to samples scalars, four frames per pass.
// samples must be a positive multiple of 4.
//
// FADDP is the whole reason this is worth writing in assembly. It adds adjacent
// pairs of the concatenation of its two source vectors, so two instructions
// halve four frames at once and a third halves the results again. The tree that
// falls out is (a0+a1) + (a2+a3) per frame -- exactly reduceLanesGeneric's
// order, with no permute needed to repair it, because FADDP keeps the frames in
// place instead of interleaving them the way VHADDPS does on amd64.
TEXT ·reduceLanesNEON(SB), NOSPLIT, $0-24
	MOVD acc+0(FP), R0
	MOVD output+8(FP), R1
	MOVD samples+16(FP), R2

	LSR $2, R2, R2
	CBZ R2, reducedone

reduceloop:
	VLD1.P 64(R0), [V0.S4, V1.S4, V2.S4, V3.S4]

	VFADDP4S(1, 0, 4) // [a0+a1, a2+a3, b0+b1, b2+b3]
	VFADDP4S(3, 2, 5) // [c0+c1, c2+c3, d0+d1, d2+d3]
	VFADDP4S(5, 4, 6) // one total per frame, still in frame order

	VST1.P [V6.S4], 16(R1)

	SUB  $1, R2
	CBNZ R2, reduceloop

reducedone:
	RET

// ADVANCEVOICEPAIR advances a rotor pair -- two rotors of eight voices -- by one
// sample and leaves the eight per-voice outputs in V26 and V27.
//
// It is ADVANCEPAIR with the lane index reinterpreted. The 64 bytes a LOADPAIR
// covers are two rotors of eight voices here instead of two blocks of eight
// rotors, so the four vectors are rotor A voices 0-3, A voices 4-7, B voices 0-3
// and B voices 4-7 -- and the sums that used to be a horizontal lane fold are
// now the reduction over rotors, which is the whole reduction. Nothing is folded
// across lanes anywhere on this path.
//
// The excitation is a pair of vectors rather than a broadcast: V30 drives the
// half-rotors holding voices 0-3 and V31 those holding voices 4-7. Reading the
// next frame is one VLD1.P of 32 bytes, still placed in the middle of the fold
// where the four independent multiplies fill the slots the dependent adds leave
// empty, and still the reason the caller must pad its input -- by a frame now,
// not by an element.
#define ADVANCEVOICEPAIR() \
	ROTORSTEP(0, 4,  8, 12, 20, 25, 26) \ // rotor A, voices 0-3
	ROTORSTEP(1, 5,  9, 13, 21, 25, 27) \ // rotor A, voices 4-7
	ROTORSTEP(2, 6, 10, 14, 22, 25, 28) \ // rotor B, voices 0-3
	VFADD4S(28, 26, 26)                 \ // A + B, voices 0-3
	ROTORSTEP(3, 7, 11, 15, 23, 25, 28) \ // rotor B, voices 4-7
	VFADD4S(28, 27, 27)                 \ // A + B, voices 4-7
	VLD1.P 32(R9), [V30.S4, V31.S4]     \ // the next excitation frame
	VFMUL4S(30, 16, 20)                 \
	VFMUL4S(31, 17, 21)                 \
	VFMUL4S(30, 18, 22)                 \
	VFMUL4S(31, 19, 23)

// LOADVOICEPAIR reads one rotor pair into registers and primes the lookahead.
// The loads are LOADPAIR's, byte for byte: only what the four vectors mean has
// changed.
#define LOADVOICEPAIR() \
	VLD1 (R0), [V0.S4, V1.S4, V2.S4, V3.S4]     \ // re
	VLD1 (R1), [V4.S4, V5.S4, V6.S4, V7.S4]     \ // im
	VLD1 (R2), [V8.S4, V9.S4, V10.S4, V11.S4]   \ // cos
	VLD1 (R3), [V12.S4, V13.S4, V14.S4, V15.S4] \ // sin
	VLD1 (R4), [V16.S4, V17.S4, V18.S4, V19.S4] \ // amp
	MOVD R6, R9                                 \ // input cursor
	MOVD R8, R10                                \ // accumulator cursor
	MOVD R7, R11                                \ // samples remaining
	VLD1.P 32(R9), [V30.S4, V31.S4]             \ // the first excitation frame
	VFMUL4S(30, 16, 20)                         \
	VFMUL4S(31, 17, 21)                         \
	VFMUL4S(30, 18, 22)                         \
	VFMUL4S(31, 19, 23)

// func oscVoiceRotorsNEON(re, im, cosCoeff, sinCoeff, amp *float32, rotors int, input *float32, samples int, acc *float32)
//
// The voice-major counterpart of oscBankBlocksNEON, and the arm64 counterpart of
// oscVoiceRotorsAVX2. Same recursion, same ROTORSTEP, same association, so the
// two remain bit-identical on the new path for the reason they are on the old
// one.
//
// Register map, held across the whole sample loop:
//
//	V0-V3    re    A.v0-3 A.v4-7 B.v0-3 B.v4-7
//	V4-V7    im    same order
//	V8-V11   cos   same order
//	V12-V15  sin   same order
//	V16-V19  amp   same order
//	V20-V23  amp*x for the sample in flight, computed one iteration ahead
//	V25      im' under construction
//	V26-V27  the per-voice outputs of the pair
//	V28-V29  the accumulator being added into
//	V30-V31  the excitation frame, voices 0-3 and 4-7
//
//	R0-R4    re, im, cos, sin, amp, walked one rotor pair at a time
//	R5       rotor pairs remaining
//	R6-R8    input base, sample count, accumulator base
//	R9-R11   input cursor, accumulator cursor, samples remaining
//
// acc is [samples][8]float32 and is the output: lane l is voice l. The first
// pair writes it and every later pair adds into it, so the caller never has to
// zero the buffer. Rule four of the numeric contract fixes that order.
TEXT ·oscVoiceRotorsNEON(SB), NOSPLIT, $0-72
	MOVD re+0(FP), R0
	MOVD im+8(FP), R1
	MOVD cosCoeff+16(FP), R2
	MOVD sinCoeff+24(FP), R3
	MOVD amp+32(FP), R4
	MOVD rotors+40(FP), R5
	MOVD input+48(FP), R6
	MOVD samples+56(FP), R7
	MOVD acc+64(FP), R8

	CMP  $0, R7
	BLE  voicedone
	LSR  $1, R5, R5 // rotor pairs
	CBZ  R5, voicedone

	// First pair: the same arithmetic, but it writes acc instead of adding to
	// it, which is what saves the caller from zeroing the buffer.
	LOADVOICEPAIR()

voicefirstloop:
	ADVANCEVOICEPAIR()
	VST1.P [V26.S4, V27.S4], 32(R10)

	SUB  $1, R11
	CBNZ R11, voicefirstloop

	NEXTPAIR()

	SUB  $1, R5
	CBZ  R5, voicedone

voicepairloop:
	LOADVOICEPAIR()

voicesampleloop:
	ADVANCEVOICEPAIR()
	VLD1   (R10), [V28.S4, V29.S4]
	VFADD4S(28, 26, 26)
	VFADD4S(29, 27, 27)
	VST1.P [V26.S4, V27.S4], 32(R10)

	SUB  $1, R11
	CBNZ R11, voicesampleloop

	NEXTPAIR()

	SUB  $1, R5
	CBNZ R5, voicepairloop

voicedone:
	RET
