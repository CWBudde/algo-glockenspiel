//go:build amd64

#include "textflag.h"

// func oscBankBlocksAVX2(re, im, cosCoeff, sinCoeff, amp *float32, blocks int, input *float32, samples int, acc *float32)
//
// Packed float32 phase-rotation kernel. Two AoSoA blocks -- 16 rotors -- are
// held in registers and advanced together: one block alone cannot fill the
// vector ports, because this recursion is latency-bound, not throughput-bound.
//
// Per rotor and sample the model is
//
//	t  = im*cos + re*sin
//	re = re*cos - im*sin
//	im = amp*x + t
//
// but computing it in that order puts three dependent operations between one
// sample's im and the next (multiply, add for t, add for im). The kernel
// instead folds the excitation into the accumulator seed,
//
//	im' = amp*x + re*sin + im*cos      two chained FMAs, eight cycles
//	t   = im' - amp*x
//
// which leaves re' and im' both eight cycles from their inputs, and takes t --
// which nothing else depends on -- off the critical path. amp*x for the next
// sample is computed one iteration ahead so it is ready when the chain needs
// it; that lookahead is why the kernel reads input[samples], and why the caller
// passes a padded scratch buffer.
//
// The eight lanes fold to four on the way out: the loop is latency-bound, so
// the extract and add that halve the accumulator cost nothing here while they
// would cost real time in the reduction pass.
//
// acc is [samples][4]float32. The first block pair writes it and every later
// pair adds into it, so a bank wider than 16 rotors is just more passes and the
// caller never has to zero the buffer.
TEXT ·oscBankBlocksAVX2(SB), NOSPLIT, $0-72
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
	VMOVUPS (AX), Y0   // re, block A
	VMOVUPS (BX), Y1   // im, block A
	VMOVUPS (CX), Y2   // cos, block A
	VMOVUPS (DX), Y3   // sin, block A
	VMOVUPS (R8), Y4   // amp, block A
	VMOVUPS 32(AX), Y5 // re, block B
	VMOVUPS 32(BX), Y6 // im, block B
	VMOVUPS 32(CX), Y7 // cos, block B
	VMOVUPS 32(DX), Y8 // sin, block B
	VMOVUPS 32(R8), Y9 // amp, block B

	MOVQ R10, SI
	MOVQ R12, DI
	MOVQ R11, R13

	VBROADCASTSS (SI), Y10
	VMULPS       Y10, Y4, Y11 // amp*x, block A, first sample
	VMULPS       Y10, Y9, Y12 // amp*x, block B, first sample

firstloop:
	VMOVAPS     Y11, Y13    // imA' = amp*x
	VFMADD231PS Y3, Y0, Y13 // imA' += re*sin
	VFMADD231PS Y2, Y1, Y13 // imA' += im*cos
	VSUBPS      Y11, Y13, Y15 // tA = imA' - amp*x

	VMULPS       Y2, Y0, Y0 // reA  = re*cos
	VFNMADD231PS Y3, Y1, Y0 // reA -= im*sin
	VMOVAPS      Y13, Y1    // imA  = imA'

	VMOVAPS     Y12, Y13
	VFMADD231PS Y8, Y5, Y13
	VFMADD231PS Y7, Y6, Y13
	VSUBPS      Y12, Y13, Y14 // tB

	VMULPS       Y7, Y5, Y5
	VFNMADD231PS Y8, Y6, Y5
	VMOVAPS      Y13, Y6

	VBROADCASTSS 4(SI), Y10
	VMULPS       Y10, Y4, Y11
	VMULPS       Y10, Y9, Y12

	VADDPS       Y14, Y15, Y15
	VEXTRACTF128 $1, Y15, X13
	VADDPS       X13, X15, X15
	VMOVUPS      X15, (DI)

	ADDQ $4, SI
	ADDQ $16, DI
	DECQ R13
	JNZ  firstloop

	VMOVUPS Y0, (AX)
	VMOVUPS Y1, (BX)
	VMOVUPS Y5, 32(AX)
	VMOVUPS Y6, 32(BX)

	ADDQ $64, AX
	ADDQ $64, BX
	ADDQ $64, CX
	ADDQ $64, DX
	ADDQ $64, R8
	DECQ R9
	JZ   done

pairloop:
	VMOVUPS (AX), Y0
	VMOVUPS (BX), Y1
	VMOVUPS (CX), Y2
	VMOVUPS (DX), Y3
	VMOVUPS (R8), Y4
	VMOVUPS 32(AX), Y5
	VMOVUPS 32(BX), Y6
	VMOVUPS 32(CX), Y7
	VMOVUPS 32(DX), Y8
	VMOVUPS 32(R8), Y9

	MOVQ R10, SI
	MOVQ R12, DI
	MOVQ R11, R13

	VBROADCASTSS (SI), Y10
	VMULPS       Y10, Y4, Y11
	VMULPS       Y10, Y9, Y12

sampleloop:
	VMOVAPS     Y11, Y13
	VFMADD231PS Y3, Y0, Y13
	VFMADD231PS Y2, Y1, Y13
	VSUBPS      Y11, Y13, Y15

	VMULPS       Y2, Y0, Y0
	VFNMADD231PS Y3, Y1, Y0
	VMOVAPS      Y13, Y1

	VMOVAPS     Y12, Y13
	VFMADD231PS Y8, Y5, Y13
	VFMADD231PS Y7, Y6, Y13
	VSUBPS      Y12, Y13, Y14

	VMULPS       Y7, Y5, Y5
	VFNMADD231PS Y8, Y6, Y5
	VMOVAPS      Y13, Y6

	VBROADCASTSS 4(SI), Y10
	VMULPS       Y10, Y4, Y11
	VMULPS       Y10, Y9, Y12

	VADDPS       Y14, Y15, Y15
	VEXTRACTF128 $1, Y15, X13
	VADDPS       X13, X15, X15
	VADDPS       (DI), X15, X15
	VMOVUPS      X15, (DI)

	ADDQ $4, SI
	ADDQ $16, DI
	DECQ R13
	JNZ  sampleloop

	VMOVUPS Y0, (AX)
	VMOVUPS Y1, (BX)
	VMOVUPS Y5, 32(AX)
	VMOVUPS Y6, 32(BX)

	ADDQ $64, AX
	ADDQ $64, BX
	ADDQ $64, CX
	ADDQ $64, DX
	ADDQ $64, R8
	DECQ R9
	JNZ  pairloop

done:
	VZEROUPPER
	RET

// func reduceLanesAVX2(acc, output *float32, samples int)
//
// Collapses [samples][4]float32 to samples scalars, eight frames per pass. The
// horizontal add tree matches reduceLanesGeneric exactly: (a0+a1)+(a2+a3).
// samples must be a positive multiple of 8.
TEXT ·reduceLanesAVX2(SB), NOSPLIT, $0-24
	MOVQ acc+0(FP), SI
	MOVQ output+8(FP), DI
	MOVQ samples+16(FP), CX

	SHRQ  $3, CX
	TESTQ CX, CX
	JLE   reducedone

	VMOVDQU ·reduceFrameOrder(SB), Y7

reduceloop:
	VMOVUPS (SI), Y0  // frames 0,1
	VMOVUPS 32(SI), Y1 // frames 2,3
	VMOVUPS 64(SI), Y2 // frames 4,5
	VMOVUPS 96(SI), Y3 // frames 6,7

	// Two rounds of pairwise adds leave one total per frame, but VHADDPS works
	// per 128-bit half, so they come out interleaved as f0,f2,f4,f6,f1,f3,f5,f7.
	VHADDPS Y1, Y0, Y4
	VHADDPS Y3, Y2, Y5
	VHADDPS Y5, Y4, Y6

	VPERMPS Y6, Y7, Y6
	VMOVUPS Y6, (DI)

	ADDQ $128, SI
	ADDQ $32, DI
	DECQ CX
	JNZ  reduceloop

reducedone:
	VZEROUPPER
	RET

// reduceFrameOrder undoes VHADDPS's per-half interleaving.
DATA ·reduceFrameOrder+0(SB)/4, $0
DATA ·reduceFrameOrder+4(SB)/4, $4
DATA ·reduceFrameOrder+8(SB)/4, $1
DATA ·reduceFrameOrder+12(SB)/4, $5
DATA ·reduceFrameOrder+16(SB)/4, $2
DATA ·reduceFrameOrder+20(SB)/4, $6
DATA ·reduceFrameOrder+24(SB)/4, $3
DATA ·reduceFrameOrder+28(SB)/4, $7
GLOBL ·reduceFrameOrder(SB), RODATA|NOPTR, $32

// func oscVoiceRotorsAVX2(re, im, cosCoeff, sinCoeff, amp *float32, rotors int, input *float32, samples int, acc *float32)
//
// The voice-major counterpart of oscBankBlocksAVX2. Same recursion, same
// association, same one-sample lookahead; the lane index is a voice index
// instead of a rotor index, which changes exactly two things.
//
// The excitation is a vector load rather than a broadcast. input is
// [samples][8]float32 interleaved, so lane l of a frame drives voice l, and the
// cursor advances 32 bytes per sample instead of 4. The lookahead reads
// 32(SI) -- one whole frame past the end on the last sample -- which is why the
// caller pads its scratch buffer by a frame rather than by an element.
//
// And nothing folds horizontally. Summing over rotors already produces one
// value per voice, so the two rotors of a pair are added together and the whole
// 256-bit result is stored: no VEXTRACTF128, no VHADDPS, no reduction pass.
// acc is [samples][8]float32 and is the output. See rule four in
// docs/oscillator-bank.md.
//
// Rotors are still taken two at a time, for the reason they always were: the
// recursion is latency-bound and one vector cannot fill the ports. A rotor pair
// is 64 bytes, which is what a block pair was, so every offset below is the one
// the rotor-major kernel uses.
TEXT ·oscVoiceRotorsAVX2(SB), NOSPLIT, $0-72
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
	VMOVUPS (AX), Y0   // re, rotor A
	VMOVUPS (BX), Y1   // im, rotor A
	VMOVUPS (CX), Y2   // cos, rotor A
	VMOVUPS (DX), Y3   // sin, rotor A
	VMOVUPS (R8), Y4   // amp, rotor A
	VMOVUPS 32(AX), Y5 // re, rotor B
	VMOVUPS 32(BX), Y6 // im, rotor B
	VMOVUPS 32(CX), Y7 // cos, rotor B
	VMOVUPS 32(DX), Y8 // sin, rotor B
	VMOVUPS 32(R8), Y9 // amp, rotor B

	MOVQ R10, SI
	MOVQ R12, DI
	MOVQ R11, R13

	VMOVUPS (SI), Y10   // one excitation frame: one sample per voice
	VMULPS  Y10, Y4, Y11 // amp*x, rotor A, first sample
	VMULPS  Y10, Y9, Y12 // amp*x, rotor B, first sample

voicefirstloop:
	VMOVAPS     Y11, Y13     // imA' = amp*x
	VFMADD231PS Y3, Y0, Y13  // imA' += re*sin
	VFMADD231PS Y2, Y1, Y13  // imA' += im*cos
	VSUBPS      Y11, Y13, Y15 // tA = imA' - amp*x

	VMULPS       Y2, Y0, Y0 // reA  = re*cos
	VFNMADD231PS Y3, Y1, Y0 // reA -= im*sin
	VMOVAPS      Y13, Y1    // imA  = imA'

	VMOVAPS     Y12, Y13
	VFMADD231PS Y8, Y5, Y13
	VFMADD231PS Y7, Y6, Y13
	VSUBPS      Y12, Y13, Y14 // tB

	VMULPS       Y7, Y5, Y5
	VFNMADD231PS Y8, Y6, Y5
	VMOVAPS      Y13, Y6

	VMOVUPS 32(SI), Y10
	VMULPS  Y10, Y4, Y11
	VMULPS  Y10, Y9, Y12

	VADDPS  Y14, Y15, Y15 // the pair's two rotors, per voice
	VMOVUPS Y15, (DI)

	ADDQ $32, SI
	ADDQ $32, DI
	DECQ R13
	JNZ  voicefirstloop

	VMOVUPS Y0, (AX)
	VMOVUPS Y1, (BX)
	VMOVUPS Y5, 32(AX)
	VMOVUPS Y6, 32(BX)

	ADDQ $64, AX
	ADDQ $64, BX
	ADDQ $64, CX
	ADDQ $64, DX
	ADDQ $64, R8
	DECQ R9
	JZ   voicedone

voicepairloop:
	VMOVUPS (AX), Y0
	VMOVUPS (BX), Y1
	VMOVUPS (CX), Y2
	VMOVUPS (DX), Y3
	VMOVUPS (R8), Y4
	VMOVUPS 32(AX), Y5
	VMOVUPS 32(BX), Y6
	VMOVUPS 32(CX), Y7
	VMOVUPS 32(DX), Y8
	VMOVUPS 32(R8), Y9

	MOVQ R10, SI
	MOVQ R12, DI
	MOVQ R11, R13

	VMOVUPS (SI), Y10
	VMULPS  Y10, Y4, Y11
	VMULPS  Y10, Y9, Y12

voicesampleloop:
	VMOVAPS     Y11, Y13
	VFMADD231PS Y3, Y0, Y13
	VFMADD231PS Y2, Y1, Y13
	VSUBPS      Y11, Y13, Y15

	VMULPS       Y2, Y0, Y0
	VFNMADD231PS Y3, Y1, Y0
	VMOVAPS      Y13, Y1

	VMOVAPS     Y12, Y13
	VFMADD231PS Y8, Y5, Y13
	VFMADD231PS Y7, Y6, Y13
	VSUBPS      Y12, Y13, Y14

	VMULPS       Y7, Y5, Y5
	VFNMADD231PS Y8, Y6, Y5
	VMOVAPS      Y13, Y6

	VMOVUPS 32(SI), Y10
	VMULPS  Y10, Y4, Y11
	VMULPS  Y10, Y9, Y12

	VADDPS  Y14, Y15, Y15
	VADDPS  (DI), Y15, Y15
	VMOVUPS Y15, (DI)

	ADDQ $32, SI
	ADDQ $32, DI
	DECQ R13
	JNZ  voicesampleloop

	VMOVUPS Y0, (AX)
	VMOVUPS Y1, (BX)
	VMOVUPS Y5, 32(AX)
	VMOVUPS Y6, 32(BX)

	ADDQ $64, AX
	ADDQ $64, BX
	ADDQ $64, CX
	ADDQ $64, DX
	ADDQ $64, R8
	DECQ R9
	JNZ  voicepairloop

voicedone:
	VZEROUPPER
	RET
