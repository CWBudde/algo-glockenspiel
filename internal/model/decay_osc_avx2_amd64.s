//go:build amd64

#include "textflag.h"

// func processBlock4AVX2Asm(realState, imagState, amplitude, cosCoeff, sinCoeff *float64, input, output *float32, count int)
TEXT ·processBlock4AVX2Asm(SB), NOSPLIT, $0-64
	MOVQ realState+0(FP), AX
	MOVQ imagState+8(FP), BX
	MOVQ amplitude+16(FP), CX
	MOVQ cosCoeff+24(FP), DX
	MOVQ sinCoeff+32(FP), R8
	MOVQ input+40(FP), SI
	MOVQ output+48(FP), DI
	MOVQ count+56(FP), R9

	VMOVUPD (AX), Y0
	VMOVUPD (BX), Y1
	VMOVUPD (CX), Y2
	VMOVUPD (DX), Y3
	VMOVUPD (R8), Y4

	TESTQ R9, R9
	JLE done

loop:
	VXORPS X5, X5, X5
	VMOVSS (SI), X5
	VCVTSS2SD X5, X5, X5
	VBROADCASTSD X5, Y5

	VMULPD Y3, Y1, Y6
	VMULPD Y4, Y0, Y7
	VADDPD Y7, Y6, Y6

	VMULPD Y3, Y0, Y8
	VMULPD Y4, Y1, Y9
	VSUBPD Y9, Y8, Y0

	VMULPD Y5, Y2, Y1
	VADDPD Y6, Y1, Y1

	VEXTRACTF128 $1, Y6, X7
	VADDPD X7, X6, X6
	VHADDPD X6, X6, X6
	VCVTSD2SS X6, X6, X6
	VMOVSS X6, (DI)

	ADDQ $4, SI
	ADDQ $4, DI
	DECQ R9
	JNZ loop

done:
	MOVQ realState+0(FP), AX
	MOVQ imagState+8(FP), BX
	VMOVUPD Y0, (AX)
	VMOVUPD Y1, (BX)
	VZEROUPPER
	RET
