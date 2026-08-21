//go:build amd64

#include "textflag.h"

// func sumSquaredDiffAVX2Asm(synth, ref *float32, count int) float64
//
// The differences are taken in float32, exactly as squaredDiffSumGeneric does,
// but are then widened to float64 before squaring and accumulating. Summing
// ~88k squares in float32 drifts far enough from the generic path that the same
// candidate scores differently on an AVX2 machine than on an ARM one, which
// makes a fit irreproducible across hosts.
//
// Two independent float64 accumulators keep the dependency chain short enough
// to hide the VADDPD latency.
TEXT ·sumSquaredDiffAVX2Asm(SB), NOSPLIT, $0-32
	MOVQ synth+0(FP), SI
	MOVQ ref+8(FP), DI
	MOVQ count+16(FP), CX

	VXORPD Y0, Y0, Y0
	VXORPD Y5, Y5, Y5

	SHRQ $3, CX
	JZ   done

loop:
	VMOVUPS (SI), Y1
	VMOVUPS (DI), Y2
	VSUBPS  Y2, Y1, Y1

	// Low four differences: float32 -> float64, square, accumulate.
	VCVTPS2PD X1, Y3
	VMULPD    Y3, Y3, Y3
	VADDPD    Y3, Y0, Y0

	// High four differences.
	VEXTRACTF128 $1, Y1, X4
	VCVTPS2PD    X4, Y4
	VMULPD       Y4, Y4, Y4
	VADDPD       Y4, Y5, Y5

	ADDQ $32, SI
	ADDQ $32, DI
	DECQ CX
	JNZ  loop

done:
	// Every instruction in the reduction is VEX-encoded. The previous tail used
	// legacy SSE (MOVHLPS/ADDPS/PSHUFD) while the upper YMM state was still
	// dirty, which costs an AVX-to-SSE transition penalty on every call.
	VADDPD       Y5, Y0, Y0
	VEXTRACTF128 $1, Y0, X1
	VADDPD       X1, X0, X0
	VHADDPD      X0, X0, X0
	VMOVSD       X0, ret+24(FP)
	VZEROUPPER
	RET
