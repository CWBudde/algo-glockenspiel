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
// acc is [samples][8]float32. The first block pair writes it and every later
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

	VADDPS  Y14, Y15, Y15
	VMOVUPS Y15, (DI)

	ADDQ $4, SI
	ADDQ $32, DI
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

	VADDPS  Y14, Y15, Y15
	VADDPS  (DI), Y15, Y15
	VMOVUPS Y15, (DI)

	ADDQ $4, SI
	ADDQ $32, DI
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
// Collapses [samples][8]float32 to samples scalars, eight frames per pass. The
// horizontal add tree matches reduceLanesGeneric exactly: ((l0+l1)+(l2+l3)) +
// ((l4+l5)+(l6+l7)). samples must be a positive multiple of 8.
TEXT ·reduceLanesAVX2(SB), NOSPLIT, $0-24
	MOVQ acc+0(FP), SI
	MOVQ output+8(FP), DI
	MOVQ samples+16(FP), CX

	SHRQ  $3, CX
	TESTQ CX, CX
	JLE   reducedone

reduceloop:
	VMOVUPS (SI), Y0
	VMOVUPS 32(SI), Y1
	VMOVUPS 64(SI), Y2
	VMOVUPS 96(SI), Y3
	VMOVUPS 128(SI), Y4
	VMOVUPS 160(SI), Y5
	VMOVUPS 192(SI), Y6
	VMOVUPS 224(SI), Y7

	// Pairwise sums within each frame. VHADDPS works per 128-bit half, so the
	// low half of each result carries the (l0+l1),(l2+l3) terms of two frames
	// and the high half their (l4+l5),(l6+l7) terms.
	VHADDPS Y1, Y0, Y8
	VHADDPS Y3, Y2, Y9
	VHADDPS Y9, Y8, Y10

	VHADDPS Y5, Y4, Y8
	VHADDPS Y7, Y6, Y9
	VHADDPS Y9, Y8, Y11

	// Y10 low = (l0+l1)+(l2+l3) for frames 0..3, high = (l4+l5)+(l6+l7).
	VEXTRACTF128 $1, Y10, X12
	VADDPS       X12, X10, X12

	VEXTRACTF128 $1, Y11, X13
	VADDPS       X13, X11, X13

	VINSERTF128 $1, X13, Y12, Y12
	VMOVUPS     Y12, (DI)

	ADDQ $256, SI
	ADDQ $32, DI
	DECQ CX
	JNZ  reduceloop

reducedone:
	VZEROUPPER
	RET
