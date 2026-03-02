//go:build amd64

#include "textflag.h"

// func sumSquaredDiffAVX2Asm(synth, ref *float32, count int) float64
TEXT ·sumSquaredDiffAVX2Asm(SB), NOSPLIT, $0-32
	MOVQ synth+0(FP), SI
	MOVQ ref+8(FP), DI
	MOVQ count+16(FP), CX

	VXORPS Y0, Y0, Y0
	SHRQ $3, CX
	JZ done

loop:
	VMOVUPS (SI), Y1
	VMOVUPS (DI), Y2
	VSUBPS Y2, Y1, Y1
	VMULPS Y1, Y1, Y1
	VADDPS Y1, Y0, Y0
	ADDQ $32, SI
	ADDQ $32, DI
	DECQ CX
	JNZ loop

done:
	VEXTRACTF128 $1, Y0, X1
	VADDPS X1, X0, X0
	MOVHLPS X0, X1
	ADDPS X1, X0
	PSHUFD $0x01, X0, X1
	ADDSS X1, X0
	CVTSS2SD X0, X0
	MOVSD X0, ret+24(FP)
	VZEROUPPER
	RET
